import json

import pytest

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
    assert events == ["call", "start", "tool", "call"]


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
        def __init__(self, repo, budget, private, model, small="", approve=None, on_event=None, **kw):
            self.approve, self.emit = approve, on_event
            self.verified = "tests passing"
            from leantoken.footprint import Footprint

            self.fp = Footprint("")
            self.turn_naive = 0

        def note(self, result):
            return {"task": "t", "answer": result, "files": []}

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
                if app.query_one("#perm").has_class("show"):
                    assert app.query_one("#prompt").has_class("hidden")
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


def test_slash_hint_lists_matches_and_tab_completes(tmp_path):
    import asyncio

    from leantoken import tui
    from leantoken.budget import Budget

    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/big", "groq/small")

    async def go():
        async with app.run_test() as pilot:
            hint = app.query_one("#hint")
            assert not hint.has_class("show")
            await pilot.press("/", "m", "o")
            assert hint.has_class("show") and app.matches == ["/models"]
            await pilot.press("tab")
            assert app.query_one("#input").value == "/models "
            assert not hint.has_class("show")

    asyncio.run(go())


def test_help_command_opens_session_with_help_text(tmp_path):
    import asyncio

    from textual.widgets import Markdown

    from leantoken import tui
    from leantoken.budget import Budget

    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/big", "groq/small")

    async def go():
        async with app.run_test() as pilot:
            await pilot.press(*"/help", "enter")
            await pilot.pause(0.2)
            assert not app.query_one("#body").has_class("home")
            assert any("Commands" in str(m.source) for m in app.query(Markdown))

    asyncio.run(go())


def test_savings_measure_what_a_naive_agent_would_have_sent(tmp_path):
    from leantoken.agent import Agent
    from leantoken.budget import Budget

    (tmp_path / "big.txt").write_text("x" * 40_000)
    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/m")
    read = {"id": "c1", "type": "function", "function": {"name": "read_file", "arguments": json.dumps({"path": "big.txt", "start": 1, "end": 3})}}
    replies = iter([
        {"model": "groq/m", "choices": [{"message": {"content": "", "tool_calls": [read]}}], "usage": {"prompt_tokens": 100, "completion_tokens": 5}},
        {"model": "groq/m", "choices": [{"message": {"content": "done"}}], "usage": {"prompt_tokens": 600, "completion_tokens": 5}},
    ])

    class R:
        status_code, text, headers = 200, "", {}

        def json(self):
            return next(replies)

    agent._post = lambda body: R()
    seen = []
    agent.emit = lambda kind, **d: seen.append(d["saved"]) if kind == "call" else None
    agent.run("t")
    t = agent.fp.totals()
    assert seen[0] == 0 and seen[1] > 0
    assert t["saved_prompt"] > 0 and 0 < t["saved_pct"] < 100
    assert t["naive_total"] == t["in"] + t["out"] + t["saved_prompt"]
    assert t["saved_kwh"] == pytest.approx(t["kwh"] * t["saved_prompt"] / (t["in"] + t["out"]))


def _ask_in_tui(tmp_path, text, keys):
    import asyncio
    import threading

    from leantoken import tui
    from leantoken.budget import Budget

    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/big", "groq/small")
    result = {}

    async def go():
        async with app.run_test() as pilot:
            t = threading.Thread(target=lambda: result.update(ok=app.approve(text)))
            t.start()
            for _ in range(60):
                await pilot.pause(0.05)
                if app.query_one("#perm").has_class("show"):
                    break
            perm = app.query_one("#perm")
            result["choices"] = list(perm.choices)
            await pilot.press(*keys)
            await pilot.pause(0.2)
            t.join(timeout=3)
            result["prompt_back"] = not app.query_one("#prompt").has_class("hidden")
            result["always"] = set(app.always)

    asyncio.run(go())
    return result


def test_permission_enter_confirms_the_highlighted_choice(tmp_path):
    r = _ask_in_tui(tmp_path, "edit a.py\n- a\n+ b", ["right", "enter"])
    assert r["choices"] == ["once", "always", "reject"]
    assert r["ok"] is True and r["always"] == {"edit"} and r["prompt_back"]


def test_permission_reject_and_shell_has_no_always(tmp_path):
    r = _ask_in_tui(tmp_path, "shell: git push", ["n"])
    assert r["choices"] == ["once", "reject"]
    assert r["ok"] is False and r["always"] == set()


def test_long_answer_leaves_the_view_at_the_bottom(tmp_path, monkeypatch):
    import asyncio

    from leantoken import tui
    from leantoken.budget import Budget
    from leantoken.footprint import Footprint

    class StubAgent:
        def __init__(self, *a, **k):
            self.verified, self.fp, self.turn_naive = "no tests run", Footprint(""), 0

        def note(self, result):
            return {"task": "t", "answer": result, "files": []}

        def run(self, task):
            return "\n\n".join(f"line {i}" for i in range(80)) + "\n\nTHE END"

    monkeypatch.setattr(tui, "Agent", StubAgent)
    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/m", "groq/m")

    async def go():
        async with app.run_test(size=(100, 30)) as pilot:
            await pilot.press(*"go", "enter")
            await app.workers.wait_for_complete()
            await pilot.pause(0.6)
            chat = app.chat
            assert chat.max_scroll_y > 0
            assert chat.scroll_y >= chat.max_scroll_y - 1
            assert any(" tokens" in str(w.render()) for w in app.query(".note"))

    asyncio.run(go())


