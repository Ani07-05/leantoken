import json
import os
from dataclasses import dataclass, field
from pathlib import Path

import httpx

from .budget import Budget, BudgetExceeded
from .gateway import bifrost_url, groq_model, headers
from .tools import SCHEMAS, Workspace, describe, needs_approval

SYSTEM = (
    "You are a terse coding agent. Use search and ranged read_file, never read whole files. "
    "Prefer edit_file over write_file. Run tests to verify. Stop when done and reply in one line."
)
KEEP_FULL = 4


@dataclass
class Usage:
    prompt: int = 0
    completion: int = 0
    calls: int = 0
    fallbacks: int = 0
    models: dict = field(default_factory=dict)


class Agent:
    def __init__(self, repo: Path, budget: Budget, private: bool, model: str = "", approve=None):
        self.ws = Workspace(repo)
        self.budget = budget
        self.private = private
        self.approve = approve or self._ask
        self.base = bifrost_url()
        self.headers = headers(private)
        if private:
            # private repos never leave the machine: local model, no cloud fallback;
            # the private virtual key also blocks any other provider at the gateway
            self.model = "ollama/" + os.environ.get("LEANTOKEN_LOCAL_MODEL", "qwen2.5-coder:7b")
            self.fallbacks = []
        else:
            self.model = groq_model(model)
            self.fallbacks = [m.strip() for m in os.environ.get("LEANTOKEN_FALLBACKS", "").split(",") if m.strip()]
        self.usage = Usage()

    @staticmethod
    def _ask(text: str) -> bool:
        return input(f"\n[approve?]\n{text}\n(y/N) ").strip().lower() == "y"

    def _trim(self, messages: list) -> list:
        tool_idx = [i for i, m in enumerate(messages) if m["role"] == "tool"]
        old = set(tool_idx[:-KEEP_FULL])
        return [
            {**m, "content": m["content"][:150] + " [elided]"} if i in old and len(m["content"]) > 150 else m
            for i, m in enumerate(messages)
        ]

    def _post(self, body: dict) -> httpx.Response:
        return httpx.post(f"{self.base}/v1/chat/completions", json=body, headers=self.headers, timeout=180)

    def _chat(self, messages: list) -> dict:
        self.budget.check()
        body = {"model": self.model, "messages": self._trim(messages), "tools": SCHEMAS, "max_tokens": 1024}
        if self.fallbacks:
            body["fallbacks"] = self.fallbacks
        r = self._post(body)
        if r.status_code >= 400 and "budget_exceeded" in r.text:
            raise BudgetExceeded("Bifrost virtual key budget used up")
        r.raise_for_status()
        data = r.json()
        u = data.get("usage", {})
        pt, ct = u.get("prompt_tokens", 0), u.get("completion_tokens", 0)
        model = data.get("model") or self.model
        self.budget.charge(model, pt, ct, local=self.private)
        self.usage.prompt += pt
        self.usage.completion += ct
        self.usage.calls += 1
        self.usage.fallbacks += r.headers.get("x-bifrost-routing-info-is-fallback") == "true"
        self.usage.models[model] = self.usage.models.get(model, 0) + 1
        msg = data["choices"][0]["message"]
        # extras like "reasoning" would only cost tokens on the next call
        clean = {"role": "assistant", "content": msg.get("content") or ""}
        if msg.get("tool_calls"):
            clean["tool_calls"] = msg["tool_calls"]
        return clean

    def _run_tool(self, call: dict) -> str:
        name = call["function"]["name"]
        try:
            args = json.loads(call["function"]["arguments"] or "{}")
        except json.JSONDecodeError as e:
            return f"error: bad arguments json: {e}"
        if needs_approval(name, args) and not self.approve(describe(name, args)):
            return "denied by user"
        try:
            return getattr(self.ws, name)(**args)
        except Exception as e:
            return f"error: {e}"

    def run(self, task: str, max_steps: int = 15) -> str:
        messages = [{"role": "system", "content": SYSTEM}, {"role": "user", "content": task}]
        for _ in range(max_steps):
            msg = self._chat(messages)
            messages.append(msg)
            if not msg.get("tool_calls"):
                return msg["content"]
            for call in msg["tool_calls"]:
                messages.append({"role": "tool", "tool_call_id": call["id"], "content": self._run_tool(call)})
        return "step limit reached"
