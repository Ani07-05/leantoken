package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Ani07-05/slendertoken/internal/config"
	"github.com/Ani07-05/slendertoken/internal/gateway"
)

func panelBox(border string, width int, body string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(border)).
		Padding(0, 1).Width(max(20, width-2)).Render(body)
}

// permission replaces the prompt while the agent waits for approval.
type permission struct {
	text    string
	choices []string
	idx     int
	reply   chan string
}

var choiceLabel = map[string]string{"once": "Allow once", "always": "Allow always", "reject": "Reject"}

func newPermission(p permMsg) *permission {
	c := []string{"once"}
	if p.always {
		c = append(c, "always")
	}
	return &permission{text: p.text, choices: append(c, "reject"), reply: p.reply}
}

func (p *permission) decide(m *Model, choice string) tea.Cmd {
	p.reply <- choice
	m.perm = nil
	return nil
}

func (p *permission) key(k tea.KeyMsg, m *Model) tea.Cmd {
	n := len(p.choices)
	switch k.String() {
	case "left", "h", "shift+tab":
		p.idx = (p.idx - 1 + n) % n
	case "right", "l", "tab":
		p.idx = (p.idx + 1) % n
	case "enter":
		return p.decide(m, p.choices[p.idx])
	case "y":
		return p.decide(m, "once")
	case "a":
		if contains(p.choices, "always") {
			return p.decide(m, "always")
		}
	case "n", "esc", "ctrl+c":
		return p.decide(m, "reject")
	}
	return nil
}

func (p *permission) view(width int) string {
	head, body, _ := strings.Cut(p.text, "\n")
	var b strings.Builder
	b.WriteString(warn("⚠ ") + bold(warn("Approve?")) + "  " + text(strings.Replace(head, "shell: ", "$ ", 1)) + "\n")
	if body != "" {
		var rows []diffRow
		for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			switch {
			case strings.HasPrefix(l, "- "):
				rows = append(rows, diffRow{'-', 0, l[2:]})
			case strings.HasPrefix(l, "+ "):
				rows = append(rows, diffRow{'+', 0, l[2:]})
			default:
				rows = append(rows, diffRow{' ', 0, l})
			}
		}
		b.WriteString(renderDiff(rows, max(30, width-6), 14) + "\n")
	}
	b.WriteString("\n")
	for i, c := range p.choices {
		if i == p.idx {
			b.WriteString(lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(lipgloss.Color(pal.Accent)).Render(" "+choiceLabel[c]+" ") + "  ")
		} else {
			b.WriteString(text(" "+choiceLabel[c]+" ") + "  ")
		}
	}
	b.WriteString(faint("  ←/→ select  enter confirm  y/a/n  esc reject"))
	return panelBox(pal.Warn, width, b.String())
}

// picker is a single-choice list. onMove previews each highlighted item; onCancel undoes the preview.
type picker struct {
	title    string
	items    []string
	current  string
	idx      int
	onPick   func(*Model, string) tea.Cmd
	onMove   func(*Model, string)
	onCancel func(*Model)
}

func (p *picker) key(k tea.KeyMsg, m *Model) tea.Cmd {
	n := len(p.items)
	switch k.String() {
	case "up", "k":
		p.idx = (p.idx - 1 + n) % n
	case "down", "j":
		p.idx = (p.idx + 1) % n
	case "enter":
		m.pick = nil
		return p.onPick(m, p.items[p.idx])
	case "esc", "ctrl+c":
		m.pick = nil
		if p.onCancel != nil {
			p.onCancel(m)
		}
		return nil
	}
	if p.onMove != nil {
		p.onMove(m, p.items[p.idx])
	}
	return nil
}

func (p *picker) view(width int) string {
	var b strings.Builder
	b.WriteString(bold(accent(p.title)) + "\n\n")
	start := max(0, min(p.idx-5, len(p.items)-10))
	for i := start; i < min(len(p.items), start+10); i++ {
		mark := "  "
		if p.items[i] == p.current {
			mark = "● "
		}
		if i == p.idx {
			b.WriteString(accent("❯ "+mark) + bold(text(p.items[i])) + "\n")
		} else {
			b.WriteString("  " + faint(mark) + muted(p.items[i]) + "\n")
		}
	}
	b.WriteString("\n" + faint("↑/↓ move   enter select   esc cancel"))
	return panelBox(pal.Accent, width, b.String())
}