def test_garbled_shell_commands_are_rejected_before_asking(tmp_path):
    from leantoken.tools import unrunnable

    assert unrunnable("sh_lev sh_lev sh_lev").startswith("error: command not found: sh_lev")
    for ok in ("git status", "FOO=1 ls -l", "cd x && ls", "[ -f a ]", "./run.sh", "git log --oneline"):
        assert unrunnable(ok) is None, ok


def test_recursive_ls_is_refused_with_a_hint():
    from leantoken.tools import unrunnable

    for cmd in ("ls -R", "ls -laR src", "ls --recursive"):
        assert "Use list_files" in unrunnable(cmd), cmd
    assert unrunnable("ls -la src") is None


def test_one_huge_output_cannot_inflate_savings(tmp_path):
    from leantoken.agent import NAIVE_CAP, Agent
    from leantoken.budget import Budget

    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/m")
    agent.naive["c1"] = NAIVE_CAP
    body = {"messages": [{"role": "tool", "tool_call_id": "c1", "content": "x" * 1000}], "tools": []}
    capped = agent._saved_prompt_tokens(body, 1000)
    agent.naive["c1"] = 10 * NAIVE_CAP
    assert agent._saved_prompt_tokens(body, 1000) > capped


def test_memory_note_is_compact_and_drops_oldest_first():
    from leantoken.agent import memory_note

    turns = [{"task": f"task {i}", "answer": "a" * 400, "files": ["x.py"] if i == 2 else []} for i in range(10)]
    note = memory_note(turns, cap=900)
    assert len(note) <= 900 and "task 9" in note and "task 0" not in note
    assert memory_note([]) == ""


def test_savings_compound_when_a_naive_agent_resends_the_whole_chat(tmp_path):
    from leantoken.agent import Agent
    from leantoken.budget import Budget

    def second_turn_saved(prior_naive):
        agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/m", memory="- Q: x\n  A: y", prior_naive=prior_naive)
        body = {"messages": [{"role": "user", "content": "hi"}], "tools": []}
        return agent._saved_prompt_tokens(body, 500)

    assert second_turn_saved(0) == 0
    assert 0 < second_turn_saved(20_000) < second_turn_saved(80_000)


def test_later_turns_get_a_memory_of_earlier_ones(tmp_path):
    from leantoken.agent import Agent
    from leantoken.budget import Budget

    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/m", memory="- Q: first\n  A: done")
    sent = []

    class R:
        status_code, text, headers = 200, "", {}

        def json(self):
            return {"model": "groq/m", "choices": [{"message": {"content": "ok"}}], "usage": {"prompt_tokens": 50, "completion_tokens": 1}}

    agent._post = lambda body: (sent.append(body), R())[1]
    agent.run("second")
    assert "Earlier in this session" in sent[0]["messages"][1]["content"] and "Task: second" in sent[0]["messages"][1]["content"]
    assert agent.turn_naive > 0 and agent.note("ok")["task"] == "second"


def test_naive_workspace_returns_whole_files_and_unclipped_output(tmp_path):
    (tmp_path / "big.txt").write_text("\n".join(f"line {i}" for i in range(1, 801)))
    lean, naive = Workspace(tmp_path, lean=True), Workspace(tmp_path, lean=False)
    assert "line 101" not in lean.read_file("big.txt") and "line 800" in naive.read_file("big.txt")
    assert len(naive.run_shell("seq 1 5000")) > 20_000 > len(lean.run_shell("seq 1 5000"))


def test_naive_agent_sends_everything_it_ever_saw(tmp_path):
    from leantoken.agent import Agent
    from leantoken.budget import Budget

    sent = []

    class R:
        status_code, text, headers = 200, "", {}

        def json(self):
            return {"model": "groq/openai/gpt-oss-120b", "choices": [{"message": {"content": "ok"}}], "usage": {"prompt_tokens": 10, "completion_tokens": 1}}

    def post(body):
        sent.append(body)
        return R()

    history = [{"role": "user", "content": "earlier"}, {"role": "tool", "tool_call_id": "z", "content": "T" * 5000}]
    naive = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/openai/gpt-oss-120b", lean=False, history=history)
    naive._post = post
    naive.run("now")
    assert sent[0]["messages"][2]["content"] == "T" * 5000 and "reasoning_effort" not in sent[0]
    lean = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/openai/gpt-oss-120b")
    lean._post = post
    lean.run("now")
    assert sent[1]["reasoning_effort"] == "low"
