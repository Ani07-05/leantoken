import json
import os
from dataclasses import dataclass, field
from pathlib import Path

import httpx

from .budget import Budget
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
    models: dict = field(default_factory=dict)


class Agent:
    def __init__(self, repo: Path, budget: Budget, private: bool, approve=None):
        self.ws = Workspace(repo)
        self.budget = budget
        self.private = private
        self.approve = approve or self._ask
        self.base = os.environ.get("BIFROST_URL", "http://localhost:8080")
        self.model = os.environ.get("LEANTOKEN_MODEL", "openai/gpt-4o-mini")
        self.local_model = os.environ.get("LEANTOKEN_LOCAL_MODEL", "ollama/qwen2.5-coder:7b")
        self.fallbacks = [
            m for m in os.environ.get("LEANTOKEN_FALLBACKS", "anthropic/claude-haiku-4-5-20251001").split(",") if m
        ]
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

    def _chat(self, messages: list) -> dict:
        self.budget.check()
        # private repos never leave the machine: no cloud model, no cloud fallback
        body = {
            "model": self.local_model if self.private else self.model,
            "messages": self._trim(messages),
            "tools": SCHEMAS,
            "max_tokens": 1024,
        }
        if self.fallbacks and not self.private:
            body["fallbacks"] = self.fallbacks
        headers = {}
        if vk := os.environ.get("BIFROST_VK"):
            headers["x-bf-vk"] = vk
        r = httpx.post(f"{self.base}/v1/chat/completions", json=body, headers=headers, timeout=120)
        r.raise_for_status()
        data = r.json()
        u = data.get("usage", {})
        pt, ct = u.get("prompt_tokens", 0), u.get("completion_tokens", 0)
        model = data.get("model", body["model"])
        self.budget.charge(model if "/" in model else body["model"], pt, ct)
        self.usage.prompt += pt
        self.usage.completion += ct
        self.usage.calls += 1
        self.usage.models[model] = self.usage.models.get(model, 0) + 1
        return data["choices"][0]["message"]

    def _run_tool(self, call: dict) -> str:
        name = call["function"]["name"]
        args = json.loads(call["function"]["arguments"] or "{}")
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
                return msg.get("content") or ""
            for call in msg["tool_calls"]:
                messages.append({"role": "tool", "tool_call_id": call["id"], "content": self._run_tool(call)})
        return "step limit reached"
