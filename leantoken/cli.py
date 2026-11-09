import argparse
import os
from pathlib import Path

from .agent import Agent
from .budget import Budget, BudgetExceeded
from .gateway import pick_model, resolve_model


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
    ap.add_argument("--model", help="Groq model id via Bifrost (default: LEANTOKEN_MODEL or your saved pick)")
    ap.add_argument("--pick-model", action="store_true", help="list Groq models and save your choice")
    ap.add_argument("--budget", type=float, default=0.50, help="USD limit, persisted across runs")
    ap.add_argument("--private", action="store_true", help="local model only, nothing leaves the machine")
    a = ap.parse_args()

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
    agent = Agent(repo, Budget(repo / ".leantoken" / "budget.json", a.budget), private, model)
    try:
        print(agent.run(a.task))
    except BudgetExceeded as e:
        print(f"budget exceeded: {e}")
    u = agent.usage
    print(f"\ncalls={u.calls} fallbacks={u.fallbacks} in={u.prompt} out={u.completion} spent=${agent.budget.spent:.4f} models={u.models}")
