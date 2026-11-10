import json
import os
import re
import time
from pathlib import Path

import httpx

from .budget import Budget, BudgetExceeded
from .footprint import Footprint
from .gateway import bifrost_url, groq_model, headers, local_model
from .tools import Workspace, describe, needs_approval, schemas, shell_cmd, unrunnable

SYSTEM_NAIVE = "You are a coding assistant. Use the tools to complete the task and reply when done."
SYSTEM = (
    "You are a terse coding agent. Use list_files, search and ranged read_file, never read whole files. "
    "Never grep, find or cat in the shell; search is ripgrep. Prefer edit_file over write_file. Run tests to verify. Stop when done and reply in one line."
)
# a question gets no write tools, so asking about the code can never change it
SYSTEM_ASK = (
    "You answer questions about a code repo. Use list_files, search and ranged read_file; read only what you need. "
    "Do not change files. Answer in a few short sentences or bullets."
)
ASK = re.compile(r"^(what|why|how|where|which|who|when|explain|describe|summari[sz]e|tell me|show me|walk me|"
                 r"give me an overview|is|are|does|do|can you (explain|tell|describe|summari[sz]e|show))\b", re.I)
CHANGE = re.compile(r"\b(fix|add|change|edit|write|create|implement|refactor|rename|remove|delete|update|make|build|"
                    r"generate|replace|convert|install|migrate|bump|commit)\b", re.I)


KEEP_FULL = 2  # older tool results shrink to a stub; every kept result is re-sent on every later call
ELIDE_TO = 120
# the naive twin gets a generous output cap too, so one giant listing cannot inflate the savings figure
NAIVE_CAP = 30_000
# Groq free tier caps tokens per minute per model, so a rate-limited call retries on another model's budget
GROQ_CHAIN = ("groq/openai/gpt-oss-120b", "groq/qwen/qwen3.8-27b", "groq/openai/gpt-oss-20b")
RATE_LIMIT_WAITS = 4  # Groq free tier allows 8k tokens/min on the large model
# right-size routing: the small model handles the task until one of these trips, then the large one finishes it
SMALL_MAX_STEPS = 8  # small model still not done after this many steps
LARGE_STEPS = 10  # steps the large model gets after an escalation
MAX_TOOL_ERRORS = 2  # consecutive invalid tool calls or tool errors
MAX_TEST_FAILS = 2  # failing test runs after the first edit
TEST_CMD = re.compile(r"\b(pytest|unittest|tox|nox|jest|vitest|mocha|(npm|pnpm|yarn|cargo|go|dotnet|mvn|gradle) test)\b")


def is_question(task: str) -> bool:
    """A request that only asks about the code, e.g. "what is this repo about?"."""
    t = task.strip()
    return bool(ASK.match(t) or t.endswith("?")) and not CHANGE.search(t)


def memory_note(turns: list[dict], cap: int = 1800) -> str:
    """Compact stand-in for earlier turns: question, short answer, files touched. Oldest turns drop first."""
    lines = []
    for t in turns:
        files = f" [files: {', '.join(t['files'])}]" if t["files"] else ""
        lines.append(f"- Q: {t['task'][:200]}\n  A: {t['answer'][:300]}{files}")
    while lines and len("\n".join(lines)) > cap:
        lines.pop(0)
    return "\n".join(lines)


