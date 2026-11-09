import argparse
import os
import sys
from pathlib import Path

from .agent import Agent
from .budget import Budget, BudgetExceeded
from .gateway import pick_model, resolve_model

DEFAULT_SMALL = "groq/openai/gpt-oss-20b"


def load_env(path: Path) -> None:
    """Minimal .env loader; real environment variables win."""
    if not path.is_file():
        return
    for line in path.read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            os.environ.setdefault(k.strip(), v.strip().strip("\"'"))


def main() -> None:
    ap = argparse.ArgumentParser(prog="leantoken")
    ap.add_argument("task", nargs="?")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--model", help="large Groq model via Bifrost (default: LEANTOKEN_MODEL or your saved pick)")
    ap.add_argument("--small", help=f"small model tried first (default: LEANTOKEN_SMALL_MODEL or {DEFAULT_SMALL})")
    ap.add_argument("--no-route", action="store_true", help="use only the large model")
    ap.add_argument("--pick-model", action="store_true", help="list Groq models and save your choice")
    ap.add_argument("--budget", type=float, default=0.50, help="USD limit, persisted across runs")
    ap.add_argument("--private", action="store_true", help="local model only, nothing leaves the machine")
    a = ap.parse_args()
    # Windows consoles choke on emoji in model replies
    sys.stdout.reconfigure(errors="replace")

    load_env(Path.cwd() / ".env")
    if a.pick_model:
        pick_model()
        if not a.task:
            return
    if not a.task:
        ap.error("task is required")

    repo = Path(a.repo).resolve()
    private = a.private or (repo / ".leantoken-private").exists()
    model = "" if private else resolve_model(a.model)
    small = "" if private or a.no_route else (a.small or os.environ.get("LEANTOKEN_SMALL_MODEL") or DEFAULT_SMALL)
    agent = Agent(repo, Budget(repo / ".leantoken" / "budget.json", a.budget), private, model, small=small)
    try:
        result = agent.run(a.task)
    except BudgetExceeded as e:
        result = f"budget exceeded: {e}"
    except Exception as e:  # still report what was spent
        result = f"error: {e}"
    print(result)
    print(agent.fp.report(result, agent.verified))
    path = agent.fp.save(repo / ".leantoken" / "runs", result, agent.verified)
    print(f"saved {path}  (budget spent ${agent.budget.spent:.4f} of ${a.budget:.2f})")
