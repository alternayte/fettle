# Fettle build recipes. `just ci` is the gate; `just check` runs the same recipes.

set shell := ["bash", "-uc"]

reports := if path_exists(".build") == "true" { ".build/reports" } else { "tmp/reports" }
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

# recipe: default
# List the recipes.
default:
    @just --list

# recipe: setup
# Install the pinned Go tools and the UI packages.
setup:
    go install github.com/alternayte/drel/cmd/drel@v0.10.4
    go install golang.org/x/tools/cmd/goimports@v0.38.0
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2
    cd ui && bun install --frozen-lockfile
    cd sdk/typescript && bun install --frozen-lockfile
    cd tests/e2e && bun install --frozen-lockfile && bunx playwright install chromium

# recipe: ui
# Build the UI into ui/dist for the embed.
ui:
    cd ui && bun run build

# recipe: build
# Build the fettle binary with the UI into bin/.
build: ui
    go build -trimpath -ldflags "-s -w -X main.version={{version}}" -o bin/fettle ./cmd/fettle

# recipe: desktop-app
# Build the desktop app (Wails v3, build tag desktop) into bin/; on macOS also bin/Fettle.app.
desktop-app: ui
    #!/usr/bin/env bash
    set -euo pipefail
    export MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0"
    go build -tags desktop -trimpath -ldflags "-s -w -X main.version={{version}}" -o bin/fettle-desktop ./cmd/fettle-desktop
    if [ "$(uname)" = "Darwin" ]; then
      rm -rf bin/Fettle.app && mkdir -p bin/Fettle.app/Contents/MacOS
      cp bin/fettle-desktop bin/Fettle.app/Contents/MacOS/fettle-desktop
      sed "s/VERSION/{{version}}/g" desktop/macos/Info.plist > bin/Fettle.app/Contents/Info.plist
      codesign --force --sign - bin/Fettle.app >/dev/null 2>&1 || true
    fi

# recipe: desktop
# The desktop app smoke test on this host (layer D): builds the Wails app with the smoke socket.
desktop: ui
    FETTLE_REPORT_DIR=$(pwd)/{{reports}} go test -count=1 -tags desktopsmoke -timeout 20m ./tests/desktop/

# recipe: smoke
# Layer S: a goreleaser snapshot, the server image, the Compose stack and install.sh in a Debian container with systemd.
smoke:
    goreleaser release --snapshot --clean --skip=publish
    FETTLE_REPORT_DIR=$(pwd)/{{reports}} go test -count=1 -tags smoke -timeout 30m ./tests/smoke/

# recipe: dev
# Run the server and the UI dev server with hot reload.
dev:
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'kill 0' EXIT
    go run ./cmd/fettle serve --data tmp/dev-data &
    cd ui && bun run dev

# recipe: gen
# Generate drel code, the migration list, the OpenAPI document and the TypeScript client.
gen:
    drel generate
    scripts/gen-migrations.sh
    go run ./cmd/fettle openapi > ui/src/api/openapi.json
    cd ui && bunx openapi-typescript src/api/openapi.json -o src/api/schema.d.ts --root-types >/dev/null && bunx prettier --write src/api/schema.d.ts >/dev/null
    # The SDK (Apache-2.0): the same document, the TypeScript types and the JSON Schemas.
    cp ui/src/api/openapi.json sdk/openapi.json
    cd ui && bunx openapi-typescript src/api/openapi.json -o ../sdk/typescript/src/schema.d.ts >/dev/null && bunx prettier --write ../sdk/typescript/src/schema.d.ts >/dev/null
    go run ./tools/schemas sdk/schemas

# recipe: gen-check
# Run gen and fail on any difference; verify migrations against fresh databases.
gen-check: gen
    git diff --exit-code -- . ':(exclude)ui/dist'
    test -z "$(git status --porcelain --untracked-files=all -- internal ui/src/api sdk)"
    go test -count=1 -run 'TestMigrationsVerify' ./internal/app/

# recipe: fmt
# Format Go and UI code.
fmt:
    gofmt -w cmd internal tools tests ui/embed.go
    goimports -l -local github.com/alternayte/fettle cmd internal tools tests | grep -v -e '_drel.go$' -e '_gen.go$' | xargs -r goimports -w -local github.com/alternayte/fettle
    cd ui && bunx prettier --write src scripts

