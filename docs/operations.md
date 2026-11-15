# Operations

## Health

| Path | Answer |
| --- | --- |
| `/healthz` | The process runs. |
| `/readyz` | The database answers, the migrations are applied and the object store works. |
| `/metrics` | Metrics in the Prometheus text format. |

## Logs

- `fettle serve` writes its log to standard error.
- `fettle up` writes the log of the background server to `logs/fettle.log` in the data folder.
- The systemd service writes to the journal. Read it with `journalctl -u fettle`.
- `FETTLE_LOG_LEVEL` sets the level. The default is `info`.
- Fettle removes known secret values from log lines.

## Doctor

```sh
fettle doctor
```

The command checks the database, the migrations, the object store and the master key.
It also checks the proxy and CA settings and the model providers.
It prints `PASS` or `FAIL` for each check. It exits with a non-zero code when a check fails.
In server mode, run it with the same variables as the server.
Instance admins can also run the checks through `GET /api/v1/admin/diagnostics`.

## Upgrades

1. Stop the server.
2. Replace the `fettle` binary, or pull the new image.
3. Start the server.

The server applies the database migrations at start, under a lock.
`fettle migrate` applies them and exits.
`/readyz` fails while migrations are not applied.

## Backup

```sh
fettle backup /var/backups/fettle
```

- With SQLite, the command writes a consistent copy of the database and all objects of the store into the folder. It works while the server runs.
- With PostgreSQL, the command copies the objects only. Back up the database with `pg_dump`.
- The backup does not hold the master key. Keep a copy of the master key apart from the backup.

To restore a SQLite backup, stop the server.
Copy `fettle.db` and the `objects` folder into the data folder, then start the server with the same master key.

## Export and import of a space

```sh
FETTLE_EXPORT_PASSPHRASE='<passphrase>' fettle export --space "My space" --out my-space.fettle.tar.gz
FETTLE_EXPORT_PASSPHRASE='<passphrase>' fettle import my-space.fettle.tar.gz --owner owner@acme.test
```

- `export` writes a portable archive of one space. It holds bots, chats, memory, skills, routines, zones, workspaces and the space settings.
- `--space` names the space. An instance with one space needs no `--space`.
- `--out` names the file. The default is `<space>.fettle.tar.gz`.
- Without `FETTLE_EXPORT_PASSPHRASE`, the archive does not hold the vault.
- With the passphrase, the archive holds the vault, sealed with a key from the passphrase.
- The archive does not hold grants or runs. Give grants again after an import.
- `import` reads an archive into this instance. In server mode, `--owner` names the account that owns the space.
- An import fails when the instance already holds the space of the archive.

Both commands open the data folder offline. Give them the same `--data`, `--db` and variables as the server.

## Master key change

```sh
fettle vault rekey
```

The command wraps all data keys with a new master key in one transaction.
`--new-key` gives the new key: base64 of 32 bytes, or `file:<path>`. Without it, Fettle makes a new key.

- In local mode, Fettle saves the new key where the old key was: the keychain or `master.key`.
- In server mode, the command prints the new key. Set `FETTLE_MASTER_KEY` to it before the next start.

## Usage export

Instance admins can read all usage events as NDJSON, one JSON object per line, oldest first:

```sh
curl -H "Authorization: Bearer $FETTLE_TOKEN" "https://fettle.acme.test/api/v1/admin/usage?since=2026-01-01T00:00:00Z"
```

The query takes `since` and `until` in RFC 3339, and `space` with a space ID.

## Capacity

| Setting | Scope | Effect | Default |
| --- | --- | --- | --- |
| `FETTLE_WORKERS` | variable | Run workers. | 4 in local mode, 16 in server mode |
| `computers.max_running` | instance | Computers that run at the same time. 0 uses a value from the memory of the host in local mode. | 0 |
| `workspaces.home_limit_gib` | space | Size limit of one bot home in GiB. Over it, checkpoints pause. | 5 |
| `bots.max_per_space` | space | Bots in one space. | 50 |
| `routines.max_per_bot` | space | Routines of one bot. | 50 |
| `chat.attachment_max_mb` | space | The largest attachment in MB. | 50 |

Change settings in **Settings**, or through `PUT /api/v1/spaces/{space}/settings/{key}`.
Fettle keeps the change history of each setting.

## Network

- Fettle reads `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` for outbound requests.
- `FETTLE_EXTRA_CA_FILE` names a PEM file of more CA certificates to trust.

## Telemetry

Telemetry is off by default.
When it is on, Fettle sends anonymous counts once a day.
It sends these fields: `version`, `mode`, `os`, `arch`, `database`, `spaces`, `bots`, `runs_24h` and `model_providers`.

- An instance admin turns it on in **Settings**, **Instance**.
- `FETTLE_TELEMETRY=on` turns it on at start.
- Fettle sends only when `FETTLE_TELEMETRY_URL` names an endpoint.