class Agent:
    def __init__(self, repo: Path, budget: Budget, private: bool, model: str = "", approve=None, small: str = "", on_event=None,
                 memory: str = "", prior_naive: int = 0, lean: bool = True, history: list | None = None):
        self.lean = lean  # False: the benchmark's baseline, no token controls at all
        self.history = history or []
        self.ws = Workspace(repo, lean=lean)
        self.budget = budget
        self.private = private
        self.approve = approve or self._ask
        self.emit = on_event or (lambda kind, **data: None)
        # Tavily sends the query off the machine, so private runs never get it
        self.tools = schemas(web=not private and bool(os.environ.get("TAVILY_API_KEY")))
        self.base = bifrost_url()
        self.headers = headers(private)
        if private:
            # private repos never leave the machine: local model, no cloud fallback;
            # the private virtual key also blocks any other provider at the gateway
            self.model = local_model()
            self.fallbacks = []
            self.large = self.model
        else:
            self.large = groq_model(model)
            self.model = groq_model(small) if small else self.large
            configured = [m.strip() for m in os.environ.get("LEANTOKEN_FALLBACKS", "").split(",") if m.strip()]
            self.fallbacks = configured or list(GROQ_CHAIN)
        self.fp = Footprint("")
        self.memory = memory  # what this agent sends about earlier turns
        self.prior_naive = prior_naive  # chars a naive agent would resend: the full transcript of earlier turns
        self.turn_naive = 0
        self.files_touched: list[str] = []
        self.naive: dict[str, int] = {}  # tool_call_id -> output size without clipping or ranged reads
        self.verified = "no tests run"
        self.step_limit = 15

    @property
    def on_small(self) -> bool:
        return self.model != self.large

    def escalate(self, step: int, reason: str) -> bool:
        if not self.on_small:
            return False
        self.fp.escalations.append({"step": step, "from": self.model, "to": self.large, "reason": reason})
        self.emit("escalate", to=self.large, reason=reason)
        self.model = self.large
        self.step_limit = max(self.step_limit, step + LARGE_STEPS)
        return True

    @staticmethod
    def _ask(text: str) -> bool:
        return input(f"\n[approve?]\n{text}\n(y/N) ").strip().lower() == "y"

    def _trim(self, messages: list) -> list:
        if not self.lean:
            return messages
        tool_idx = [i for i, m in enumerate(messages) if m["role"] == "tool"]
        old = set(tool_idx[:-KEEP_FULL])
        return [
            {**m, "content": m["content"][:ELIDE_TO] + " [elided]"} if i in old and len(m["content"]) > ELIDE_TO else m
            for i, m in enumerate(messages)
        ]

    def _post(self, body: dict) -> httpx.Response:
        return httpx.post(f"{self.base}/v1/chat/completions", json=body, headers=self.headers, timeout=180)

    def _chat(self, messages: list, step: int = 0, retried: bool = False) -> dict:
        self.budget.check()
        body = {"model": self.model, "messages": self._trim(messages), "tools": self.tools, "max_tokens": 1024}
        if self.lean and "gpt-oss" in self.model:
            body["reasoning_effort"] = "low"  # hidden reasoning is billed as output tokens and counts against the minute cap
        if fallbacks := [m for m in self.fallbacks if m != self.model]:
            body["fallbacks"] = fallbacks
        for _ in range(RATE_LIMIT_WAITS + 1):
            t0 = time.perf_counter()
            r = self._post(body)
            latency = time.perf_counter() - t0
            if r.status_code != 429 or "budget_exceeded" in r.text:
                break
            wait = min(60.0, float(r.headers.get("x-bifrost-retry-after-ms") or 15000) / 1000)
            self.emit("wait", seconds=wait)
            self.fp.rate_limit_wait_s += wait
            time.sleep(wait)
        if r.status_code >= 400 and "budget_exceeded" in r.text:
            raise BudgetExceeded("Bifrost virtual key budget used up")
        # Groq answers 400 when a model emits a malformed tool call: escalate, or resample once on the large model
        if r.status_code == 400:
            if self.escalate(step, "provider rejected small model output"):
                return self._chat(messages, step)
            if not retried:
                return self._chat(messages, step, retried=True)
            if "tool_use_failed" in r.text:
                # show the model its schema error instead of failing the task
                err = r.json().get("error", {}).get("message", "invalid tool call")[:300]
                return {"role": "user", "content": f"Your tool call was rejected: {err}. Retry with valid arguments."}
        if r.status_code >= 400:
            raise RuntimeError(f"Bifrost {r.status_code}: {r.text[:400]}")
        data = r.json()
        u = data.get("usage", {})
        pt, ct = u.get("prompt_tokens", 0), u.get("completion_tokens", 0)
        saved = self._saved_prompt_tokens(body, pt)
        self.fp.saved_prompt += saved
        model = data.get("model") or self.model
        self.budget.charge(model, pt, ct, local=self.private)
        region = data.get("extra_fields", {}).get("provider_response_headers", {}).get("X-Groq-Region", "")
        self.fp.add(model, pt, ct, latency, local=self.private, region=region)
        self.emit("call", model=model, prompt=pt, completion=ct, saved=saved)
        self.fp.fallbacks += r.headers.get("x-bifrost-routing-info-is-fallback") == "true"
        msg = data["choices"][0]["message"]
        # extras like "reasoning" would only cost tokens on the next call
        clean = {"role": "assistant", "content": msg.get("content") or ""}
        if msg.get("tool_calls"):
            clean["tool_calls"] = msg["tool_calls"]
        return clean

    def _saved_prompt_tokens(self, body: dict, prompt_tokens: int) -> float:
        """Prompt tokens a naive agent would have added to this request: whole-file reads, unclipped output, no history trimming.

        Sizes are measured; they become tokens at this request's real tokens-per-character ratio.
        """
        tools = [m for m in body["messages"] if m["role"] == "tool"]
        sent_chars = len(json.dumps(body["messages"])) + len(json.dumps(body["tools"]))
        history = max(0, self.prior_naive - len(self.memory))
        if not sent_chars or (not tools and not history):
            return 0.0
        extra = history + sum(max(0, self.naive.get(m["tool_call_id"], len(m["content"])) - len(m["content"])) for m in tools)
        return extra * prompt_tokens / sent_chars

    def _run_tool(self, call: dict) -> tuple[str, bool]:
        """Returns the tool output and whether the call failed."""
        name = call["function"]["name"]
        self.ws.naive = None
        if not hasattr(Workspace, name) or name.startswith("_"):
            return f"error: unknown tool {name}", True
        try:
            args = json.loads(call["function"]["arguments"] or "{}")
        except json.JSONDecodeError as e:
            return f"error: bad arguments json: {e}", True
        self.emit("start", name=name, args=call["function"]["arguments"])
        if self.lean and name == "run_shell" and (err := unrunnable(shell_cmd(args))):
            return err, True
        if needs_approval(name, args) and not self.approve(describe(name, args)):
            return "denied by user", False
        try:
            out = getattr(self.ws, name)(**args)
        except Exception as e:
            return f"error: {e}", True
        return out, out.startswith("error:")

    def note(self, result: str) -> dict:
        return {"task": self.fp.task, "answer": result, "files": self.files_touched}

    def run(self, task: str, max_steps: int = 15) -> str:
        self.fp.task = task
        ask = self.lean and is_question(task)
        if ask:
            self.tools = schemas(web=any(t["function"]["name"] == "web_search" for t in self.tools), read_only=True)
        prompt = f"Earlier in this session:\n{self.memory}\n\nTask: {task}" if self.memory else task
        messages = [{"role": "system", "content": SYSTEM_ASK if ask else SYSTEM if self.lean else SYSTEM_NAIVE}, *self.history, {"role": "user", "content": prompt}]
        self.messages = messages
        try:
            return self._loop(messages, max_steps)
        finally:
            sizes = [self.naive.get(m["tool_call_id"], len(m["content"])) if m["role"] == "tool" else len(json.dumps(m)) for m in messages[1 + len(self.history):]]
            self.turn_naive = sum(sizes) - (len(prompt) - len(task))

    def _loop(self, messages: list, max_steps: int) -> str:
        tool_errors = test_fails = 0
        last_error = ""
        edited = False
        self.step_limit = max_steps
        step = 0
        while step < self.step_limit:
            step += 1
            if self.on_small and step > SMALL_MAX_STEPS:
                self.escalate(step, f"small model not done after {SMALL_MAX_STEPS} steps")
            msg = self._chat(messages, step)
            messages.append(msg)
            if msg["role"] == "user":
                tool_errors += 1
                continue
            if not msg.get("tool_calls"):
                # a small model claiming success over failing tests gets a second opinion
                if self.verified == "tests failing" and self.escalate(step, "finished with tests failing"):
                    messages.append({"role": "user", "content": "Tests still fail. Fix them."})
                    continue
                return msg["content"]
            for call in msg["tool_calls"]:
                out, failed = self._run_tool(call)
                name = call["function"]["name"]
                if name in ("edit_file", "write_file") and not failed:
                    edited = True
                    path = json.loads(call["function"]["arguments"] or "{}").get("path")
                    if path and path not in self.files_touched:
                        self.files_touched.append(path)
                if name == "run_shell" and out.startswith("exit=") and TEST_CMD.search(json.dumps(call["function"]["arguments"])):
                    ok = out.startswith("exit=0")
                    self.verified = "tests passing" if ok else "tests failing"
                    test_fails += edited and not ok
                tool_errors = tool_errors + 1 if failed else 0
                last_error = out[:120] if failed else ""
                self.naive[call["id"]] = min(NAIVE_CAP, self.ws.naive if self.ws.naive is not None else len(out))
                messages.append({"role": "tool", "tool_call_id": call["id"], "content": out})
                self.emit("tool", name=name, args=call["function"]["arguments"], out=out, failed=failed)
                self.fp.trace.append({"step": step, "model": self.model, "tool": name,
                                      "args": call["function"]["arguments"][:120], "out": out[:120]})
            if tool_errors >= MAX_TOOL_ERRORS:
                self.escalate(step, f"{tool_errors} tool errors in a row ({last_error})")
            if test_fails >= MAX_TEST_FAILS:
                self.escalate(step, f"tests failed {test_fails} times after edits")
        return "step limit reached"
