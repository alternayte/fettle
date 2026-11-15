#!/usr/bin/env bash
# Raw SQL must use $1, $2, ... once each and in order: drel rewrites them to positional ?
# on SQLite, which binds the arguments by their place in the text.
set -euo pipefail
cd "$(dirname "$0")/.."
bad=$(git ls-files '*.go' | grep -v -e '_drel.go$' -e '_gen.go$' | xargs perl -ne '
  while (/"((?:[^"\\]|\\.)*\$\d(?:[^"\\]|\\.)*)"|`([^`]*\$\d[^`]*)`/g) {
    my $s = defined $1 ? $1 : $2;
    next unless $s =~ /\b(SELECT|INSERT|UPDATE|DELETE)\b/i;
    my @n = ($s =~ /\$(\d+)/g);
    for my $i (0..$#n) { if ($n[$i] != $i + 1) { print "$ARGV:$.: $s\n"; last } }
  }
  close ARGV if eof;' || true)
if [ -n "$bad" ]; then
  echo "SQL placeholders must be \$1, \$2, ... once each and in order:"
  echo "$bad"
  exit 1
fi
