// Package agent runs one task: small model first, escalate to the large one when a step fails.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Ani07-05/slendertoken/internal/budget"
	"github.com/Ani07-05/slendertoken/internal/footprint"
	"github.com/Ani07-05/slendertoken/internal/gateway"
	"github.com/Ani07-05/slendertoken/internal/tools"
	"github.com/Ani07-05/slendertoken/internal/web"
)

const (
	systemNaive = "You are a coding assistant. Use the tools to complete the task and reply when done."
	system      = "You are a terse coding agent. Use list_files, search and ranged read_file, never read whole files. " +
		"Never grep, find or cat in the shell; search is ripgrep. Prefer edit_file over write_file. Run tests to verify. Stop when done and reply in one line."
	// a question gets no write tools, so asking about the code can never change it
	systemAsk = "You answer questions about a code repo. Use list_files, search and ranged read_file; read only what you need. " +
		"Do not change files. Answer in a few short sentences or bullets."

	keepFull = 2 // older tool results shrink to a stub; every kept result is re-sent on every later call
	elideTo  = 120
	// the naive twin gets a generous output cap too, so one giant listing cannot inflate the savings figure
	naiveCap = 30_000
	// Groq's free tier caps tokens per minute per model, so a rate-limited Groq call retries on another model's budget
	rateLimitWaits = 4
	// right-size routing: the small model handles the task until one of these trips, then the large one finishes it
	smallMaxSteps = 8
	// with a step limit, escalating grants the large model this many more steps; without one it runs until done
	largeSteps    = 10
	maxToolErrors = 2
	maxTestFails  = 2
)

var groqChain = []string{"groq/openai/gpt-oss-120b", "groq/qwen/qwen3.8-27b", "groq/openai/gpt-oss-20b"}

var (
	askRe    = regexp.MustCompile(`(?i)^(what|why|how|where|which|who|when|explain|describe|summari[sz]e|tell me|show me|walk me|give me an overview|is|are|does|do|can you (explain|tell|describe|summari[sz]e|show))\b`)
	changeRe = regexp.MustCompile(`(?i)\b(fix|add|change|edit|write|create|implement|refactor|rename|remove|delete|update|make|build|generate|replace|convert|install|migrate|bump|commit)\b`)
	testCmd  = regexp.MustCompile(`\b(pytest|unittest|tox|nox|jest|vitest|mocha|(npm|pnpm|yarn|cargo|go|dotnet|mvn|gradle) test)\b`)
)

// IsQuestion reports a request that only asks about the code, e.g. "what is this repo about?".
func IsQuestion(task string) bool {
	t := strings.TrimSpace(task)
	return (askRe.MatchString(t) || strings.HasSuffix(t, "?")) && !changeRe.MatchString(t)
}

type Turn struct {
	Task, Answer string
	Files        []string
}

