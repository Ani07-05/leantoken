import json
import os
from pathlib import Path

# Local backup to the Bifrost virtual-key budget, which is the real limit.
# Groq USD per 1M tokens (input, output), console.groq.com/docs/models, Nov 2026.
# Unpublished prices get DEFAULT_PRICE so the limit still binds; override with SLENDERTOKEN_PRICE="in,out".
PRICES = {
    "openai/gpt-oss-120b": (0.15, 0.60),
    "openai/gpt-oss-20b": (0.075, 0.30),
    "qwen/qwen3.8-27b": (0.80, 4.00),
}
DEFAULT_PRICE = (1.00, 4.00)


def price(model: str) -> tuple[float, float]:
    if override := os.environ.get("SLENDERTOKEN_PRICE"):
        pin, pout = (float(x) for x in override.split(","))
        return pin, pout
    return PRICES.get(model.removeprefix("groq/"), DEFAULT_PRICE)


class BudgetExceeded(Exception):
    pass


class Budget:
    def __init__(self, path: Path, limit_usd: float):
        self.path = path
        self.limit = limit_usd
        self.spent = json.loads(path.read_text())["spent"] if path.exists() else 0.0

    def check(self) -> None:
        if self.spent >= self.limit:
            raise BudgetExceeded(f"spent ${self.spent:.4f} of ${self.limit:.2f}")

    def charge(self, model: str, prompt_tokens: int, completion_tokens: int, local: bool = False) -> float:
        pin, pout = (0.0, 0.0) if local else price(model)
        cost = (prompt_tokens * pin + completion_tokens * pout) / 1e6
        self.spent += cost
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text(json.dumps({"spent": self.spent}))
        return cost
