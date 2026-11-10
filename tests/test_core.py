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


def _agent(tmp_path, monkeypatch, private=False, responses=None, small=""):
    from leantoken.agent import Agent

    monkeypatch.setenv("BIFROST_VK", "sk-bf-user")
    monkeypatch.setenv("BIFROST_PRIVATE_VK", "sk-bf-private")
    monkeypatch.setenv("LEANTOKEN_FALLBACKS", "openai/gpt-4o-mini")
    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), private, "openai/gpt-oss-120b",
                  approve=lambda _: True, small=small)
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
    assert agent.fp.fallbacks == 1 and list(agent.fp.totals()["by_model"]) == ["openai/gpt-4o-mini"]


SMALL = "openai/gpt-oss-20b"


def _tool_reply(model, name, args):
    call = {"id": "c1", "type": "function", "function": {"name": name, "arguments": json.dumps(args)}}
    msg = {"content": "", "tool_calls": [call]}
    return FakeResponse(body={"model": model, "choices": [{"message": msg}], "usage": {"prompt_tokens": 50, "completion_tokens": 5}})


def test_small_model_first_and_no_escalation_when_fine(tmp_path, monkeypatch):
    agent, sent = _agent(tmp_path, monkeypatch, small=SMALL)
    agent.run("task")
    assert sent[0]["model"] == "groq/openai/gpt-oss-20b" and agent.fp.escalations == []


def test_escalates_on_provider_400_and_retries_large(tmp_path, monkeypatch):
    bad = FakeResponse(400, text='{"error":{"code":"tool_use_failed"}}')
    agent, sent = _agent(tmp_path, monkeypatch, responses=[bad], small=SMALL)
    assert agent.run("task") == "done"
    assert [b["model"] for b in sent] == ["groq/openai/gpt-oss-20b", "groq/openai/gpt-oss-120b"]
    assert agent.fp.escalations[0]["reason"] == "provider rejected small model output"


def test_escalates_after_repeated_tool_errors_and_stays_large(tmp_path, monkeypatch):
    bad = [_tool_reply(SMALL, "read_file", {"path": "missing.py"}) for _ in range(2)]
    agent, sent = _agent(tmp_path, monkeypatch, responses=bad, small=SMALL)
    agent.run("task")
    assert [b["model"].split("-")[-1] for b in sent] == ["20b", "20b", "120b"]
    assert len(agent.fp.escalations) == 1


def test_done_with_failing_tests_escalates(tmp_path, monkeypatch):
    (tmp_path / "a.txt").write_text("x")
    steps = [_tool_reply(SMALL, "edit_file", {"path": "a.txt", "old": "x", "new": "y"}),
             _tool_reply(SMALL, "run_shell", {"cmd": "python -c \"import sys; sys.exit(1)\" # pytest"}),
             FakeResponse(body=_reply(SMALL))]
    agent, sent = _agent(tmp_path, monkeypatch, responses=steps, small=SMALL)
    agent.run("task")
    assert agent.fp.escalations[0]["reason"] == "finished with tests failing"
    assert sent[-1]["model"] == "groq/openai/gpt-oss-120b"


def test_no_route_uses_large_only(tmp_path, monkeypatch):
    agent, sent = _agent(tmp_path, monkeypatch)
    agent.run("task")
    assert sent[0]["model"] == "groq/openai/gpt-oss-120b" and not agent.on_small


def test_footprint_energy_and_report(tmp_path):
    from leantoken.footprint import Footprint, energy_kwh, gpus

    assert gpus(117) == 4 and gpus(21) == 1
    assert energy_kwh("groq/openai/gpt-oss-120b", 100, 1.0) > energy_kwh("groq/openai/gpt-oss-20b", 100, 1.0) > 0
    fp = Footprint("t")
    fp.add("groq/openai/gpt-oss-20b", 1000, 100, 0.5)
    t = fp.totals()
    assert t["in"] == 1000 and t["gco2e"] > 0 and t["cost_usd"] > 0
    assert "mgCO2e" in fp.report("done", "tests passing")
    saved = json.loads(fp.save(tmp_path, "done", "tests passing").read_text())
    assert saved["totals"]["calls"] == 1


def test_rate_limit_waits_then_retries(tmp_path, monkeypatch):
    import leantoken.agent as agent_mod

    monkeypatch.setattr(agent_mod.time, "sleep", lambda s: None)
    limited = FakeResponse(429, text="rate limited", headers={"x-bifrost-retry-after-ms": "2000"})
    agent, sent = _agent(tmp_path, monkeypatch, responses=[limited])
    assert agent.run("task") == "done"
    assert len(sent) == 2 and agent.fp.rate_limit_wait_s == 2


def test_edit_tolerates_tab_indent(tmp_path):
    (tmp_path / "a.py").write_text("def f():\n    return 1\n")
    assert Workspace(tmp_path).edit_file("a.py", "\treturn 1", "\treturn 2") == "edited a.py"
    assert (tmp_path / "a.py").read_text() == "def f():\n    return 2\n"


def test_grid_follows_groq_region():
    from leantoken.footprint import Footprint

    fp = Footprint("t")
    mumbai = fp.add("groq/openai/gpt-oss-20b", 100, 100, 1.0, region="bom")
    unknown = fp.add("groq/openai/gpt-oss-20b", 100, 100, 1.0)
    assert mumbai.gco2e > unknown.gco2e


def test_read_file_accepts_guessed_range_names(tmp_path):
    (tmp_path / "a.txt").write_text("1\n2\n3\n")
    assert Workspace(tmp_path).read_file("a.txt", line_start=2, line_end=2).startswith("2\t2")


def test_bash_wrapper_unwrapped():
    from leantoken.tools import unwrap

    assert unwrap(["bash", "-lc", "pytest -q"]) == "pytest -q"
    assert unwrap(["python", "-m", "pytest"]) == ["python", "-m", "pytest"]
    assert not needs_approval("run_shell", {"cmd": ["bash", "-lc", "git status"]})


def test_private_cli_never_resolves_cloud_models(tmp_path, monkeypatch):
    from leantoken import cli

    def boom(*a, **k):
        raise AssertionError("private run touched the Groq model list")

    seen = {}

    class StubAgent:
        def __init__(self, repo, budget, private, model, small="", **k):
            seen.update(private=private, model=model, small=small)
            self.fp, self.verified, self.budget = type("F", (), {"report": lambda *a: "", "save": lambda *a: ""})(), "", budget

        def run(self, task):
            return "ok"

    monkeypatch.setattr(cli, "resolve_model", boom)
    monkeypatch.setattr(cli, "resolve_small", boom)
    monkeypatch.setattr(cli, "Agent", StubAgent)
    monkeypatch.setattr("sys.argv", ["leantoken", "task", "--private", "--repo", str(tmp_path)])
    cli.main()
    assert seen == {"private": True, "model": "", "small": ""}


def test_tui_private_toggle_needs_the_private_key(tmp_path, monkeypatch):
    import asyncio

    from leantoken import tui

    monkeypatch.setenv("BIFROST_VK", "sk-bf-x")
    monkeypatch.delenv("BIFROST_PRIVATE_VK", raising=False)
    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/big", "groq/small")

    async def go():
        async with app.run_test(size=(100, 40)) as pilot:
            await pilot.pause(0.1)
            app.command("/private")
            assert not app.private
            monkeypatch.setenv("BIFROST_PRIVATE_VK", "sk-bf-p")
            app.command("/private")
            assert app.private
            assert "qwen2.5-coder" in str(app.route().plain)

    asyncio.run(go())
