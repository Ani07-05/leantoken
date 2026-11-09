# leantoken

A coding agent that finishes real dev tasks on the fewest tokens, and reports what each task cost in money, energy and carbon.

Built for the Geek Room Global AI Hackathon, theme: *Build a Coding Agent That Gets the Job Done on the Fewest Tokens*. Every model call goes through the [Bifrost](https://github.com/maximhq/bifrost) gateway.

## Problem

Coding agents read whole files, resend long histories and use the largest model for every step. Most steps don't need that. The cost shows up on the bill; the energy and carbon never show up at all. Teams also need agents that survive provider outages, respect spending limits, keep private code private and never touch files or git without asking.

## Features

| Need | How leantoken does it |
|---|---|
| Fewest tokens | Ranged file reads, capped search hits, exact-match edits, clipped tool output, old tool results trimmed, terse prompt, step limit |
| Right-sized model | Small model first; escalates to a larger one only when a step fails |
| Survives outages | Bifrost fails over to a second provider |
| Spending limits | Each user has a Bifrost virtual key with its own budget and rate limit |
| Private repos | `--private` uses a virtual key that only allows local Ollama; Bifrost refuses any cloud call |
| Safe actions | Asks before file writes, edits, git actions and any shell command that isn't read-only |
| Footprint report | Tokens, cost, latency, energy (kWh) and carbon (gCO2e) per task |
| Live docs (optional) | Tavily `web_search` tool, short answer plus 3 clipped results; off in private mode and when no key is set |

## Architecture

```
leantoken CLI ──> Bifrost gateway ──> Groq        (default)
                       │          ├──> fallback provider
                       │          ├──> Ollama      (private repos only)
                       │          └──> Tavily API  (docs lookup, direct from the agent)
                       └─ virtual keys: budget, rate limit, allowed providers
```

1. The agent sends each step to the small model through Bifrost, with the user's virtual key.
2. Tools run locally inside the repo. Anything risky waits for approval.
3. If a step fails (tests fail, bad tool call, repeated error), the agent switches to the larger model.
4. Bifrost handles retries, fallbacks and budgets, and logs every request.
5. At the end, leantoken prints the task's tokens, cost, models used, energy and carbon.

## Status

| Part | State |
|---|---|
| Agent loop, tools, approval gate, context trimming | Done |
| Groq model picker (via Bifrost) | Done |
| All calls through Bifrost | Done |
| Small-first routing | Done |
| Footprint report | Done |
| Benchmark vs a naive agent | Done, see Benchmark |
| Tavily docs lookup | Done (`web_search` tool, direct Tavily API) |
| Terminal UI | Done (`leantoken` with no task) |

## Install

Requires Python 3.10+ and Node 18+.

```
git clone <repo> && cd leantoken
pip install -e '.[dev]'
npx -y @maximhq/bifrost        # gateway + UI on http://localhost:8080
```

In the Bifrost UI:
1. Add the Groq provider with your Groq API key.
2. Create a virtual key with a budget (for example $1 per day).
3. For private repos, add Ollama and a second virtual key that allows only Ollama.

```
cp .env.example .env           # set BIFROST_VK (and BIFROST_PRIVATE_VK)
leantoken --pick-model         # choose a Groq model
```

## Usage

```
leantoken                      # full-screen TUI (OpenCode-style): /help, !cmd, ctrl+b sidebar, ctrl+o models
leantoken "fix the failing test in tests/test_api.py" --repo path/to/repo
leantoken "add input validation to create_user" --budget 0.50
leantoken "rename Config to Settings" --private
leantoken "..." --model groq/openai/gpt-oss-120b --small groq/openai/gpt-oss-20b
leantoken "..." --no-route     # large model only
```

The small model (default `groq/openai/gpt-oss-20b`) runs first. leantoken switches to the large model for the rest of the task when:
- the small model is not done after 8 steps
- 2 tool calls in a row fail
- tests fail twice after an edit
- it says it is done while tests still fail
- Groq rejects its tool call

Each run prints a footprint report and saves it, with a per-step trace, to `<repo>/.leantoken/runs/`.

## Token savings

Every task reports how many prompt tokens it saved against a naive agent on the same run. The naive twin of each request reads whole files instead of line ranges, keeps tool output unclipped and never trims old tool results. Each naive tool output is capped at 30,000 characters, roughly what real agents truncate at, so one giant listing cannot inflate the figure. We measure the size of each tool output both ways, then convert the extra characters to tokens at that request's real tokens-per-character ratio (from the API's `usage`). Output tokens are not counted as saved. The energy and carbon saved are scaled from the token saving, assuming energy per token stays constant, because the energy model ignores input tokens; treat them as rough estimates. The shorter system prompt and tool schemas are not counted either, so the figure is conservative. In a long session the gap grows: leantoken carries a compact note of earlier turns (question, short answer, files touched) while the naive twin resends the whole earlier transcript every turn. The TUI plots both per turn. It is an estimate of one run, not a benchmark: the benchmark against a real naive agent is separate.

