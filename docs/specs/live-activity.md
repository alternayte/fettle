# Live activity

## What it does
A person sees what a bot does while it works, from the moment they send a message.
One live line under the bot's reply in the chat names the current step, for example "Reading example.com" or "Running `npm test`", and how long it has taken.
The line changes at each step and goes away when the reply is final. A click on the line opens the run timeline.
The run timeline shows where the time of a run went: the wait for a worker, the router, the first token of each model turn, and each tool.
The first reply comes sooner, because the router has a time limit and the model provider can cache the start of the prompt.

## Decisions
- A new chat stream event `activity` carries the run, the bot, a short label, the tool name when there is one, and the start time — the UI needs no poll and no journal read to show the line.
- The server publishes `activity` when it takes the message ("Thinking"), before the router call, before each model turn, before each tool, and when the run waits for a computer, a card or a helper — every silent gap gets a label.
- A label never contains tool arguments in full: it uses the host of a URL, the first line of a command cut to 60 characters, or the path of a file — the chat stream does not carry secrets or long text.
- A label is not a journal event — the journal records steps, and a label is only a view of the current step.
- The run line is published again after each tool result — the run line shows the current duration and cost during long tool chains.
- When the final message of a turn arrives, or the run ends, parks or fails, the server publishes an empty `activity` — the line goes away.
- The router call has a time limit, setting `agent.router_timeout` (default 4 s). When it passes, the step is `direct` and the journal records the reason — a slow router does not hold the first token.
- The `anthropic-messages` adapter sends the system prompt as blocks with `cache_control` on the last block, and the tool list with `cache_control` on the last tool — the stable start of the prompt is cached.
- The `openai-chat` adapter sends `cache_control` on the system message only when the provider is OpenRouter and the model is an Anthropic or Google model — other OpenAI-compatible servers can refuse the field.
- Each run keeps timings in its `run_started` and model-turn journal events: queue wait, router time and time to first token. The timeline shows them as a bar — a person or a bot can see if the model or Fettle is slow.

## Out
- A full list of tool steps in the chat. The timeline has it.
- Streaming of tool-call arguments.
- Caching on providers that need a separate cache API.

## How I know it works
- With the fake model that waits before its first token, the chat stream has `activity` "Thinking" before the first `delta`.
- A run that calls `exec` with `npm test` has an `activity` with the label "Running npm test" before the tool result, and a `run` event after it.
- A run line in the UI shows the live label and the elapsed seconds, and the label goes away when the final message arrives.
- A router that answers after 10 s makes the step `direct` after the time limit, and the reply streams.
- The Anthropic adapter request has `cache_control` on the last system block; the OpenRouter request for `anthropic/…` has it, and the OpenAI request has no `cache_control`.
- The timeline of a finished run shows the queue wait, the router time and the time to first token.
