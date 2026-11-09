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
| Live docs (optional) | Tavily search through Bifrost's MCP gateway, off in private mode |

## Architecture

```
leantoken CLI ──> Bifrost gateway ──> Groq        (default)
                       │          ├──> fallback provider
                       │          ├──> Ollama      (private repos only)
                       │          └──> Tavily MCP  (docs lookup)
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
| Small-first routing | Planned |
| Footprint report | Planned |
| Benchmark vs a naive agent | Planned |
| Tavily docs lookup | Planned |

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
leantoken "fix the failing test in tests/test_api.py" --repo path/to/repo
leantoken "add input validation to create_user" --budget 0.50
leantoken "rename Config to Settings" --private
```

## Measurement

Energy is estimated from model size and tokens with the EcoLogits method; carbon is energy times grid intensity. Results are reported per completed task. A task only counts if its tests pass, and an escalated step is charged for both models. Benchmark numbers will be added here once the run is done; no savings are claimed before that.

## Tech

Python, httpx, Bifrost, Groq, Ollama, Tavily, EcoLogits, pytest.

## AI usage

- Groq-hosted LLMs run the agent; Ollama models run it for private repos.
- Tavily provides live documentation search.
- Claude (Claude Code) helped write the code and documentation. The team reviewed it and is responsible for all of it.
