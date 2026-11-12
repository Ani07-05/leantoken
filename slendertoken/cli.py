import argparse
import os
import sys
from pathlib import Path

from . import config
from .agent import Agent
from .budget import Budget, BudgetExceeded
from .gateway import DEFAULT_SMALL, pick_model, resolve_model, resolve_small, saved_model
from .tui import TUI



def plain_event(kind: str, **d) -> None:
    if kind == "wait":
        print(f"[rate limited, waiting {d['seconds']:.0f}s]")
    elif kind == "escalate":
        print(f"[escalated to {d['to']}: {d['reason']}]")


def main() -> None:
    # Windows consoles choke on emoji in model replies
    sys.stdout.reconfigure(errors="replace")
    config.load_all()
    if sys.argv[1:2] == ["config"]:
        config.main(sys.argv[2:])
        return
    ap = argparse.ArgumentParser(prog="slendertoken", epilog="settings: slendertoken config [list|set|unset|edit|path]")
    ap.add_argument("task", nargs="?")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--model", help="large Groq model via Bifrost (default: SLENDERTOKEN_MODEL or your saved pick)")
    ap.add_argument("--small", help=f"first model, tried before the large one (default: SLENDERTOKEN_SMALL_MODEL, your saved pick or {DEFAULT_SMALL})")
    ap.add_argument("--no-route", action="store_true", help="use only the large model")
    ap.add_argument("--pick-model", action="store_true", help="pick the large and first models and save them")
    ap.add_argument("--budget", type=float, default=0.50, help="USD limit, persisted across runs")
    ap.add_argument("--private", action="store_true", help="local model only, nothing leaves the machine")
    a = ap.parse_args()
    if a.task and not os.environ.get("BIFROST_VK") and not a.private:
        print(f"note: BIFROST_VK is not set. Run `slendertoken config set BIFROST_VK <key>` ({config.GLOBAL_ENV})")
    if a.pick_model:
        pick_model()
        if not a.task:
            return
    if not a.task and not sys.stdin.isatty():
        ap.error("task is required when stdin is not a terminal")

    repo = Path(a.repo).resolve()
    private = a.private or (repo / ".slendertoken-private").exists()
    # the TUI opens settings and the model picker itself, so don't block on them here
    if private:
        model = small = ""
    else:
        model = resolve_model(a.model) if a.task else (a.model or os.environ.get("SLENDERTOKEN_MODEL") or saved_model() or "")
        small = "" if a.no_route else resolve_small(a.small)
    budget = Budget(repo / ".slendertoken" / "budget.json", a.budget)
    if not a.task:
        TUI(repo, budget, private, model, small).loop()
        return
    agent = Agent(repo, budget, private, model, small=small, on_event=plain_event)
    try:
        result = agent.run(a.task)
    except BudgetExceeded as e:
        result = f"budget exceeded: {e}"
    except Exception as e:  # still report what was spent
        result = f"error: {e}"
    print(result)
    print(agent.fp.report(result, agent.verified))
    path = agent.fp.save(repo / ".slendertoken" / "runs", result, agent.verified)
    print(f"saved {path}  (budget spent ${agent.budget.spent:.4f} of ${a.budget:.2f})")
