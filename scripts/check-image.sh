#!/usr/bin/env bash
# Checks that a computer image on ghcr.io is public: it gets an anonymous pull token and
# reads the manifest of the image, with no login. The release jobs run it on the image that
# the built app names, before a person publishes the draft release.
#
#   scripts/check-image.sh ghcr.io/alternayte/fettle-computer:1.0.0-rc.4
set -euo pipefail
image="${1:?image}"
case "$image" in
  ghcr.io/*) ;;
  *) echo "check-image: $image is not on ghcr.io; a release build names its published image" >&2; exit 1 ;;
esac
ref="${image#ghcr.io/}"
repo="${ref%:*}"
tag="${ref##*:}"
if [ "$repo" = "$ref" ]; then
  echo "check-image: $image has no tag" >&2
  exit 1
fi
token=$(curl -fsS "https://ghcr.io/token?scope=repository:${repo}:pull" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -z "$token" ]; then
  echo "check-image: ghcr.io gives no anonymous pull token for $repo; the package is missing or private" >&2
  exit 1
fi
accept="application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"
code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" -H "Accept: $accept" "https://ghcr.io/v2/${repo}/manifests/${tag}")
if [ "$code" != "200" ]; then
  echo "check-image: the manifest of $image answers $code without a login; the image is missing or private" >&2
  exit 1
fi
echo "check-image: $image is public"
