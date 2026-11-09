"""Terminal UI: a REPL where each line is a task for the agent."""

import json
from pathlib import Path

import httpx
from prompt_toolkit import PromptSession
from prompt_toolkit.history import FileHistory
from rich.console import Console
from rich.markdown import Markdown
from rich.panel import Panel
from rich.table import Table
from rich.text import Text

from .agent import Agent
from .budget import Budget, BudgetExceeded
from .gateway import CONFIG, bifrost_url, pick_model

HELP = """/help      this list
/private   toggle private mode (local model only, no web search)
/model     pick the large Groq model
/budget N  set the USD limit
/report    footprint of the last task
/exit      quit"""


def short(model: str) -> str:
    return model.rsplit("/", 1)[-1]


def brief(name: str, args: str) -> str:
    try:
        a = json.loads(args or "{}")
    except ValueError:
        return args[:60]
    if name in ("read_file", "edit_file", "write_file"):
        end = f":{a.get('start', 1)}-{a['end']}" if name == "read_file" and "end" in a else ""
        return f"{a.get('path', '')}{end}"
    cmd = a.get("cmd", a.get("query", a.get("pattern", "")))
    return (" ".join(cmd) if isinstance(cmd, list) else str(cmd))[:80]


def diff_text(text: str) -> Text:
    out = Text()
    for line in text.splitlines():
        style = "red" if line.startswith("- ") else "green" if line.startswith("+ ") else "bold"
        out.append(line + "\n", style=style)
    return out


class TUI:
    def __init__(self, repo: Path, budget: Budget, private: bool, model: str, small: str):
        self.repo, self.budget, self.private = repo, budget, private
        self.model, self.small = model, small
        self.console = Console(highlight=False)
        self.status = None
        self.last: Agent | None = None
        self.last_result = ""

    def toolbar(self) -> str:
        mode = "private" if self.private else f"{short(self.small) + ' > ' if self.small else ''}{short(self.model)}"
        return f" {mode}  |  ${self.budget.spent:.4f} / ${self.budget.limit:.2f}  |  /help "

    def banner(self) -> None:
        c = self.console
        c.print(Text("leantoken", style="bold cyan") + Text("  the coding agent that watches its tokens", style="dim"))
        c.print(Text(f"  {self.repo}", style="dim"))
        c.print(Text(f"  gateway {bifrost_url()}", style="dim"))
        c.print()

    def on_event(self, kind: str, **d) -> None:
        c = self.console
        if kind == "call" and self.status:
            self.status.update(f"thinking  {short(d['model'])}  {d['prompt'] + d['completion']:,} tok")
        elif kind == "tool":
            mark = Text("● ", style="red" if d["failed"] else "green")
            c.print(mark + Text(d["name"], style="bold") + Text(f"  {brief(d['name'], d['args'])}", style="dim"))
            first = (d["out"].strip().splitlines() or [""])[0][:100]
            c.print(Text(f"  ⎿ {first}", style="red" if d["failed"] else "dim"))
        elif kind == "escalate":
            c.print(Text(f"↑ escalated to {short(d['to'])}: {d['reason']}", style="yellow"))
        elif kind == "wait":
            c.print(Text(f"  rate limited, waiting {d['seconds']:.0f}s", style="dim"))

    def approve(self, text: str) -> bool:
        if self.status:
            self.status.stop()
        self.console.print(Panel(diff_text(text), title="approval needed", border_style="yellow", expand=False))
        ok = self.console.input("[bold]allow? (y/N) [/]").strip().lower() == "y"
        if self.status:
            self.status.start()
        return ok

    def footprint(self, agent: Agent) -> None:
        t = agent.fp.totals()
        grid = Table.grid(padding=(0, 2))
        rows = [
            ("verified", agent.verified),
            ("calls", f"{t['calls']}  (fallbacks {agent.fp.fallbacks}, escalations {len(agent.fp.escalations)})"),
            ("tokens", f"{t['in'] + t['out']:,}  (in {t['in']:,} / out {t['out']:,})"),
            ("cost", f"${t['cost_usd']:.5f}"),
            ("energy", f"{t['kwh'] * 1000:.4f} Wh"),
            ("carbon", f"{t['gco2e'] * 1000:.3f} mgCO2e"),
        ]
        for k, v in rows:
            grid.add_row(Text(k, style="dim"), v)
        self.console.print(Panel(grid, title="footprint", border_style="cyan", expand=False))

    def run_task(self, task: str) -> None:
        try:
            agent = Agent(self.repo, self.budget, self.private, self.model, small=self.small, approve=self.approve, on_event=self.on_event)
        except SystemExit as e:
            self.console.print(Text(str(e), style="red"))
            return
        self.last = agent
        try:
            with self.console.status("thinking", spinner="dots") as st:
                self.status = st
                result = agent.run(task)
        except BudgetExceeded as e:
            result = f"budget exceeded: {e}"
        except httpx.ConnectError:
            result = f"error: cannot reach Bifrost at {bifrost_url()}. Start it with: npx -y @maximhq/bifrost"
        except Exception as e:  # still show what was spent
            result = f"error: {e}"
        finally:
            self.status = None
        self.last_result = result
        self.console.print()
        self.console.print(Markdown(result))
        self.footprint(agent)
        agent.fp.save(self.repo / ".leantoken" / "runs", result, agent.verified)

    def command(self, line: str) -> bool:
        cmd, _, arg = line.partition(" ")
        if cmd in ("/exit", "/quit"):
            return False
        if cmd == "/help":
            self.console.print(HELP)
        elif cmd == "/private":
            self.private = not self.private
            self.console.print(f"private mode {'on' if self.private else 'off'}")
        elif cmd == "/model":
            self.model = pick_model()
        elif cmd == "/budget" and arg.replace(".", "", 1).isdigit():
            self.budget.limit = float(arg)
        elif cmd == "/report" and self.last:
            self.footprint(self.last)
        else:
            self.console.print("unknown command, try /help")
        return True

    def loop(self) -> None:
        CONFIG.parent.mkdir(parents=True, exist_ok=True)
        session = PromptSession(history=FileHistory(str(CONFIG.parent / "history")), bottom_toolbar=self.toolbar)
        self.banner()
        while True:
            try:
                line = session.prompt("❯ ").strip()
            except (EOFError, KeyboardInterrupt):
                return
            if not line:
                continue
            if line.startswith("/"):
                if not self.command(line):
                    return
                continue
            try:
                self.run_task(line)
            except KeyboardInterrupt:
                self.console.print("[dim]interrupted[/]")
