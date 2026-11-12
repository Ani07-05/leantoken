package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// Palette paints the whole screen. Add and Del are the only colors the default theme uses besides white and grey.
type Palette struct {
	Name                                                                          string
	Light                                                                         bool
	Bg, Accent, Text, Muted, Faint, Line, Add, Del, AddBg, DelBg, Warn, Good, Bad string
}

const (
	addGreen = "#5fd787"
	delRed   = "#ff6b6b"
	// row tints behind added and removed lines in a diff
	darkAddBg = "#12301d"
	darkDelBg = "#35171a"
)

var Palettes = []Palette{
	// mono: white on near-black; good/bad/warn collapse to plain text so only +/- lines carry color
	{"mono", false, "#0a0a0a", "#ffffff", "#ededed", "#9a9a9a", "#5c5c5c", "#2a2a2a", addGreen, delRed, darkAddBg, darkDelBg, "#ededed", "#ededed", "#ededed"},
	{"slender", false, "#0b1015", "#3ddc97", "#e6edf3", "#8b98a5", "#56626e", "#26333f", addGreen, delRed, darkAddBg, darkDelBg, "#f5b94a", "#3ddc97", "#f47067"},
	{"ember", false, "#120d0b", "#ff7a45", "#f1e6df", "#a8917f", "#6e5b4f", "#3a2a24", addGreen, delRed, darkAddBg, darkDelBg, "#ffc857", "#9ece6a", "#ff5d6c"},
	{"nord", false, "#2e3440", "#88c0d0", "#eceff4", "#9aa5b8", "#667085", "#4c566a", "#a3be8c", "#bf616a", darkAddBg, darkDelBg, "#ebcb8b", "#a3be8c", "#bf616a"},
	{"gruvbox", false, "#1d2021", "#fabd2f", "#ebdbb2", "#a89984", "#665c54", "#504945", "#b8bb26", "#fb4934", darkAddBg, darkDelBg, "#fe8019", "#b8bb26", "#fb4934"},
	{"dracula", false, "#1e1f29", "#bd93f9", "#f8f8f2", "#9aa0c0", "#626a8e", "#44475a", "#50fa7b", "#ff5555", darkAddBg, darkDelBg, "#f1fa8c", "#50fa7b", "#ff5555"},
	{"paper", true, "#faf7f2", "#0f766e", "#2b2a27", "#6b655a", "#a8a193", "#d6cdbb", "#15803d", "#b91c1c", "#d7f0de", "#fadcdc", "#b45309", "#15803d", "#b91c1c"},
}

var pal = Palettes[0]

func PaletteNamed(name string) (Palette, bool) {
	for _, p := range Palettes {
		if p.Name == strings.ToLower(strings.TrimSpace(name)) {
			return p, true
		}
	}
	return Palettes[0], false
}

func SetTheme(p Palette) { pal = p }

func fg(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)) }

func accent(s string) string { return fg(pal.Accent).Render(s) }
func text(s string) string   { return fg(pal.Text).Render(s) }
func muted(s string) string  { return fg(pal.Muted).Render(s) }
func faint(s string) string  { return fg(pal.Faint).Render(s) }
func warn(s string) string   { return fg(pal.Warn).Render(s) }
func good(s string) string   { return fg(pal.Good).Render(s) }
func bad(s string) string    { return fg(pal.Bad).Render(s) }
func added(s string) string  { return fg(pal.Add).Render(s) }
func removed(s string) string {
	return fg(pal.Del).Render(s)
}
func bold(s string) string { return lipgloss.NewStyle().Bold(true).Render(s) }

func rgbSeq(hex string, bg bool) string {
	var r, g, b int
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	kind := 38
	if bg {
		kind = 48
	}
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", kind, r, g, b)
}

func sp(s string) *string { return &s }

// markdown renders model output in the theme's colors on the theme's background.
func markdown(src string, width int) string {
	cfg := styles.DarkStyleConfig
	if pal.Light {
		cfg = styles.LightStyleConfig
	}
	zero := uint(0)
	cfg.Document.BackgroundColor, cfg.Document.Margin, cfg.Document.BlockPrefix, cfg.Document.BlockSuffix = nil, &zero, "", ""
	cfg.Document.Color = sp(pal.Text)
	for _, h := range []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.BackgroundColor, h.Color, h.Prefix, h.Suffix, h.Bold = nil, sp(pal.Accent), "", "", boolp(true)
	}
	cfg.Code.BackgroundColor, cfg.Code.Color = nil, sp(pal.Accent)
	cfg.CodeBlock.BackgroundColor, cfg.CodeBlock.Margin = nil, &zero
	cfg.Link.Color, cfg.LinkText.Color = sp(pal.Muted), sp(pal.Text)
	if pal.Name == "mono" {
		cfg.CodeBlock.Chroma = &ansi.Chroma{Text: ansi.StylePrimitive{Color: sp(pal.Text)}}
	} else if cfg.CodeBlock.Chroma != nil {
		cfg.CodeBlock.Chroma.Background = ansi.StylePrimitive{}
		cfg.CodeBlock.Chroma.Text.BackgroundColor = nil
		cfg.CodeBlock.Chroma.Error.BackgroundColor = nil
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(max(20, width)))
	if err != nil {
		return src
	}
	out, err := r.Render(src)
	if err != nil {
		return src
	}
	return strings.Trim(out, "\n")
}

func boolp(b bool) *bool { return &b }
