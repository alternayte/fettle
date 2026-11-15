#!/usr/bin/env bash
# Fails on placeholder words and private words in tracked text files and commit messages.
# Binary files such as screenshots are skipped: their bytes match words by chance.
set -uo pipefail
cd "$(dirname "$0")/.."
fail=0
words=scripts/forbid-words.txt
files=$(git ls-files | grep -v -e '^scripts/forbid-words.txt$' -e '_drel.go$' -e '_gen.go$' -e '^ui/bun.lock$' -e '^ui/src/api/schema.d.ts$' -e '^LICENSE$' -e '/LICENSE$')
while IFS= read -r w; do
  [ -z "$w" ] && continue
  hits=$(printf '%s\n' $files | xargs grep -I -n -F -- "$w" 2>/dev/null | grep -v -e '^scripts/forbid.sh:')
  if [ -n "$hits" ]; then
    echo "forbid: \"$w\" found:" >&2
    echo "$hits" | head -20 >&2
    fail=1
  fi
done < "$words"
private=.build/private-words.txt
if [ -f "$private" ]; then
  msgs=$(git log --format=%B)
  while IFS= read -r w; do
    [ -z "$w" ] && continue
    if printf '%s\n' $files | xargs grep -I -l -i -F -- "$w" >/dev/null 2>&1; then
      echo "forbid: a private word is in a tracked file:" >&2
      printf '%s\n' $files | xargs grep -I -l -i -F -- "$w" 2>/dev/null | head -5 >&2
      fail=1
    fi
    if printf '%s' "$msgs" | grep -q -i -F -- "$w"; then
      echo "forbid: a private word is in a commit message" >&2
      fail=1
    fi
  done < "$private"
fi
exit $fail
