"""Benchmark: the same model on the same tasks, with and without slendertoken's token controls.

  .venv/bin/python bench/run.py --model groq/openai/gpt-oss-120b

Both arms go through Bifrost. Token counts are the API's own `usage`, not estimates.
The naive arm keeps whole files, unclipped tool output and the full transcript across turns.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
sys.path.insert(0, str(Path(__file__).parent.parent))

from make_repo import BUGS, build  # noqa: E402

from slendertoken.agent import Agent, memory_note  # noqa: E402
from slendertoken.budget import Budget  # noqa: E402

CODING_PROMPT = "The tests in this project are failing. Run them, find the bug and fix it."
# every expected group must appear (any word within a group) in the lowercased answer
SESSION = [
    ("Which module computes discounts, and which function?", [["pricing"], ["apply_discount"]]),
    ("What does that function return for a price of 100 and a discount of 0.2?", [["80"]]),
    ("Where is the tax rate defined and what is it?", [["0.18", "18%"]]),
    ("How does the cart compute its total?", [["qty", "quantity"]]),
    ("When does the inventory say an item needs restocking?", [["threshold"]]),
    ("Which module groups orders by month?", [["reports"]]),
    ("What format are the keys of the monthly summary?", [["year"], ["month"]]),
    ("Summarise what you told me about pricing in two bullets.", [["pricing"], ["discount", "tax"]]),
]


def load_env() -> None:
    for line in (Path(__file__).parent.parent / ".env").read_text().splitlines():
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            os.environ.setdefault(k, v)


def passes(repo: Path) -> bool:
    return subprocess.run([sys.executable, "-m", "pytest", "-q"], cwd=repo, capture_output=True).returncode == 0


def measure(agent: Agent, started: float, result: str, error: str = "") -> dict:
    t = agent.fp.totals()
    return {"tokens": t["in"] + t["out"], "in": t["in"], "out": t["out"], "calls": t["calls"], "cost_usd": round(t["cost_usd"], 5),
            "seconds": round(time.time() - started, 1), "models": sorted(t["by_model"]), "error": error, "answer": result[:300]}


def new_agent(repo: Path, model: str, lean: bool, **kw) -> Agent:
    budget = Budget(Path(tempfile.mkdtemp()) / "b.json", 50.0)
    return Agent(repo, budget, False, model, small=model, approve=lambda _: True, lean=lean, **kw)


def coding(bug: str, model: str, lean: bool) -> dict:
    repo = build(Path(tempfile.mkdtemp()) / "shop", bug)
    agent, started, result, error = new_agent(repo, model, lean), time.time(), "", ""
    try:
        result = agent.run(CODING_PROMPT)
    except Exception as e:
        error = str(e)[:200]
    row = measure(agent, started, result, error)
    row["passed"] = passes(repo)
    return row


def session(model: str, lean: bool, pause: int) -> list[dict]:
    repo = build(Path(tempfile.mkdtemp()) / "shop")
    turns, prior, history, rows = [], 0, [], []
    for q, expect in SESSION:
        kw = {"memory": memory_note(turns), "prior_naive": prior} if lean else {"history": history}
        agent, started, result, error = new_agent(repo, model, lean, **kw), time.time(), "", ""
        try:
            result = agent.run(q)
        except Exception as e:
            error = str(e)[:200]
        row = measure(agent, started, result, error)
        row["question"] = q
        row["passed"] = not error and all(any(w in result.lower() for w in group) for group in expect)
        rows.append(row)
        if error:
            break
        turns.append(agent.note(result))
        prior += agent.turn_naive
        if not lean:
            history = agent.messages[1:]
        time.sleep(pause)
    return rows


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="groq/openai/gpt-oss-120b")
    ap.add_argument("--pause", type=int, default=45, help="seconds between runs, lets the per-minute token budget refill")
    ap.add_argument("--only", choices=["coding", "session"])
    ap.add_argument("--bugs", nargs="*", default=list(BUGS))
    ap.add_argument("--out", default=str(Path(__file__).parent / "results.json"))
    a = ap.parse_args()
    load_env()
    results = {"model": a.model, "coding": {}, "session": {}}
    for arm in ("lean", "naive"):
        lean = arm == "lean"
        if a.only != "session":
            results["coding"][arm] = {}
            for bug in a.bugs:
                row = coding(bug, a.model, lean)
                results["coding"][arm][bug] = row
                print(f"[coding/{arm}] {bug}: {row['tokens']:,} tokens, {row['calls']} calls, passed={row['passed']} {row['error']}", flush=True)
                Path(a.out).write_text(json.dumps(results, indent=1))
                time.sleep(a.pause)
        if a.only != "coding":
            rows = session(a.model, lean, a.pause)
            results["session"][arm] = rows
            for i, r in enumerate(rows, 1):
                print(f"[session/{arm}] turn {i}: {r['tokens']:,} tokens, passed={r['passed']} {r['error']}", flush=True)
            Path(a.out).write_text(json.dumps(results, indent=1))
    print("saved", a.out)


if __name__ == "__main__":
    main()
