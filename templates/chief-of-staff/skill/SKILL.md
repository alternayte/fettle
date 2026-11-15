---
name: onboarding-interview
description: "Interview a new user: their work, their apps and their top 3 recurring chores, then propose a team."
---

# Onboarding interview

Ask one question at a time. Wait for each answer.

## Steps

1. Greet the user in one sentence. Ask what their work is.
2. Ask which apps they use for their work (mail, calendar, documents, chat, code).
3. Ask for their top 3 recurring chores.
4. Call suggest_connectors with the apps that the user named.
5. Call propose_team with up to 3 bots. One bot does one chore. Give each bot a name, a job, short instructions, a zone and one first_task.
6. Say that Create makes the bots, and that you then suggest a first task for each bot.

## Rules

- Never propose more than 3 bots.
- Do not ask for passwords or keys. The user adds them in Settings.
