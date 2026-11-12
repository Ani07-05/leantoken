// Package tui is the full-screen terminal UI: a scrolling transcript, a stats sidebar, a prompt box and a status line,
// all painted on the theme's own background.
package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Ani07-05/slendertoken/internal/agent"
	"github.com/Ani07-05/slendertoken/internal/budget"
	"github.com/Ani07-05/slendertoken/internal/config"
	"github.com/Ani07-05/slendertoken/internal/gateway"
)

const (
	hold     = 15 // ticks (0.1s) an animal stays on screen, so a fast tool call is still visible
	sidebarW = 34
	minSideW = 110 // below this terminal width the sidebar hides itself
)

type Options struct {
	Repo         string
	Budget       *budget.Budget
	Private      bool
	Model, Small string
	Version      string
}

type mode struct{ name, blurb, role string }

var modes = []mode{
	{"Ask", "asks before every edit and shell command", "accent"},
	{"Accept edits", "edits files without asking, still asks for shell", "warn"},
	{"Auto", "runs everything without asking", "bad"},
}

type (
	tickMsg   time.Time
	healthMsg bool
	eventMsg  agent.Event
	doneMsg   struct {
		ag     *agent.Agent
		result string
		err    error
	}
	shellDoneMsg struct{ out string }
	permMsg      struct {
		text   string
		always bool
		reply  chan string
	}
	askMsg struct {
		question string
		options  []string
		reply    chan string
	}
	modelsMsg struct {
		models []string
		err    error
		open   bool
	}
)

type Model struct {
	o      Options
	p      *tea.Program
	input  textarea.Model
	vp     viewport.Model
	img    *images
	width  int
	height int

	transparent            bool // no painted background: the terminal's own shows through
	follow                 bool // transcript sticks to the newest line until the user scrolls up
	home                   bool // opening screen: everything centered, until the first task
	side                   bool
	transcript             []string
	busy, quitArm          bool
	n, busySince           int
	scene, detail          string
	sceneSince, shownUntil int
	pending                *[2]string
	toolRunning            bool
	fade                   int
	taskTokens             int
	taskSaved              float64
	taskAdded, taskRemoved int
	session                stats
	series                 [][2]int
	files                  []string
	always                 map[string]bool
	turns                  []agent.Turn
	priorNaive             int
	mode                   atomic.Int32
	gatewayOK              *bool
	matches                []string
	sel                    int
	history                []string
	histIdx                int
	started                time.Time
	cancel                 context.CancelFunc
	notice                 string
	noticeUntil            int

	perm *permission
	ask  *asker
	pick *picker
	set  *settings
}

type stats struct {
	tokens, saved, tasks int
	added, removed       int
	kwh, gco2e           float64
}

func New(o Options) *Model {
	ta := textarea.New()
	ta.Prompt, ta.ShowLineNumbers, ta.CharLimit = "", false, 0
	ta.SetHeight(1)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	ta.Focus()
	vp := viewport.New(0, 0)
	vp.MouseWheelEnabled = true
	m := &Model{o: o, input: ta, vp: vp, img: newImages(), follow: true, home: true, side: true, width: 100, height: 30, scene: "think",
		always: map[string]bool{}, started: time.Now(), histIdx: -1}
	if p, ok := PaletteNamed(os.Getenv("SLENDERTOKEN_THEME")); ok {
		SetTheme(p)
	}
	m.transparent = os.Getenv("SLENDERTOKEN_TRANSPARENT") == "1"
	m.applyInputStyle()
	return m
}

func (m *Model) applyInputStyle() {
	m.input.FocusedStyle.Text = fg(pal.Text)
	m.input.FocusedStyle.Placeholder = fg(pal.Faint)
	m.input.BlurredStyle.Text = fg(pal.Muted)
	m.input.BlurredStyle.Placeholder = fg(pal.Faint)
	m.input.Cursor.Style = fg("#ffffff")
	m.input.Placeholder = `Ask anything...  "fix the failing test"`
}

// Run starts the program and blocks until it exits.
func Run(o Options) error {
	m := New(o)
	m.syncTerminalBg()
	defer resetTerminal()
	defer m.img.clear()
	m.p = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := m.p.Run()
	return err
}

// The terminal's own padding and the pixels left over past the last cell are outside the grid we can paint.
// OSC 11 sets the terminal's default background to the theme's so that margin blends in; OSC 111 restores it on exit.
func (m *Model) syncTerminalBg() {
	if m.transparent {
		resetTerminal()
		return
	}
	paintTerminal(pal.Bg)
}

func paintTerminal(bg string) {
	if st, err := os.Stdout.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintf(os.Stdout, "\x1b]11;%s\x1b\\", bg)
	}
}