# recipe: fmt-check
# Fail when Go or UI code is not formatted.
fmt-check:
    test -z "$(gofmt -l cmd ee internal tools tests ui/embed.go)"
    test -z "$(goimports -l -local github.com/alternayte/fettle cmd ee internal tools tests | grep -v -e '_drel.go$' -e '_gen.go$')"
    cd ui && bunx prettier --check src scripts

# recipe: lint
# Linters, type check, copy lint, forbidden words, licences and repository checks.
lint:
    golangci-lint run ./...
    golangci-lint run --build-tags ee ./ee/... ./cmd/fettle/
    cd ui && bunx eslint . && bunx tsc -b --noEmit && bun scripts/copylint.ts
    cd sdk/typescript && bunx tsc -p . --noEmit
    scripts/forbid.sh
    go run ./tools/licensecheck ui
    go run ./tools/licensecheck sdk/typescript
    for c in checks/*.sh; do bash "$c" || exit 1; done

# recipe: test
# Unit and integration tests on SQLite and PostgreSQL (layers U and I).
test:
    #!/usr/bin/env bash
    set -euo pipefail
    go build -o bin/fettle-agentd ./cmd/fettle-agentd
    FETTLE_AGENTD_BIN=$(pwd)/bin/fettle-agentd go test -race -count=1 ./...
    go test -race -count=1 -tags ee ./ee/... ./internal/app/
    # The overhead step: the idle memory scenario waits 10 minutes beside the load scenarios.
    go test -count=1 -timeout 15m -run 'TestSCN_UX_012' ./cmd/fettle/ & idle=$!
    FETTLE_AGENTD_BIN=$(pwd)/bin/fettle-agentd go test -count=1 -run 'TestSCN_RUN_009|TestSCN_RUN_010' ./internal/agent/
    wait $idle
    cd ui && bun test scripts

# recipe: contract
# Contract tests against the fakes (layer C).
contract:
    #!/usr/bin/env bash
    set -euo pipefail
    files=$(grep -rl --include='*_test.go' '^//go:build contract' internal tests cmd agentd 2>/dev/null || true)
    names=$(grep -h '^func Test' $files | sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' | paste -sd'|' -)
    go test -race -count=1 -tags contract -run "^(${names})$" ./...
    # The Enterprise contract scenarios need the ee build tag as well.
    go test -race -count=1 -tags 'ee contract' -run '^TestSCN_EE_' ./ee/

# recipe: image
# Build the computer image fettle-computer:dev for this host's Docker.
image:
    docker build -q -t fettle-computer:dev --build-arg VERSION={{version}} -f image/Dockerfile . >/dev/null

# recipe: e2e
# Build the binary and the computer image and run the end-to-end flows and Playwright (layer E).
e2e: build image
    go build -tags fakes -o bin/fettle-fakes ./cmd/fettle
    GOOS=linux GOARCH=$(docker version -f '{{{{.Server.Arch}}') CGO_ENABLED=0 go build -tags fakes -o bin/linux/fettle-fakes ./cmd/fettle
    go test -count=1 -tags e2e ./tests/flows/...
    go test -count=1 -tags 'e2e ee' -run '^TestSCN_EE_' ./tests/flows/
    cd tests/e2e && FETTLE_BIN=../../bin/fettle FETTLE_FAKES_BIN=../../bin/fettle-fakes FETTLE_REPORT_DIR=$(cd ../.. && pwd)/{{reports}} bunx playwright test

# recipe: providers
# Provider conformance, resume times and the sleeping-bot load on this host (layer P).
providers: image
    FETTLE_REPORT_DIR=$(pwd)/{{reports}} go test -count=1 -tags providers -timeout 20m ./internal/providers/...
    FETTLE_REPORT_DIR=$(pwd)/{{reports}} go test -count=1 -tags 'e2e providers' -timeout 20m -run 'TestSCN_CMP_017' ./tests/flows/

# recipe: ci
# The gate (grows by slice, D-105).
ci: gen-check fmt-check lint test contract e2e providers smoke

# recipe: check
# The owner's gate name; runs the same recipes as ci (D-101).
check: ci

# recipe: forbid
# Fail on placeholder words and private words.
forbid:
    scripts/forbid.sh

# recipe: live
# Manual only: chat once with a real model from FETTLE_LIVE_* variables.
live: build
    go test -count=1 -tags live -run TestLive ./tests/flows/...
