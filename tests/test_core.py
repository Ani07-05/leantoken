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
    b.charge("openai/gpt-4o-mini", 10_000, 0)
    with pytest.raises(BudgetExceeded):
        Budget(tmp_path / "b.json", 0.001).check()
    assert json.loads((tmp_path / "b.json").read_text())["spent"] > 0
