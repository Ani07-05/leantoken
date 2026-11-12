package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Ani07-05/slendertoken/internal/agent"
	"github.com/Ani07-05/slendertoken/internal/footprint"
	"github.com/Ani07-05/slendertoken/internal/gateway"
	"github.com/Ani07-05/slendertoken/internal/web"
)

const barW = 20

var toolLabel = map[string]string{
	"read_file": "Read", "search": "Search", "list_files": "List", "edit_file": "Edit",
	"write_file": "Write", "run_shell": "Bash", "web_search": "Web Search", "ask_user": "Ask",
}

var glyphs = map[rune][3]string{
	's': {"█▀▀▀", "▀▀▀█", "▀▀▀▀"}, 'd': {"   █", "█▀▀█", "▀▀▀▀"}, 'r': {"█▀▀▄", "█   ", "▀   "},
	'l': {"█   ", "█   ", "▀▀▀▀"}, 'e': {"█▀▀█", "█▀▀▀", "▀▀▀▀"}, 'a': {"█▀▀█", "█▀▀█", "▀  ▀"},
	'n': {"█▀▀▄", "█  █", "▀  ▀"}, 't': {"▀█▀▀", " █  ", " ▀▀▀"}, 'o': {"█▀▀█", "█  █", "▀▀▀▀"}, 'k': {"█ ▄▀", "█▀▄ ", "▀  ▀"},
}

// bigGlyphs is the wordmark in the shadowed block style: solid cells are the letter, box-drawing cells its shadow.
var bigGlyphs = map[rune][6]string{
	's': {"███████╗", "██╔════╝", "███████╗", "╚════██║", "███████║", "╚══════╝"},
	'd': {"██████╗ ", "██╔══██╗", "██║  ██║", "██║  ██║", "██████╔╝", "╚═════╝ "},
	'r': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔══██╗", "██║  ██║", "╚═╝  ╚═╝"},
	'l': {"██╗     ", "██║     ", "██║     ", "██║     ", "███████╗", "╚══════╝"},
	'e': {"███████╗", "██╔════╝", "█████╗  ", "██╔══╝  ", "███████╗", "╚══════╝"},
	'a': {" █████╗ ", "██╔══██╗", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},
	'n': {"███╗   ██╗", "████╗  ██║", "██╔██╗ ██║", "██║╚██╗██║", "██║ ╚████║", "╚═╝  ╚═══╝"},
	't': {"████████╗", "╚══██╔══╝", "   ██║   ", "   ██║   ", "   ██║   ", "   ╚═╝   "},
	'o': {" ██████╗ ", "██╔═══██╗", "██║   ██║", "██║   ██║", "╚██████╔╝", " ╚═════╝ "},
	'k': {"██╗  ██╗", "██║ ██╔╝", "█████╔╝ ", "██╔═██╗ ", "██║  ██╗", "╚═╝  ╚═╝"},
}

const bigLogoW = 102

// logo is the wordmark: the big shadowed one when width allows, a compact three-row one otherwise.
func logo(width int) []string {
	if width >= bigLogoW+6 {
		return bigLogo()
	}
	rows := make([]string, 3)
	for r := range rows {
		var b strings.Builder
		for i, ch := range "slendertoken" {
			col := pal.Muted
			if i >= 7 {
				col = pal.Text
			}
			b.WriteString(fg(col).Bold(true).Render(glyphs[ch][r]) + " ")
		}
		rows[r] = b.String()
	}
	return rows
}

func bigLogo() []string {
	rows := make([]string, 6)
	for r := range rows {
		var b strings.Builder
		for i, ch := range "slendertoken" {
			solid := pal.Accent
			if i >= 7 {
				solid = pal.Muted
			}
			for _, c := range bigGlyphs[ch][r] {
				switch c {
				case '█':
					b.WriteString(fg(solid).Render("█"))
				case ' ':
					b.WriteByte(' ')
				default:
					b.WriteString(fg(pal.Faint).Render(string(c)))
				}
			}
		}
		rows[r] = b.String()
	}
	return rows
}

func prettyPath(p string, width int) string {
	if home, err := os.UserHomeDir(); err == nil {
		p = strings.Replace(p, home, "~", 1)
	}
	if r := []rune(p); len(r) > width {
		return "…" + string(r[len(r)-width+1:])
	}
	return p
}

