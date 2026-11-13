<div align="center">

<pre>
█▀▀▀ █    █▀▀█ █▀▀▄    █ █▀▀█ █▀▀▄ ▀█▀▀ █▀▀█ █ ▄▀ █▀▀█ █▀▀▄
▀▀▀█ █    █▀▀▀ █  █ █▀▀█ █▀▀▀ █     █   █  █ █▀▄  █▀▀▀ █  █
▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀  ▀ ▀▀▀▀ ▀▀▀▀ ▀     ▀▀▀ ▀▀▀▀ ▀  ▀ ▀▀▀▀ ▀  ▀
</pre>

**A coding agent for your terminal that finishes the job on the fewest tokens, then shows what the task cost in money, energy and carbon.**

![go](https://img.shields.io/badge/go-1.22+-00add8?style=flat-square)
![gateway](https://img.shields.io/badge/gateway-Bifrost-808080?style=flat-square)
![providers](https://img.shields.io/badge/providers-16-5fd787?style=flat-square)

[slendertoken.vercel.app](https://slendertoken.vercel.app) · [Pitch deck (PDF)](slendertoken-deck.pdf)

</div>

## Install

```
curl -fsSL https://raw.githubusercontent.com/Ani07-05/slendertoken/main/install.sh | sh    # macOS / Linux
irm https://raw.githubusercontent.com/Ani07-05/slendertoken/main/install.ps1 | iex         # Windows
```

To remove slendertoken on macOS or Linux, run `curl -fsSL https://raw.githubusercontent.com/Ani07-05/slendertoken/main/uninstall.sh | sh`. It asks before removing the executable and `~/.slendertoken`, including saved provider keys and Bifrost data. Add `-s -- --yes` to the `sh` command to skip the prompt, or set `SLENDERTOKEN_BIN_DIR` if you used a custom install directory.

One command, no Python, Node or Go. It:

1. Downloads the `slendertoken` binary and checks its SHA-256.
2. Runs `slendertoken setup`: pick a provider, paste your own API key (input hidden).
3. Installs [Bifrost](https://github.com/maximhq/bifrost), the gateway every model call goes through, and starts it headless in the background on `127.0.0.1`.
4. Saves the settings and sends one tiny request to prove it works.

Then run `slendertoken` in any repo. Set your provider's key first (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, ...) to skip the prompt, or `SLENDERTOKEN_NO_SETUP=1` to install only. From source: `go build -o slendertoken ./cmd/slendertoken`.

## Use

```
slendertoken                          # full-screen app; /help lists everything
slendertoken "fix the failing test"   # one-shot run, prints a footprint report
slendertoken "..." --private          # local model only, nothing leaves the machine
slendertoken "..." --model anthropic/claude-sonnet-5-5 --small anthropic/claude-haiku-4-5-20251001
```

In the app: `shift+tab` cycles permission modes, `esc` interrupts, `ctrl+o` picks models, `ctrl+s` opens settings, `ctrl+b` hides the sidebar, `!cmd` runs a shell command, `/theme` switches themes, `/stats` shows savings.

Edits and shell commands wait for your approval, shown as a numbered diff. When a task is ambiguous the agent asks you a multiple-choice question. With a `TAVILY_API_KEY` it can also search the web.

## Providers

Any provider Bifrost accepts with just a key: OpenAI, Anthropic, Google Gemini, OpenRouter, Groq, Mistral, DeepSeek, xAI, Cerebras, Fireworks, Perplexity, Cohere, Nebius, Parasail, Hugging Face and MiniMax.

```
slendertoken setup --provider openai    # skip the menu
slendertoken provider add anthropic     # add or replace one later (also: list, remove)
slendertoken bifrost status             # also: start, stop, logs, path
```

Your keys live only in Bifrost's private config under `~/.slendertoken/bifrost/` (mode 600). Bring your own key; you pay the provider directly. Daily budget on the virtual key: `slendertoken setup --budget 5` (default $2). Already run your own Bifrost? Skip `setup` and enter its URL and virtual key in Settings.

## How it saves tokens

- Reads 100-line ranges, never whole files; search stops at five hits per file; tool output is clipped.
- Only the two newest tool results stay in full; older ones shrink to a stub.
- A small model works first and the large one takes over when a step fails, tests stay red, or a tool call is rejected.
- Edits replace one exact, unique string.

Each task prints and saves a footprint report (tokens, cost, latency, energy, carbon) to `<repo>/.slendertoken/runs/`. Energy uses the [EcoLogits](https://ecologits.ai/latest/methodology/llm_inference/) method and is an estimate. The app also shows the tokens saved against a naive twin of each request (whole-file reads, unclipped output, full history).

## Private mode

For repos that must not leave your machine: one local Ollama model through Bifrost, no cloud provider, no `web_search`, and common network commands blocked in the shell (best effort, not a sandbox).

```
ollama pull qwen2.5-coder:7b
slendertoken config set BIFROST_PRIVATE_VK <ollama-only key>
slendertoken --private                  # or: touch <repo>/.slendertoken-private
```

## Settings

`~/.slendertoken/.env`, overridden by a `.env` in the current folder and then by real environment variables. `slendertoken config` lists what is in effect and where each value comes from (secrets masked); `config set|unset|edit|path` change them.

## Tech

Go, Bubble Tea, Lip Gloss, Glamour, Bifrost, Ollama, Tavily, EcoLogits. Built for the Geek Room Global AI Hackathon.