func (m *Model) pickModels(models []string) {
	step2 := func(m *Model, large string) tea.Cmd {
		if large != "" {
			m.o.Model = large
			_ = gateway.SaveModel(&large, nil)
		}
		items := append([]string{noneItem}, models...)
		cur := m.o.Small
		if cur == "" {
			cur = noneItem
		}
		m.pick = &picker{title: "Step 2 of 2: first model, tried before the large one", items: items, current: cur, idx: indexOf(items, cur),
			onPick: func(m *Model, small string) tea.Cmd {
				if small == noneItem {
					small = ""
				}
				m.o.Small = small
				_ = gateway.SaveModel(nil, &small)
				return nil
			}}
		return nil
	}
	m.pick = &picker{title: "Step 1 of 2: large model, used when a task gets hard", items: models, current: m.o.Model, idx: indexOf(models, m.o.Model), onPick: step2}
}

const noneItem = "none: use the large model only"

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return 0
}

func (m *Model) pickTheme() {
	before := pal
	names := themeNames()
	m.pick = &picker{
		title: "Select a theme", items: names, current: pal.Name, idx: indexOf(names, pal.Name),
		onMove:   func(m *Model, name string) { p, _ := PaletteNamed(name); SetTheme(p); m.applyInputStyle() },
		onCancel: func(m *Model) { SetTheme(before); m.applyInputStyle() },
		onPick:   func(m *Model, name string) tea.Cmd { return m.setTheme(name) },
	}
}

// settings edits ~/.slendertoken/.env; saved values apply from the next request.
type settings struct {
	reason string
	fields []config.Field
	inputs []textinput.Model
	idx    int
}

func (m *Model) openSettings(reason string) {
	s := &settings{reason: reason, fields: config.Fields()}
	for _, f := range s.fields {
		ti := textinput.New()
		ti.SetValue(f.Value)
		ti.Placeholder = f.Example
		ti.Prompt = ""
		ti.PlaceholderStyle = fg(pal.Faint)
		ti.TextStyle = fg(pal.Text)
		if config.IsSecret(f.Key) {
			ti.EchoMode = textinput.EchoPassword
		}
		s.inputs = append(s.inputs, ti)
	}
	for i, f := range s.fields {
		if f.Value == "" && f.Key != "BIFROST_URL" {
			s.idx = i
			break
		}
	}
	s.inputs[s.idx].Focus()
	m.set = s
}

func (s *settings) move(d int) {
	s.inputs[s.idx].Blur()
	s.idx = (s.idx + d + len(s.inputs)) % len(s.inputs)
	s.inputs[s.idx].Focus()
}

func (s *settings) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	s.inputs[s.idx], cmd = s.inputs[s.idx].Update(msg)
	return cmd
}

func (s *settings) key(k tea.KeyMsg, m *Model) tea.Cmd {
	switch k.String() {
	case "esc", "ctrl+c":
		m.set = nil
		return nil
	case "ctrl+s":
		return s.save(m)
	case "down", "tab", "enter":
		s.move(1)
		return nil
	case "up", "shift+tab":
		s.move(-1)
		return nil
	}
	return s.update(k)
}

func (s *settings) save(m *Model) tea.Cmd {
	for i, f := range s.fields {
		v := strings.TrimSpace(s.inputs[i].Value())
		if v == "" {
			_ = config.SetValue(f.Key, nil)
		} else {
			_ = config.SetValue(f.Key, &v)
		}
		if config.Overridden(f.Key) == "" {
			if v != "" {
				os.Setenv(f.Key, v)
			} else {
				os.Unsetenv(f.Key)
			}
		}
	}
	m.set = nil
	if p, ok := PaletteNamed(os.Getenv("SLENDERTOKEN_THEME")); ok {
		SetTheme(p)
		m.applyInputStyle()
	}
	cmds := []tea.Cmd{m.say(muted("settings saved to " + config.GlobalEnv)), m.checkGateway()}
	if m.o.Model == "" && !m.o.Private && os.Getenv("BIFROST_VK") != "" {
		cmds = append(cmds, m.openModels())
	}
	return tea.Batch(cmds...)
}

