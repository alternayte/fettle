#!/bin/sh
# Package the Linux desktop app as an AppImage (§17).
#   desktop/linux/appimage.sh <binary> <version> <out-dir>
# Needs appimagetool on PATH.
set -eu
bin="${1:?binary}"; version="${2:?version}"; out="${3:?out dir}"
here="$(cd "$(dirname "$0")" && pwd)"
dir="$(mktemp -d)/Fettle.AppDir"
mkdir -p "$dir/usr/bin"
install -m 0755 "$bin" "$dir/usr/bin/fettle-desktop"
cp "$here/fettle.desktop" "$dir/fettle.desktop"
cp "$here/fettle.png" "$dir/fettle.png"
cat > "$dir/AppRun" <<'RUN'
#!/bin/sh
here="$(dirname "$(readlink -f "$0")")"
exec "$here/usr/bin/fettle-desktop" "$@"
RUN
chmod +x "$dir/AppRun"
mkdir -p "$out"
ARCH="$(uname -m)" appimagetool "$dir" "$out/Fettle-$version-linux-$(uname -m).AppImage"
