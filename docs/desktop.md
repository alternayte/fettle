# Desktop app

The desktop app is a window and a tray icon around the Fettle server.
On this computer, the app runs the server in its own process, in local mode.
The app can also open a Fettle server that you or your team runs.

## Install

Download the file for your system from <https://github.com/alternayte/fettle/releases/latest>.

- macOS 14 or later: open `Fettle-<version>-macos-universal.dmg` and move Fettle to Applications.
- Windows on x64: run `Fettle-<version>-windows-amd64.msi`.
- Linux: make `Fettle-<version>-linux-<arch>.AppImage` executable and run it.
- Debian and Ubuntu: install `fettle-desktop_<version>_<arch>.deb` with `sudo apt install ./fettle-desktop_<version>_<arch>.deb`.

## First run

The setup has these steps.

1. **Where.** Select **This computer** or **Connect to a server**.
   - With **This computer**, Fettle runs on this computer.
   - With **Connect to a server**, type the server address. The app opens that server. Nothing runs on this computer.
2. **Computer.** Fettle looks for a way to run computers and prepares one. You can continue while it works.
3. **Model.** Add a model provider and its key. Fettle tests the key at once.
4. **Owner model.** Pick the model that does the work of each run.
5. **You.** Type your name and your time zone.

Then the chat with the primary bot, Chief, opens. Chief starts a short interview.

Sometimes Fettle finds no way to run computers. Then bots can chat, but they cannot use a browser, a terminal or files.
Add a computer later in **Settings**, **Computers**.
Read [computers.md](computers.md).

## Tray

Closing the window hides it. The server and the routines continue to run.
The tray menu has these items:

- **Open Fettle** shows the window.
- **Start at login** starts Fettle when you sign in to the computer.
- **Quit Fettle** stops the app and its server.

The app shows system notifications for work that needs you. Select a notification to open its screen.

## Data folder

The app keeps its data in the app-data folder of your user.

| System | Folder |
| --- | --- |
| macOS | `~/Library/Application Support/Fettle` |
| Windows | `%AppData%\Fettle` |
| Linux | `$XDG_DATA_HOME/fettle`, else `~/.local/share/fettle` |

`FETTLE_DATA` sets another folder.
The file `desktop.json` in the folder holds the choice of the first step.

The master key of the vault is in the keychain of the system.
When no keychain works, Fettle writes `master.key` with mode 0600 in the data folder.
Keep a copy of the master key. A backup does not hold it.

## Updates

The app checks the release feed at start and then each day.
It downloads a newer package and checks its Ed25519 signature.
Then it asks you to install the update and restart. Running work continues after the restart.
When the window is hidden, a notification tells you that the update is ready.

## Build from source

```sh
(cd ui && bun install --frozen-lockfile)
just desktop-app
```

The recipe builds `bin/fettle-desktop`. On macOS it also builds `bin/Fettle.app`.
A Linux build needs the GTK 3 and WebKitGTK 4.1 development packages.