## Benchmark

Same model (`groq/openai/gpt-oss-120b`), same tasks, same Groq free-tier limits, every call through Bifrost. Token counts are the API's own `usage`. The naive arm uses the same loop and tools but reads whole files, keeps tool output unclipped, never trims history and resends the full transcript on every turn. Reproduce with `.venv/bin/python bench/run.py`; raw numbers are in `bench/results.json`. The chart is generated from those numbers with `.venv/bin/python bench/chart.py`.

![Token efficiency: leantoken vs a naive agent](bench/token-efficiency.svg)

**Coding: fix a seeded bug in a small shop library** (pass = `pytest` is green afterwards)

| Task | leantoken | naive | fewer tokens |
|---|---|---|---|
| discount | 9,287 | 25,641 | 63% |
| restock | 4,276 | 7,622 | 43% |
| cart | 4,385 | 8,309 | 47% |
| month | 4,754 | 8,860 | 46% |
| **total** | **22,702 ($0.0030)** | **50,432 ($0.0065)** | **54%** |

Both arms fixed 4 of 4.

**Session: 8 follow-up questions about the same project**

| After turn | leantoken (cumulative) | naive (cumulative) |
|---|---|---|
| 1 | 968 | 3,480 |
| 3 | 4,724 | 12,654 |
| 5 | 9,394 | 28,810 |
| 6 | 12,710 | stopped: Groq rejected a 19,450-token request (413) |
| 8 | 15,984 | did not finish |

leantoken answered 7 of 8 checks. The miss is turn 7: it answered `YYYY-MM`, which is correct, but our keyword check wanted the words "year" and "month"; we kept the check as written. The naive agent answered 5 of 5 before it failed, then could not continue: its transcript outgrew the provider's per-request limit.

Limits: one run per arm, one model, one small synthetic project, free-tier rate limits. On a paid tier the naive agent would not have crashed, it would just have cost more. Treat the percentages as indicative, not general.

## Measurement

Energy uses the [EcoLogits](https://ecologits.ai/latest/methodology/llm_inference/) LLM inference formula: GPU energy from active parameters and output tokens, server energy from request latency, times PUE 1.2. Carbon is energy times the grid intensity of the Groq region that served the call (`X-Groq-Region`; Our World in Data 2023). EcoLogits does not cover Groq and models H100 servers, not Groq LPUs, and it ignores input tokens for GPU energy, so the numbers are comparable estimates, not measurements. Cost uses Groq list prices; Bifrost's logs hold the authoritative cost. Results are reported per completed task. A task only counts if its tests pass, and an escalated step is charged for both models. The Benchmark section above reports one run per arm; the energy and carbon figures there are scaled from token counts, so read them as estimates.

## Tech

Python, httpx, Textual, Bifrost, Groq, Ollama, Tavily, EcoLogits, pytest.

## AI usage

- Groq-hosted LLMs run the agent; Ollama models run it for private repos.
- Tavily provides live documentation search.
- Claude (Claude Code) helped write the code and documentation. The team reviewed it and is responsible for all of it.
