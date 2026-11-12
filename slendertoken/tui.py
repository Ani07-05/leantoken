"""Full-screen terminal UI: header bar, savings sidebar on the left, conversation, rounded prompt box."""

import json
import os
import sys
import threading
import time
from pathlib import Path

import httpx
from rich.text import Text
from textual import events, work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.screen import ModalScreen
from textual.widgets import Input, Markdown, OptionList, Static

from . import config
from .agent import Agent, memory_note
from .budget import Budget, BudgetExceeded
from textual_image.widget import Image

from . import art, themes
from .art import TOOL_SCENE, color, verb
from .gateway import bifrost_url, cached_models, list_models, local_model, save_model
from .tools import Workspace

BAR = 20
COMMANDS = {
    "/theme": "switch color theme, e.g. /theme nord",
    "/clear": "clear the context and start fresh",
    "/models": "pick the large and first models",
    "/private": "toggle private mode",
    "/budget": "set the USD limit, e.g. /budget 0.5",
    "/settings": "edit keys and options",
    "/sidebar": "toggle the sidebar",
    "/help": "show help",
    "/exit": "quit",
}
KEYS = (("shift+tab", "cycle permission mode"), ("ctrl+b", "toggle sidebar"), ("ctrl+n", "clear the context"),
        ("ctrl+o", "pick models"), ("ctrl+s", "settings"), ("ctrl+q", "quit"))
HELP = ("**Commands**\n\n" + "\n".join(f"- `{c}`  {d}" for c, d in COMMANDS.items())
        + "\n\n**Prompt**\n\n- `!cmd`  run a shell command yourself\n- `@path`  mention a file; the agent reads only what it needs"
        + "\n\n**Keys**\n\n" + "\n".join(f"- `{k}`  {d}" for k, d in KEYS))
PAL = themes.get(None)
ACCENT = WARN = TEXT = MUTED = FAINT = LINE = BG = GOOD = BAD = ""


def apply_palette(p: themes.Palette) -> None:
    """Rich text is styled from these module names at render time, so a theme switch only has to reassign them."""
    global PAL, ACCENT, WARN, TEXT, MUTED, FAINT, LINE, BG, GOOD, BAD
    PAL = p
    ACCENT, WARN, TEXT, MUTED, FAINT, LINE, BG, GOOD, BAD = p.accent, p.warn, p.text, p.muted, p.faint, p.line, p.bg, p.good, p.bad


apply_palette(PAL)
HOLD = 15  # ticks (0.1s) an animal stays on screen, so a fast tool call is still visible
FRAMES = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"


