# AGENTS.md

## What this is
Fettle is an open-source team of persistent AI teammates, called bots, that work on sandboxed computers with a browser, a terminal and files, and ask before consequential actions.
One Go binary, fettle, is the server, the CLI and the desktop backend. A second binary, fettle-agentd, runs inside each computer.

## Run
- The repository holds no code yet. The build adds the justfile, and this section lists its recipes after that.

## Test
- No gate exists yet. The build adds just ci as the gate, and just check runs the same recipes.
- `checks/agents-md.sh` checks this file.

## Stack rules
- The repository is public. Tracked files, fixtures, tests, docs and commit messages contain no employer names, private hostnames, real people, real data or business material. Test data uses only the synthetic acme fixtures.
- Storage is SQLite by default, or PostgreSQL 16 or later. Every feature works on both.
- Data access and migrations use drel, which generates both dialects. Hot or dialect-specific paths use the drel raw SQL API. Do not use the drel outbox, inbox or event store.
- Durable runs use River in the same database plus the run journal. No Temporal, Redis or message broker. Only runs keep a journal; all other data is plain tables.
- A tool call with side effects writes an intent event before it runs and a result event after it. Never replay an intent that has no result.
- The agent loop runs in the server. Model keys and vault values never enter a computer or a model context.
- Providers only manage machine lifecycle. All other computer operations go to agentd over HTTPS with the computer token.
- The UI is one React SPA in the ui directory, embedded in the binary. UI tooling uses Bun. Take components from shadcn and theme them with the Fettle tokens.
- The desktop app uses Wails v3. The desktop UI reaches the local server over HTTP with the local token, never through Wails bound methods.
- Core code is AGPL-3.0-only. Enterprise code lives in the ee directory behind the build tag ee, with its own licence. Core packages never import ee. The sdk and templates directories are Apache-2.0.
- Add a dependency only when its licence is compatible with AGPL-3.0.
- Tests never call a real model or a real third-party service. They use the fakes in internal/fakes and testcontainers.
- A Go scenario test is named TestSCN_AREA_nnn_Words. A Playwright scenario test starts with its scenario ID.
- Write docs, UI copy, default bot prompts and commit messages in ASD-STE100 style.

## Domain words
- space: an authorization boundary that holds bots, chats, zones, skills, memory and connectors.
- bot: a persistent teammate with a name, a job, instructions, model choices, memory, a zone, skills and routines. Avoid: agent.
- chat: one conversation thread, direct with one bot or a group with 2 to 6 bots.
- envelope: one unit of work with a goal, an owner, acceptance checks, a budget and capabilities. Avoid: task.
- run: one durable execution of an envelope by its owner bot, a River job plus its journal.
- journal: the append-only list of the steps of a run. It is the checkpoint and the timeline of the run, not event sourcing.
- step: one topology choice on the ladder, from direct to hire.
- sub-agent: a short-lived helper of a run, with no chat and no memory.
- verifier: the role that checks a result against the acceptance checks of its envelope.
- zone: a trust zone, which is a set of credential grants, a network policy and one computer.
- computer: the sandbox of a zone, a machine from a provider that runs the Fettle computer image. Avoid in UI copy: sandbox, VM.
- workspace: the durable files of a bot or of a zone.
- provider: a computer backend, such as host, docker, applecontainer, kiln or e2b.
- agentd: the Go daemon inside each computer that runs commands, files, the browser and the screen.
- grant: a permission for a zone to use one vault item or one connector account.
- skill: a SKILL.md folder with instructions and optional files.
- routine: a skill or prompt that runs on a schedule or a webhook.
- steering signal: a human action that shows a bot was wrong or incomplete.
- card: an interactive chat item, such as an approval, a question or a proposal.