// MemoryNote is a compact stand-in for earlier turns: question, short answer, files touched. Oldest turns drop first.
func MemoryNote(turns []Turn, limit int) string {
	var lines []string
	for _, t := range turns {
		files := ""
		if len(t.Files) > 0 {
			files = " [files: " + strings.Join(t.Files, ", ") + "]"
		}
		lines = append(lines, fmt.Sprintf("- Q: %s\n  A: %s%s", cut(t.Task, 200), cut(t.Answer, 300), files))
	}
	for len(lines) > 0 && len(strings.Join(lines, "\n")) > limit {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Event is what the UI hears while a task runs.
type Event struct {
	Kind               string // start call tool escalate wait
	Name, Args, Out    string
	Failed             bool
	Model              string
	Prompt, Completion int
	Saved              float64
	To, Reason         string
	Seconds            float64
	Sources            []web.Source
}

type Msg = map[string]any

type Agent struct {
	Lean      bool
	WS        *tools.Workspace
	Budget    *budget.Budget
	Private   bool
	Approve   func(text string) bool
	Ask       func(question string, options []string) string
	Emit      func(Event)
	Tools     []map[string]any
	Base      string
	Headers   map[string]string
	Model     string
	Large     string
	Fallbacks []string
	FP        *footprint.Footprint
	Memory    string
	// PriorNaive is the chars a naive agent would resend: the full transcript of earlier turns.
	PriorNaive   int
	TurnNaive    int
	FilesTouched []string
	Verified     string
	History      []Msg
	// Messages is the last Run's full conversation, system prompt first.
	Messages []Msg
	Client   *http.Client
	// Ctx cancels the running task: the in-flight request, rate-limit waits and shell commands.
	Ctx context.Context

	naive     map[string]int // tool_call_id -> output size without clipping or ranged reads
	stepLimit int
}

type Options struct {
	Repo, Model, Small, Memory string
	Budget                     *budget.Budget
	Private, Lean              bool
	PriorNaive                 int
	Approve                    func(string) bool
	// Ask shows a multiple-choice question to the user and returns their answer; nil disables the ask_user tool.
	Ask     func(question string, options []string) string
	Emit    func(Event)
	History []Msg
	Ctx     context.Context
}

func New(o Options) (*Agent, error) {
	hdr, err := gateway.Headers(o.Private)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		Lean: o.Lean, WS: tools.NewWorkspace(o.Repo, o.Lean), Budget: o.Budget, Private: o.Private, Approve: o.Approve,
		Ask: o.Ask, Emit: o.Emit, Base: gateway.URL(), Headers: hdr, FP: footprint.New(""), Memory: o.Memory, PriorNaive: o.PriorNaive,
		Verified: "no tests run", History: o.History, naive: map[string]int{},
		Client: &http.Client{Timeout: 180 * time.Second}, Ctx: context.Background(),
	}
	if a.Emit == nil {
		a.Emit = func(Event) {}
	}
	if a.Approve == nil {
		a.Approve = func(string) bool { return false }
	}
	// Tavily sends the query off the machine, so private runs never get it
	if o.Ctx != nil {
		a.Ctx = o.Ctx
	}
	a.WS.Ctx = a.Ctx
	a.Tools = tools.Schemas(!o.Private && os.Getenv("TAVILY_API_KEY") != "", false)
	if o.Ask != nil {
		a.Tools = append(a.Tools, tools.AskSchema())
	}
	if o.Private {
		// private repos never leave the machine: local model, no cloud fallback;
		// the private virtual key also blocks any other provider at the gateway
		a.Model = gateway.LocalModel()
		a.Large = a.Model
		return a, nil
	}
	a.Large = o.Model
	a.Model = a.Large
	if o.Small != "" {
		a.Model = o.Small
	}
	for _, m := range strings.Split(os.Getenv("SLENDERTOKEN_FALLBACKS"), ",") {
		if m = strings.TrimSpace(m); m != "" {
			a.Fallbacks = append(a.Fallbacks, m)
		}
	}
	if len(a.Fallbacks) == 0 && strings.HasPrefix(a.Large, "groq/") {
		a.Fallbacks = groqChain
	}
	return a, nil
}

func (a *Agent) onSmall() bool { return a.Model != a.Large }

func (a *Agent) escalate(step int, reason string) bool {
	if !a.onSmall() {
		return false
	}
	a.FP.Escalations = append(a.FP.Escalations, footprint.Escalation{Step: step, From: a.Model, To: a.Large, Reason: reason})
	a.Emit(Event{Kind: "escalate", To: a.Large, Reason: reason})
	a.Model = a.Large
	if a.stepLimit > 0 {
		a.stepLimit = max(a.stepLimit, step+largeSteps)
	}
	return true
}

func (a *Agent) trim(messages []Msg) []Msg {
	if !a.Lean {
		return messages
	}
	var toolIdx []int
	for i, m := range messages {
		if m["role"] == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	old := map[int]bool{}
	for _, i := range toolIdx[:max(0, len(toolIdx)-keepFull)] {
		old[i] = true
	}
	out := make([]Msg, len(messages))
	for i, m := range messages {
		out[i] = m
		if c, _ := m["content"].(string); old[i] && len(c) > elideTo {
			cp := Msg{}
			for k, v := range m {
				cp[k] = v
			}
			cp["content"] = c[:elideTo] + " [elided]"
			out[i] = cp
		}
	}
	return out
}

func (a *Agent) post(body Msg) (int, http.Header, []byte, error) {
	data, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(a.Ctx, http.MethodPost, a.Base+"/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw, nil
}

// ErrGatewayDown wraps a connection failure so the UI can say how to start Bifrost.
var ErrGatewayDown = errors.New("cannot reach Bifrost")

func (a *Agent) chat(messages []Msg, step int, retried bool) (Msg, error) {
	if err := a.Budget.Check(); err != nil {
		return nil, err
	}
	body := Msg{"model": a.Model, "messages": a.trim(messages), "tools": a.Tools, "max_tokens": 1024}
	if a.Lean && strings.Contains(a.Model, "gpt-oss") {
		body["reasoning_effort"] = "low" // hidden reasoning is billed as output tokens and counts against the minute cap
	}
	var fb []string
	for _, m := range a.Fallbacks {
		if m != a.Model {
			fb = append(fb, m)
		}
	}
	if len(fb) > 0 {
		body["fallbacks"] = fb
	}
	var status int
	var hdr http.Header
	var raw []byte
	var latency float64
	for range rateLimitWaits + 1 {
		t0 := time.Now()
		var err error
		status, hdr, raw, err = a.post(body)
		if err != nil && a.Ctx.Err() != nil {
			return nil, a.Ctx.Err()
		}
		if err != nil {
			return nil, fmt.Errorf("%w at %s. Start it with: npx -y @maximhq/bifrost (%v)", ErrGatewayDown, a.Base, err)
		}
		latency = time.Since(t0).Seconds()
		if status != 429 || bytes.Contains(raw, []byte("budget_exceeded")) {
			break
		}
		wait := 15.0
		if ms, err := strconv.ParseFloat(hdr.Get("x-bifrost-retry-after-ms"), 64); err == nil {
			wait = ms / 1000
		}
		wait = math.Min(60, wait)
		a.Emit(Event{Kind: "wait", Seconds: wait})
		a.FP.RateLimitWaitS += wait
		select {
		case <-time.After(time.Duration(wait * float64(time.Second))):
		case <-a.Ctx.Done():
			return nil, a.Ctx.Err()
		}
	}
	if status >= 400 && bytes.Contains(raw, []byte("budget_exceeded")) {
		return nil, fmt.Errorf("%w: Bifrost virtual key budget used up", budget.ErrExceeded)
	}
	// Groq answers 400 when a model emits a malformed tool call: escalate, or resample once on the large model
	if status == 400 {
		if a.escalate(step, "provider rejected small model output") {
			return a.chat(messages, step, false)
		}
		if !retried {
			return a.chat(messages, step, true)
		}
		if bytes.Contains(raw, []byte("tool_use_failed")) {
			// show the model its schema error instead of failing the task
			var e struct{ Error struct{ Message string } }
			_ = json.Unmarshal(raw, &e)
			msg := cut(e.Error.Message, 300)
			if msg == "" {
				msg = "invalid tool call"
			}
			return Msg{"role": "user", "content": "Your tool call was rejected: " + msg + ". Retry with valid arguments."}, nil
		}
	}
	if status >= 400 {
		return nil, fmt.Errorf("Bifrost %d: %s", status, cut(string(raw), 400))
	}
	var data struct {
		Model string
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		}
		ExtraFields struct {
			ProviderResponseHeaders map[string]string `json:"provider_response_headers"`
		} `json:"extra_fields"`
		Choices []struct{ Message Msg }
	}
	if err := json.Unmarshal(raw, &data); err != nil || len(data.Choices) == 0 {
		return nil, fmt.Errorf("Bifrost returned an unreadable reply: %s", cut(string(raw), 200))
	}
	pt, ct := data.Usage.PromptTokens, data.Usage.CompletionTokens
	saved := a.savedPromptTokens(body, pt)
	a.FP.SavedPrompt += saved
	model := data.Model
	if model == "" {
		model = a.Model
	}
	a.Budget.Charge(model, pt, ct, a.Private)
	a.FP.Add(model, pt, ct, latency, a.Private, data.ExtraFields.ProviderResponseHeaders["X-Groq-Region"])
	a.Emit(Event{Kind: "call", Model: model, Prompt: pt, Completion: ct, Saved: saved})
	if hdr.Get("x-bifrost-routing-info-is-fallback") == "true" {
		a.FP.Fallbacks++
	}
	msg := data.Choices[0].Message
	// extras like "reasoning" would only cost tokens on the next call
	content, _ := msg["content"].(string)
	clean := Msg{"role": "assistant", "content": content}
	if tc, ok := msg["tool_calls"]; ok && tc != nil {
		clean["tool_calls"] = tc
	}
	return clean, nil
}

// savedPromptTokens: prompt tokens a naive agent would have added to this request: whole-file reads, unclipped output,
// no history trimming. Sizes are measured; they become tokens at this request's real tokens-per-character ratio.
func (a *Agent) savedPromptTokens(body Msg, promptTokens int) float64 {
	msgs := body["messages"].([]Msg)
	var toolMsgs []Msg
	for _, m := range msgs {
		if m["role"] == "tool" {
			toolMsgs = append(toolMsgs, m)
		}
	}
	mj, _ := json.Marshal(msgs)
	tj, _ := json.Marshal(body["tools"])
	sent := len(mj) + len(tj)
	history := max(0, a.PriorNaive-len(a.Memory))
	if sent == 0 || (len(toolMsgs) == 0 && history == 0) {
		return 0
	}
	extra := history
	for _, m := range toolMsgs {
		content, _ := m["content"].(string)
		n, ok := a.naive[fmt.Sprint(m["tool_call_id"])]
		if !ok {
			n = len(content)
		}
		extra += max(0, n-len(content))
	}
	return float64(extra) * float64(promptTokens) / float64(sent)
}

type toolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func parseCalls(msg Msg) []toolCall {
	raw, _ := json.Marshal(msg["tool_calls"])
	var calls []toolCall
	_ = json.Unmarshal(raw, &calls)
	return calls
}

// runTool returns the tool output and whether the call failed.
func (a *Agent) runTool(call toolCall) (string, bool, []web.Source) {
	name := call.Function.Name
	a.WS.Naive = -1
	known := map[string]bool{"ask_user": a.Ask != nil, "read_file": true, "search": true, "list_files": true, "edit_file": true, "write_file": true, "run_shell": true, "web_search": true}
	if !known[name] {
		return "error: unknown tool " + name, true, nil
	}
	args := map[string]any{}
	if s := strings.TrimSpace(call.Function.Arguments); s != "" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return "error: bad arguments json: " + err.Error(), true, nil
		}
	}
	a.Emit(Event{Kind: "start", Name: name, Args: call.Function.Arguments})
	if name == "ask_user" {
		var q struct {
			Question string
			Options  []string
		}
		_ = json.Unmarshal([]byte(call.Function.Arguments), &q)
		if strings.TrimSpace(q.Question) == "" || len(q.Options) < 2 {
			return "error: ask_user needs a question and 2 to 4 options", true, nil
		}
		return a.Ask(q.Question, q.Options[:min(len(q.Options), 4)]), false, nil
	}
	if name == "run_shell" {
		cmd := tools.ShellCmd(args)
		if a.Private {
			if err := tools.NetworkCommand(cmd); err != "" {
				return err, true, nil
			}
		}
		if a.Lean {
			if err := tools.Unrunnable(cmd, func(p string) bool { _, err := exec.LookPath(p); return err == nil }); err != "" {
				return err, true, nil
			}
		}
	}
	if tools.NeedsApproval(name, args) && !a.Approve(tools.Describe(name, args)) {
		return "denied by user", false, nil
	}
	out, err := a.WS.Call(name, args)
	if err != nil {
		return "error: " + err.Error(), true, nil
	}
	return out, strings.HasPrefix(out, "error:"), a.WS.Sources
}

