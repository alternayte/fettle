# Computers

Each zone has one computer. Bots in a zone share its computer.
A computer has a shell, files, Python, Node and, from the `browser` size, a browser.
The server reaches each computer only through `fettle-agentd` over HTTPS with a computer token.

## Zones

A new space has two zones: `personal` and `browsing`.
A zone has these settings:

| Setting | Values |
| --- | --- |
| Provider | A provider kind, or the default provider. |
| Size | `small`, `browser` or `desktop`. |
| Network | `open` reaches all sites. `allowlist` reaches only the listed hosts. |
| Sleep after | Minutes without work before the computer sleeps, from 1 to 1440. |

A bot asks for a new zone with a card. The zone exists after a person allows the card.
A change of the provider or the network replaces the computer of the zone.

Each bot works in one zone. You choose the zone in the **New bot** form. A template suggests its own zone.
To move a bot, open its settings page and use **Computer**. An owner can move a bot when it has no work in progress. The bot uses the new computer from its next run, and its own files go with it.

Settings > Computers lists the zones and the providers.
The menu at the end of a zone row tests the computer (it starts the computer and runs one command) and puts a running computer to sleep. A sleeping computer keeps its files, and the next work wakes it.

![The Computers settings page. It lists the zones browsing and personal with running computers, and the computer providers.](images/settings-computers.png)

## Sizes

| Size | CPU | Memory | Disk | Contents |
| --- | --- | --- | --- | --- |
| `small` | 1 | 1 GiB | 10 GiB | Shell, files, Python and Node. |
| `browser` | 2 | 2 GiB | 10 GiB | Adds a browser. |
| `desktop` | 2 | 3 GiB | 10 GiB | Adds screens that you can take over. |

## Providers

| Provider | Where it works | Site allowlist | Screens |
| --- | --- | --- | --- |
| `applecontainer` | macOS 26 on Apple silicon | yes | yes |
| `docker` | Docker Desktop, Docker Engine or Podman | yes | yes |
| `host` | any system | no | no |
| `kiln` (remote) | a Kiln host, with its URL and token | yes | yes |
| `e2b` (remote) | E2B, with an API key | yes | yes |

### applecontainer

The provider runs each computer as a lightweight VM with Apple container 1.x.
It works on macOS 26 on Apple silicon.
When the runtime is missing, Fettle downloads the pinned package and checks its checksum and its Apple signature.
Fettle installs the runtime into the data folder. No step needs sudo.
It is the default provider where it works.

### docker

The provider runs one container per computer through the Docker Engine API.
It connects to the engine from `DOCKER_HOST` or the default socket.
It works with Docker Desktop, Docker Engine and Podman.
It is the default provider when `applecontainer` does not work.

### host

The provider runs `fettle-agentd` as a child process of Fettle, with a home folder under the data folder.
It has no isolation. Bots run commands with the rights of the user that runs Fettle.
It cannot limit sites, so a zone with an allowlist cannot use it.
Turning it on is a hard ask: a card asks a person in every approval mode.
It is never the default provider.
Fettle looks for `fettle-agentd` next to the `fettle` binary. `FETTLE_AGENTD_BIN` names another path.

### kiln

The provider runs each computer as a Firecracker microVM on a remote Kiln host.
A computer sleeps as a snapshot and wakes from it with its memory and disk.
A fork is a native copy of the running computer.

Fettle reaches `fettle-agentd` through a Kiln port stream.
Fettle runs its own TLS inside the stream and checks the pinned certificate of the computer.
The Kiln host does not see the requests to `fettle-agentd`.

To use a Kiln host:

1. Test the host and the token: `fettle computer add kiln --url https://kiln.acme.test --kiln-token <token>`.
2. Set `FETTLE_KILN_URL` and `FETTLE_KILN_TOKEN` for the server, and restart it.
3. Build the computer templates: `fettle computer add kiln`.
4. Select `kiln` as the provider of a zone.

A zone with an allowlist gets the same hosts on the Kiln host and in the computer.
An open zone lets the computer reach every host name on ports 80 and 443.
`kiln` is never the default provider, except when `FETTLE_DEFAULT_PROVIDER` names it.
This version runs Kiln only as a remote host. A local Kiln on the same computer is not in this version.

### e2b