func short(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

func brief(name, args string) string {
	var a map[string]any
	if json.Unmarshal([]byte(args), &a) != nil {
		return cutRunes(args, 60)
	}
	switch name {
	case "read_file", "edit_file", "write_file":
		p := fmt.Sprint(a["path"])
		if name == "read_file" && a["end"] != nil {
			start := 1
			if s, ok := a["start"].(float64); ok {
				start = int(s)
			}
			return fmt.Sprintf("%s:%d-%v", p, start, a["end"])
		}
		return p
	}
	for _, k := range []string{"cmd", "query", "pattern"} {
		if v, ok := a[k]; ok {
			if list, ok := v.([]any); ok {
				parts := make([]string, len(list))
				for i, p := range list {
					parts[i] = fmt.Sprint(p)
				}
				return cutRunes(strings.Join(parts, " "), 80)
			}
			return cutRunes(fmt.Sprint(v), 80)
		}
	}
	return ""
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func userLine(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if i == 0 {
			lines[i] = accent("▌ ") + bold(text(l))
		} else {
			lines[i] = accent("▌ ") + text(l)
		}
	}
	return "\n" + strings.Join(lines, "\n")
}

func dimBlock(out string) string {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = "  " + muted(l)
	}
	return strings.Join(lines, "\n")
}

type diffRow struct {
	kind byte // ' ' context, '+' added, '-' removed
	num  int  // 0 when the line number is unknown
	text string
}

func splitLines(s string) []string { return strings.Split(strings.TrimSuffix(s, "\n"), "\n") }

// editRows is what a diff viewer shows for one edit: the changed lines with line numbers and up to two unchanged
// lines of context on each side, read from the file as it is now. Without the file the rows carry no numbers.
func editRows(repo, path, old, new string) []diffRow {
	data, readErr := os.ReadFile(filepath.Join(repo, path))
	text := string(data)
	at := -1
	if readErr == nil {
		at = strings.Index(text, new)
	}
	if at >= 0 {
		// models often replace a fragment of a line; show whole lines, as a diff viewer would
		lineStart := strings.LastIndex(text[:at], "\n") + 1
		lineEnd := len(text)
		if i := strings.Index(text[at+len(new):], "\n"); i >= 0 {
			lineEnd = at + len(new) + i
		}
		head, tail := text[lineStart:at], text[at+len(new):lineEnd]
		old, new = head+old+tail, head+new+tail
		at = lineStart
	}
	oldL, newL := splitLines(old), splitLines(new)
	pre := 0
	for pre < len(oldL) && pre < len(newL) && oldL[pre] == newL[pre] {
		pre++
	}
	suf := 0
	for suf < len(oldL)-pre && suf < len(newL)-pre && oldL[len(oldL)-1-suf] == newL[len(newL)-1-suf] {
		suf++
	}
	oldCore, newCore := oldL[pre:len(oldL)-suf], newL[pre:len(newL)-suf]
	var fileLines []string
	coreStart := 0
	if readErr == nil {
		fileLines = splitLines(text)
		if at >= 0 {
			coreStart = strings.Count(text[:at], "\n") + 1 + pre
		}
	}
	num := func(i int) int {
		if coreStart == 0 {
			return 0
		}
		return coreStart + i
	}
	var rows []diffRow
	if coreStart > 0 {
		for ln := max(1, coreStart-2); ln < coreStart && ln <= len(fileLines); ln++ {
			rows = append(rows, diffRow{' ', ln, fileLines[ln-1]})
		}
	}
	for i, l := range oldCore {
		rows = append(rows, diffRow{'-', num(i), l})
	}
	for i, l := range newCore {
		rows = append(rows, diffRow{'+', num(i), l})
	}
	if coreStart > 0 {
		for ln := coreStart + len(newCore); ln < coreStart+len(newCore)+2 && ln <= len(fileLines); ln++ {
			rows = append(rows, diffRow{' ', ln, fileLines[ln-1]})
		}
	}
	return rows
}

func addedRows(content string, limit int) []diffRow {
	var rows []diffRow
	for i, l := range splitLines(content) {
		rows = append(rows, diffRow{'+', i + 1, l})
	}
	return rows
}

