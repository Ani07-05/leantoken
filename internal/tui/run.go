package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Ani07-05/slendertoken/internal/agent"
	"github.com/Ani07-05/slendertoken/internal/art"
	"github.com/Ani07-05/slendertoken/internal/budget"
	"github.com/Ani07-05/slendertoken/internal/gateway"
	"github.com/Ani07-05/slendertoken/internal/tools"
)

func (m *Model) runTask(task string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	memory := agent.MemoryNote(m.turns, 1800)
	prior := m.priorNaive
	return func() tea.Msg {
		defer cancel()
		ag, err := agent.New(agent.Options{
			Repo: m.o.Repo, Model: m.o.Model, Small: m.o.Small, Budget: m.o.Budget, Private: m.o.Private, Lean: true,
			Memory: memory, PriorNaive: prior, Approve: m.approve, Ask: m.askUser, Ctx: ctx,
			Emit: func(e agent.Event) { m.p.Send(eventMsg(e)) },
		})
		if err != nil {
			return doneMsg{err: err}
		}
		result, err := ag.Run(task, 0)
		if path, saveErr := ag.FP.Save(m.o.Repo+"/.slendertoken/runs", result, ag.Verified); saveErr != nil || path == "" {
			_ = saveErr
		}
		return doneMsg{ag: ag, result: result, err: err}
	}
}

func (m *Model) runShell(cmd string) tea.Cmd {
	ws := tools.NewWorkspace(m.o.Repo, true)
	return func() tea.Msg {
		out, err := ws.Call("run_shell", map[string]any{"cmd": cmd})
		if err != nil {
			out = "error: " + err.Error()
		}
		return shellDoneMsg{strings.TrimRight(strings.TrimPrefix(out, "exit=0\n"), "\n")}
	}
}

// approve runs on the agent goroutine and blocks until the user answers in the permission panel.
func (m *Model) approve(text string) bool {
	kind := strings.TrimSuffix(strings.Fields(text)[0], ":")
	md := m.mode.Load()
	if m.always[kind] || md == 2 || (md == 1 && (kind == "edit" || kind == "write")) {
		return true
	}
	reply := make(chan string, 1)
	m.p.Send(permMsg{text: text, always: kind == "edit" || kind == "write", reply: reply})
	choice := <-reply
	if choice == "always" {
		m.always[kind] = true
	}
	return choice == "once" || choice == "always"
}

// askUser runs on the agent goroutine and blocks until the user picks an option or types an answer.
func (m *Model) askUser(question string, options []string) string {
	reply := make(chan string, 1)
	m.p.Send(askMsg{question, options, reply})
	return <-reply
}

func (m *Model) onEvent(e agent.Event) tea.Cmd {
	switch e.Kind {
	case "start":
		m.toolRunning = true
		scene, ok := art.ToolScene[e.Name]
		if !ok {
			scene = "think"
		}
		m.pending = &[2]string{scene, brief(e.Name, e.Args)}
	case "call":
		m.taskTokens += e.Prompt + e.Completion
		m.taskSaved += e.Saved
	case "tool":
		m.toolRunning = false
		if !e.Failed && (e.Name == "edit_file" || e.Name == "write_file") {
			add, del := lineDelta(e.Name, e.Args)
			m.taskAdded, m.taskRemoved = m.taskAdded+add, m.taskRemoved+del
			m.session.added, m.session.removed = m.session.added+add, m.session.removed+del
		}
		return m.say(m.toolBlock(e))
	case "escalate":
		return m.say(warn("  ↑ switched to " + short(e.To) + ": " + e.Reason))
	case "wait":
		return m.say(muted(fmt.Sprintf("  rate limited, waiting %.0fs", e.Seconds)))
	}
	return nil
}

func (m *Model) onDone(d doneMsg) tea.Cmd {
	m.busy, m.fade, m.cancel = false, 8, nil
	m.taskTokens, m.taskSaved = 0, 0
	switch {
	case d.err != nil && (errors.Is(d.err, context.Canceled) || errors.Is(d.err, errCancelled)):
		return m.say(muted("  ⎿ interrupted"))
	case d.err != nil && errors.Is(d.err, budget.ErrExceeded):
		return m.say(bad("  " + d.err.Error()))
	case d.err != nil && errors.Is(d.err, agent.ErrGatewayDown):
		return m.say(bad("  " + d.err.Error()))
	case d.err != nil:
		return m.say(bad("  error: " + d.err.Error()))
	}
	t := d.ag.FP.Totals()
	m.session.tokens += t.In + t.Out
	m.session.saved += t.SavedPrompt
	m.session.kwh += t.SavedKWh
	m.session.gco2e += t.SavedGCO2e
	m.session.tasks++
	m.series = append(m.series, [2]int{t.In + t.Out, t.NaiveTotal})
	m.priorNaive += d.ag.TurnNaive
	m.turns = append(m.turns, d.ag.Note(d.result))
	for _, f := range d.ag.FilesTouched {
		if !contains(m.files, f) {
			m.files = append(m.files, f)
		}
	}
	return m.say(m.answerBlock(d.result), m.doneLine(d.ag, t)+"\n")
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// shellOK reports whether the program exists, for the one-shot CLI path.
func shellOK(p string) bool { _, err := exec.LookPath(p); return err == nil }

var _ = os.Getenv
var _ = gateway.URL
var _ = shellOK

func gatewayPrivateKey() string { return os.Getenv("BIFROST_PRIVATE_VK") }
