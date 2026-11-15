#!/bin/sh
# Install the Fettle server on Linux with systemd (§17).
#
#   curl -fsSL https://github.com/alternayte/fettle/releases/latest/download/install.sh | sudo sh
#
# The script downloads the fettle binary for this computer, checks it against the
# release's SHA256SUMS, installs it to /usr/local/bin, and creates the fettle user, the
# data folder /var/lib/fettle, /etc/fettle/fettle.env and the fettle systemd service.
#
# Variables:
#   FETTLE_VERSION      a release tag, for example v1.0.0 (default: the latest release)
#   FETTLE_RELEASE_URL  the folder of the release files (default: GitHub releases)
#   FETTLE_BASE_URL     the public URL of the server (default: http://<host name>:7420)
#   FETTLE_NO_START     1 installs without starting the service
set -eu

fail() { echo "install.sh: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run as root, for example with sudo"
[ "$(uname -s)" = "Linux" ] || fail "this script installs on Linux; on macOS and Windows use the desktop app"
command -v systemctl >/dev/null 2>&1 || fail "this script needs systemd"

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "no Fettle build for $(uname -m)" ;;
esac

version="${FETTLE_VERSION:-latest}"
if [ -z "${FETTLE_RELEASE_URL:-}" ]; then
  if [ "$version" = latest ]; then
    FETTLE_RELEASE_URL="https://github.com/alternayte/fettle/releases/latest/download"
  else
    FETTLE_RELEASE_URL="https://github.com/alternayte/fettle/releases/download/$version"
  fi
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q "$1" -O "$2"; }
else
  fail "install curl or wget first"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
archive="fettle_linux_${arch}.tar.gz"
echo "Downloading $archive"
fetch "$FETTLE_RELEASE_URL/$archive" "$tmp/$archive" || fail "the download of $archive failed"
fetch "$FETTLE_RELEASE_URL/SHA256SUMS" "$tmp/SHA256SUMS" || fail "the download of SHA256SUMS failed"

want="$(grep " $archive\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
[ -n "$want" ] || fail "SHA256SUMS has no line for $archive"
if command -v sha256sum >/dev/null 2>&1; then
  have="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
else
  have="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
fi
[ "$want" = "$have" ] || fail "the checksum of $archive does not match SHA256SUMS: the file is not the release"
echo "Checksum OK"

tar -xzf "$tmp/$archive" -C "$tmp"
install -m 0755 "$tmp/fettle" /usr/local/bin/fettle
[ -f "$tmp/fettle-agentd" ] && install -m 0755 "$tmp/fettle-agentd" /usr/local/bin/fettle-agentd

id fettle >/dev/null 2>&1 || useradd --system --home-dir /var/lib/fettle --shell /usr/sbin/nologin fettle
install -d -m 0700 -o fettle -g fettle /var/lib/fettle
install -d -m 0750 -o root -g fettle /etc/fettle

if [ ! -f /etc/fettle/fettle.env ]; then
  key="$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
  base="${FETTLE_BASE_URL:-http://$(hostname):7420}"
  umask 077
  cat > /etc/fettle/fettle.env <<ENV
# Fettle settings (Appendix A). Keep FETTLE_MASTER_KEY: it opens the vault.
FETTLE_MODE=server
FETTLE_BASE_URL=$base
FETTLE_MASTER_KEY=$key
FETTLE_ADDR=:7420
ENV
  chown root:fettle /etc/fettle/fettle.env
  chmod 0640 /etc/fettle/fettle.env
  echo "Wrote /etc/fettle/fettle.env with a new master key"
fi

if [ -f "$tmp/fettle.service" ]; then
  install -m 0644 "$tmp/fettle.service" /etc/systemd/system/fettle.service
else
  fetch "$FETTLE_RELEASE_URL/fettle.service" /etc/systemd/system/fettle.service || fail "the download of fettle.service failed"
fi
systemctl daemon-reload
if [ "${FETTLE_NO_START:-}" = 1 ]; then
  systemctl enable fettle >/dev/null
  echo "Installed. Start it with: systemctl start fettle"
else
  systemctl enable --now fettle >/dev/null
  echo "Fettle runs. Create the first account with:"
  echo "  sudo -u fettle sh -c 'set -a; . /etc/fettle/fettle.env; fettle admin create-user --data /var/lib/fettle --email you@example.com --name You --admin'"
fi
