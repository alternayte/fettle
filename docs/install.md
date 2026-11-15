# Install

Fettle runs in two modes.

- **Local mode** has one owner. The server listens only on a loopback address. The browser signs in with the local token.
- **Server mode** has accounts with email and password. More people can use one server.

The desktop app runs Fettle in local mode, or opens a server.

## Release files

Each release at <https://github.com/alternayte/fettle/releases/latest> holds these files.

| File | Use |
| --- | --- |
| `Fettle-<version>-macos-universal.dmg` | The desktop app for macOS 14 or later. |
| `Fettle-<version>-windows-amd64.msi` | The desktop app installer for Windows on x64. |
| `Fettle-<version>-windows-amd64.exe` | The desktop app for Windows, without an installer. |
| `Fettle-<version>-linux-x86_64.AppImage`, `Fettle-<version>-linux-aarch64.AppImage` | The desktop app for Linux. |
| `fettle-desktop_<version>_amd64.deb`, `fettle-desktop_<version>_arm64.deb` | The desktop app for Debian and Ubuntu. |
| `fettle_<os>_<arch>.tar.gz` | The `fettle` binary for Linux and macOS. The Linux archives also hold `fettle-agentd` and `fettle.service`. |
| `fettle_windows_<arch>.zip` | The `fettle` binary for Windows. |
| `install.sh` | The installer for a Linux server with systemd. |
| `compose.yaml` | The Docker Compose stack of a server. |
| `fettle.service` | The systemd unit. |
| `e2b.Dockerfile` | The E2B template definition of the release. |
| `SHA256SUMS`, `SHA256SUMS-desktop` | The checksums of the files. |
| `update.json` | The signed update feed of the desktop app. |

The release also publishes two images:

- `ghcr.io/alternayte/fettle`: the server.
- `ghcr.io/alternayte/fettle-computer`: the computer image.

## Desktop app

Read [desktop.md](desktop.md).

The `.deb` package needs `libgtk-3-0` and `libwebkit2gtk-4.1-0`.

## Linux server with install.sh

```sh
curl -fsSL https://github.com/alternayte/fettle/releases/latest/download/install.sh | sudo sh
```

The script needs root, Linux and systemd. It works on amd64 and arm64.
It downloads `fettle_linux_<arch>.tar.gz` and checks it against `SHA256SUMS`.
Then it does these steps:

1. It installs `fettle` and `fettle-agentd` to `/usr/local/bin`.
2. It creates the system user `fettle`.
3. It creates the data folder `/var/lib/fettle` with mode 0700.
4. It writes `/etc/fettle/fettle.env` with a new master key, if the file does not exist.
5. It installs `/etc/systemd/system/fettle.service`.
6. It enables and starts the `fettle` service.

These variables change the script:

| Variable | Effect | Default |
| --- | --- | --- |
| `FETTLE_VERSION` | The release tag to install, for example `v1.0.0`. | the latest release |
| `FETTLE_RELEASE_URL` | The folder of the release files. | GitHub releases |
| `FETTLE_BASE_URL` | The public URL of the server, written to `fettle.env`. | `http://<host name>:7420` |
| `FETTLE_NO_START` | `1` installs the service without starting it. | not set |

Give a variable to the shell that runs the script:

```sh
curl -fsSL https://github.com/alternayte/fettle/releases/latest/download/install.sh | sudo FETTLE_BASE_URL=https://fettle.acme.test sh
```

Create the first account:

```sh
sudo -u fettle sh -c 'set -a; . /etc/fettle/fettle.env; fettle admin create-user --data /var/lib/fettle --email owner@acme.test --name Owner --admin'
```

Read [server.md](server.md) for the next steps.

## Docker Compose

```sh
FETTLE_MASTER_KEY=$(openssl rand -base64 32) docker compose -f deploy/compose.yaml up -d
docker compose -f deploy/compose.yaml exec fettle fettle admin create-user --email owner@acme.test --name Owner --admin
```

Read [server.md](server.md#docker-compose) for the variables of the stack.

## From source

The build needs Go, Bun and `just`.

```sh
(cd ui && bun install --frozen-lockfile)
just build
```

`just build` builds the UI into `ui/dist` and the binary into `bin/fettle`.
`just setup` installs the pinned Go tools, the UI packages and the test browser for development.

Other recipes:

- `just image` builds the computer image `fettle-computer:dev` in the local Docker.
- `just desktop-app` builds the desktop app into `bin/fettle-desktop`. On macOS it also makes `bin/Fettle.app`.
- `go build -tags ee -o bin/fettle ./cmd/fettle` builds the binary with the Enterprise features.

A development build uses the computer image `fettle-computer:dev`. Run `just image` before a bot uses a computer.
