"""Full-screen terminal UI in the style of OpenCode: conversation, bordered prompt, session sidebar."""

import json
import os
import threading
from pathlib import Path

import httpx
from rich.text import Text
from textual import events, work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.screen import ModalScreen
from textual.widgets import Input, Markdown, OptionList, Static

from .agent import Agent
from .budget import Budget, BudgetExceeded
from .gateway import bifrost_url, list_models, save_model
from .tools import Workspace

HELP = """**Commands**
`/new` clear the session  `/models` pick the large model  `/private` toggle private mode
`/budget N` set the USD limit  `/sidebar` toggle sidebar  `/help`  `/exit`

**Prompt**
`!cmd` run a shell command yourself  `@path` mention a file (the agent reads only what it needs)

**Keys**
`ctrl+b` sidebar  `ctrl+n` new session  `ctrl+o` models  `ctrl+q` quit"""
BAR = 20
ACCENT, WARN, TEXT, MUTED, PANEL, BG = "#5c9cf5", "#f5a742", "#eeeeee", "#808080", "#1e1e1e", "#0a0a0a"
FRAMES = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"


def pretty_path(path: Path, width: int = 48) -> str:
    text = str(path).replace(str(Path.home()), "~", 1)
    return text if len(text) <= width else "…" + text[-(width - 1):]


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


def edit_diff(args: str) -> Text | None:
    try:
        a = json.loads(args)
        old, new = a["old"], a["new"]
    except (ValueError, KeyError, TypeError):
        return None
    out = Text()
    for line in old.splitlines():
        out.append(f"- {line}\n", style="red")
    for line in new.splitlines():
        out.append(f"+ {line}\n", style="green")
    out.rstrip()
    return out


class Approval(ModalScreen[bool]):
    BINDINGS = [("y", "allow", "allow"), ("n", "deny", "deny"), ("escape", "deny", "deny")]

    def __init__(self, text: str):
        super().__init__()
        self.text = text

    def compose(self) -> ComposeResult:
        with Vertical(id="dialog"):
            yield Static("Permission required", id="dtitle")
            yield Static(diff_text(self.text.rstrip()), id="dbody")
            yield Static("[b]y[/b] allow   [b]n[/b] deny", id="dhint")

    def action_allow(self) -> None:
        self.dismiss(True)

    def action_deny(self) -> None:
        self.dismiss(False)


class ModelPicker(ModalScreen[str]):
    BINDINGS = [("escape", "cancel", "cancel")]

    def __init__(self, models: list[str], current: str):
        super().__init__()
        self.models, self.current = models, current

    def compose(self) -> ComposeResult:
        with Vertical(id="dialog"):
            yield Static("Select the large model", id="dtitle")
            yield OptionList(*[("● " if m == self.current else "  ") + m for m in self.models], id="models")

    def on_option_list_option_selected(self, event: OptionList.OptionSelected) -> None:
        self.dismiss(self.models[event.option_index])

    def action_cancel(self) -> None:
        self.dismiss("")


