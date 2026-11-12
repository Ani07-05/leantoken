"""Render the README screenshots (docs/home.svg, docs/working.svg, docs/animals.png).

  .venv/bin/python docs/screens.py
"""

import asyncio
import os
import tempfile
import time
from pathlib import Path

os.environ["BIFROST_VK"] = "sk-bf-demo"

from PIL import Image  # noqa: E402

from slendertoken import art, tui
from slendertoken.budget import Budget
from slendertoken.footprint import Footprint

HERE = Path(__file__).parent
SIZE = (120, 36)


class StubAgent:
    def __init__(self, *a, on_event=None, **k):
        self.on_event, self.verified, self.fp, self.turn_naive = on_event, "tests passing", Footprint(""), 0

    def note(self, result):
        return {"task": "t", "answer": result, "files": []}

    def run(self, task):
        steps = (("search", '{"pattern": "def apply_discount"}', "shop/pricing.py:14: def apply_discount(total, pct):"),
                 ("read_file", '{"path": "shop/pricing.py", "start": 10, "end": 30}', "def apply_discount(total, pct): ..."),
                 ("run_shell", '{"command": "pytest -q"}', "1 failed, 11 passed"))
        for name, args, out in steps:
            self.on_event("start", name=name, args=args)
            time.sleep(1.2)
            self.on_event("call", prompt=900, completion=60, saved=1400)
            self.on_event("tool", name=name, args=args, out=out, failed=name == "run_shell")
        self.on_event("start", name="edit_file", args='{"path": "shop/pricing.py"}')
        time.sleep(30)
        return "done"


async def shoot() -> None:
    tmp = Path(tempfile.mkdtemp()) / "shop"
    tmp.mkdir()
    tui.pretty_path = lambda path, width=48: "~/projects/shop"
    tui.Agent = StubAgent
    app = tui.TUI(tmp, Budget(tmp / "b.json", 1.0), False, "groq/openai/gpt-oss-120b", "groq/openai/gpt-oss-20b")
    async with app.run_test(size=SIZE) as pilot:
        await pilot.pause(0.5)
        app.gateway_ok = True
        app.refresh_chrome()
        (HERE / "home.svg").write_text(app.export_screenshot(title="slendertoken"))
        await pilot.press(*"fix the failing discount test", "enter")
        await pilot.pause(4.5)
        app.gateway_ok = True
        (HERE / "working.svg").write_text(app.export_screenshot(title="slendertoken"))
        app.workers.cancel_all()


def animals() -> None:
    scenes = ("think", "type", "search", "read", "shell", "web")
    frames = [art.frame(s, 6) for s in scenes]
    w, h = frames[0].size
    strip = Image.new("RGBA", (w * len(frames), h), (0, 0, 0, 0))
    for i, f in enumerate(frames):
        strip.paste(f, (i * w, 0))
    strip.save(HERE / "animals.png")


asyncio.run(shoot())
animals()
