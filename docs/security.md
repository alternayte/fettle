# Security

This page describes how Fettle keeps secrets, limits bots and records their work.

## Where code runs

- The model loop of each run runs in the server. Model calls go from the server to the model provider.
- Bots run commands, files and the browser in computers.
- Providers only start, stop and remove machines.
- All other computer operations go to `fettle-agentd` over HTTPS with the computer token.
- `fettle-agentd` removes its secret variables from its environment after it reads them. No child process sees them.
- As root, `fettle-agentd` applies the network rules of the zone. Then it drops to the user `fettle` for good.
- The computer image has no `sudo` and no `su`.

## Secrets

- Model keys and vault values never enter a computer or a model context.
- The vault holds logins, secrets and one-time code seeds. After a save, the UI shows only the last four characters of a value.
- Bots use vault items through broker tools: `fill_login`, `totp_code` and `use_secret`.
- The broker releases an item only to a bot whose zone holds a grant for that item.
- The browser fills a login only after it checks the origin of the page.
- Fettle replaces known secret values with `[secret:<name>]`. This applies to logs, journals and model context.
- The replacement knows every vault value of a space, the model keys, the computer tokens and the OAuth tokens.
- If Fettle cannot read the vault, the replacement returns a fixed text instead of the original text.

## Encryption

- Each space has its own data keys. Fettle encrypts vault items, connector tokens, skill archives and webhook secrets with them.
- The master key wraps the data keys.
- In local mode, the master key is in the system keychain. When no keychain works, it is `master.key` with mode 0600 in the data folder.
- In server mode, `FETTLE_MASTER_KEY` holds the master key: base64 of 32 bytes, or `file:<path>`.
- `fettle vault rekey` wraps all data keys with a new master key in one transaction.
- `fettle doctor` checks that the master key opens every data key.

## Sign-in

- In local mode, the server listens only on a loopback address. The browser exchanges the local token for a cookie.
- In server mode, people sign in with an email and a password.
- A session ends after 12 hours without use, and after 30 days at most.
- Fettle limits sign-ins to 10 per minute from one address, and 10 per 15 minutes for one email.
- Personal access tokens start with `fet_`. Fettle shows a token one time.
- A space is the authorization boundary. A person has the role `owner` or `member` in a space.

## Approvals

Each action has a class.

| Class | Meaning |
| --- | --- |
| `read` | Reads data. |
| `write_local` | Changes data in the computer. |
| `write_external` | Changes data outside the computer. |
| `send` | Sends a message to a person. |
| `spend` | Pays or buys. |
| `admin` | Changes bots, routines or settings. |

A browser click on a button, a link or a menu item gets a class.
The class comes from the name of the item and its form:

- Pay, buy, purchase or checkout is `spend`.
- Send, post, publish or reply is `send`.
- Submit, delete, confirm, order or remove is `write_external`.
- Another form submission is `write_external`.
- All other browser actions are `read`.

### Approval modes

The space setting `approval.mode` has three values. The default is `ask`.

| Mode | Effect |
| --- | --- |
| `ask` | Bots ask before each change outside the computer and each message they send. |
| `auto` | Bots act without asking when the action clearly matches what you asked. Else they ask. The verifier role checks the match. |
| `never` | Bots act without asking. Hard asks still need a card. |

A change of the approval mode needs a card.

### Order of decisions

Fettle decides each action in this order:

1. A `spend` action is blocked. Payments and purchases do not work in this version.
2. A hard ask always needs a card, in every mode.
3. A block rule blocks the action.
4. After a run reads outside content, a memory write or an `admin` action needs a card.
5. An ask rule makes a `write_external`, `send` or `admin` action need a card.
6. An allow rule lets a `write_external` or `send` action run.
7. A `read` or `write_local` action runs.
8. An `admin` action needs a card. In `never` mode, an allow rule lets it run.
9. A `write_external` or `send` action of work that no person started needs a card.
10. Else the approval mode decides.

### Hard asks

These changes always need a card in the chat of the primary bot:

- Create a zone.
- Give a zone a vault item or a connector account.
- Turn on the `host` provider.
- Change the approval mode.
- Make a bot the primary bot.
- The first external send of a new routine.

A hard-ask card has no **Always allow** button.

### Rules

A rule has an effect: `allow`, `ask` or `block`.
A rule matches a tool, a connector, a site pattern or a recipient pattern.
A tool name without a connector prefix also matches the tool of a connector.
Patterns use shell glob syntax, for example `*.acme.test`.
**Always allow** on a card makes an allow rule.

## Journal

- Each run keeps a journal. The journal is the append-only list of the steps of the run.
- A tool call with side effects writes an intent event before it runs and a result event after it.
- After a restart, Fettle marks an intent without a result as uncertain. Fettle never runs it again.

## Audit log

- Fettle records changes in the audit log of the space. Examples are changes of bots, zones, grants, the vault, connectors, rules, skills, routines, members and settings.
- The API has no route that changes the audit log.
- The Enterprise build can export the audit log. Read [enterprise.md](enterprise.md#audit-log-export).

## Webhooks

- A webhook routine checks the HMAC-SHA256 signature of the body, or its secret, before Fettle parses the body.
- An unknown routine ID and a bad signature get the same answer.