func resetTerminal() {
	if st, err := os.Stdout.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stdout, "\x1b]111\x1b\\")
	}
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, tick(), m.checkGateway()}
	switch {
	case m.o.Private && os.Getenv("BIFROST_PRIVATE_VK") == "":
		m.openSettings("Private mode needs the Ollama-only virtual key (BIFROST_PRIVATE_VK).")
	case !m.o.Private && os.Getenv("BIFROST_VK") == "":
		m.openSettings("Add your Bifrost virtual key (BIFROST_VK) to get started.")
	case m.o.Model == "" && !m.o.Private:
		cmds = append(cmds, m.openModels())
	}
	return tea.Batch(cmds...)
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) checkGateway() tea.Cmd {
	return func() tea.Msg {
		c := http.Client{Timeout: 2 * time.Second}
		resp, err := c.Get(gateway.URL() + "/health")
		if err != nil {
			return healthMsg(false)
		}
		resp.Body.Close()
		return healthMsg(resp.StatusCode == 200)
	}
}

// mainW is the width of the transcript and prompt column.
func (m *Model) mainW() int {
	if m.showSide() {
		return m.width - sidebarW - 1
	}
	return m.width
}

func (m *Model) showSide() bool { return m.side && !m.home && m.width >= minSideW }

// say appends to the transcript. View scrolls to the newest line when follow is set, once the viewport has its real size.
func (m *Model) say(parts ...string) tea.Cmd {
	block := lipgloss.NewStyle().Width(max(20, m.mainW()-2)).Render(strings.Join(parts, "\n"))
	m.transcript = append(m.transcript, block)
	m.vp.SetContent(strings.Join(m.transcript, "\n"))
	return nil
}

