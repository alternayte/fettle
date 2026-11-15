# Skills and routines

## Skills

A skill is a folder with a `SKILL.md` file and optional files.
Each space has a skill library. Open it in **Skills**.

### SKILL.md

`SKILL.md` starts with YAML frontmatter. The frontmatter needs a name and a description.

```markdown
---
name: weekly-report
description: Write the weekly report from the work and the notes of the week.
---

1. Read the finished runs of the week.
2. Write the report with one section for each project.
```

- The name has 1 to 63 characters: lowercase letters, digits and hyphens. It starts with a letter or a digit.
- A skill folder is at most 20 MiB.
- A save with a name that exists makes a new version of that skill.

### Bots and skills

- Give a bot a skill on the skill page.
- A bot with a skill sees its name and description in its skill index.
- The bot loads the full skill when the work needs it.
- Fettle then copies the files of the skill to `.skills/<name>/` in the home of the bot.
- In a chat, type `/<name>` to start work with a skill.

### Make a skill

- Write `SKILL.md` in **New skill**.
- Import a zip of a skill folder.
- Select **Save as skill** on a finished run. Fettle writes a draft from the steps of the run. Read and change the draft, then save it.

**Export zip** writes the current version of a skill as a zip.

### Evals

A skill can hold `evals.json` in the skill-creator format.
Fettle runs the cases of a version and the verifier role grades them.
A case passes when at least half of its trials meet all expectations.

### Skael sync

Fettle can pull a set of skills from a Skael registry on a schedule.
Put the Skael API key in the vault as a secret first.
These space settings control the sync:

| Setting | Effect | Default |
| --- | --- | --- |
| `skael.url` | The Skael registry URL. | none |
| `skael.set` | The set to pull. | none |
| `skael.key_item` | The name of the vault secret with the API key. | none |
| `skael.sync_minutes` | Minutes between syncs. | 60 |

Skael manages the skills that it syncs. Change them in Skael, then sync.
Fettle tells Skael when a bot loads a skill. It sends hashes of the space and the bot, not their IDs.

### Skill patches

Steering signals show that a bot was wrong or incomplete.
A daily job looks for skills with 3 or more signals in 14 days.
For each skill, the owner role writes a small patch.
Fettle runs the evals of the skill on the patch.
A patch that passes the evals becomes a card in the chat of the primary bot.
The skill changes only after a person allows the card.

## Templates

Fettle includes five bot templates. Each template has a skill and a routine. The routine starts paused.

| Template | Bot | Job | Zone |
| --- | --- | --- | --- |
| `chief-of-staff` | Chief | Routes work, answers questions and reports back. | `personal` |
| `inbox` | Inbox | Sorts mail and writes reply drafts. | `personal` |
| `research` | Research | Finds sources and writes short summaries. | `browsing` |
| `weekly-report` | Reporter | Writes the weekly report. | `personal` |
| `pr-reviewer` | Reviewer | Reviews pull requests. | `personal` |

```sh
fettle bot create --template research
```

## Routines

A routine gives a bot work on a schedule or from a webhook. Results go to the chat of the bot.
Open routines in **Routines**.

A routine has these fields:

| Field | Values |
| --- | --- |
| Bot | The bot that does the work. |
| Name | A name. |
| Prompt | What the bot does, written as a message to the bot. |
| Trigger | `cron` or `webhook`. |
| Schedule | A cron line, for example `0 9 * * 1-5` for 09:00 on work days. |
| Time zone | The time zone of the schedule. The default is UTC. |
| Notify | `always`, `on_failure` or `never`. The default is `on_failure`. |

### Missed ticks

Ticks that come due while Fettle does not run become one firing at the first due time.
Fettle records the other ticks as coalesced.
The routine page shows the 20 newest firings.

### Webhooks

A webhook routine has a secret. Fettle shows the secret one time, when you create the routine.
Call the routine with `POST /hooks/<routine-id>`. The request body becomes the data of the work.
Sign the request in one of two ways:

- Send `X-Fettle-Signature: sha256=<hex>`, the HMAC-SHA256 of the body with the secret.
- Send the secret in `X-Fettle-Secret`.

```sh
body='{"ticket":"ACME-42"}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* //')
curl -X POST "https://fettle.acme.test/hooks/$ROUTINE_ID" -H "X-Fettle-Signature: sha256=$sig" -d "$body"
```

### Approvals of routines

No person starts the work of a routine.
So each action that changes something outside the computer or sends a message needs a card.
An allow rule can let such an action run. Read [security.md](security.md#rules).
The first external send of a new routine always needs a card.
A card of such work expires after 30 minutes. The space setting `cards.expire_minutes` changes this.

### Limits

The space setting `routines.max_per_bot` limits the routines of one bot. The default is 50.
Archiving a bot pauses its routines.
