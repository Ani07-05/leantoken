import asyncio
import json

from leantoken import gateway, tools, tui
from leantoken.agent import Agent, is_question
from leantoken.budget import Budget
from leantoken.tools import Workspace, schemas, unrunnable


def names(s):
    return [t["function"]["name"] for t in s]


def test_questions_are_read_only():
    for q in ["tell me what is the code about", "what does this repo do?", "explain main.c", "how does the parser work"]:
        assert is_question(q), q
    for t in ["fix the failing test", "add validation to create_user", "can you fix the bug?", "rename Config to Settings"]:
        assert not is_question(t), t
    ro = names(schemas(read_only=True))
    assert "write_file" not in ro and "edit_file" not in ro and "read_file" in ro and "list_files" in ro


def test_agent_offers_no_write_tools_for_a_question(tmp_path, monkeypatch):
    sent = []

    class R:
        status_code = 200
        headers = {}
        text = ""

        def json(self):
            return {"choices": [{"message": {"content": "It is a C optimizer explainer."}}], "usage": {}}

    agent = Agent(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/big")
    monkeypatch.setattr(agent, "_post", lambda body: sent.append(body) or R())
    assert agent.run("tell me what is the code about") == "It is a C optimizer explainer."
    assert "write_file" not in names(sent[0]["tools"]) and "Do not change files" in sent[0]["messages"][0]["content"]


def test_python_search_and_list_files(tmp_path, monkeypatch):
    (tmp_path / "src").mkdir()
    (tmp_path / "src" / "a.py").write_text("x = 1\nneedle = 2\n")
    (tmp_path / "node_modules").mkdir()
    (tmp_path / "node_modules" / "b.js").write_text("needle")
    ws = Workspace(tmp_path)
    assert ws._py_search("needle", tmp_path) == "src/a.py:2:needle = 2"
    assert ws._py_search("(", tmp_path) == ""  # bad regex falls back to a literal search
    assert ws.list_files() == "src/a.py"
    monkeypatch.setattr(tools.shutil, "which", lambda name: None)
    assert ws.search("needle") == "src/a.py:2:needle = 2"


def test_cmd_builtins_are_runnable_on_windows(monkeypatch):
    monkeypatch.setattr(tools.shutil, "which", lambda name: None)
    assert unrunnable("dir") is None if tools.os.name == "nt" else unrunnable("dir")


def test_first_model_saved_and_resolved(monkeypatch):
    assert gateway.resolve_small(None) == gateway.DEFAULT_SMALL
    gateway.save_model(model="groq/big")
    gateway.save_model(small="")
    assert gateway.saved_model() == "groq/big" and gateway.resolve_small(None) == ""
    gateway.save_model(small="groq/tiny")
    assert gateway.resolve_small(None) == "groq/tiny" and gateway.resolve_small("groq/x") == "groq/x"


def test_picker_opens_from_cache_and_sets_both_models(tmp_path, monkeypatch):
    gateway.MODELS_CACHE.write_text(json.dumps(["groq/a", "groq/b", "groq/c"]))
    monkeypatch.setattr(tui, "list_models", lambda: ["groq/a", "groq/b", "groq/c"])
    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/a", "groq/b")

    async def go():
        async with app.run_test(size=(100, 40)) as pilot:
            await pilot.press("ctrl+o")
            await pilot.pause(0.1)
            assert isinstance(app.screen, tui.ModelPicker)  # no network wait
            await pilot.press("down", "enter")  # large: a -> b
            await pilot.pause(0.1)
            assert isinstance(app.screen, tui.ModelPicker)
            await pilot.press("home", "enter")  # first model: none
            await pilot.pause(0.1)
            assert not isinstance(app.screen, tui.ModelPicker)

    asyncio.run(go())
    assert app.model == "groq/b" and app.small == ""
    assert gateway.saved_model() == "groq/b" and gateway.saved_small() == ""
