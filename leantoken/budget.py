import json
from pathlib import Path

# USD per 1M tokens (input, output)
PRICES = {
    "openai/gpt-4o-mini": (0.15, 0.60),
    "anthropic/claude-haiku-4-5-20251001": (1.00, 5.00),
}


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

    def charge(self, model: str, prompt_tokens: int, completion_tokens: int) -> float:
        pin, pout = PRICES.get(model, (0.0, 0.0))
        cost = (prompt_tokens * pin + completion_tokens * pout) / 1e6
        self.spent += cost
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text(json.dumps({"spent": self.spent}))
        return cost
