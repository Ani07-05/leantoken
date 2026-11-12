"""Color themes. One Palette drives both the Textual CSS (as $lt-* variables) and the Rich text the TUI builds."""

from dataclasses import dataclass

from textual.theme import Theme


@dataclass(frozen=True)
class Palette:
    name: str
    dark: bool
    bg: str
    panel: str
    raised: str
    line: str
    text: str
    muted: str
    faint: str
    accent: str
    warn: str
    good: str
    bad: str

    def textual(self) -> Theme:
        return Theme(
            name=f"lt-{self.name}", primary=self.accent, secondary=self.accent, warning=self.warn, error=self.bad,
            success=self.good, accent=self.accent, foreground=self.text, background=self.bg, surface=self.panel,
            panel=self.raised, dark=self.dark,
            variables={f"lt-{k}": v for k, v in vars(self).items() if k not in ("name", "dark")},
        )


PALETTES = {p.name: p for p in (
    Palette("slender", True, "#0b1015", "#121a21", "#1a242d", "#26333f", "#e6edf3", "#7d8b99", "#4a5866", "#3ddc97", "#f5b94a", "#3ddc97", "#f47067"),
    Palette("ember", True, "#120d0b", "#1b1311", "#271b17", "#3a2a24", "#f1e6df", "#9a8579", "#5f4f46", "#ff7a45", "#ffc857", "#9ece6a", "#ff5d6c"),
    Palette("nord", True, "#2e3440", "#343b49", "#3b4252", "#4c566a", "#eceff4", "#9aa5b8", "#667085", "#88c0d0", "#ebcb8b", "#a3be8c", "#bf616a"),
    Palette("gruvbox", True, "#1d2021", "#282828", "#32302f", "#504945", "#ebdbb2", "#a89984", "#665c54", "#fabd2f", "#fe8019", "#b8bb26", "#fb4934"),
    Palette("dracula", True, "#1e1f29", "#282a36", "#343746", "#44475a", "#f8f8f2", "#9aa0c0", "#626a8e", "#bd93f9", "#f1fa8c", "#50fa7b", "#ff5555"),
    Palette("paper", False, "#faf7f2", "#f1ece3", "#e8e1d4", "#d6cdbb", "#2b2a27", "#7a7468", "#a8a193", "#0f766e", "#b45309", "#15803d", "#b91c1c"),
)}
DEFAULT = "slender"


def get(name: str | None) -> Palette:
    return PALETTES.get((name or "").strip().lower(), PALETTES[DEFAULT])
