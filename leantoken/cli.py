import argparse
from pathlib import Path

from .agent import Agent
from .budget import Budget, BudgetExceeded


def main() -> None:
    ap = argparse.ArgumentParser(prog="leantoken")
    ap.add_argument("task")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--budget", type=float, default=0.50, help="USD limit, persisted across runs")
    ap.add_argument("--private", action="store_true", help="local model only, nothing leaves the machine")
    a = ap.parse_args()

    repo = Path(a.repo).resolve()
    private = a.private or (repo / ".leantoken-private").exists()
    agent = Agent(repo, Budget(repo / ".leantoken" / "budget.json", a.budget), private)
    try:
        print(agent.run(a.task))
    except BudgetExceeded as e:
        print(f"budget exceeded: {e}")
    u = agent.usage
    print(f"\ncalls={u.calls} in={u.prompt} out={u.completion} spent=${agent.budget.spent:.4f} models={u.models}")
