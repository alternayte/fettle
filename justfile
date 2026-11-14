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
    cd tests/e2e && bun install --frozen-lockfile && bunx playwright install chromium

# recipe: ui
# Build the UI into ui/dist for the embed.
ui:
    cd ui && bun run build

# recipe: build
# Build the fettle binary with the UI into bin/.
build: ui
    go build -trimpath -ldflags "-s -w -X main.version={{version}}" -o bin/fettle ./cmd/fettle

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

# recipe: gen-check
# Run gen and fail on any difference; verify migrations against fresh databases.
gen-check: gen
    git diff --exit-code -- . ':(exclude)ui/dist'
    test -z "$(git status --porcelain --untracked-files=all -- internal ui/src/api)"
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
    test -z "$(gofmt -l cmd internal tools tests ui/embed.go)"
    test -z "$(goimports -l -local github.com/alternayte/fettle cmd internal tools tests | grep -v -e '_drel.go$' -e '_gen.go$')"
    cd ui && bunx prettier --check src scripts

# recipe: lint
# Linters, type check, copy lint, forbidden words, licences and repository checks.
lint:
    golangci-lint run ./...
    cd ui && bunx eslint . && bunx tsc -b --noEmit && bun scripts/copylint.ts
    scripts/forbid.sh
    go run ./tools/licensecheck ui
    for c in checks/*.sh; do bash "$c" || exit 1; done

# recipe: test
# Unit and integration tests on SQLite and PostgreSQL (layers U and I).
test:
    go test -race -count=1 ./...
    cd ui && bun test scripts

# recipe: contract
# Contract tests against the fakes (layer C).
contract:
    #!/usr/bin/env bash
    set -euo pipefail
    files=$(grep -rl --include='*_test.go' '^//go:build contract' internal tests cmd agentd 2>/dev/null || true)
    names=$(grep -h '^func Test' $files | sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' | paste -sd'|' -)
    go test -race -count=1 -tags contract -run "^(${names})$" ./...

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
    cd tests/e2e && FETTLE_BIN=../../bin/fettle FETTLE_FAKES_BIN=../../bin/fettle-fakes FETTLE_REPORT_DIR=$(cd ../.. && pwd)/{{reports}} bunx playwright test

# recipe: ci
# The gate (grows by slice, D-105).
ci: gen-check fmt-check lint test contract e2e

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