// renderDiff draws rows as full-width bands: green behind added lines, red behind removed ones, with a number gutter.
func renderDiff(rows []diffRow, width, limit int) string {
	extra := 0
	if len(rows) > limit {
		extra, rows = len(rows)-limit, rows[:limit]
	}
	digits := 0
	for _, r := range rows {
		if r.num > 0 {
			digits = max(digits, len(fmt.Sprint(r.num)))
		}
	}
	out := make([]string, 0, len(rows)+1)
	for _, r := range rows {
		gutter := strings.Repeat(" ", digits)
		if r.num > 0 {
			gutter = fmt.Sprintf("%*d", digits, r.num)
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(pal.Muted)).Width(width)
		switch r.kind {
		case '+':
			style = style.Foreground(lipgloss.Color(pal.Text)).Background(lipgloss.Color(pal.AddBg))
		case '-':
			style = style.Foreground(lipgloss.Color(pal.Text)).Background(lipgloss.Color(pal.DelBg))
		}
		line := fmt.Sprintf("%s %c %s", gutter, r.kind, strings.ReplaceAll(r.text, "\t", "    "))
		out = append(out, style.Render(ansi.Truncate(line, width, "…")))
	}
	if extra > 0 {
		out = append(out, faint(fmt.Sprintf("  ... %d more lines", extra)))
	}
	return strings.Join(out, "\n")
}

