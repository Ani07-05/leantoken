"""Builds the benchmark project: a small shop library, with tests, plus one seeded bug per coding task.

Deterministic, so every run and both agents see byte-identical files.
"""

from pathlib import Path

MODULES = {
    "pricing": '''"""Price calculations for the shop."""

TAX_RATE = 0.18


def apply_discount(price, discount):
    """Return the price after a fractional discount, where 0.2 means 20% off."""
    return round(price * (1 - discount), 2)


def add_tax(price, rate=TAX_RATE):
    """Return the price including tax."""
    return round(price * (1 + rate), 2)


def bulk_price(unit_price, quantity):
    """Unit price falls by 5% from 10 units and by 10% from 50 units."""
    if quantity >= 50:
        return round(unit_price * 0.90, 2)
    if quantity >= 10:
        return round(unit_price * 0.95, 2)
    return unit_price
''',
    "inventory": '''"""Stock tracking for the shop."""


class Inventory:
    def __init__(self):
        self.stock = {}

    def add(self, item, quantity):
        """Add units of an item to stock."""
        self.stock[item] = self.stock.get(item, 0) + quantity

    def remove(self, item, quantity):
        """Take units out of stock, refusing to go negative."""
        if self.stock.get(item, 0) < quantity:
            raise ValueError(f"not enough {item} in stock")
        self.stock[item] -= quantity

    def needs_restock(self, item, threshold):
        """True when the stock of an item is at or below the threshold."""
        return self.stock.get(item, 0) <= threshold
''',
    "cart": '''"""Shopping cart."""

from pricing import apply_discount


class Cart:
    def __init__(self):
        self.items = []

    def add(self, name, price, qty=1):
        """Put a line item in the cart."""
        self.items.append({"name": name, "price": price, "qty": qty})

    def total(self, discount=0.0):
        """Sum of price times quantity over all lines, after any cart-wide discount."""
        raw = sum(i["price"] * i["qty"] for i in self.items)
        return apply_discount(raw, discount)
''',
    "orders": '''"""Orders and their life cycle."""

import datetime

STATUSES = ("new", "paid", "shipped", "delivered")


def make_order(order_id, total, when=None):
    """Create an order in the new state."""
    return {"id": order_id, "total": total, "status": "new", "date": when or datetime.date.today()}


def advance(order):
    """Move an order to the next status; delivered orders stay delivered."""
    i = STATUSES.index(order["status"])
    order["status"] = STATUSES[min(i + 1, len(STATUSES) - 1)]
    return order
''',
    "reports": '''"""Sales reports."""


def monthly_summary(orders):
    """Total order value per month, keyed by year-month such as 2026-03."""
    out = {}
    for order in orders:
        d = order["date"]
        key = f"{d.year}-{d.month:02d}"
        out[key] = round(out.get(key, 0) + order["total"], 2)
    return out
''',
}

TESTS = {
    "test_pricing": '''from pricing import add_tax, apply_discount, bulk_price


def test_apply_discount():
    assert apply_discount(100, 0.2) == 80


def test_add_tax():
    assert add_tax(100) == 118


def test_bulk_price():
    assert bulk_price(10, 50) == 9
''',
    "test_inventory": '''from inventory import Inventory


def test_needs_restock_at_threshold():
    inv = Inventory()
    inv.add("pen", 5)
    assert inv.needs_restock("pen", 5)
    assert not inv.needs_restock("pen", 4)
''',
    "test_cart": '''from cart import Cart


def test_total_counts_quantity():
    cart = Cart()
    cart.add("pen", 2.0, qty=3)
    cart.add("book", 10.0)
    assert cart.total() == 16.0
''',
    "test_reports": '''import datetime

from orders import make_order
from reports import monthly_summary


def test_monthly_summary():
    orders = [make_order(1, 10.0, datetime.date(2026, 3, 5)), make_order(2, 5.5, datetime.date(2026, 3, 20))]
    assert monthly_summary(orders) == {"2026-03": 15.5}
''',
}

# (file, clean text, buggy text): applied to one fresh copy per coding task
BUGS = {
    "discount": ("pricing.py", "price * (1 - discount)", "price * discount"),
    "restock": ("inventory.py", "<= threshold", "< threshold"),
    "cart": ("cart.py", 'i["price"] * i["qty"]', 'i["price"]'),
    "month": ("reports.py", "{d.month:02d}", "{d.month - 1:02d}"),
}


def helpers(module: str, count: int = 14) -> str:
    """Plain utility functions so every module is a realistic few hundred lines, not a toy."""
    seed = sum(map(ord, module))
    out = []
    for i in range(count):
        a, m, k = (seed * (i + 3)) % 17 + 2, (seed + i * 7) % 29 + 11, i % 5 + 3
        out.append(f'''

def {module}_rule_{i}(value):
    """Normalise a value according to {module} rule {i}."""
    total = 0
    for step in range({k}):
        total += (value * {a} + step) % {m}
    if total > {m * k // 2}:
        total -= {k}
    return total
''')
    return "".join(out)


def build(root: Path, bug: str | None = None) -> Path:
    root.mkdir(parents=True, exist_ok=True)
    (root / "tests").mkdir(exist_ok=True)
    for name, code in MODULES.items():
        if bug and BUGS[bug][0] == f"{name}.py":
            _, clean, buggy = BUGS[bug]
            assert clean in code
            code = code.replace(clean, buggy)
        (root / f"{name}.py").write_text(code + helpers(name))
    for name, code in TESTS.items():
        (root / "tests" / f"{name}.py").write_text(code)
    (root / "conftest.py").write_text("import sys, pathlib\nsys.path.insert(0, str(pathlib.Path(__file__).parent))\n")
    return root
