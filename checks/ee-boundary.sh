#!/usr/bin/env bash
# check: ee-boundary
# born: 2026-11-15
# failure: a core file imports the ee package, so the AGPL build links Enterprise code
# rule: only files under ee/ or files with the ee build tag import github.com/alternayte/fettle/ee, and the default fettle binary has no ee package in its dependencies
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"
bad=0
while IFS= read -r f; do
  case "$f" in ee/*) continue ;; esac
  if ! head -3 "$f" | grep -q '^//go:build .*\bee\b'; then
    echo "ee-boundary: $f imports ee without the ee build tag"
    bad=1
  fi
done < <(git ls-files -co --exclude-standard '*.go' | xargs grep -lE '^[[:space:]]*(import[[:space:]]+)?([A-Za-z_.]+[[:space:]]+)?"github.com/alternayte/fettle/ee' 2>/dev/null || true)
if go list -deps ./cmd/fettle | grep -q '^github.com/alternayte/fettle/ee'; then
  echo "ee-boundary: the default fettle binary links an ee package"
  bad=1
fi
exit "$bad"
