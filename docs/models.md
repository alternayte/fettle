# Models

Bots use your own model keys.
Fettle keeps each key encrypted in the vault of the space.
The model loop of each run runs in the server. A key never enters a computer or a model context.

Add model providers in **Settings**, **Models** (`/settings/models`).

## API types

A model provider uses one of three API types.

| API type | API |
| --- | --- |
| `openai-chat` | The OpenAI Chat Completions API, and servers that copy it. |
| `openai-responses` | The OpenAI Responses API. |
| `anthropic-messages` | The Anthropic Messages API. |

## Presets

A preset fills the provider form.

| Preset | API type | Base URL | Key style |
| --- | --- | --- | --- |
| OpenRouter | `openai-chat` | `https://openrouter.ai/api/v1` | bearer |
| OpenAI | `openai-responses` | `https://api.openai.com/v1` | bearer |
| Anthropic | `anthropic-messages` | `https://api.anthropic.com/v1` | `x-api-key` header |
| Google | `openai-chat` | `https://generativelanguage.googleapis.com/v1beta/openai` | bearer |
| DeepSeek | `openai-chat` | `https://api.deepseek.com/v1` | bearer |
| Ollama | `openai-chat` | `http://127.0.0.1:11434/v1` | no key |
| LM Studio | `openai-chat` | `http://127.0.0.1:1234/v1` | no key |
| OpenAI-compatible | `openai-chat` | your URL | bearer |

The key styles are `bearer`, `x_api_key`, `header` with a header name, and `none`.
A provider can also send extra headers.

## Provider test

Fettle tests a provider with four checks:

1. Plain answer.
2. Tool call round trip.
3. Structured output that matches a JSON Schema.
4. Image input.

A pass of the image check marks the model as able to read images.
A provider that fails the tool call check cannot fill the owner role.
A change of the URL, the API type or the key clears the test result.

## Roles

Each role has an ordered list of models.

| Role | Work |
| --- | --- |
| `owner` | Does the work of each run. |
| `router` | Picks how to do the work. |
| `verifier` | Checks a result against the acceptance checks of its envelope. |
| `computer_use` | Uses the screen of a computer. |
| `summarizer` | Makes long runs shorter. |
| `transcribe` | Turns spoken teach notes into text. |

A role without a model uses the owner models. The `transcribe` role is the exception: without a model, it has no model, and the teach view shows no microphone.
The `transcribe` role needs an OpenAI-compatible provider, because Fettle calls its `/audio/transcriptions` endpoint from the server.
Bots cannot work until the owner role has a model.
A bot can override a role. Open the settings page of the bot and go to **Models**. Pick a model for the role, then select **Use for this bot**. **Use the space models** removes the override. In the API, `PUT /api/v1/spaces/{space}/model-roles/{role}` with `bot_id` sets it, and an empty list removes it.

## Fallback

Fettle calls the models of a role in order.
A model can fail with a 429 answer, a 5xx answer or a timeout before the first token.
Then Fettle tries the same model 2 more times.
Then Fettle uses the next model of the role.
A stream that fails after its first token does not get a retry.
The run timeline records each switch to the next model.

## Router time limit

Before a bot starts new work, the `router` role picks how to do the work.
The space setting `agent.router_timeout` sets the time limit of this call. The default is `4s`.
When the router does not answer in this time, the bot does the work itself (the step is **Work**), and the reply starts at once.
The `route_decision` row of the run timeline then shows the rule `router_timeout`.
To make the router fast, give the `router` role a small, fast model.

## Prompt caching

Fettle marks the start of each prompt as a cache point, so that the provider can keep it for the next turns of a run.
A cached start makes the first token come sooner and costs less.

- `anthropic-messages`: Fettle sends a cache point after the tool list and after the system prompt.
- `openai-chat` with OpenRouter: Fettle sends a cache point after the system prompt for Anthropic models (`anthropic/…`) and Google models (`google/…`). OpenRouter sends it to the model provider.
- Other providers: Fettle sends no cache point. OpenAI and DeepSeek cache the start of a prompt automatically.

The time of day is after the cache point, so a change of the time does not stop the cache.
Fettle records the cached input tokens of each call as cache reads in the usage export.

## Budgets

Each envelope has a budget. These space settings set the defaults:

| Setting | Effect | Default |
| --- | --- | --- |
| `budget.envelope_usd` | USD for one piece of work. | 2.0 |
| `budget.envelope_tokens` | Tokens for one piece of work. | 2000000 |
| `budget.envelope_wall_ms` | Time limit for one piece of work, in milliseconds. | 3600000 |
| `budget.hops` | How many times work can pass from bot to bot. | 4 |
| `budget.max_subagents` | Sub-agents that one piece of work can start. | 5 |
| `budget.bot_day_usd` | USD per bot per day. | 10.0 |
| `budget.space_month_usd` | USD per space per month. | 100.0 |
| `agent.max_turns` | Model turns before a bot stops and asks you. | 200 |

**Settings**, **Cost** shows the use of a space by run, bot, step, role, model, day or month.
