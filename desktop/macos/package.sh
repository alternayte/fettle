#!/bin/sh
# Build the macOS desktop app as a universal binary and package it as a .dmg (§17).
#
#   desktop/macos/package.sh <version> <out-dir>
#
# FETTLE_UPDATE_PUBLIC_KEY (base64 Ed25519) turns on signed updates from the release feed.
# With MACOS_SIGN_IDENTITY ("Developer ID Application: ...") the app is signed with the
# hardened runtime; with APPLE_ID, APPLE_TEAM_ID and APPLE_APP_PASSWORD the .dmg is also
# notarized and stapled. Without them the app has an ad-hoc signature (for local tests).
set -eu
version="${1:?version}"
out="${2:?out dir}"
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

export MACOSX_DEPLOYMENT_TARGET=13.0
for arch in arm64 amd64; do
  carch=$arch; [ "$arch" = amd64 ] && carch=x86_64
  (cd "$repo" && CGO_ENABLED=1 GOOS=darwin GOARCH=$arch \
    CGO_CFLAGS="-mmacosx-version-min=13.0 -arch $carch" CGO_LDFLAGS="-mmacosx-version-min=13.0 -arch $carch" \
    go build -tags desktop -trimpath -ldflags "-s -w -X main.version=$version -X main.updateKey=${FETTLE_UPDATE_PUBLIC_KEY:-}" -o "$work/fettle-desktop-$arch" ./cmd/fettle-desktop)
done

app="$work/Fettle.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
lipo -create -output "$app/Contents/MacOS/fettle-desktop" "$work/fettle-desktop-arm64" "$work/fettle-desktop-amd64"
sed "s/VERSION/$version/g" "$here/Info.plist" > "$app/Contents/Info.plist"

if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
  codesign --force --options runtime --timestamp --sign "$MACOS_SIGN_IDENTITY" "$app"
else
  codesign --force --sign - "$app"
fi

mkdir -p "$out"
dmg="$out/Fettle-$version-macos-universal.dmg"
stage="$work/dmg"
mkdir -p "$stage"
cp -R "$app" "$stage/"
ln -s /Applications "$stage/Applications"
rm -f "$dmg"
hdiutil create -quiet -volname "Fettle" -srcfolder "$stage" -fs HFS+ -format UDZO -imagekey zlib-level=9 "$dmg"

if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
  codesign --force --timestamp --sign "$MACOS_SIGN_IDENTITY" "$dmg"
fi
if [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ]; then
  xcrun notarytool submit "$dmg" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD" --wait
  xcrun stapler staple "$dmg"
fi

# The update package: a tar.gz with Fettle.app at its top level (internal/desktop/update.go).
tar -czf "$out/Fettle-$version-darwin-universal.tar.gz" -C "$work" Fettle.app
echo "$dmg"