func (m *Model) flash(s string) { m.notice, m.noticeUntil = s, m.n+30 }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(10, m.mainW()-6))
		return m, nil
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return m, cmd
	case tickMsg:
		return m, m.onTick()
	case healthMsg:
		ok := bool(msg)
		m.gatewayOK = &ok
		return m, nil
	case eventMsg:
		return m, m.onEvent(agent.Event(msg))
	case doneMsg:
		return m, m.onDone(msg)
	case shellDoneMsg:
		m.busy = false
		return m, m.say(dimBlock(msg.out))
	case permMsg:
		m.perm = newPermission(msg)
		return m, nil
	case askMsg:
		m.ask = newAsker(msg)
		return m, nil
	case modelsMsg:
		return m, m.onModels(msg)
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	if m.set != nil {
		return m, m.set.update(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) onTick() tea.Cmd {
	m.n++
	if m.n >= m.shownUntil {
		switch {
		case m.pending != nil:
			m.show(m.pending[0], m.pending[1])
			m.pending = nil
		case m.busy && !m.toolRunning && m.scene != "think":
			m.show("think", "")
		}
	}
	if !m.busy && m.fade > 0 {
		m.fade--
	}
	cmds := []tea.Cmd{tick()}
	if m.n%150 == 0 {
		cmds = append(cmds, m.checkGateway())
	}
	return tea.Batch(cmds...)
}

func (m *Model) show(scene, detail string) {
	m.scene, m.sceneSince, m.detail = scene, m.n, detail
	m.shownUntil = m.n + hold
}

func (m *Model) mode_() mode { return modes[m.mode.Load()] }

func (m *Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.perm != nil:
		return m, m.perm.key(k, m)
	case m.ask != nil:
		return m, m.ask.key(k, m)
	case m.pick != nil:
		return m, m.pick.key(k, m)
	case m.set != nil:
		return m, m.set.key(k, m)
	}
	key := k.String()
	if key != "ctrl+c" {
		m.quitArm = false
	}
	switch key {
	case "ctrl+c":
		switch {
		case m.busy:
			m.interrupt()
		case m.input.Value() != "":
			m.input.Reset()
			m.refreshMatches()
		case m.quitArm:
			return m, tea.Quit
		default:
			m.quitArm = true
			m.flash("press ctrl+c again to quit")
		}
		return m, nil
	case "ctrl+d":
		if m.input.Value() == "" {
			return m, tea.Quit
		}
	case "esc":
		switch {
		case m.busy:
			m.interrupt()
		case len(m.matches) > 0:
			m.matches = nil
		}
		return m, nil
	case "pgup", "pgdown", "ctrl+u":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(tea.KeyMsg{Type: map[string]tea.KeyType{"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "ctrl+u": tea.KeyPgUp}[key]})
		m.follow = m.vp.AtBottom()
		return m, cmd
	case "ctrl+b":
		m.side = !m.side
		return m, nil
	case "shift+tab":
		m.mode.Store((m.mode.Load() + 1) % int32(len(modes)))
		md := m.mode_()
		m.flash(md.name + ": " + md.blurb)
		return m, nil
	case "ctrl+o":
		return m, m.openModels()
	case "ctrl+s":
		m.openSettings("")
		return m, nil
	case "ctrl+n":
		return m, m.newSession()
	case "tab":
		if len(m.matches) > 0 {
			m.complete()
		}
		return m, nil
	case "up", "down":
		if len(m.matches) > 0 {
			d := map[string]int{"up": -1, "down": 1}[key]
			m.sel = (m.sel + d + len(m.matches)) % len(m.matches)
			return m, nil
		}
		if m.input.LineCount() == 1 && len(m.history) > 0 && (m.input.Value() == "" || m.histIdx >= 0) {
			m.browseHistory(key)
			return m, nil
		}
	case "enter":
		return m, m.submit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	m.input.SetHeight(min(6, max(1, m.input.LineCount())))
	m.refreshMatches()
	return m, cmd
}

func (m *Model) browseHistory(key string) {
	if key == "up" {
		m.histIdx = min(m.histIdx+1, len(m.history)-1)
	} else {
		m.histIdx--
	}
	if m.histIdx < 0 {
		m.histIdx = -1
		m.input.Reset()
		return
	}
	m.input.SetValue(m.history[len(m.history)-1-m.histIdx])
	m.input.CursorEnd()
}

func (m *Model) refreshMatches() {
	v := m.input.Value()
	m.matches = nil
	if strings.HasPrefix(v, "/") && !strings.ContainsAny(v, " \n") {
		for _, c := range commandOrder {
			if strings.HasPrefix(c, v) {
				m.matches = append(m.matches, c)
			}
		}
	}
	m.sel = min(m.sel, max(0, len(m.matches)-1))
}

func (m *Model) complete() {
	m.input.SetValue(m.matches[m.sel] + " ")
	m.input.CursorEnd()
	m.matches = nil
}

func (m *Model) interrupt() {
	if m.cancel != nil {
		m.cancel()
	}
	m.flash("interrupted")
}

func (m *Model) submit() tea.Cmd {
	line := strings.TrimSpace(m.input.Value())
	if len(m.matches) > 0 && !strings.Contains(line, " ") {
		line = m.matches[m.sel]
	}
	m.input.Reset()
	m.input.SetHeight(1)
	m.matches, m.histIdx = nil, -1
	if line == "" {
		return nil
	}
	if m.busy {
		m.flash("still working on the last task")
		return nil
	}
	m.history = append(m.history, line)
	m.follow = true
	switch {
	case strings.HasPrefix(line, "/"):
		return m.command(line)
	case strings.HasPrefix(line, "!"):
		m.home = false
		m.begin()
		m.say(userLine("! " + strings.TrimSpace(line[1:])))
		return m.runShell(strings.TrimSpace(line[1:]))
	}
	m.home = false
	m.begin()
	m.say(userLine(line))
	return m.runTask(line)
}

func (m *Model) begin() {
	m.busy, m.busySince, m.pending, m.toolRunning = true, m.n, nil, false
	m.taskTokens, m.taskSaved, m.taskAdded, m.taskRemoved = 0, 0, 0, 0
	m.show("think", "")
}

func (m *Model) newSession() tea.Cmd {
	if m.busy {
		return nil
	}
	m.session, m.files, m.turns, m.series = stats{}, nil, nil, nil
	m.always, m.priorNaive, m.started = map[string]bool{}, 0, time.Now()
	m.transcript, m.follow = nil, true
	m.vp.SetContent("")
	m.home = true
	return nil
}

var errCancelled = errors.New("cancelled")

func (m *Model) onModels(msg modelsMsg) tea.Cmd {
	if msg.err != nil {
		if msg.open {
			return m.say(bad("cannot list models: " + msg.err.Error()))
		}
		return nil
	}
	if msg.open {
		if len(msg.models) == 0 {
			return m.say(bad("Bifrost lists no Groq models"))
		}
		m.pickModels(msg.models)
	}
	return nil
}

func (m *Model) openModels() tea.Cmd {
	if m.o.Private {
		return m.say(muted("private mode runs " + gateway.LocalModel() + ". Change it with SLENDERTOKEN_LOCAL_MODEL."))
	}
	if cached := gateway.CachedModels(); len(cached) > 0 {
		m.pickModels(cached)
		return m.fetchModels(false)
	}
	return tea.Batch(m.say(muted("loading models from Bifrost...")), m.fetchModels(true))
}

func (m *Model) fetchModels(open bool) tea.Cmd {
	return func() tea.Msg {
		models, err := gateway.ListModels()
		return modelsMsg{models, err, open}
	}
}

func (m *Model) setTheme(name string) tea.Cmd {
	p, ok := PaletteNamed(name)
	if !ok {
		return m.say(bad("unknown theme. Choose from: " + strings.Join(themeNames(), ", ")))
	}
	SetTheme(p)
	m.applyInputStyle()
	m.syncTerminalBg()
	_ = config.SetValue("SLENDERTOKEN_THEME", &name)
	os.Setenv("SLENDERTOKEN_THEME", name)
	return nil
}

func themeNames() []string {
	out := make([]string, len(Palettes))
	for i, p := range Palettes {
		out[i] = p.Name
	}
	return out
}

func (m *Model) elapsed() string {
	mins := int(time.Since(m.started).Minutes())
	if mins >= 60 {
		return fmt.Sprintf("%dh %02dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}

// toggleTransparent switches between the painted background and the terminal's own, and remembers the choice.
func (m *Model) toggleTransparent() tea.Cmd {
	m.transparent = !m.transparent
	v := map[bool]string{true: "1", false: "0"}[m.transparent]
	_ = config.SetValue("SLENDERTOKEN_TRANSPARENT", &v)
	os.Setenv("SLENDERTOKEN_TRANSPARENT", v)
	m.syncTerminalBg()
	return nil
}
