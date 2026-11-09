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
GOOD, BAD = "#7fd88f", "#e06c75"
COMMANDS = {
    "/new": "clear the session",
    "/models": "pick the large model",
    "/private": "toggle private mode",
    "/budget": "set the USD limit, e.g. /budget 0.5",
    "/sidebar": "toggle the sidebar",
    "/help": "show help",
    "/exit": "quit",
}
ACCENT, WARN, TEXT, MUTED, PANEL, BG = "#5c9cf5", "#f5a742", "#eeeeee", "#808080", "#1e1e1e", "#0a0a0a"
FRAMES = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"


GLYPHS = {
    "l": ("█   ", "█   ", "▀▀▀▀"),
    "e": ("█▀▀█", "█▀▀▀", "▀▀▀▀"),
    "a": ("█▀▀█", "█▀▀█", "▀  ▀"),
    "n": ("█▀▀▄", "█  █", "▀  ▀"),
    "t": ("▀█▀▀", " █  ", " ▀▀▀"),
    "o": ("█▀▀█", "█  █", "▀▀▀▀"),
    "k": ("█ ▄▀", "█▀▄ ", "▀  ▀"),
}


def logo() -> Text:
    out = Text(justify="center")
    for row in range(3):
        for i, ch in enumerate("leantoken"):
            out.append(GLYPHS[ch][row] + ("" if i == 8 else " "), style=MUTED if i < 4 else TEXT)
        out.append("\n" if row < 2 else "")
    return out


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


LABELS = {"once": "Allow once", "always": "Allow always", "reject": "Reject"}
# a shell command can differ every time, so "always" is only offered for file edits
ALWAYS_KINDS = ("edit", "write")


