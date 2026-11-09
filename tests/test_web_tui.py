import json

import httpx

from leantoken import web
from leantoken.tools import Workspace, needs_approval, schemas
from leantoken.tui import brief, diff_text


class Resp:
    def __init__(self, status=200, body=None):
        self.status_code, self._body, self.text = status, body or {}, json.dumps(body or {})

    def json(self):
        return self._body


def test_search_without_key_is_an_error(monkeypatch):
    monkeypatch.delenv("TAVILY_API_KEY", raising=False)
    assert web.search("q").startswith("error:")


def test_search_is_compact_and_authenticated(monkeypatch):
    monkeypatch.setenv("TAVILY_API_KEY", "tvly-x")
    sent = {}

    def post(url, headers, json, timeout):
        sent.update(headers=headers, body=json)
        return Resp(body={"answer": "use X", "results": [{"title": "T", "url": "http://u", "content": "c" * 900}]})

    monkeypatch.setattr(httpx, "post", post)
    out = web.search("how to X", max_results=99)
    assert sent["headers"]["Authorization"] == "Bearer tvly-x"
    assert sent["body"]["max_results"] == 5
    assert out.startswith("use X") and "<http://u>" in out
    assert len(out) < 600


def test_search_http_error_is_flagged(monkeypatch):
    monkeypatch.setenv("TAVILY_API_KEY", "tvly-x")
    monkeypatch.setattr(httpx, "post", lambda *a, **k: Resp(status=401))
    assert web.search("q").startswith("error: tavily 401")


def test_web_tool_only_listed_when_enabled_and_never_asks():
    names = lambda s: [t["function"]["name"] for t in s]
    assert "web_search" not in names(schemas())
    assert "web_search" in names(schemas(web=True))
    assert not needs_approval("web_search", {"query": "x"})
    assert hasattr(Workspace, "web_search")


def test_private_agent_never_gets_web_search(tmp_path, monkeypatch):
    from leantoken.agent import Agent
    from leantoken.budget import Budget

    monkeypatch.setenv("TAVILY_API_KEY", "tvly-x")
    monkeypatch.setenv("BIFROST_PRIVATE_VK", "sk-bf-private")
    private = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), True)
    cloud = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "m")
    assert "web_search" not in [t["function"]["name"] for t in private.tools]
    assert "web_search" in [t["function"]["name"] for t in cloud.tools]


def test_events_reach_the_ui(tmp_path, monkeypatch):
    from leantoken.agent import Agent
    from leantoken.budget import Budget

    events = []
    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "m", on_event=lambda k, **d: events.append(k))
    call = {"function": {"name": "search", "arguments": "{}"}, "id": "1"}
    reply = {"model": "groq/m", "choices": [{"message": {"content": "", "tool_calls": [call]}}], "usage": {}}
    final = {"model": "groq/m", "choices": [{"message": {"content": "ok"}}], "usage": {}}
    bodies = iter([reply, final])

    class R:
        status_code, text, headers = 200, "", {}

        def json(self):
            return next(bodies)

    agent._post = lambda body: R()
    monkeypatch.setattr(Workspace, "search", lambda self, pattern="": "hit")
    assert agent.run("t") == "ok"
    assert events == ["call", "tool", "call"]


def test_tui_helpers():
    assert brief("read_file", '{"path": "a.py", "start": 3, "end": 9}') == "a.py:3-9"
    assert brief("run_shell", '{"cmd": ["git", "status"]}') == "git status"
    assert brief("search", "not json") == "not json"
    styles = [s.style for s in diff_text("edit a\n- old\n+ new").spans]
    assert styles == ["bold", "red", "green"]


def test_argv_with_a_whole_command_line_runs_as_a_shell_string(tmp_path):
    out = Workspace(tmp_path).run_shell(["echo hi there"])
    assert out.startswith("exit=0") and "hi there" in out
    assert Workspace(tmp_path).run_shell(["echo", "a b"]).strip().endswith("a b")


def test_edit_diff_marks_removed_and_added_lines():
    from leantoken.tui import edit_diff

    d = edit_diff(json.dumps({"path": "a", "old": "x = 1\ny = 2", "new": "x = 2"}))
    assert d.plain == "- x = 1\n- y = 2\n+ x = 2"
    assert edit_diff("not json") is None


def test_tui_runs_a_task_and_shows_the_answer(tmp_path, monkeypatch):
    import asyncio

    from textual.widgets import Markdown

    from leantoken import tui
    from leantoken.budget import Budget

    class StubAgent:
        def __init__(self, repo, budget, private, model, small="", approve=None, on_event=None):
            self.approve, self.emit = approve, on_event
            self.verified = "tests passing"
            from leantoken.footprint import Footprint

            self.fp = Footprint("")

        def run(self, task):
            self.emit("tool", name="edit_file", args=json.dumps({"path": "a.py", "old": "a", "new": "b"}), out="edited a.py", failed=False)
            assert self.approve("edit a.py\n- a\n+ b") is True
            return "all done"

    monkeypatch.setattr(tui, "Agent", StubAgent)
    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/big", "groq/small")

    async def go():
        async with app.run_test() as pilot:
            assert app.query_one("#body").has_class("home")
            await pilot.press(*"fix it", "enter")
            assert not app.query_one("#body").has_class("home")
            for _ in range(50):
                await pilot.pause(0.05)
                if isinstance(app.screen, tui.Approval):
                    await pilot.press("y")
                    break
            await app.workers.wait_for_complete()
            await pilot.pause(0.2)
            texts = [str(m.source) for m in app.query(Markdown)]
            assert "all done" in texts
            assert app.files == ["a.py"]
            assert app.session["tasks"] == 1 and not app.busy
            await pilot.press("ctrl+n")
            assert app.query_one("#body").has_class("home")

    asyncio.run(go())


def test_read_file_caps_span_and_numbers_from_one(tmp_path):
    (tmp_path / "big.txt").write_text("\n".join(f"l{i}" for i in range(1, 501)))
    out = Workspace(tmp_path).read_file("big.txt", start=0, end=500)
    assert out.startswith("1\tl1\n") and "151\t" not in out and "[500 lines total]" in out


def test_search_survives_binary_files(tmp_path):
    (tmp_path / "a.bin").write_bytes(b"needle \x9c\xff\x00 needle")
    (tmp_path / "b.txt").write_text("needle here")
    out = Workspace(tmp_path).search("needle")
    assert "b.txt" in out and not out.startswith("error")


def test_groq_models_back_each_other_up_on_rate_limits(tmp_path, monkeypatch):
    from leantoken.agent import Agent
    from leantoken.budget import Budget

    monkeypatch.delenv("LEANTOKEN_FALLBACKS", raising=False)
    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "openai/gpt-oss-120b", small="openai/gpt-oss-20b")
    sent = []
    agent._post = lambda body: (sent.append(body), type("R", (), {"status_code": 200, "text": "", "headers": {}, "json": lambda s: {"model": body["model"], "choices": [{"message": {"content": "ok"}}], "usage": {}}})())[1]
    agent.run("t")
    assert sent[0]["model"] == "groq/openai/gpt-oss-20b"
    assert sent[0]["fallbacks"] == ["groq/openai/gpt-oss-120b", "groq/qwen/qwen3.8-27b"]
    assert sent[0]["reasoning_effort"] == "low"