The provider runs each computer as an E2B sandbox in your E2B account.
On first use, Fettle builds one E2B template for each size from the computer image of the release.
A template name holds the Fettle version, so a new release builds new templates.
The template has no start command. After Fettle creates a sandbox, it starts `fettle-agentd` as root through envd, with the token and the key pair of that computer.

A computer sleeps with an E2B pause, which keeps its memory, its processes and its disk. The next work resumes it.
A fork is a native E2B fork of the running computer.
E2B pauses a sandbox when its time to live of one hour ends. An open connection from Fettle extends the time to live.

E2B reaches a sandbox port only through its HTTPS proxy, and the proxy ends TLS.
So `fettle-agentd` also listens on port 7422 for a WebSocket that it pipes, byte for byte, to its own TLS port.
Fettle runs its own TLS inside the WebSocket and checks the pinned certificate of the computer. E2B sees only the encrypted stream.
The port URLs need the traffic access token of the sandbox.

To use E2B:

1. Test the API key: `fettle computer add e2b --key <key>`.
2. Set `FETTLE_E2B_API_KEY` for the server, and restart it.
3. Build the computer templates: `fettle computer add e2b`.
4. Select `e2b` as the provider of a zone.

E2B builds the templates from a registry image. A release uses `ghcr.io/alternayte/fettle-computer:<version>`.
A development build uses `fettle-computer:dev`, which E2B cannot pull. Set `FETTLE_E2B_IMAGE` to a registry image for a development build.
`FETTLE_E2B_API_URL` names another E2B API. The default is `https://api.e2b.app`.

A zone with an allowlist gets the same hosts as the E2B egress allowlist, and E2B refuses all other addresses. `fettle-agentd` applies the same rules in the computer.
An open zone has no egress limit at E2B.
`e2b` is never the default provider, except when `FETTLE_DEFAULT_PROVIDER` names it.
The release holds `e2b.Dockerfile`, the E2B template definition of the computer image.

### Default provider

Fettle picks the default provider at start: `applecontainer` where it works, else `docker`.
`FETTLE_DEFAULT_PROVIDER` sets another default.

## The computer image

Every provider except `host` runs the computer image.
The image is Debian slim with the non-root user `fettle`.
It holds Chromium, Xvfb, x11vnc, git, ripgrep, curl, jq, Python 3 with uv, Node 22, pdftotext, pandoc and `fettle-agentd`.
The image has no `sudo` and no `su`.

| Build | Image |
| --- | --- |
| A release, also a pre-release such as `1.0.0-rc.4` | `ghcr.io/alternayte/fettle-computer:<version>` |
| A development build | `fettle-computer:dev`, from `just image` |

The release build sets the image with the `main.computerImage` ldflag.
A build without this ldflag uses `fettle-computer:dev`.
`fettle version --image` prints the image of a build.

Fettle chooses the image in this order:

1. `FETTLE_COMPUTER_IMAGE`.
2. The setting **Computer image** in Settings, Computers, This server.
3. The image of the build.

**Reset to default** clears the setting.
The next computer that starts uses a new image. A running computer keeps its image.
Kiln and E2B template names contain a short hash of the image, so a new image builds new templates.

The first start downloads the image. Setup shows "Downloading the computer image (N MB)" and the percent.
When the download fails, the error names the image and points to Settings, Computers.
For `fettle-computer:dev`, the error tells you to run `just image`.

## Settings of this server

Instance admins see the section **This server** in Settings, Computers.
The settings apply to every space.
An environment variable wins over each setting. Then the field is read-only, shows the value of the variable and names the variable.

| Setting | Field | Variable that locks it |
| --- | --- | --- |
| `computers.image` | Computer image | `FETTLE_COMPUTER_IMAGE` |
| `computers.extra_ca` | Extra root certificates (PEM) | `FETTLE_EXTRA_CA_FILE` |
| `computers.proxy` | Proxy URL, with the user name | `HTTPS_PROXY` |
| `computers.proxy_password` | Proxy password | `HTTPS_PROXY` |
| `computers.use_host_settings` | Use this computer's certificates and proxy (desktop app only) | none |

The desktop app ignores `FETTLE_COMPUTER_IMAGE`. Use the field to change the image there.
A release ignores a computer image that names another release of `ghcr.io/alternayte/fettle-computer`, and uses its own image. An upgrade thus never keeps an old pin. Fettle refuses to save such an image.

The proxy password is an instance secret in the vault. The API never returns it. It shows only whether a password is set.
Do not put the password in the proxy URL. Fettle refuses such a URL.
The API is `GET /api/v1/admin/settings` and `PUT /api/v1/admin/settings/{key}`. An empty text resets a setting.