class TUI(App):
    TITLE = "leantoken"
    CSS = """
    Screen { background: #0a0a0a; color: #eeeeee; }
    #body { height: 1fr; }
    #main { width: 1fr; padding: 0 2; }
    #chat { height: 1fr; scrollbar-size-vertical: 1; scrollbar-background: #0a0a0a; scrollbar-color: #1e1e1e; }
    #side { width: 34; padding: 1 2; border-left: solid #1e1e1e; color: #808080; }
    #side.hidden { display: none; }
    #prompt { height: auto; background: #1e1e1e; border-left: heavy #5c9cf5; padding: 1 2 0 2; margin-top: 1; }
    #prompt.busy { border-left: heavy #f5a742; }
    #input { border: none; padding: 0; background: transparent; height: 1; color: #eeeeee; }
    #input:focus { border: none; background: transparent; background-tint: transparent; }
    #input > .input--placeholder { color: #808080; text-style: none; }
    #mode { height: 1; margin-top: 1; }
    #edge { height: 1; }
    #foot { height: 1; padding: 0 1; }
    .user { border-left: heavy #5c9cf5; padding: 0 2; margin-top: 1; background: #1e1e1e; }
    .answer { margin: 1 0 0 0; padding: 0; }
    .tool { color: #808080; margin: 0; }
    .fail { color: #e06c75; }
    .esc { color: #f5a742; }
    .diff { border-left: heavy #1e1e1e; padding: 0 1; margin: 0 0 0 2; }
    .note { color: #808080; }
    Approval, ModelPicker { align: center middle; }
    #dialog { width: 72; max-width: 90%; height: auto; max-height: 80%; border-left: heavy #f5a742; background: #1e1e1e; padding: 1 2; }
    ModelPicker #dialog { border-left: heavy #5c9cf5; }
    #dialog OptionList { background: #1e1e1e; border: none; padding: 0; }
    #dtitle { text-style: bold; margin-bottom: 1; }
    #dhint { margin-top: 1; color: #808080; }
    """
    BINDINGS = [
        Binding("ctrl+q", "quit", "quit", priority=True),
        Binding("ctrl+c", "quit", "quit", priority=True),
        Binding("ctrl+b", "sidebar", "sidebar", priority=True),
        Binding("ctrl+n", "new", "new", priority=True),
        Binding("ctrl+o", "models", "models", priority=True),
    ]

    def __init__(self, repo: Path, budget: Budget, private: bool, model: str, small: str):
        super().__init__()
        self.repo, self.budget, self.private = repo, budget, private
        self.model, self.small = model, small
        self.busy = False
        self.frame = 0
        self.task_tokens = 0
        self.session = {"tokens": 0, "kwh": 0.0, "gco2e": 0.0, "tasks": 0}
        self.files: list[str] = []

    def compose(self) -> ComposeResult:
        with Horizontal(id="body"):
            with Vertical(id="main"):
                yield VerticalScroll(id="chat")
                with Vertical(id="prompt"):
                    yield Input(placeholder="Ask anything...  \"fix the failing test\"", id="input")
                    yield Static(id="mode")
                yield Static(id="edge")
                yield Static(id="foot")
            yield Static(id="side")

    def on_mount(self) -> None:
        self.query_one("#input").focus()
        self.set_interval(0.1, self.tick)
        self.say(Static(Text("leantoken", style="bold") + Text("  the coding agent that watches its tokens", style="dim")))
        self.say(Static(Text(f"{pretty_path(self.repo)}  via {bifrost_url()}", style="dim")))
        self.call_after_refresh(self.refresh_chrome)

    @property
    def chat(self) -> VerticalScroll:
        return self.query_one("#chat", VerticalScroll)

    def say(self, widget, classes: str = "") -> None:
        if classes:
            widget.add_class(*classes.split())
        self.chat.mount(widget)
        self.chat.scroll_end(animate=False)

    def route(self) -> Text:
        t = Text()
        if self.private:
            t.append("private", style=TEXT)
            t.append(" · ", style=MUTED)
            t.append("local model only", style=MUTED)
            return t
        t.append(f"{short(self.small) + ' > ' if self.small else ''}{short(self.model)}", style=TEXT)
        t.append("  Groq", style=MUTED)
        return t

    def tick(self) -> None:
        if self.busy:
            self.frame = (self.frame + 1) % len(FRAMES)
            self.refresh_chrome()

    def refresh_chrome(self) -> None:
        self.query_one("#prompt").set_class(self.busy, "busy")
        color = WARN if self.busy else ACCENT
        mode = Text()
        if self.busy:
            mode.append(f"{FRAMES[self.frame]} working", style=WARN)
            mode.append(f"  {self.task_tokens:,} tok", style=MUTED)
        else:
            mode.append("Build", style=ACCENT)
        mode.append(" · ", style=MUTED)
        mode.append_text(self.route())
        self.query_one("#mode", Static).update(mode)
        self.query_one("#edge", Static).update(Text("╹" + "▀" * 400, style=color, no_wrap=True, overflow="crop"))
        left = Text(pretty_path(self.repo), style=MUTED)
        hints = Text()
        for key, label in (("ctrl+b", "sidebar"), ("ctrl+n", "new"), ("ctrl+o", "models"), ("ctrl+q", "quit")):
            hints.append(key + " ", style=TEXT)
            hints.append(label + "  " if key != "ctrl+q" else label, style=MUTED)
        width = self.query_one("#foot").size.width or 100
        gap = max(2, width - 2 - len(left.plain) - len(hints.plain))
        self.query_one("#foot", Static).update(left + Text(" " * gap) + hints)
        self.query_one("#side", Static).update(self.sidebar())

    def on_resize(self, event: events.Resize) -> None:
        self.call_after_refresh(self.refresh_chrome)

    def sidebar(self) -> Text:
        s, spent, limit = self.session, self.budget.spent, self.budget.limit
        filled = min(BAR, int(BAR * spent / limit)) if limit else 0
        web = not self.private and bool(os.environ.get("TAVILY_API_KEY"))
        t = Text()
        t.append("Session\n", style="bold")
        t.append(f"{self.repo.name}\n{s['tasks']} task(s)\n\n")
        t.append("Context\n", style="bold")
        t.append(f"{s['tokens'] + self.task_tokens:,} tokens\n")
        t.append(f"${spent:.4f} of ${limit:.2f}\n")
        t.append("█" * filled, style="cyan")
        t.append("░" * (BAR - filled) + "\n\n", style="dim")
        t.append("Footprint\n", style="bold")
        t.append(f"{s['kwh'] * 1000:.3f} Wh\n{s['gco2e'] * 1000:.2f} mgCO2e\n\n")
        t.append("Mode\n", style="bold")
        t.append(f"{'private (local only)' if self.private else 'cloud via Bifrost'}\n")
        t.append(f"web search {'on' if web else 'off'}\n\n")
        if self.files:
            t.append("Modified files\n", style="bold")
            t.append("\n".join(self.files[-8:]))
        return t

    def on_input_submitted(self, event: Input.Submitted) -> None:
        line = event.value.strip()
        event.input.value = ""
        if not line:
            return
        if self.busy:
            self.notify("agent is working, wait for it to finish", severity="warning")
            return
        if line.startswith("/"):
            self.command(line)
        elif line.startswith("!"):
            self.say(Static(Text(line, style="bold")), "user")
            self.busy = True
            self.shell(line[1:].strip())
        else:
            self.say(Static(line), "user")
            self.busy = True
            self.run_task(line)

    def command(self, line: str) -> None:
        cmd, _, arg = line.partition(" ")
        if cmd in ("/exit", "/quit"):
            self.exit()
        elif cmd == "/help":
            self.say(Markdown(HELP), "answer")
        elif cmd == "/new":
            self.action_new()
        elif cmd == "/models":
            self.action_models()
        elif cmd == "/sidebar":
            self.action_sidebar()
        elif cmd == "/private":
            self.private = not self.private
            self.say(Static(f"private mode {'on' if self.private else 'off'}"), "note")
        elif cmd == "/budget" and arg.replace(".", "", 1).isdigit():
            self.budget.limit = float(arg)
            self.say(Static(f"budget limit ${self.budget.limit:.2f}"), "note")
        else:
            self.say(Static("unknown command, try /help"), "note")
        self.refresh_chrome()

    def action_sidebar(self) -> None:
        self.query_one("#side").toggle_class("hidden")
        self.call_after_refresh(self.refresh_chrome)

    def action_new(self) -> None:
        if self.busy:
            return
        self.chat.remove_children()
        self.session = {"tokens": 0, "kwh": 0.0, "gco2e": 0.0, "tasks": 0}
        self.files = []
        self.refresh_chrome()

    @work(thread=True, exclusive=True, group="models")
    def action_models(self) -> None:
        try:
            models = list_models()
        except Exception as e:
            self.call_from_thread(self.say, Static(f"cannot list models: {e}"), "note fail")
            return
        if not models:
            self.call_from_thread(self.say, Static("Bifrost lists no Groq models"), "note fail")
            return
        done, box = threading.Event(), []
        self.call_from_thread(self.push_screen, ModelPicker(models, self.model), lambda m: (box.append(m), done.set()))
        done.wait()
        if box[0]:
            self.model = box[0]
            save_model(self.model)
            self.call_from_thread(self.refresh_chrome)

    @work(thread=True, exclusive=True, group="agent")
    def shell(self, cmd: str) -> None:
        try:
            out = Workspace(self.repo).run_shell(cmd)
        except Exception as e:
            out = f"error: {e}"
        self.call_from_thread(self.say, Static(Text(out.rstrip(), style="dim")), "diff")
        self.busy = False

    def approve(self, text: str) -> bool:
        done, box = threading.Event(), []
        self.call_from_thread(self.push_screen, Approval(text), lambda ok: (box.append(ok), done.set()))
        done.wait()
        return box[0]

    def on_agent_event(self, kind: str, **d) -> None:
        self.call_from_thread(self.show_event, kind, d)

    def show_event(self, kind: str, d: dict) -> None:
        if kind == "call":
            self.task_tokens += d["prompt"] + d["completion"]
        elif kind == "tool":
            name, failed = d["name"], d["failed"]
            self.say(Static(Text("● ", style="red" if failed else "green") + Text(name, style="bold") + Text(f"  {brief(name, d['args'])}", style="dim")), "tool")
            if name == "edit_file" and not failed and (diff := edit_diff(d["args"])):
                self.say(Static(diff), "diff")
                if (path := brief(name, d["args"])) not in self.files:
                    self.files.append(path)
            else:
                first = (d["out"].strip().splitlines() or [""])[0][:110]
                self.say(Static(Text(f"  ⎿ {first}")), "tool fail" if failed else "tool")
                if name == "write_file" and not failed and (path := brief(name, d["args"])) not in self.files:
                    self.files.append(path)
        elif kind == "escalate":
            self.say(Static(f"↑ escalated to {short(d['to'])}: {d['reason']}"), "esc")
        elif kind == "wait":
            self.say(Static(f"  rate limited, waiting {d['seconds']:.0f}s"), "note")
        self.refresh_chrome()

    @work(thread=True, exclusive=True, group="agent")
    def run_task(self, task: str) -> None:
        try:
            agent = Agent(self.repo, self.budget, self.private, self.model, small=self.small, approve=self.approve, on_event=self.on_agent_event)
        except SystemExit as e:
            self.call_from_thread(self.say, Static(str(e)), "note fail")
            self.busy = False
            return
        self.task_tokens = 0
        try:
            result = agent.run(task)
        except BudgetExceeded as e:
            result = f"budget exceeded: {e}"
        except httpx.ConnectError:
            result = f"cannot reach Bifrost at {bifrost_url()}. Start it with: pnpm dlx @maximhq/bifrost"
        except Exception as e:  # still show what was spent
            result = f"error: {e}"
        agent.fp.save(self.repo / ".leantoken" / "runs", result, agent.verified)
        self.call_from_thread(self.finish, agent, result)

    def finish(self, agent: Agent, result: str) -> None:
        t = agent.fp.totals()
        self.busy = False
        self.session["tokens"] += t["in"] + t["out"]
        self.session["kwh"] += t["kwh"]
        self.session["gco2e"] += t["gco2e"]
        self.session["tasks"] += 1
        self.task_tokens = 0
        self.say(Markdown(result), "answer")
        line = (f"{t['in'] + t['out']:,} tok  ${t['cost_usd']:.5f}  {t['kwh'] * 1000:.3f} Wh  {t['gco2e'] * 1000:.1f} mgCO2e"
                f"  {t['calls']} calls  {len(agent.fp.escalations)} escalations  {agent.verified}")
        self.say(Static(Text(line, style="dim")), "note")
        self.refresh_chrome()

    def loop(self) -> None:
        self.run()
