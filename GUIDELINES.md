# GUIDELINES

Theme: Build a Coding Agent That Gets the Job Done on the Fewest Tokens.
Deadline: 15 Nov 2026, 11:59 PM.

## Hackathon requirements

- Public GitHub repo with detailed README: project, install, implementation, tech used
- Pitch deck: problem, solution, architecture, working prototype
- Project description, problem statement, tech list, how AI is used, AI tools declared
- Working prototype or demo (video optional)
- One submission per team, max 4 members, primary theme selected in the form

## Theme requirements

- Fewest tokens per completed task
- Keeps working through provider outages
- Enforces individual spending limits
- Confidential repos use local models only
- Approval before file writes, shell commands, git actions

## Run

```
npx -y @maximhq/bifrost
pip install -e '.[dev]' && pytest
export BIFROST_URL=http://localhost:8080 BIFROST_VK=<virtual key>
leantoken "task" --repo path/to/repo --budget 0.50
leantoken "task" --private
```

Env: `LEANTOKEN_MODEL`, `LEANTOKEN_FALLBACKS` (comma list), `LEANTOKEN_LOCAL_MODEL`.

## Repo rules

- Conventional Commits, no co-author lines, no em dashes
- Tests for approval, budget, path safety
- Declare AI-assisted code in README
- Never commit keys