func (a *Agent) Note(result string) Turn {
	return Turn{Task: a.FP.Task, Answer: result, Files: a.FilesTouched}
}

// Run executes one task and returns the model's final answer. maxSteps 0 means no step limit: the task runs until
// the model answers, the budget runs out or Ctx is cancelled.
func (a *Agent) Run(task string, maxSteps int) (string, error) {
	a.FP.Task = task
	ask := a.Lean && IsQuestion(task)
	if ask {
		hasWeb := false
		for _, t := range a.Tools {
			hasWeb = hasWeb || t["function"].(map[string]any)["name"] == "web_search"
		}
		a.Tools = tools.Schemas(hasWeb, true)
	}
	prompt := task
	if a.Memory != "" {
		prompt = "Earlier in this session:\n" + a.Memory + "\n\nTask: " + task
	}
	sys := systemNaive
	if a.Lean {
		sys = system
		if ask {
			sys = systemAsk
		}
		if a.Ask != nil && !ask {
			sys += " If the task is ambiguous, call ask_user with 2-4 short options instead of guessing."
		}
	}
	messages := append([]Msg{{"role": "system", "content": sys}}, a.History...)
	messages = append(messages, Msg{"role": "user", "content": prompt})
	defer func() {
		a.Messages = messages
		total := 0
		for _, m := range messages[1+len(a.History):] {
			if m["role"] == "tool" {
				content, _ := m["content"].(string)
				n, ok := a.naive[fmt.Sprint(m["tool_call_id"])]
				if !ok {
					n = len(content)
				}
				total += n
			} else {
				j, _ := json.Marshal(m)
				total += len(j)
			}
		}
		a.TurnNaive = total - (len(prompt) - len(task))
	}()
	return a.loop(&messages, maxSteps)
}