class Permission(Vertical):
    """Replaces the prompt box while the agent waits for approval, like OpenCode."""

    can_focus = True

    def __init__(self, **kw):
        super().__init__(**kw)
        self.choices = ["once", "reject"]
        self.index = 0
        self.done = None

    def compose(self) -> ComposeResult:
        yield Static(id="ptitle")
        yield Static(id="pbody")
        yield Static(id="popts")

    def ask(self, text: str, allow_always: bool, done) -> None:
        self.choices = ["once"] + (["always"] if allow_always else []) + ["reject"]
        self.index, self.done = 0, done
        head, _, body = text.partition("\n")
        title = Text("△ ", style=WARN) + Text("Permission required", style=f"bold {WARN}")
        title.append("   " + head.replace("shell: ", "$ ", 1), style=TEXT)
        self.query_one("#ptitle", Static).update(title)
        lines = body.rstrip().splitlines()
        shown = lines[:14] + ([f"... {len(lines) - 14} more lines"] if len(lines) > 14 else [])
        box = self.query_one("#pbody", Static)
        box.display = bool(lines)
        box.update(diff_text("\n".join(shown)))
        self.add_class("show")
        self.draw()
        self.focus()

    def draw(self) -> None:
        t = Text()
        for i, choice in enumerate(self.choices):
            t.append(f" {LABELS[choice]} ", style=f"bold {BG} on {ACCENT}" if i == self.index else TEXT)
            t.append("  ")
        t.append("  arrows select  enter confirm  esc reject", style=MUTED)
        self.query_one("#popts", Static).update(t)

    def decide(self, choice: str) -> None:
        done, self.done = self.done, None
        self.remove_class("show")
        if done:
            done(choice)

    def on_key(self, event: events.Key) -> None:
        key, n = event.key, len(self.choices)
        if key in ("left", "h", "shift+tab"):
            self.index = (self.index - 1) % n
        elif key in ("right", "l"):
            self.index = (self.index + 1) % n
        elif key == "enter":
            self.decide(self.choices[self.index])
        elif key == "y":
            self.decide("once")
        elif key == "a" and "always" in self.choices:
            self.decide("always")
        elif key in ("n", "escape"):
            self.decide("reject")
        else:
            return
        event.stop()
        event.prevent_default()
        if self.done:
            self.draw()


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
    #hint { display: none; height: auto; margin-top: 1; padding: 0 2; background: #141414; border-left: heavy #1e1e1e; }
    #hint.show { display: block; }
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
    #foot { height: 1; padding: 0 3; }
    #logo { display: none; height: auto; width: 100%; margin-bottom: 2; text-align: center; }
    #body.home #logo { display: block; width: 80; max-width: 100%; }
    #body.home #chat { display: none; }
    #body.home #side { display: none; }
    #body.home #main { align: center middle; }
    #body.home #prompt { width: 80; max-width: 100%; margin-top: 0; }
    .user { border-left: heavy #5c9cf5; padding: 1 2; margin-top: 1; background: #1e1e1e; }
    .answer { margin: 1 0 0 0; padding: 0 2; border-left: heavy #2a2a2a; height: auto; background: transparent; }
    .answer MarkdownParagraph, .answer Markdown > * { margin: 0 0 1 0; }
    MarkdownH1, MarkdownH2, MarkdownH3, MarkdownH4 { color: #5c9cf5; text-style: bold; background: transparent; border: none; padding: 0; margin: 1 0 0 0; text-align: left; content-align: left top; }
    MarkdownFence { background: #141414; margin: 0 0 1 0; padding: 0 1; max-height: 20; }
    MarkdownBulletList, MarkdownOrderedList { margin: 0 0 1 0; }
    MarkdownTable { margin: 0 0 1 0; }
    .tool { color: #808080; margin: 0; }
    .saved { margin: 0 0 0 2; }
    .fail { color: #e06c75; }
    .esc { color: #f5a742; }
    .diff { border-left: heavy #1e1e1e; padding: 0 1; margin: 0 0 0 2; }
    .note { color: #808080; }
    ModelPicker { align: center middle; }
    #dialog { width: 72; max-width: 90%; height: auto; max-height: 80%; border-left: heavy #5c9cf5; background: #1e1e1e; padding: 1 2; }
    #dialog OptionList { background: #1e1e1e; border: none; padding: 0; }
    #dtitle { text-style: bold; margin-bottom: 1; }
    #dhint { margin-top: 1; color: #808080; }
    #prompt.hidden { display: none; }
    #perm { display: none; height: auto; background: #1e1e1e; border-left: heavy #f5a742; padding: 1 2; margin-top: 1; }
    #perm.show { display: block; }
    #pbody { margin-top: 1; }
    #popts { margin-top: 1; }
    """
    BINDINGS = [
        Binding("ctrl+q", "quit", "quit", priority=True),
        Binding("ctrl+c", "quit", "quit", priority=True),
        Binding("ctrl+b", "sidebar", "sidebar", priority=True),
        Binding("ctrl+n", "new", "new", priority=True),
        Binding("ctrl+o", "models", "models", priority=True),
        Binding("tab", "complete", "complete", priority=True),
    ]

    def __init__(self, repo: Path, budget: Budget, private: bool, model: str, small: str):
        super().__init__()
        self.repo, self.budget, self.private = repo, budget, private
        self.model, self.small = model, small
        self.busy = False
        self.frame = 0
        self.task_tokens = 0
        self.task_saved = 0.0
        self.session = {"tokens": 0, "saved": 0, "kwh": 0.0, "gco2e": 0.0, "tasks": 0}
        self.files: list[str] = []
        self.matches: list[str] = []
        self.always: set[str] = set()
        self.gateway_ok: bool | None = None

    def compose(self) -> ComposeResult:
        with Horizontal(id="body", classes="home"):
            with Vertical(id="main"):
                yield Static(logo(), id="logo")
                yield VerticalScroll(id="chat")
                yield Static(id="hint")
                with Vertical(id="prompt"):
                    yield Input(placeholder="Ask anything...  \"fix the failing test\"", id="input")
                    yield Static(id="mode")
                yield Permission(id="perm")
            yield Static(id="side")
        yield Static(id="foot")

    def on_mount(self) -> None:
        self.query_one("#input").focus()
        self.set_interval(0.1, self.tick)
        self.set_interval(15, self.check_gateway)
        self.check_gateway()
        self.call_after_refresh(self.refresh_chrome)

    @work(thread=True, exclusive=True, group="health")
    def check_gateway(self) -> None:
        try:
            ok = httpx.get(f"{bifrost_url()}/health", timeout=2).status_code == 200
        except httpx.HTTPError:
            ok = False
        self.call_from_thread(self.set_gateway, ok)

    def set_gateway(self, ok: bool) -> None:
        self.gateway_ok = ok
        self.refresh_chrome()

    def on_input_changed(self, event: Input.Changed) -> None:
        value = event.value
        self.matches = [c for c in COMMANDS if c.startswith(value)] if value.startswith("/") and " " not in value else []
        hint = self.query_one("#hint", Static)
        hint.set_class(bool(self.matches), "show")
        if not self.matches:
            return
        t = Text()
        for i, c in enumerate(self.matches):
            t.append(f"{c:<10}", style=ACCENT if i == 0 else TEXT)
            t.append(COMMANDS[c] + ("\n" if i < len(self.matches) - 1 else ""), style=MUTED)
        hint.update(t)

    def action_complete(self) -> None:
        if not self.matches:
            return
        box = self.query_one("#input", Input)
        box.value = self.matches[0] + " "
        box.cursor_position = len(box.value)

    @property
    def chat(self) -> VerticalScroll:
        return self.query_one("#chat", VerticalScroll)

    def enter_session(self) -> None:
        self.query_one("#body").remove_class("home")
        self.call_after_refresh(self.refresh_chrome)

    def say(self, widget, classes: str = "") -> None:
        self.enter_session()
        if classes:
            widget.add_class(*classes.split())
        self.chat.mount(widget)
        self.chat.anchor()  # stays pinned to the newest line until the user scrolls up
        self.call_after_refresh(self.chat.scroll_end, animate=False)

    def route(self) -> Text:
        t = Text()
        if self.private:
            t.append("private", style=TEXT)
            t.append(" · ", style=MUTED)
            t.append("local model only", style=MUTED)
            return t
        routed = self.small and self.small != self.model
        t.append(f"{short(self.small) + ' > ' if routed else ''}{short(self.model)}", style=TEXT)
        t.append("  Groq", style=MUTED)
        return t

    def tick(self) -> None:
        if self.busy:
            self.frame = (self.frame + 1) % len(FRAMES)
            self.refresh_chrome()

    def refresh_chrome(self) -> None:
        self.query_one("#prompt").set_class(self.busy, "busy")
        mode = Text()
        if self.busy:
            mode.append(f"{FRAMES[self.frame]} working", style=WARN)
            mode.append(f"  {self.task_tokens:,} tok", style=MUTED)
            if self.task_saved >= 1:
                mode.append(f"  ↓ {int(self.task_saved):,} saved", style=GOOD)
        else:
            mode.append("Build", style=ACCENT)
        mode.append(" · ", style=MUTED)
        mode.append_text(self.route())
        self.query_one("#mode", Static).update(mode)
        dot = MUTED if self.gateway_ok is None else GOOD if self.gateway_ok else BAD
        left = Text("● ", style=dot) + Text("Bifrost  " if self.gateway_ok is not False else "Bifrost down  ", style=MUTED) + Text(pretty_path(self.repo), style=MUTED)
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
        ratio = spent / limit if limit else 0
        filled = min(BAR, int(BAR * ratio))
        bar_color = GOOD if ratio < 0.6 else WARN if ratio < 0.85 else BAD
        web = not self.private and bool(os.environ.get("TAVILY_API_KEY"))
        t = Text()

        def head(name: str) -> None:
            t.append(name + "\n", style=f"bold {ACCENT}")

        head("Session")
        t.append(f"{self.repo.name}\n", style=TEXT)
        t.append(f"{s['tasks']} task(s)\n\n")
        used = s["tokens"] + self.task_tokens
        saved = int(s["saved"] + self.task_saved)
        naive = used + saved
        head("Tokens")
        t.append(f"used   {used:,}\n", style=TEXT)
        if saved > 0:
            cells = min(BAR, max(1, round(BAR * saved / naive)))
            t.append(f"naive ~{naive:,}\n")
            t.append(f"saved  ↓ {round(100 * saved / naive)}%\n", style=f"bold {GOOD}")
            t.append("█" * (BAR - cells), style=ACCENT)
            t.append("█" * cells + "\n", style=GOOD)
            t.append("est. vs whole-file reads,\nno clipping, no trimming\n", style="#555555")
        t.append("\n")
        head("Budget")
        t.append(f"${spent:.4f} of ${limit:.2f}\n")
        t.append("█" * filled, style=bar_color)
        t.append("░" * (BAR - filled) + "\n\n", style="#2a2a2a")
        head("Footprint")
        t.append(f"{s['kwh'] * 1000:.3f} Wh\n{s['gco2e'] * 1000:.2f} mgCO2e\n\n")
        head("Gateway")
        up = self.gateway_ok
        t.append("● ", style=MUTED if up is None else GOOD if up else BAD)
        t.append(f"{'checking' if up is None else 'connected' if up else 'unreachable'}\n")
        t.append(f"{bifrost_url().split('//')[-1]}\n\n")
        head("Models")
        if self.private:
            t.append("local only, nothing leaves\nthis machine\n\n")
        else:
            routed = self.small and self.small != self.model
            t.append(f"{short(self.small) if routed else short(self.model)}\n", style=TEXT)
            if routed:
                t.append(f"then {short(self.model)} if stuck\n", style=MUTED)
            t.append("\n")
        head("Tools")
        t.append("read  search  edit  shell\n")
        t.append(f"web search {'on' if web else 'off'}\n\n")
        if self.files:
            head("Modified files")
            t.append("\n".join(self.files[-8:]), style=TEXT)
        return t

    def on_input_submitted(self, event: Input.Submitted) -> None:
        line = event.value.strip()
        event.input.value = ""
        if not line:
            return
        if self.busy:
            self.notify("Still working on the last task", severity="warning")
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
            self.say(Static("Unknown command. Type /help to see what is available."), "note")
        self.refresh_chrome()

    def action_sidebar(self) -> None:
        self.query_one("#side").toggle_class("hidden")
        self.call_after_refresh(self.refresh_chrome)

    def action_new(self) -> None:
        if self.busy:
            return
        self.chat.remove_children()
        self.query_one("#body").add_class("home")
        self.session = {"tokens": 0, "saved": 0, "kwh": 0.0, "gco2e": 0.0, "tasks": 0}
        self.files = []
        self.always = set()
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
        kind = text.split()[0].rstrip(":")
        if kind in self.always:
            return True
        done, box = threading.Event(), []

        def decided(choice: str) -> None:
            self.query_one("#prompt").remove_class("hidden")
            self.query_one("#input").focus()
            box.append(choice)
            done.set()

        self.call_from_thread(self.ask_permission, text, kind in ALWAYS_KINDS, decided)
        done.wait()
        if box[0] == "always":
            self.always.add(kind)
        return box[0] in ("once", "always")

    def ask_permission(self, text: str, allow_always: bool, decided) -> None:
        self.query_one("#prompt").add_class("hidden")
        self.query_one("#perm", Permission).ask(text, allow_always, decided)

    def on_agent_event(self, kind: str, **d) -> None:
        self.call_from_thread(self.show_event, kind, d)

    def show_event(self, kind: str, d: dict) -> None:
        if kind == "call":
            self.task_tokens += d["prompt"] + d["completion"]
            self.task_saved += d.get("saved", 0)
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
            self.say(Static(f"↑ switched to {short(d['to'])}: {d['reason']}"), "esc")
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
        self.task_tokens, self.task_saved = 0, 0.0
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
        self.session["saved"] += t["saved_prompt"]
        self.session["kwh"] += t["kwh"]
        self.session["gco2e"] += t["gco2e"]
        self.session["tasks"] += 1
        self.task_tokens, self.task_saved = 0, 0.0
        self.say(Markdown(result), "answer")
        if t["saved_prompt"] > 0:
            win = Text("↓ ", style=f"bold {GOOD}") + Text(f"{t['saved_pct']}% fewer tokens", style=f"bold {GOOD}")
            win.append(f"  ~{t['saved_prompt']:,} saved vs a naive agent", style=GOOD)
            self.say(Static(win), "saved")
        parts = [f"{t['in'] + t['out']:,} tokens", f"${t['cost_usd']:.4f}", f"{t['kwh'] * 1000:.3f} Wh", f"{t['gco2e'] * 1000:.1f} mgCO2e",
                 f"{t['calls']} call{'s' * (t['calls'] != 1)}"]
        if agent.fp.escalations:
            parts.append(f"{len(agent.fp.escalations)} model switch{'es' * (len(agent.fp.escalations) != 1)}")
        parts.append({"tests passing": "tests pass", "tests failing": "tests fail"}.get(agent.verified, "tests not run"))
        line = "  ·  ".join(parts)
        self.say(Static(Text(line, style="dim")), "note")
        self.refresh_chrome()

    def loop(self) -> None:
        self.run()
