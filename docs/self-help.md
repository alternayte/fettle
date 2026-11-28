# Help from your bots

Each bot knows Fettle. You can ask a bot about Fettle in its chat, for example "How do I add a model?" or "Why can't you use the browser?".
A bot can also draft a GitHub issue about a bug in Fettle. You read the draft and open it on GitHub yourself.

## What a bot can read

| Tool | What it does |
| --- | --- |
| `fettle_docs` | Lists the pages of these docs, searches them, and reads one page or one section. The binary holds the docs of the version that runs, so the bot needs no network. |
| `fettle_status` | Shows the Fettle version, the OS, the mode, the bot's models per role, its zone, network policy, computer provider and computer state, its skills, the connectors and the web search of the space, and the errors of its last 5 failed runs. |

`fettle_status` never shows a key, a password or a vault value.
Only an instance admin gets the doctor checks of the instance in `fettle_status`. Other members get the setup of the bot only. An admin also sees the doctor checks in **Admin**, **Diagnostics**.

A bot cannot change settings. It tells you the fix and the Settings page. These are the routes of the Settings pages:

| Page | Route |
| --- | --- |
| Models | `/settings/models` |
| Computers and zones | `/settings/computers` |
| Connectors | `/settings/connectors` |
| Web search | `/settings/web` |
| Vault | `/settings/vault` |
| Approvals | `/settings/approvals` |
| Budgets and usage | `/settings/budgets`, `/settings/usage` |
| Members and space | `/settings/members`, `/settings/space` |
| Account, notifications and tokens | `/settings/account`, `/settings/notifications`, `/settings/tokens` |
| Server settings and diagnostics (instance admins) | `/admin` |

## Report a problem

There are two ways to report a problem:

- Ask a bot. When the cause is a bug in Fettle, the bot offers to draft an issue. It calls `draft_issue`, and an issue card comes into the chat. The run does not wait for the card.
- Select **Help**, **Report a problem** at the bottom of the side bar. Type a title, what happened, what you expected and the steps.

Both add a diagnostics block: the version, the OS and architecture, the mode, the computer provider, the kinds of model providers, and the failed doctor checks (for an instance admin).

Before you see the draft, Fettle removes these details from it:

- email addresses, IP addresses and the user part of URLs with credentials
- host names, except public hosts such as `github.com`, `openrouter.ai` and the other model and search providers
- home folder paths (`/home/<name>`, `/Users/<name>`, `C:\Users\<name>`)
- your name, your email and the user name of the computer account that runs Fettle
- keys, tokens and vault values

Read the draft. Then select **Open on GitHub**. Your browser opens a new-issue page with the title and the text. Nothing goes to GitHub before you submit the issue there. Fettle needs no GitHub token for this.

A browser cannot open a very long link. When the draft is longer than the link can hold, the link has only the start of the text. Select **Copy the full text** and paste it on the GitHub page.

## Settings

| Setting | Default | What it does |
| --- | --- | --- |
| `fettle.issue_repo` | `alternayte/fettle` | The GitHub repository, as owner/name, that gets the issues. Change it in **Settings**, **Computers**, **This server** (instance admins) to send reports to a fork. |
