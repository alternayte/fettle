# Server

A server runs `fettle serve` in server mode. People sign in with an email and a password.

## Linux with systemd

Install with `install.sh`. Read [install.md](install.md#linux-server-with-installsh).

The unit `fettle.service` runs this command as the user `fettle`:

```sh
/usr/local/bin/fettle serve --mode server --data /var/lib/fettle
```

It reads its variables from `/etc/fettle/fettle.env`. The script writes this file:

```sh
FETTLE_MODE=server
FETTLE_BASE_URL=http://<host name>:7420
FETTLE_MASTER_KEY=<a new random key>
FETTLE_ADDR=:7420
```

Change the file, then restart the service:

```sh
sudo systemctl restart fettle
```

Read the log:

```sh
journalctl -u fettle
```

## Docker Compose

`deploy/compose.yaml` runs three services: `fettle`, `postgres` (PostgreSQL 16) and `minio`.
Fettle creates its bucket in MinIO.

```sh
FETTLE_MASTER_KEY=$(openssl rand -base64 32) docker compose -f deploy/compose.yaml up -d
docker compose -f deploy/compose.yaml exec fettle fettle admin create-user --email owner@acme.test --name Owner --admin
```

The stack reads these variables from the shell or from an `.env` file:

| Variable | Effect | Default |
| --- | --- | --- |
| `FETTLE_MASTER_KEY` | The master key of the vault. It is required. | none |
| `FETTLE_BASE_URL` | The public URL of the server. | `http://localhost:7420` |
| `FETTLE_IMAGE` | The server image. | `ghcr.io/alternayte/fettle:latest` |
| `FETTLE_PORT` | The host port of the server. | `7420` |
| `POSTGRES_PASSWORD` | The password of the database user `fettle`. | `fettle` |
| `MINIO_PASSWORD` | The MinIO password and the S3 secret key of Fettle. | `fettle-minio-secret` |

Change `POSTGRES_PASSWORD` and `MINIO_PASSWORD` before the first start.
Keep the value of `FETTLE_MASTER_KEY`. Each later start needs the same value.

PostgreSQL and MinIO are optional.
Without them, remove their services and the `FETTLE_DB` and `FETTLE_S3_*` lines.
Fettle then keeps SQLite and files in the volume `fettle-data`.

## First account

`fettle admin create-user` creates an account. It works offline on the data folder and the database.

```sh
fettle admin create-user --data /var/lib/fettle --email owner@acme.test --name Owner --admin
```

| Flag | Effect |
| --- | --- |
| `--email` | The email address of the account. It is required. |
| `--name` | The display name. |
| `--admin` | Makes the account an instance admin. An admin without a space gets a new space that they own. |
| `--password-stdin` | Reads the password from standard input. |

Without `--password-stdin`, the command reads the password from `FETTLE_PASSWORD`.
Without both, it generates a password and prints it one time.
The command needs `FETTLE_MASTER_KEY` and `FETTLE_BASE_URL`, as the server does.

## People and spaces

- A space owner makes invite links with `POST /api/v1/spaces/{space}/invites`. An invite link works one time and has a role.
- Fettle keeps only the hash of the invite token. An invite expires.
- A space has the roles `owner` and `member`. A space keeps at least one owner.
- An instance admin can make a new space. The admin becomes its owner.

## Sessions and tokens

- A session ends after 12 hours without use, and after 30 days at most.
- Fettle limits sign-ins to 10 per minute from one address, and 10 per 15 minutes for one email.
- A person makes personal access tokens in **Settings**, **Access tokens**. Fettle shows a token one time.
- A token starts with `fet_`. It expires after at most 366 days, or it has no expiry.

## The CLI against a server

Set the server URL and a personal access token:

```sh
export FETTLE_SERVER=https://fettle.acme.test
export FETTLE_TOKEN=fet_<token>
fettle bot list
fettle chat Chief "What waits for me?" --watch
```

The commands `bot`, `chat`, `computer` and `run` take these flags:

| Flag | Variable | Effect |
| --- | --- | --- |
| `--server` | `FETTLE_SERVER` | The server URL. Without it, the CLI uses the local server. |
| `--token` | `FETTLE_TOKEN` | The personal access token. |
| `--space` | `FETTLE_SPACE` | The space ID or name. The default is the first space. |
| `--data` | `FETTLE_DATA` | The data folder of the local server. |

## HTTPS

Fettle serves plain HTTP.
Put a reverse proxy with TLS in front of it.
Set `FETTLE_BASE_URL` to the `https://` URL.
With an `https://` base URL, the session cookie has the Secure attribute.

## Computers on a server

The default provider is `docker` on Linux.
The `docker` provider uses the Docker Engine from `DOCKER_HOST` or the default socket.
The user that runs Fettle needs access to the Docker Engine.
The Compose stack gives the `fettle` service no Docker socket.
Read [computers.md](computers.md).

## Variables

The server reads these variables. A flag overrides its variable.

| Variable | Flag | Effect | Default |
| --- | --- | --- | --- |
| `FETTLE_MODE` | `--mode` | `local` or `server`. | `local` |
| `FETTLE_DATA` | `--data` | The data folder. | the app-data folder in local mode, `/var/lib/fettle` in server mode |
| `FETTLE_DB` | `--db` | `sqlite`, `sqlite:<path>`, or a `postgres://` URL. | `sqlite` (`fettle.db` in the data folder) |
| `FETTLE_ADDR` | `--addr` | The listen address. Local mode accepts only a loopback address. | `127.0.0.1:7420` in local mode, `:7420` in server mode |
| `FETTLE_BASE_URL` | | The public URL of the server. Server mode needs it. | none |
| `FETTLE_MASTER_KEY` | | The master key: base64 of 32 bytes, or `file:<path>`. Server mode needs it. | the keychain or `master.key` in local mode |
| `FETTLE_NO_KEYCHAIN` | | Any value stops the use of the system keychain in local mode. | not set |
| `FETTLE_OBJECT_STORE` | | `disk` or `s3`. | `disk` |
| `FETTLE_S3_ENDPOINT` | | The endpoint of the S3-compatible store. | none |
| `FETTLE_S3_REGION` | | The region. | none |
| `FETTLE_S3_BUCKET` | | The bucket. Fettle creates it. | none |
| `FETTLE_S3_ACCESS_KEY` | | The access key. | none |
| `FETTLE_S3_SECRET_KEY` | | The secret key. | none |
| `FETTLE_S3_PATH_STYLE` | | `true` uses path-style URLs, for example for MinIO. | not set |
| `FETTLE_WORKERS` | | The number of run workers. | 4 in local mode, 16 in server mode |
| `FETTLE_EXTRA_CA_FILE` | | A PEM file of more CA certificates to trust. | none |
| `FETTLE_LOG_LEVEL` | | The log level. | `info` |
| `FETTLE_TELEMETRY` | | `on` turns on telemetry at start. | off |
| `FETTLE_TELEMETRY_URL` | | The endpoint that telemetry sends to. | none |
| `FETTLE_DEFAULT_PROVIDER` | | The default computer provider. | detected |
| `FETTLE_COMPUTER_IMAGE` | | The computer image. | `ghcr.io/alternayte/fettle-computer:<version>` |
| `FETTLE_AGENTD_BIN` | | The `fettle-agentd` binary of the `host` provider. | next to `fettle` |

Fettle also reads `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` for its outbound requests.

The CLI reads `FETTLE_SERVER`, `FETTLE_TOKEN`, `FETTLE_SPACE`, `FETTLE_PASSWORD` and `FETTLE_EXPORT_PASSPHRASE`.
The Enterprise build reads `FETTLE_LICENSE` and the `FETTLE_AUDIT_*` variables. Read [enterprise.md](enterprise.md).

## Commands

| Command | Effect |
| --- | --- |
| `fettle serve` | Runs the server in the foreground. |
| `fettle up` | Starts a local server in the background and prints its URL with the local token. |
| `fettle down` | Stops the background local server. |
| `fettle migrate` | Applies the database migrations and exits. |
| `fettle doctor` | Checks the storage, the keys, the models, the providers and the settings. |
| `fettle admin create-user` | Creates a server-mode account. |
| `fettle backup <dir>` | Writes a copy of the SQLite database and the object store into a folder. |
| `fettle export` | Writes a portable archive of a space. |
| `fettle import <file>` | Reads a space archive into this instance. |
| `fettle vault rekey` | Wraps all data keys with a new master key. |
| `fettle bot list`, `bot show <bot>`, `bot create`, `bot archive <bot>` | Manage bots. |
| `fettle chat <bot> "<message>"` | Sends a message to a bot. `--watch` streams the reply and asks cards as numbered prompts. |
| `fettle computer list`, `computer add <provider>`, `computer test [zone]`, `computer stop <zone>` | Manage computers. |
| `fettle run show <run-id>` | Shows a run and its journal. `--full` prints the data of each event. |
| `fettle openapi` | Prints the OpenAPI document of the HTTP API. |
| `fettle version` | Prints the version. |

`fettle <command> --help` lists the flags of a command.
The HTTP API is under `/api/v1`.