GLYPHS = {
    "s": ("█▀▀▀", "▀▀▀█", "▀▀▀▀"),
    "d": ("   █", "█▀▀█", "▀▀▀▀"),
    "r": ("█▀▀▄", "█   ", "▀   "),
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
        for i, ch in enumerate("slendertoken"):
            out.append(GLYPHS[ch][row] + ("" if i == 8 else " "), style=ACCENT if i < 4 else TEXT)
        out.append("\n" if row < 2 else "")
    return out


def pretty_path(path: Path, width: int = 48) -> str:
    text = str(path).replace(str(Path.home()), "~", 1)
    return text if len(text) <= width else "…" + text[-(width - 1):]


def spark(values: list[int], top: int) -> str:
    bars = "▁▂▃▄▅▆▇█"
    return "".join(bars[min(7, int(7 * v / top))] if top else bars[0] for v in values)


def energy(kwh: float, gco2e: float) -> str:
    return f"{kwh * 1000:.2f} Wh and {gco2e * 1000:.0f} mg CO2"


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


MODES = (("Ask", "asks before every edit and shell command", "accent"),
         ("Accept edits", "edits files without asking, still asks for shell", "warn"),
         ("Auto", "runs everything without asking", "bad"))
LABELS = {"once": "Allow once", "always": "Allow always", "reject": "Reject"}
# a shell command can differ every time, so "always" is only offered for file edits
ALWAYS_KINDS = ("edit", "write")


class Permission(Vertical):
    """Replaces the prompt box while the agent waits for approval."""

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
        title = Text("⚠ ", style=WARN) + Text("Approve?", style=f"bold {WARN}")
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


class ModelPicker(ModalScreen[str | None]):
    """Returns the chosen model, NONE for "no first model", or None when cancelled (keep the current one)."""

    BINDINGS = [("escape", "cancel", "cancel")]
    NONE = "none: use the large model only"

    def __init__(self, models: list[str], current: str, title: str = "Select the large model", allow_none: bool = False):
        super().__init__()
        self.models = ([self.NONE] if allow_none else []) + models
        self.current, self.title_text = current, title

    def compose(self) -> ComposeResult:
        with Vertical(id="dialog"):
            yield Static(self.title_text, id="dtitle")
            yield OptionList(*[("● " if m == self.current else "  ") + m for m in self.models], id="models")
            yield Static("enter select   esc keep current", id="dhint")

    def on_mount(self) -> None:
        if self.current in self.models:
            self.query_one(OptionList).highlighted = self.models.index(self.current)

    def on_option_list_option_selected(self, event: OptionList.OptionSelected) -> None:
        self.dismiss(self.models[event.option_index])

    def action_cancel(self) -> None:
        self.dismiss(None)


class Settings(ModalScreen[bool]):
    """Edit ~/.slendertoken/.env in place; saved values apply from the next request."""

    BINDINGS = [("escape", "cancel", "cancel"), Binding("ctrl+s", "save", "save", priority=True)]

    def __init__(self, reason: str = ""):
        super().__init__()
        self.reason = reason

    def compose(self) -> ComposeResult:
        with Vertical(id="settings"):
            yield Static("Settings", id="dtitle")
            if self.reason:
                yield Static(self.reason, classes="fail")
            with VerticalScroll(id="sfields"):
                for key, desc, example, value in config.fields():
                    yield Static(Text(key, style="bold") + Text(f"  {desc}", style=MUTED), classes="slabel")
                    if src := config.overridden(key):
                        yield Static(f"overridden by {src}", classes="esc")
                    yield Input(value=value, placeholder=example, password=config.is_secret(key), id=f"f-{key}", classes="sinput")
            yield Static(Text(f"ctrl+s save   esc cancel   saved to {config.GLOBAL_ENV}", style=MUTED), id="dhint")

    def on_mount(self) -> None:
        empty = [i for i in self.query(".sinput").results(Input) if not i.value and i.id != "f-BIFROST_URL"]
        (empty or list(self.query(".sinput").results(Input)))[0].focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        event.stop()
        self.focus_next()

    def action_save(self) -> None:
        for box in self.query(".sinput").results(Input):
            key, value = box.id.removeprefix("f-"), box.value.strip()
            config.set_value(key, value or None)
            if not config.overridden(key):
                if value:
                    os.environ[key] = value
                else:
                    os.environ.pop(key, None)
        self.dismiss(True)

    def action_cancel(self) -> None:
        self.dismiss(False)


class TUI(App):
    TITLE = "slendertoken"
    CSS = """
    Screen { background: $lt-bg; color: $lt-text; }
    #top { height: 1; padding: 0 2; background: $lt-panel; color: $lt-muted; }
    #body { height: 1fr; }
    #activity { display: none; height: 2; margin-top: 1; padding: 0 2; }
    #art { width: 10; height: 2; }
    #activitytext { width: 1fr; height: 2; padding: 0 0 0 2; }
    #hint { display: none; height: auto; margin-top: 1; padding: 0 2; background: $lt-panel; border: round $lt-line; }
    #hint.show { display: block; }
    #main { width: 1fr; height: 100%; padding: 0 2; }
    #chat { height: 1fr; scrollbar-size-vertical: 1; scrollbar-background: $lt-bg; scrollbar-color: $lt-line; }
    #side { width: 34; height: 100%; padding: 1 2; background: $lt-panel; border-right: solid $lt-line; color: $lt-muted; scrollbar-size-vertical: 1; scrollbar-background: $lt-panel; scrollbar-color: $lt-line; }
    #sidetext { height: auto; }
    #side.hidden { display: none; }
    #prompt { height: auto; background: $lt-panel; border: round $lt-line; padding: 0 1; margin: 1 0 0 0; }
    #prompt:focus-within { border: round $lt-accent; }
    #prompt.busy { border: round $lt-warn; }
    #input { border: none; padding: 0; background: transparent; height: 1; color: $lt-text; }
    #input:focus { border: none; background: transparent; background-tint: transparent; }
    #input > .input--placeholder { color: $lt-muted; text-style: none; }
    #mode { height: 1; }
    #foot { height: 1; padding: 0 2; background: $lt-panel; color: $lt-muted; }
    #logo { display: none; height: auto; width: 100%; margin-bottom: 2; text-align: center; }
    #body.home #logo { display: block; width: 100; max-width: 100%; }
    #body.home #chat { display: none; }
    #body.home #main { align: center middle; }
    #body.home #prompt { width: 100; max-width: 100%; margin: 0; }
    #body.home #side { display: none; }
    #body.home #activity { display: none; }
    .user { border: round $lt-line; padding: 0 1; margin: 1 0 0 8; background: $lt-panel; }
    .answer { margin: 1 0 0 0; padding: 0 1; height: auto; background: transparent; }
    .answer MarkdownParagraph, .answer Markdown > * { margin: 0 0 1 0; }
    MarkdownH1, MarkdownH2, MarkdownH3, MarkdownH4 { color: $lt-accent; text-style: bold; background: transparent; border: none; padding: 0; margin: 1 0 0 0; text-align: left; content-align: left top; }
    MarkdownFence { background: $lt-panel; margin: 0 0 1 0; padding: 0 1; max-height: 20; }
    MarkdownBulletList, MarkdownOrderedList { margin: 0 0 1 0; }
    MarkdownTable { margin: 0 0 1 0; }
    .tool { color: $lt-muted; margin: 0; }
    .saved { margin: 0 0 0 2; }
    .fail { color: $lt-bad; }
    .esc { color: $lt-warn; }
    .diff { border: round $lt-line; padding: 0 1; margin: 0 0 0 2; }
    .note { color: $lt-muted; }
    ModelPicker, Settings { align: center middle; }
    #settings { width: 90; max-width: 95%; height: 90%; border: round $lt-accent; background: $lt-panel; padding: 1 2; }
    #sfields { height: 1fr; scrollbar-size-vertical: 1; }
    .slabel { margin-top: 1; }
    .sinput { border: none; height: 1; padding: 0 1; background: $lt-bg; }
    .sinput:focus { border: none; background: $lt-raised; }
    #dialog { width: 72; max-width: 90%; height: auto; max-height: 80%; border: round $lt-accent; background: $lt-panel; padding: 1 2; }
    #dialog OptionList { background: $lt-panel; border: none; padding: 0; }
    #dtitle { text-style: bold; margin-bottom: 1; }
    #dhint { margin-top: 1; color: $lt-muted; }
    #prompt.hidden { display: none; }
    #perm { display: none; height: auto; background: $lt-panel; border: round $lt-warn; padding: 0 1; margin-top: 1; }
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
        Binding("ctrl+s", "settings", "settings"),
        Binding("shift+tab", "cycle_mode", "mode", priority=True),
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
        self.session = {"tokens": 0, "saved": 0, "saved_kwh": 0.0, "saved_gco2e": 0.0, "tasks": 0}
        self.files: list[str] = []
        self.matches: list[str] = []
        self.always: set[str] = set()
        self.gateway_ok: bool | None = None
        self.turns: list[dict] = []
        self.prior_naive = 0
        self.series: list[tuple[int, int]] = []
        self.started = time.monotonic()
        self.mode = 0
        self.scene, self.detail = "think", ""
        self.n, self.busy_since, self.scene_since, self.fade = 0, 0, 0, 0
        self.shown_until, self.pending, self.tool_running = 0, None, False
        for p in themes.PALETTES.values():
            self.register_theme(p.textual())
        self.use_theme(os.environ.get("SLENDERTOKEN_THEME"))

    def use_theme(self, name: str | None) -> None:
        apply_palette(themes.get(name))
        self.theme = f"lt-{PAL.name}"

    def compose(self) -> ComposeResult:
        yield Static(id="top")
        with Horizontal(id="body", classes="home"):
            with VerticalScroll(id="side"):
                yield Static(id="sidetext")
            with Vertical(id="main"):
                yield Static(logo(), id="logo")
                yield VerticalScroll(id="chat")
                with Horizontal(id="activity"):
                    yield Image(art.frame("think", 0), id="art")
                    yield Static(id="activitytext")
                yield Static(id="hint")
                with Vertical(id="prompt"):
                    yield Input(placeholder="Ask anything...  \"fix the failing test\"", id="input")
                    yield Static(id="mode")
                yield Permission(id="perm")
        yield Static(id="foot")

    def on_mount(self) -> None:
        self.query_one("#input").focus()
        self.set_interval(0.1, self.tick)
        self.set_interval(15, self.check_gateway)
        self.check_gateway()
        self.call_after_refresh(self.refresh_chrome)
        if self.private and not os.environ.get("BIFROST_PRIVATE_VK"):
            self.action_settings("Private mode needs the Ollama-only virtual key (BIFROST_PRIVATE_VK).")
        elif not self.private and not os.environ.get("BIFROST_VK"):
            self.action_settings("Add your Bifrost virtual key (BIFROST_VK) to get started.")
        elif not self.model and not self.private:
            self.action_models()

    def action_settings(self, reason: str = "") -> None:
        def saved(ok: bool) -> None:
            if not ok:
                return
            self.say(Static(f"settings saved to {config.GLOBAL_ENV}"), "note")
            self.check_gateway()
            if not self.model and not self.private and os.environ.get("BIFROST_VK"):
                self.action_models()

        self.push_screen(Settings(reason), saved)

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
            t.append(short(local_model()), style=MUTED)
            return t
        routed = self.small and self.small != self.model
        t.append(f"{short(self.small) + ' > ' if routed else ''}{short(self.model)}", style=TEXT)
        t.append("  Groq", style=MUTED)
        return t

    def begin(self) -> None:
        self.busy, self.busy_since = True, self.n
        self.show("think", "")
        self.pending, self.tool_running = None, False

    def show(self, scene: str, detail: str) -> None:
        self.scene, self.scene_since, self.detail = scene, self.n, detail
        self.shown_until = self.n + HOLD

    def draw_activity(self) -> None:
        if not (found := self.query("#activity")):
            return
        box = found.first()
        if not self.busy and self.fade <= 0:
            box.display = False
            return
        grow = min(1.0, (self.n - self.busy_since) / 6) if self.busy else self.fade / 8
        grow = round(grow * 8) / 8
        self.query_one("#art", Image).image = art.frame(self.scene, self.n // 2, grow)
        text = Text()
        text.append("✻ ", style=ACCENT)
        text.append(verb(self.scene, self.n) + "…", style=f"bold {TEXT}")
        text.append(f"  {(self.n - self.busy_since) // 10}s  ·  {self.task_tokens:,} tok\n", style=MUTED)
        text.append(self.detail[:60], style=MUTED)
        self.query_one("#activitytext", Static).update(text)
        box.display = True

    def tick(self) -> None:
        self.n += 1
        if self.n >= self.shown_until:
            if self.pending:
                self.show(*self.pending)
                self.pending = None
            elif self.busy and not self.tool_running and self.scene != "think":
                self.show("think", "")
        if not self.busy and self.fade > 0:
            self.fade -= 1
        self.draw_activity()
        if self.busy:
            self.frame = (self.frame + 1) % len(FRAMES)
            self.refresh_chrome()

    def refresh_chrome(self) -> None:
        self.query_one("#prompt").set_class(self.busy, "busy")
        mode = Text()
        name, _, role = MODES[self.mode]
        mode.append(name, style=getattr(PAL, role))
        mode.append(" · ", style=MUTED)
        mode.append_text(self.route())
        self.query_one("#mode", Static).update(mode)
        dot = MUTED if self.gateway_ok is None else GOOD if self.gateway_ok else BAD
        left = Text("▍", style=ACCENT) + Text("slendertoken", style=f"bold {TEXT}") + Text("  " + pretty_path(self.repo), style=MUTED)
        right = Text(PAL.name + "  ", style=FAINT) + Text("● ", style=dot) + Text("Bifrost" if self.gateway_ok is not False else "Bifrost down", style=MUTED)
        top_gap = max(2, (self.query_one("#top").size.width or 100) - 4 - len(left.plain) - len(right.plain))
        self.query_one("#top", Static).update(left + Text(" " * top_gap) + right)
        keys = (("shift+tab", "mode"), ("ctrl+s", "settings"), ("ctrl+q", "quit")) if self.query_one("#body").has_class("home") else \
            (("shift+tab", "mode"), ("ctrl+s", "settings"), ("ctrl+b", "sidebar"), ("ctrl+n", "new"), ("ctrl+o", "models"), ("ctrl+q", "quit"))
        hints = Text()
        for key, label in keys:
            hints.append(key + " ", style=TEXT)
            hints.append(label + "  ", style=FAINT)
        self.query_one("#foot", Static).update(hints)
        self.query_one("#sidetext", Static).update(self.sidebar())

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
            if not self.private:
                t.append(f"~{energy(s['saved_kwh'], s['saved_gco2e'])} less\n", style=GOOD)
            t.append("█" * (BAR - cells), style=ACCENT)
            t.append("█" * cells + "\n", style=GOOD)
            t.append("est. vs whole-file reads,\nno clipping, no trimming" + ("" if self.private else ";\nenergy scales with tokens") + "\n", style=FAINT)
        t.append("\n")
        if len(self.series) > 1:
            shown = self.series[-12:]
            top = max(n for _, n in shown)
            head("Per turn")
            t.append("naive ", style=MUTED)
            t.append(spark([n for _, n in shown], top) + "\n", style=BAD)
            t.append("lean  ", style=MUTED)
            t.append(spark([u for u, _ in shown], top) + "\n", style=GOOD)
            t.append("gap widens as the chat grows\n\n", style=FAINT)
        head("Stats")
        mins = int((time.monotonic() - self.started) // 60)
        done = s["tasks"]
        rows = (
            ("elapsed", f"{mins // 60}h {mins % 60:02d}m" if mins >= 60 else f"{mins}m"),
            ("turns", str(len(self.series))),
            ("tok/task", f"{used // done:,}" if done else "-"),
            ("$/task", f"${spent / done:.4f}" if done else "-"),
            ("files", f"{len(self.files)} modified"),
        )
        for label, value in rows:
            t.append(f"{label:<9}", style=MUTED)
            t.append(value + "\n", style=TEXT)
        t.append("\n")
        head("Budget")
        t.append(f"${spent:.4f} of ${limit:.2f}\n")
        t.append("█" * filled, style=bar_color)
        t.append("░" * (BAR - filled) + "\n\n", style=LINE)
        head("Gateway")
        up = self.gateway_ok
        t.append("● ", style=MUTED if up is None else GOOD if up else BAD)
        t.append(f"{'checking' if up is None else 'connected' if up else 'unreachable'}\n")
        t.append(f"{bifrost_url().split('//')[-1]}\n\n")
        head("Models")
        if self.private:
            t.append(f"{short(local_model())}\n", style=TEXT)
            t.append("local only, nothing leaves\nthis machine\n\n", style=MUTED)
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
            self.begin()
            self.shell(line[1:].strip())
        else:
            self.say(Static(line), "user")
            self.begin()
            self.run_task(line)

    def command(self, line: str) -> None:
        cmd, _, arg = line.partition(" ")
        if cmd in ("/exit", "/quit"):
            self.exit()
        elif cmd == "/help":
            self.say(Markdown(HELP), "answer")
        elif cmd in ("/clear", "/new"):
            self.action_new()
        elif cmd == "/models":
            self.action_models()
        elif cmd == "/sidebar":
            self.action_sidebar()
        elif cmd == "/theme":
            self.command_theme(arg.strip().lower())
        elif cmd == "/private":
            if not self.private and not os.environ.get("BIFROST_PRIVATE_VK"):
                self.say(Static("private mode needs BIFROST_PRIVATE_VK. Set it in /settings."), "note fail")
            else:
                self.private = not self.private
                self.say(Static(f"private mode on: {local_model()}, nothing leaves this machine" if self.private else "private mode off"), "note")
        elif cmd == "/budget" and arg.replace(".", "", 1).isdigit():
            self.budget.limit = float(arg)
            self.say(Static(f"budget limit ${self.budget.limit:.2f}"), "note")
        elif cmd in ("/settings", "/config"):
            self.action_settings()
        else:
            self.say(Static("Unknown command. Type /help to see what is available."), "note")
        self.refresh_chrome()

    def command_theme(self, name: str) -> None:
        if name and name not in themes.PALETTES:
            self.say(Static(f"unknown theme. Choose from: {', '.join(themes.PALETTES)}"), "note fail")
        elif name:
            self.set_theme(name)
        else:
            self.push_screen(ModelPicker(list(themes.PALETTES), PAL.name, "Select a theme"), lambda picked: picked and self.set_theme(picked))

    def set_theme(self, name: str) -> None:
        self.use_theme(name)
        config.set_value("SLENDERTOKEN_THEME", name)
        os.environ["SLENDERTOKEN_THEME"] = name
        self.paint_terminal()
        self.refresh_chrome()

    def paint_terminal(self) -> None:
        if tty := sys.__stdout__ if sys.__stdout__ and sys.__stdout__.isatty() else None:
            tty.write(f"\x1b]11;{BG}\x1b\\")
            tty.flush()

    def action_cycle_mode(self) -> None:
        self.mode = (self.mode + 1) % len(MODES)
        name, blurb, _ = MODES[self.mode]
        self.notify(f"{name}: {blurb}")
        self.refresh_chrome()

    def action_sidebar(self) -> None:
        self.query_one("#side").toggle_class("hidden")
        self.call_after_refresh(self.refresh_chrome)

    def action_new(self) -> None:
        if self.busy:
            return
        self.chat.remove_children()
        self.query_one("#body").add_class("home")
        self.session = {"tokens": 0, "saved": 0, "saved_kwh": 0.0, "saved_gco2e": 0.0, "tasks": 0}
        self.files = []
        self.always = set()
        self.turns, self.prior_naive, self.series = [], 0, []
        self.started = time.monotonic()
        self.refresh_chrome()

    def action_models(self) -> None:
        """Pick the large model, then the first model. Opens from the cached list; Bifrost refreshes it behind."""
        if self.private:
            self.say(Static(f"private mode runs {local_model()}. Change it with SLENDERTOKEN_LOCAL_MODEL."), "note")
            return
        if models := cached_models():
            self.pick_models(models)
            self.fetch_models(open_picker=False)
        else:
            self.say(Static("loading models from Bifrost..."), "note")
            self.fetch_models(open_picker=True)

    @work(thread=True, exclusive=True, group="models")
    def fetch_models(self, open_picker: bool) -> None:
        try:
            models = list_models()
        except (Exception, SystemExit) as e:
            if open_picker:
                self.call_from_thread(self.say, Static(f"cannot list models: {e}"), "note fail")
            return
        if open_picker:
            if models:
                self.call_from_thread(self.pick_models, models)
            else:
                self.call_from_thread(self.say, Static("Bifrost lists no Groq models"), "note fail")

    def pick_models(self, models: list[str]) -> None:
        def got_small(small: str | None) -> None:
            if small is not None:
                self.small = "" if small == ModelPicker.NONE else small
                save_model(small=self.small)
            self.refresh_chrome()

        def got_large(model: str | None) -> None:
            if model:
                self.model = model
                save_model(model=model)
            self.push_screen(ModelPicker(models, self.small or ModelPicker.NONE, "Step 2 of 2: first model, tried before the large one",
                                         allow_none=True), got_small)

        self.push_screen(ModelPicker(models, self.model, "Step 1 of 2: large model, used when a task gets hard"), got_large)

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
        if kind in self.always or self.mode == 2 or (self.mode == 1 and kind in ("edit", "write")):
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
        if kind == "start":
            self.tool_running = True
            self.pending = (TOOL_SCENE.get(d["name"], "think"), brief(d["name"], d["args"]))
        elif kind == "call":
            self.task_tokens += d["prompt"] + d["completion"]
            self.task_saved += d.get("saved", 0)
        elif kind == "tool":
            name, failed = d["name"], d["failed"]
            self.tool_running = False
            line = Text()
            line.append("● ", style=BAD if failed else color(TOOL_SCENE.get(name, "think")))
            line.append(name, style="bold")
            line.append(f"  {brief(name, d['args'])}", style="dim")
            self.say(Static(line), "tool")
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
            agent = Agent(self.repo, self.budget, self.private, self.model, small=self.small, approve=self.approve, on_event=self.on_agent_event,
                          memory=memory_note(self.turns), prior_naive=self.prior_naive)
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
        agent.fp.save(self.repo / ".slendertoken" / "runs", result, agent.verified)
        self.call_from_thread(self.finish, agent, result)

    def finish(self, agent: Agent, result: str) -> None:
        t = agent.fp.totals()
        self.busy = False
        self.fade = 8
        self.session["tokens"] += t["in"] + t["out"]
        self.session["saved"] += t["saved_prompt"]
        self.session["saved_kwh"] += t["saved_kwh"]
        self.session["saved_gco2e"] += t["saved_gco2e"]
        self.session["tasks"] += 1
        self.series.append((t["in"] + t["out"], t["naive_total"]))
        self.prior_naive += agent.turn_naive
        if not result.startswith(("error:", "budget exceeded")):
            self.turns.append(agent.note(result))
        self.task_tokens, self.task_saved = 0, 0.0
        self.say(Markdown(result), "answer")
        line = Text(f"{t['in'] + t['out']:,} tokens", style="dim")
        if t["saved_prompt"] > 0:
            line.append("  ·  ", style="dim")
            line.append(f"↓ {t['saved_pct']}% fewer than naive", style=f"bold {GOOD}")
        if agent.verified == "tests failing":
            line.append("  ·  tests fail", style=BAD)
        self.say(Static(line), "note")
        self.refresh_chrome()

    def loop(self) -> None:
        # The terminal's own padding, scrollbar gutter and the pixels left over past the last cell are
        # outside the grid Textual can paint. OSC 11 sets the terminal's default background to match the
        # app so that margin blends in; OSC 111 restores the user's colour on exit.
        tty = sys.__stdout__ if sys.__stdout__ and sys.__stdout__.isatty() else None
        self.paint_terminal()
        try:
            self.run()
        finally:
            if tty:
                tty.write("\x1b]111\x1b\\")
                tty.flush()
