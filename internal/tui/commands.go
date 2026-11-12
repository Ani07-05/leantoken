package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Ani07-05/slendertoken/internal/gateway"
)

var commandOrder = []string{"/theme", "/transparent", "/stats", "/clear", "/models", "/private", "/budget", "/settings", "/help", "/exit"}

var commands = map[string]string{
	"/theme":       "switch color theme, e.g. /theme nord",
	"/transparent": "toggle the background: painted or your terminal's own",
	"/stats":       "token, cost and energy savings",
	"/clear":       "clear the context and start fresh",
	"/models":      "pick the large and first models",
	"/private":     "toggle private mode",
	"/budget":      "set the USD limit, e.g. /budget 0.5",
	"/settings":    "edit keys and options",
	"/help":        "show help",
	"/exit":        "quit",
}

var keyHelp = [][2]string{
	{"shift+tab", "cycle permission mode"}, {"esc", "interrupt the running task"}, {"ctrl+n", "clear the context"},
	{"ctrl+o", "pick models"}, {"ctrl+s", "settings"}, {"alt+enter", "newline in the prompt"}, {"ctrl+c", "interrupt, clear, then quit"},
}

func (m *Model) help() string {
	var b strings.Builder
	b.WriteString("\n  " + bold(accent("Commands")) + "\n")
	for _, c := range commandOrder {
		b.WriteString("  " + text(fmt.Sprintf("%-10s", c)) + muted(commands[c]) + "\n")
	}
	b.WriteString("\n  " + bold(accent("Prompt")) + "\n")
	b.WriteString("  " + text(fmt.Sprintf("%-10s", "!cmd")) + muted("run a shell command yourself") + "\n")
	b.WriteString("\n  " + bold(accent("Keys")) + "\n")
	for _, k := range keyHelp {
		b.WriteString("  " + text(fmt.Sprintf("%-10s", k[0])) + muted(k[1]) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) command(line string) tea.Cmd {
	cmd, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "/exit", "/quit":
		return tea.Quit
	case "/help":
		return m.say(m.help())
	case "/clear", "/new":
		return m.newSession()
	case "/models":
		return m.openModels()
	case "/transparent":
		return m.toggleTransparent()
	case "/stats":
		return m.say(m.statsPanel())
	case "/theme":
		if arg == "" {
			m.pickTheme()
			return nil
		}
		return m.setTheme(strings.ToLower(arg))
	case "/private":
		if !m.o.Private && gatewayPrivateKey() == "" {
			return m.say(bad("private mode needs BIFROST_PRIVATE_VK. Set it in /settings."))
		}
		m.o.Private = !m.o.Private
		if m.o.Private {
			return m.say(muted("private mode on: " + gateway.LocalModel() + ", nothing leaves this machine"))
		}
		return m.say(muted("private mode off"))
	case "/budget":
		v, err := strconv.ParseFloat(arg, 64)
		if err != nil {
			return m.say(bad("usage: /budget 0.5"))
		}
		m.o.Budget.SetLimit(v)
		return m.say(muted(fmt.Sprintf("budget limit $%.2f", v)))
	case "/settings", "/config":
		m.openSettings("")
		return nil
	}
	return m.say(muted("Unknown command. Type /help to see what is available."))
}
