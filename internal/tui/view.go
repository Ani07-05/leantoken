package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Ani07-05/slendertoken/internal/art"
)

func (m *Model) View() string {
	if m.home {
		return m.paint(m.homeView())
	}
	mainW := m.mainW()
	var bottom []string
	if m.busy || m.fade > 0 {
		bottom = append(bottom, m.activity())
	}
	switch {
	case m.perm != nil:
		bottom = append(bottom, m.perm.view(mainW))
	case m.ask != nil:
		bottom = append(bottom, m.ask.view(mainW))
	case m.pick != nil:
		bottom = append(bottom, m.pick.view(mainW))
	case m.set != nil:
		bottom = append(bottom, m.set.view(mainW))
	default:
		if len(m.matches) > 0 {
			bottom = append(bottom, m.popup())
		}
		bottom = append(bottom, m.promptBox(mainW))
	}
	bottomStr := strings.Join(bottom, "\n")
	m.vp.Width, m.vp.Height = mainW, max(3, m.height-1-lipgloss.Height(bottomStr))
	if m.follow {
		m.vp.GotoBottom()
	}
	body := lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), bottomStr)
	if m.showSide() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, " ", m.sidebarView(m.height-1))
	}
	return m.paint(body + "\n" + m.statusLine())
}

// paint fills every cell with the theme background (unless transparent, which leaves the terminal's own): lines are padded to the full width, cut at it, and the background
// is re-applied after each style reset so colored spans do not punch holes in it.
func (m *Model) paint(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if m.transparent {
		for i, l := range lines[:m.height] {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
		return strings.Join(lines[:m.height], "\n")
	}
	base := rgbSeq(pal.Bg, true) + rgbSeq(pal.Text, false)
	for i, l := range lines[:m.height] {
		l = ansi.Truncate(l, m.width, "")
		l = strings.ReplaceAll(l, "\x1b[0m", "\x1b[0m"+base)
		if pad := m.width - lipgloss.Width(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		lines[i] = base + l + "\x1b[0m"
	}
	return strings.Join(lines[:m.height], "\n")
}

func (m *Model) promptBox(width int) string {
	border := pal.Line
	if m.busy {
		border = pal.Muted
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(border)).
		Padding(0, 1).Width(max(20, width-2)).Render(accent("❯ ") + m.input.View())
}

func (m *Model) popup() string {
	var lines []string
	for i, c := range m.matches {
		name := fmt.Sprintf("%-10s", c)
		if i == m.sel {
			lines = append(lines, "  "+accent("❯ "+name)+muted(commands[c]))
		} else {
			lines = append(lines, "    "+text(name)+faint(commands[c]))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) activity() string {
	var sprite string
	width := art.Cols
	if m.img.on {
		sprite, width = strings.Join(m.img.lines(m.scene, m.n/2), "\n"), art.ImageCols
	} else {
		sprite = strings.Join(art.Frame(m.scene, m.n/2), "\n")
	}
	secs := (m.n - m.busySince) / 10
	head := accent(spinner(m.n)+" ") + bold(text(art.Verb(m.scene, m.n)+"…")) +
		muted(fmt.Sprintf("  %ds  ·  %s tok", secs, commas(m.taskTokens)))
	right := strings.Join([]string{head, muted(cutRunes(m.detail, max(10, m.mainW()-width-8))), faint("esc to interrupt")}, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, " ", sprite, "  ", right)
}

var spinFrames = []string{"✻", "✼", "✽", "✾", "✽", "✼"}

func spinner(n int) string { return spinFrames[(n/2)%len(spinFrames)] }

func (m *Model) sidebarView(height int) string {
	head := bold(text("slendertoken")) + "\n" + muted(prettyPath(m.o.Repo, sidebarW-6)) + "\n" + muted(m.route())
	body := head + "\n" + m.statsPanel()
	return lipgloss.NewStyle().Width(sidebarW).Height(height).MaxHeight(height).
		Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lipgloss.Color(pal.Line)).
		PaddingLeft(1).Render(body)
}

func (m *Model) statusLine() string {
	md := m.mode_()
	role := map[string]string{"accent": pal.Accent, "warn": pal.Warn, "bad": pal.Bad}[md.role]
	left := fg(role).Render("⏵⏵ "+md.name) + faint("  ·  ") + muted(m.route())
	if m.notice != "" && m.n < m.noticeUntil {
		left = text(m.notice)
	}
	dot, label := faint("●"), "Bifrost"
	if m.gatewayOK != nil {
		if *m.gatewayOK {
			dot = good("●")
		} else {
			dot, label = bad("○"), "Bifrost down"
		}
	}
	right := dot + " " + muted(label)
	hint := ""
	if !m.busy && m.input.Value() == "" {
		hint = faint("  / commands · ! shell · shift+tab mode · ctrl+b sidebar")
	}
	gap := max(2, m.width-2-lipgloss.Width(left)-lipgloss.Width(right)-lipgloss.Width(hint))
	return " " + left + hint + strings.Repeat(" ", gap) + right
}

// homeView is the opening screen: logo, prompt and tips centered in the terminal.
func (m *Model) homeView() string {
	w := min(78, max(30, m.width-4))
	var inner string
	switch {
	case m.perm != nil:
		inner = m.perm.view(w)
	case m.ask != nil:
		inner = m.ask.view(w)
	case m.pick != nil:
		inner = m.pick.view(w)
	case m.set != nil:
		inner = m.set.view(w)
	default:
		inner = m.promptBox(w)
		if len(m.matches) > 0 {
			inner += "\n" + m.popup()
		}
	}
	head := lipgloss.JoinVertical(lipgloss.Center, append(logo(m.width), "", muted("fewest tokens for every task"))...)
	info := muted(prettyPath(m.o.Repo, 40)) + faint("  ·  ") + muted(m.route())
	tips := faint("/help commands   shift+tab mode   ! shell   /theme colors")
	block := lipgloss.JoinVertical(lipgloss.Center, head, "", "", inner, "", info, tips)
	body := lipgloss.Place(m.width, max(1, m.height-1), lipgloss.Center, lipgloss.Center, block)
	return body + "\n" + m.statusLine()
}