func (s *settings) view(width int) string {
	var b strings.Builder
	b.WriteString(bold(accent("Settings")) + "\n")
	if s.reason != "" {
		b.WriteString(bad(s.reason) + "\n")
	}
	start := max(0, min(s.idx-2, len(s.fields)-5))
	for i := start; i < min(len(s.fields), start+5); i++ {
		f := s.fields[i]
		label := bold(text(f.Key)) + muted("  "+f.Desc)
		if i == s.idx {
			label = accent("❯ ") + label
		} else {
			label = "  " + label
		}
		b.WriteString("\n" + label + "\n")
		if src := config.Overridden(f.Key); src != "" {
			b.WriteString("  " + warn("overridden by "+src) + "\n")
		}
		b.WriteString("  " + s.inputs[i].View() + "\n")
	}
	b.WriteString("\n" + faint(fmt.Sprintf("↑/↓ field   ctrl+s save   esc cancel   %s", config.GlobalEnv)))
	return panelBox(pal.Accent, width, b.String())
}

// asker is the model's multiple-choice question; the last entry lets the user type their own answer.
type asker struct {
	question string
	options  []string
	idx      int
	typing   bool
	input    textinput.Model
	reply    chan string
}

const otherItem = "Something else (type it)"

func newAsker(msg askMsg) *asker {
	ti := textinput.New()
	ti.Prompt = "  "
	ti.Placeholder = "type your answer, enter to send"
	ti.PlaceholderStyle = fg(pal.Faint)
	ti.TextStyle = fg(pal.Text)
	return &asker{question: msg.question, options: append(append([]string{}, msg.options...), otherItem), input: ti, reply: msg.reply}
}

func (a *asker) answer(m *Model, answer string) tea.Cmd {
	a.reply <- answer
	m.ask = nil
	return m.say(muted("? "+a.question), accent("  → ")+text(answer))
}

func (a *asker) key(k tea.KeyMsg, m *Model) tea.Cmd {
	if a.typing {
		switch k.String() {
		case "enter":
			if v := strings.TrimSpace(a.input.Value()); v != "" {
				return a.answer(m, v)
			}
		case "esc":
			a.typing = false
			a.input.Blur()
		default:
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(k)
			return cmd
		}
		return nil
	}
	n := len(a.options)
	switch s := k.String(); s {
	case "up", "k":
		a.idx = (a.idx - 1 + n) % n
	case "down", "j", "tab":
		a.idx = (a.idx + 1) % n
	case "enter":
		if a.idx == n-1 {
			a.typing = true
			return a.input.Focus()
		}
		return a.answer(m, a.options[a.idx])
	case "esc", "ctrl+c":
		return a.answer(m, "(no answer: the user dismissed the question, make a reasonable choice)")
	default:
		if len(s) == 1 && s[0] >= '1' && int(s[0]-'1') < n-1 {
			return a.answer(m, a.options[s[0]-'1'])
		}
	}
	return nil
}

func (a *asker) view(width int) string {
	var b strings.Builder
	b.WriteString(bold(accent("? ")) + bold(text(a.question)) + "\n\n")
	for i, o := range a.options {
		label := fmt.Sprintf("%d. %s", i+1, o)
		if i == len(a.options)-1 {
			label = "   " + o
		}
		if i == a.idx {
			b.WriteString(accent("❯ ") + bold(text(label)) + "\n")
		} else {
			b.WriteString("  " + muted(label) + "\n")
		}
	}
	if a.typing {
		b.WriteString("\n" + a.input.View() + "\n")
	}
	b.WriteString("\n" + faint("↑/↓ move   enter choose   1-9 quick pick   esc skip"))
	return panelBox(pal.Accent, width, b.String())
}