func (a *Agent) loop(messages *[]Msg, maxSteps int) (string, error) {
	toolErrors, testFails := 0, 0
	lastError := ""
	edited := false
	a.stepLimit = maxSteps
	for step := 1; a.stepLimit <= 0 || step <= a.stepLimit; step++ {
		if a.onSmall() && step > smallMaxSteps {
			a.escalate(step, fmt.Sprintf("small model not done after %d steps", smallMaxSteps))
		}
		if err := a.Ctx.Err(); err != nil {
			return "", err
		}
		msg, err := a.chat(*messages, step, false)
		if err != nil {
			return "", err
		}
		*messages = append(*messages, msg)
		if msg["role"] == "user" {
			toolErrors++
			continue
		}
		calls := parseCalls(msg)
		if len(calls) == 0 {
			// a small model claiming success over failing tests gets a second opinion
			if a.Verified == "tests failing" && a.escalate(step, "finished with tests failing") {
				*messages = append(*messages, Msg{"role": "user", "content": "Tests still fail. Fix them."})
				continue
			}
			content, _ := msg["content"].(string)
			return content, nil
		}
		for _, call := range calls {
			out, failed, sources := a.runTool(call)
			name := call.Function.Name
			if (name == "edit_file" || name == "write_file") && !failed {
				edited = true
				var args struct{ Path string }
				_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
				if args.Path != "" && !contains(a.FilesTouched, args.Path) {
					a.FilesTouched = append(a.FilesTouched, args.Path)
				}
			}
			if name == "run_shell" && strings.HasPrefix(out, "exit=") && testCmd.MatchString(call.Function.Arguments) {
				ok := strings.HasPrefix(out, "exit=0\n") || out == "exit=0"
				a.Verified = map[bool]string{true: "tests passing", false: "tests failing"}[ok]
				if edited && !ok {
					testFails++
				}
			}
			if failed {
				toolErrors++
				lastError = cut(out, 120)
			} else {
				toolErrors, lastError = 0, ""
			}
			naive := len(out)
			if a.WS.Naive >= 0 {
				naive = a.WS.Naive
			}
			a.naive[call.ID] = min(naiveCap, naive)
			*messages = append(*messages, Msg{"role": "tool", "tool_call_id": call.ID, "content": out})
			a.Emit(Event{Kind: "tool", Name: name, Args: call.Function.Arguments, Out: out, Failed: failed, Sources: sources})
			a.FP.Trace = append(a.FP.Trace, footprint.TraceEntry{Step: step, Model: a.Model, Tool: name, Args: cut(call.Function.Arguments, 120), Out: cut(out, 120)})
		}
		if toolErrors >= maxToolErrors {
			a.escalate(step, fmt.Sprintf("%d tool errors in a row (%s)", toolErrors, lastError))
		}
		if testFails >= maxTestFails {
			a.escalate(step, fmt.Sprintf("tests failed %d times after edits", testFails))
		}
	}
	return "step limit reached", nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