## Certificates and proxy

Some networks inspect HTTPS or need a proxy.
Fettle uses the extra roots and the proxy for its own requests and gives them to each computer.
For each value, the variable wins, then the setting, then the values of the host when the desktop option is on.
A change applies at once to the server and to the next computer that starts.
The `host` provider runs on the host itself and gets none of them.

At boot, as root, agentd installs the extra roots:

- It writes them to `/usr/local/share/ca-certificates` and runs `update-ca-certificates`.
- It writes the Chromium policy `/etc/chromium/policies/managed/fettle.json` with `CACertificates`. Chromium on Linux does not read the system store.
- Commands get `SSL_CERT_FILE`, `SSL_CERT_DIR`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE` and `UV_SYSTEM_CERTS=1`.

### Filter proxy

With a proxy, agentd starts a filter proxy on `127.0.0.1:3128` as the system user `fettle-net`.
It accepts CONNECT and plain HTTP.
In an allowlist zone, it checks each host against the list of the zone. `*.acme.test` matches `acme.test` and its subdomains, on port 80 or 443.
It refuses other hosts with status 403 and the text "<host> is not on the allowlist of this zone."
It sends allowed traffic to the upstream proxy, and signs in when the proxy has a user and a password.

nftables lets only `fettle-net` reach the upstream proxy and DNS. All other processes reach only loopback.
This applies in open zones too, so all traffic goes through the filter proxy and the upstream proxy.
Commands get `HTTP_PROXY`, `HTTPS_PROXY`, the lower-case forms and `NO_PROXY=localhost,127.0.0.1`.
Chromium gets `--proxy-server` with the filter proxy. Loopback stays direct.
A proxy that listens only on the loopback address of the host is not reachable from a computer.

### Errors

The browser and `exec` put network errors in plain words:

| Error | Message |
| --- | --- |
| A certificate error | This network inspects HTTPS. Add your company root certificate in Settings, Computers. |
| `ERR_NAME_NOT_RESOLVED` | The computer could not look up <host>. |
| Refused or reset in an allowlist zone, for a host that is not on the list | <host> is not on the allowlist of this zone. |
| `ERR_PROXY_CONNECTION_FAILED`, `ERR_TUNNEL_CONNECTION_FAILED` | The proxy did not answer. |

`exec` adds the certificate message when a command fails and its output shows a certificate error.

### Check network

**Check network** is in the menu of each zone in Settings, Computers.
It starts the computer of the zone and fetches one HTTPS page with curl and with Chromium.
It shows the DNS, connect and TLS steps of each fetch, and writes an audit row.
An allowlist zone fetches its first listed site.
`FETTLE_NETWORK_CHECK_URL` sets the page. The default is `https://example.com/`.

`fettle doctor --network` runs Check network for each zone through the running server.
Plain `fettle doctor` starts no computer.

## CLI

```sh
fettle computer list
fettle computer add docker
fettle computer test
fettle computer test browsing
fettle computer stop personal
```

- `computer list` shows the zones with their provider, state, size, network and bots. Then it shows the providers.
- `computer add <docker|applecontainer|host|kiln|e2b>` turns on a provider and prepares it. For `host`, a card in the chat of the primary bot asks you to allow it. For `kiln` and `e2b`, the server needs their variables first.
- `computer test [zone]` starts the computer of a zone and runs one command in it. The default zone is `personal`.
- `computer stop <zone>` puts the computer of a zone to sleep. Its workspace stays.
- `doctor --network` checks DNS, connect and TLS in the computer of each zone.

## Sleep and limits

A computer sleeps after its idle time. The next work wakes it.
The instance setting `computers.max_running` limits the computers that run at the same time.
With 0, local mode uses a value from the memory of the host: 1 computer per 2 GiB above 4 GiB, at least 1.
The space setting `workspaces.home_limit_gib` limits the size of one bot home. The default is 5 GiB.

## Screens

A computer of the `desktop` size has a screen.
You can open the screen of a bot read-only from the chat header of the bot, from the **Computer** section of its settings page, or from the zone row in Settings > Computers.
You can take control of the screen for 15 minutes at a time. The bot waits while you control it.

![The screen of the bot Research, read-only. The browser of its computer shows the Beta Desk page of the catalogue.](images/computer-screen.png)
