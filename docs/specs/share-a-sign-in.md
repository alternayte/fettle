# Share a sign-in

## What it does
A person gives a bot their signed-in session for one site from their own Chrome, so the bot does not have to sign in again.
A Fettle extension for Chrome reads the cookies and the local storage of that site and sends them to the Fettle server.
agentd puts them into the browser profile of one bot, and Fettle keeps no copy.
A share starts from the extension popup on any site, or from a card when a bot meets a sign-in page during a run.
The bot page lists the sites where the bot is signed in, and "Sign out" removes one site.

## Decisions
- A Chrome extension reads the session through `chrome.cookies` and a content script — it works on macOS, Windows and Linux, it is not blocked by App-Bound Encryption, and it reaches a server on a VPS.
- The extension asks for host permission for one site at a time, at the moment of the share — a person grants only the site that they share.
- A share copies the cookies of the registrable domain of the tab (for example `github.com` and its subdomains) and the local storage of the tab origin — many web apps keep their token in local storage, so cookies alone fail without an error.
- A site that signs in through another domain (for example a Google sign-in) needs a second share for that domain — the extension copies only what the person sees and grants.
- The server sends the cookies and storage to agentd of the bot's zone, and agentd sets them in the bot's persistent context with CDP `Network.setCookies` and DOM storage — this is the same result as a sign-in by takeover.
- Fettle keeps no copy of a session — no new secret store, and the SI-01 rules for vault values do not change.
- The server keeps the session in memory only, until agentd confirms or the request fails, and the redactor gets its values for that time — a value does not reach a log or an event.
- Each share writes an audit entry with the site, the bot, the person and the time, and no values — an owner can see who gave a bot which session.
- A share follows the same rights as a takeover: a space member can share with a bot of that space — a takeover sign-in already gives a shared bot a person's session.
- The confirm step in the extension names the bot, the space and the number of people who can use the bot — the person sees who can act with the session.
- `request_takeover` gets an optional `site` field — the bot already decides when it meets a sign-in page, so no new detector is needed.
- A takeover card with a `site` shows "Share from Chrome" beside "Take over"; the button opens the site in Chrome, and the extension shows the share step for that bot — a non-technical person follows one button.
- A share for the bot and site of an open card returns control as "Done" does, and the run continues with a new snapshot — one path for the run.
- After a share, the bot checks the site; when it is still signed out, it asks for a takeover — sessions that a site binds to a device (Google DBSC on Windows) fail this way, without special code.
- The bot page lists the cookie domains in the bot's profile, read through agentd, with "Sign out" for each — a person can undo a share and a takeover sign-in.
- Pairing: "Connect Chrome" in Settings opens a pairing page of the server in the default browser, and the extension takes a one-time code from it — no token to copy.
- In server mode, pairing creates an auth-all API key with only the scope `share-sign-in` (auth-all v0.9.0 has scopes), shown in Account and revocable there — a lost browser cannot do more than share.
- In local mode, pairing creates an extension token, stored as a hash, that LocalAuth accepts only on the share routes, shown and revocable in Settings — the local token gives full control, so it never goes to the extension.
- When the default browser is not Chrome, the pairing page shows the code to type in the extension popup.
- The extension stores the server URL from the pairing; the desktop server uses `127.0.0.1:7420` — the address stays the same between starts.
- The extension lives in `extension/`, is built with Bun, is AGPL-3.0-only like core, and is published on the Chrome Web Store — a non-technical person installs it from the store, and Edge, Brave and Arc use the same build.
- The extension states one purpose and has a privacy policy page — the Chrome Web Store treats sign-in cookies as user data.
- Sharing needs a bot with a zone whose size is `browser` or `desktop` — only those sizes have a browser.

## Out
- A store of sessions in the vault, or a share with all bots of a zone.
- Reading Chrome profile files from the desktop app, and Chrome remote debugging or auto-connect.
- Firefox and Safari.
- Automatic refresh of a session that expires. The person shares again.
- A share into an ephemeral context of a sub-agent.

## How I know it works
- After pairing in server mode, Account lists a key with the scope `share-sign-in`; after pairing in the desktop app, Settings lists the extension token; a request with either token to any other API route gets 403.
- On `github.com` in Chrome, the popup shares with a bot; the bot's `browser_navigate` to `https://github.com/settings/profile` shows the profile page, not the sign-in page.
- A run where the bot calls `request_takeover` with `site: "github.com"` shows "Share from Chrome"; after the share, the run continues without "Done".
- Events, logs and the audit entry of a share contain no cookie value.
- The bot page lists `github.com`; after "Sign out", the same navigation shows the sign-in page.
- A shared Google session from Windows Chrome leads to a takeover card, not to a run that acts signed out.
- Revoking the token makes the next share from the popup fail with "Connect Chrome again".