// lineDelta counts the lines an edit adds and removes, ignoring the unchanged lines at both ends of old and new.
func lineDelta(name, args string) (added, removed int) {
	var a struct{ Old, New, Content string }
	if json.Unmarshal([]byte(args), &a) != nil {
		return 0, 0
	}
	if name == "write_file" {
		return strings.Count(a.Content, "\n") + btoi(a.Content != "" && !strings.HasSuffix(a.Content, "\n")), 0
	}
	old, new := strings.Split(a.Old, "\n"), strings.Split(a.New, "\n")
	for len(old) > 0 && len(new) > 0 && old[0] == new[0] {
		old, new = old[1:], new[1:]
	}
	for len(old) > 0 && len(new) > 0 && old[len(old)-1] == new[len(new)-1] {
		old, new = old[:len(old)-1], new[:len(new)-1]
	}
	return len(new), len(old)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func plusMinus(add, del int) string {
	var parts []string
	if add > 0 {
		parts = append(parts, added(fmt.Sprintf("+%d", add)))
	}
	if del > 0 {
		parts = append(parts, removed(fmt.Sprintf("-%d", del)))
	}
	return strings.Join(parts, " ")
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func (m *Model) diffWidth() int { return max(30, m.mainW()-8) }

func osc8(url, label string) string { return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\" }

func (m *Model) toolBlock(e agent.Event) string {
	dot := muted("▸")
	if e.Failed {
		dot = bad("✗")
	}
	label := toolLabel[e.Name]
	if label == "" {
		label = e.Name
	}
	head := dot + " " + bold(text(label)) + "  " + muted(brief(e.Name, e.Args))
	first := strings.TrimSpace(firstLine(e.Out))
	style := muted
	if e.Failed {
		style = bad
	}
	var lines []string
	switch {
	case e.Name == "edit_file" && !e.Failed:
		var a struct{ Old, New string }
		_ = json.Unmarshal([]byte(e.Args), &a)
		add, del := lineDelta(e.Name, e.Args)
		lines = []string{faint("    ↳ ") + muted(first) + "  " + plusMinus(add, del),
			indent(renderDiff(editRows(m.o.Repo, brief(e.Name, e.Args), a.Old, a.New), m.diffWidth(), 18), "    ")}
	case e.Name == "write_file" && !e.Failed:
		add, _ := lineDelta(e.Name, e.Args)
		var w struct{ Content string }
		_ = json.Unmarshal([]byte(e.Args), &w)
		lines = []string{faint("    ↳ ") + muted(first) + "  " + plusMinus(add, 0),
			indent(renderDiff(addedRows(w.Content, 12), m.diffWidth(), 12), "    ")}
	case e.Name == "web_search" && len(e.Sources) > 0:
		lines = []string{faint("    ↳ ") + muted(fmt.Sprintf("%d sources", len(e.Sources)))}
		for _, s := range e.Sources {
			lines = append(lines, "    "+faint("· ")+accent(osc8(s.URL, s.Domain()))+faint("  "+cutRunes(s.Title, 60)))
		}
	default:
		lines = []string{faint("    ↳ ") + style(cutRunes(first, 110))}
	}
	return head + "\n" + strings.Join(lines, "\n")
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

func (m *Model) answerBlock(result string) string {
	md := markdown(result, m.width-4)
	lines := strings.Split(md, "\n")
	for i, l := range lines {
		if i == 0 {
			lines[i] = "\n  " + strings.TrimLeft(l, " ")
		} else {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

func energy(kwh, gco2e float64) string {
	return fmt.Sprintf("%.2f Wh and %.0f mg CO2", kwh*1000, gco2e*1000)
}

func (m *Model) doneLine(ag *agent.Agent, t footprint.Totals) string {
	parts := []string{fmt.Sprintf("%s tokens", commas(t.In+t.Out))}
	if t.SavedPrompt > 0 {
		parts = append(parts, good(fmt.Sprintf("↓ %d%% fewer than naive", t.SavedPct)))
	}
	if !m.o.Private {
		parts = append(parts, fmt.Sprintf("$%.4f", t.CostUSD))
	}
	if m.taskAdded+m.taskRemoved > 0 {
		parts = append(parts, plusMinus(m.taskAdded, m.taskRemoved))
	}
	if ag.Verified == "tests failing" {
		parts = append(parts, bad("tests fail"))
	} else if ag.Verified == "tests passing" {
		parts = append(parts, good("tests pass"))
	}
	return "  " + faint("─ ") + muted(strings.Join(parts, muted("  ·  ")))
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (m *Model) route() string {
	if m.o.Private {
		return "private · " + short(gateway.LocalModel())
	}
	if m.o.Small != "" && m.o.Small != m.o.Model {
		return short(m.o.Small) + " → " + short(m.o.Model)
	}
	return short(m.o.Model)
}

func spark(values []int, top int) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range values {
		if top == 0 {
			b.WriteRune(bars[0])
			continue
		}
		b.WriteRune(bars[min(7, 7*v/top)])
	}
	return b.String()
}

func (m *Model) statsPanel() string {
	s, spent, limit := m.session, m.o.Budget.Spent(), m.o.Budget.Limit
	var b strings.Builder
	head := func(t string) { b.WriteString("\n  " + accent(bold(t)) + "\n") }
	row := func(k, v string) { b.WriteString("  " + muted(fmt.Sprintf("%-9s", k)) + text(cutRunes(v, 20)) + "\n") }
	head("Tokens")
	used := s.tokens
	naive := used + s.saved
	row("used", commas(used))
	if s.saved > 0 {
		cells := min(barW, max(1, int(float64(barW)*float64(s.saved)/float64(naive)+0.5)))
		row("naive", "~"+commas(naive))
		b.WriteString("  " + muted(fmt.Sprintf("%-9s", "saved")) + good(fmt.Sprintf("↓ %d%%", 100*s.saved/naive)) + "\n")
		if !m.o.Private {
			b.WriteString("  " + good("~"+energy(s.kwh, s.gco2e)+" less") + "\n")
		}
		b.WriteString("  " + accent(strings.Repeat("█", barW-cells)) + good(strings.Repeat("█", cells)) + "\n")
		b.WriteString("  " + faint("est. vs a naive agent") + "\n")
	}
	if len(m.series) > 1 {
		shown := m.series[max(0, len(m.series)-12):]
		top := 0
		var lean, nv []int
		for _, p := range shown {
			top = max(top, p[1])
			lean, nv = append(lean, p[0]), append(nv, p[1])
		}
		head("Per turn")
		b.WriteString("  " + muted("naive ") + bad(spark(nv, top)) + "\n  " + muted("lean  ") + good(spark(lean, top)) + "\n")
	}
	head("Session")
	row("repo", filepath.Base(m.o.Repo))
	row("elapsed", m.elapsed())
	row("tasks", fmt.Sprint(s.tasks))
	if s.tasks > 0 {
		row("tok/task", commas(used/s.tasks))
		row("$/task", fmt.Sprintf("$%.4f", spent/float64(s.tasks)))
	}
	row("files", fmt.Sprintf("%d modified", len(m.files)))
	head("Budget")
	ratio := 0.0
	if limit > 0 {
		ratio = spent / limit
	}
	filled := min(barW, int(float64(barW)*ratio))
	col := good
	if ratio >= 0.85 {
		col = bad
	} else if ratio >= 0.6 {
		col = warn
	}
	b.WriteString("  " + muted(fmt.Sprintf("$%.4f of $%.2f", spent, limit)) + "\n  " + col(strings.Repeat("█", filled)) + faint(strings.Repeat("░", barW-filled)) + "\n")
	web := !m.o.Private && os.Getenv("TAVILY_API_KEY") != ""
	head("Setup")
	switch {
	case m.o.Private:
		row("model", short(gateway.LocalModel()))
	case m.o.Small != "" && m.o.Small != m.o.Model:
		row("first", short(m.o.Small))
		row("then", short(m.o.Model))
	default:
		row("model", short(m.o.Model))
	}
	row("gateway", strings.TrimPrefix(strings.TrimPrefix(gateway.URL(), "http://"), "https://"))
	row("web", map[bool]string{true: "on (Tavily)", false: "off"}[web])
	if len(m.files) > 0 {
		head("Modified files")
		for _, f := range m.files[max(0, len(m.files)-8):] {
			b.WriteString("  " + text(f) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

var _ web.Source
