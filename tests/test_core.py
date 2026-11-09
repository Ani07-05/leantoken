import json

import pytest

from leantoken.budget import Budget, BudgetExceeded
from leantoken.tools import Workspace, needs_approval


def test_path_escape_blocked(tmp_path):
    with pytest.raises(ValueError):
        Workspace(tmp_path).read_file("../etc/passwd")


def test_edit_requires_unique_match(tmp_path):
    (tmp_path / "a.txt").write_text("x x")
    assert "2 times" in Workspace(tmp_path).edit_file("a.txt", "x", "y")


def test_approval_gate():
    assert not needs_approval("read_file", {})
    assert not needs_approval("run_shell", {"cmd": "git status"})
    assert needs_approval("run_shell", {"cmd": "git status; rm -rf /"})
    assert needs_approval("run_shell", {"cmd": "git push"})
    assert needs_approval("write_file", {})
    assert needs_approval("edit_file", {})


def test_budget_blocks_and_persists(tmp_path):
    b = Budget(tmp_path / "b.json", 0.001)
    b.charge("openai/gpt-oss-120b", 10_000, 0)
    with pytest.raises(BudgetExceeded):
        Budget(tmp_path / "b.json", 0.001).check()
    assert json.loads((tmp_path / "b.json").read_text())["spent"] > 0



class FakeResponse:
    def __init__(self, status=200, body=None, headers=None, text=""):
        self.status_code, self._body, self.headers, self.text = status, body or {}, headers or {}, text

    def json(self):
        return self._body

    def raise_for_status(self):
        if self.status_code >= 400:
            raise RuntimeError(f"http {self.status_code}")


def _reply(model="groq/openai/gpt-oss-120b"):
    msg = {"content": "done", "reasoning": "x" * 50}
    return {"model": model, "choices": [{"message": msg}], "usage": {"prompt_tokens": 100, "completion_tokens": 10}}


def _agent(tmp_path, monkeypatch, private=False, responses=None):
    from leantoken.agent import Agent

    monkeypatch.setenv("BIFROST_VK", "sk-bf-user")
    monkeypatch.setenv("BIFROST_PRIVATE_VK", "sk-bf-private")
    monkeypatch.setenv("LEANTOKEN_FALLBACKS", "openai/gpt-4o-mini")
    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), private, "openai/gpt-oss-120b", approve=lambda _: True)
    sent = []

    def post(body):
        sent.append(body)
        return responses.pop(0) if responses else FakeResponse(body=_reply(body["model"]))

    agent._post = post
    return agent, sent


def test_cloud_run_goes_through_bifrost_with_fallbacks(tmp_path, monkeypatch):
    agent, sent = _agent(tmp_path, monkeypatch)
    assert agent.base == "http://localhost:8080"
    assert agent.headers == {"x-bf-vk": "sk-bf-user"}
    assert agent.run("task") == "done"
    assert sent[0]["model"] == "groq/openai/gpt-oss-120b"
    assert sent[0]["fallbacks"] == ["openai/gpt-4o-mini"]


def test_private_run_uses_ollama_key_and_no_fallbacks(tmp_path, monkeypatch):
    agent, sent = _agent(tmp_path, monkeypatch, private=True)
    assert agent.headers == {"x-bf-vk": "sk-bf-private"}
    agent.run("task")
    assert sent[0]["model"].startswith("ollama/") and "fallbacks" not in sent[0]
    assert agent.budget.spent == 0


def test_private_run_requires_private_key(tmp_path, monkeypatch):
    from leantoken.agent import Agent

    monkeypatch.delenv("BIFROST_PRIVATE_VK", raising=False)
    with pytest.raises(SystemExit):
        Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), True)


def test_bifrost_budget_error_stops_run(tmp_path, monkeypatch):
    err = FakeResponse(402, text='{"error":{"type":"budget_exceeded"}}')
    agent, _ = _agent(tmp_path, monkeypatch, responses=[err])
    with pytest.raises(BudgetExceeded):
        agent.run("task")


def test_fallback_counted_and_reasoning_dropped(tmp_path, monkeypatch):
    fb = FakeResponse(body=_reply("openai/gpt-4o-mini"), headers={"x-bifrost-routing-info-is-fallback": "true"})
    agent, _ = _agent(tmp_path, monkeypatch, responses=[fb])
    assert agent._chat([]) == {"role": "assistant", "content": "done"}
    assert agent.usage.fallbacks == 1 and agent.usage.models == {"openai/gpt-4o-mini": 1}
