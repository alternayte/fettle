# Self-help

## What it does
A bot knows Fettle. It can read the Fettle docs, check the health of the instance and explain its own setup, so a person can ask "Why can't you use the browser?" or "How do I add a model?" in the chat.
A bot can draft a GitHub issue about Fettle. A card shows the draft, and "Open on GitHub" opens a filled new-issue page in the person's browser.
The help menu of the UI has "Report a problem", which opens the same draft without a bot.

## Decisions
- The binary embeds the docs folder — a bot reads the docs of the version that runs, with no network.
- `fettle_docs` (class `read`) lists the pages, searches them for words, or returns one page or one section — a bot can find an answer without loading all docs.
- `fettle_status` (class `read`) returns the Fettle version, the OS and architecture, the mode (local, desktop or server), the doctor checks with pass or fail and the reason, the bot's own setup (its model targets per role, zone, network policy, computer provider and state, skills, connectors, web search) and the last 5 failed runs of the bot with their error — a bot can see what is wrong with its own setup.
- `fettle_status` never returns a secret, a vault value or a key, and the doctor results go through the redactor — model keys and vault values never enter a model context.
- The doctor checks in `fettle_status` have a time limit of 10 s in total, and their result is kept for 60 s — a status question does not start many network checks.
- A member who is not an instance admin gets the space-level part only: no instance checks and no instance settings — the bot does not show a member what the Diagnostics page hides from them.
- Bots do not change Fettle settings. They explain the fix and give the Settings page by its route, for example `/settings/models` — a wrong change cannot reach the instance through a chat.
- The base rules tell each bot to use `fettle_docs` and `fettle_status` for questions about Fettle and for its own errors, and to offer `draft_issue` when the cause is a Fettle bug.
- `draft_issue` (class `read`) takes a title, what happened, what the person expected and the steps, and posts an issue card. The card adds a diagnostics block: version, OS, architecture, mode, computer provider, model provider kinds and the failed doctor checks — a report has the facts a maintainer needs.
- The diagnostics block and the bot's text go through an issue redactor before the card shows them: it removes email addresses, IP addresses, host names that are not public model or provider hosts, home folder paths, user names, URLs with credentials and long tokens — the repository is public, and a person can use Fettle on a work machine.
- The person reads the draft in the card. "Open on GitHub" opens `https://github.com/<repo>/issues/new` with the title and body in the query, in a new tab. Nothing goes to GitHub before the person submits there — no token is needed and the person sees each word.
- The repository is the instance setting `fettle.issue_repo` (default `alternayte/fettle`) — a fork can get its own reports.
- When the URL would be longer than 7,500 characters, the body is cut and the card has "Copy" for the full text — browsers and GitHub refuse very long URLs.
- The help menu "Report a problem" calls `GET /issue-draft`, which returns the same redacted diagnostics, and shows the same form with empty text fields.

## Out
- Changes to settings by a bot.
- A GitHub token, or an issue that the server creates.
- Log upload.

## How I know it works
- A bot asked "How do I add an OpenRouter key?" calls `fettle_docs` and answers with the steps and `/settings/models`.
- A bot with no computer provider calls `fettle_status`, and the result names the failed check and the reason; the result contains no key value.
- A member who is not an admin gets no instance checks from `fettle_status`.
- `draft_issue` posts a card whose body contains the version and no email address, IP address, home path or private host name from the bot text or the diagnostics.
- The card URL opens the new-issue page of `fettle.issue_repo` with the title and body, and a long body is cut below 7,500 characters.
- "Report a problem" in the help menu opens the form with the diagnostics.
