# GUIDELINES

Theme: Build a Coding Agent That Gets the Job Done on the Fewest Tokens (Bifrost track).
Submission deadline: 15 Nov 2026, 11:59 PM. Judging: 29 Nov 2026.

## Submission

- Public GitHub repo: working code, README with project, features, setup, implementation, tech
- PPT (required): problem, solution, architecture, implementation, prototype demo
- If Tavily is used: tick the Tavily integration checkbox and explain what each technology contributes
- Demo video (optional)
- Declare AI tools used
- One submission per team, 1–4 members, everyone on the official Discord

## Judging

Innovation, technical execution, problem and impact, use of AI, user experience, scalability.

## Theme requirements

- Fewest tokens per completed task
- Keeps working through provider outages
- Enforces individual spending limits
- Confidential repos use local models only
- Approval before file writes, shell commands, git actions
- No loss of correctness, privacy or security

## Our additions

- Small model first, escalate only when a step fails
- Per-task report: tokens, cost, latency, kWh, gCO2e
- Benchmark against a naive agent before claiming any savings
- Optional Tavily docs lookup through Bifrost MCP

## Rules

- Every model call goes through Bifrost. No direct provider calls.
- Groq is the default provider; the user picks the model.
- Private repos use the Ollama-only virtual key; no web lookups.
- Keep code and docs short. Comments only where the why isn't obvious.

## Run

```
pip install -e '.[dev]' && pytest
npx -y @maximhq/bifrost
cp .env.example .env
slendertoken --pick-model
slendertoken "task" --repo path/to/repo --budget 0.50
slendertoken "task" --private
slendertoken "task" --no-route   # large model only, the baseline arm
```
