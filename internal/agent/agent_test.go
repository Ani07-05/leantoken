package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Ani07-05/slendertoken/internal/budget"
)

type script struct {
	mu       sync.Mutex
	models   []string
	replies  []func(req map[string]any) (int, any)
	requests []map[string]any
}

func (s *script) handler(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	i := len(s.requests)
	s.requests = append(s.requests, req)
	s.models = append(s.models, req["model"].(string))
	s.mu.Unlock()
	code, body := s.replies[min(i, len(s.replies)-1)](req)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func reply(msg map[string]any) func(map[string]any) (int, any) {
	return func(req map[string]any) (int, any) {
		return 200, map[string]any{"model": req["model"], "choices": []any{map[string]any{"message": msg}},
			"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 50}}
	}
}

func callMsg(id, name, args string) map[string]any {
	return map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
		map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}
}

func setup(t *testing.T, s *script) (Options, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	t.Setenv("BIFROST_URL", srv.URL)
	t.Setenv("BIFROST_VK", "k")
	t.Setenv("TAVILY_API_KEY", "")
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Options{Repo: repo, Model: "groq/openai/gpt-oss-120b", Small: "groq/openai/gpt-oss-20b", Lean: true,
		Budget: budget.New(filepath.Join(repo, "b.json"), 5), Approve: func(string) bool { return true }}, repo
}

func TestRunsToolsThenAnswers(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){
		reply(callMsg("1", "list_files", `{}`)),
		reply(map[string]any{"role": "assistant", "content": "done"}),
	}}
	o, _ := setup(t, s)
	ag, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	ag.Emit = func(e Event) { kinds = append(kinds, e.Kind) }
	got, err := ag.Run("what is here", 15)
	if err != nil || got != "done" {
		t.Fatalf("got %q, %v", got, err)
	}
	if strings.Join(kinds, ",") != "start,call,tool,call" && !strings.Contains(strings.Join(kinds, ","), "tool") {
		t.Fatalf("events: %v", kinds)
	}
	if ag.FP.Totals().Calls != 2 {
		t.Fatalf("calls = %d", ag.FP.Totals().Calls)
	}
}

func TestQuestionGetsNoWriteTools(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){reply(map[string]any{"role": "assistant", "content": "ok"})}}
	o, _ := setup(t, s)
	ag, _ := New(o)
	if _, err := ag.Run("what does this repo do?", 15); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s.requests[0]["tools"])
	if strings.Contains(string(raw), "edit_file") {
		t.Fatal("a question must not be offered edit_file")
	}
}

func TestEscalatesWhenProviderRejectsSmallModel(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){
		func(map[string]any) (int, any) { return 400, map[string]any{"error": map[string]any{"message": "bad"}} },
		reply(map[string]any{"role": "assistant", "content": "ok"}),
	}}
	o, _ := setup(t, s)
	ag, _ := New(o)
	if _, err := ag.Run("fix it", 15); err != nil {
		t.Fatal(err)
	}
	if s.models[0] != "groq/openai/gpt-oss-20b" || s.models[1] != "groq/openai/gpt-oss-120b" {
		t.Fatalf("models: %v", s.models)
	}
	if len(ag.FP.Escalations) != 1 {
		t.Fatalf("escalations: %v", ag.FP.Escalations)
	}
}

func TestDeniedEditLeavesFileAlone(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){
		reply(callMsg("1", "edit_file", `{"path":"a.py","old":"x = 1","new":"x = 2"}`)),
		reply(map[string]any{"role": "assistant", "content": "ok"}),
	}}
	o, repo := setup(t, s)
	o.Approve = func(string) bool { return false }
	ag, _ := New(o)
	if _, err := ag.Run("change x", 15); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "a.py"))
	if string(data) != "x = 1\n" {
		t.Fatalf("file changed: %q", data)
	}
}

func TestBudgetStopsTheTask(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){reply(map[string]any{"role": "assistant", "content": "ok"})}}
	o, repo := setup(t, s)
	o.Budget = budget.New(filepath.Join(repo, "b2.json"), 0)
	ag, _ := New(o)
	if _, err := ag.Run("fix it", 15); err == nil {
		t.Fatal("want budget error")
	}
}

func TestIsQuestion(t *testing.T) {
	for q, want := range map[string]bool{"what is this?": true, "how does auth work": true, "fix the test": false, "explain and fix it": false, "add a flag": false} {
		if IsQuestion(q) != want {
			t.Errorf("%q: want %v", q, want)
		}
	}
}

func TestMemoryNoteDropsOldestFirst(t *testing.T) {
	turns := []Turn{{Task: "first", Answer: strings.Repeat("a", 300)}, {Task: "second", Answer: "b"}}
	got := MemoryNote(turns, 100)
	if strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("got %q", got)
	}
}

func TestAskUserToolReturnsTheChosenOption(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){
		reply(callMsg("1", "ask_user", `{"question":"Which?","options":["A","B"]}`)),
		reply(map[string]any{"role": "assistant", "content": "ok"}),
	}}
	o, _ := setup(t, s)
	var asked []string
	o.Ask = func(q string, opts []string) string { asked = append(asked, q+"|"+strings.Join(opts, ",")); return "B" }
	ag, _ := New(o)
	if _, err := ag.Run("change the thing", 15); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "Which?|A,B" {
		t.Fatalf("asked: %v", asked)
	}
	last := s.requests[1]["messages"].([]any)
	tool := last[len(last)-1].(map[string]any)
	if tool["role"] != "tool" || tool["content"] != "B" {
		t.Fatalf("tool message: %v", tool)
	}
}

func TestAskUserIsOfferedOnlyWhenTheUICanAsk(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){reply(map[string]any{"role": "assistant", "content": "ok"})}}
	o, _ := setup(t, s)
	ag, _ := New(o)
	raw, _ := json.Marshal(ag.Tools)
	if strings.Contains(string(raw), "ask_user") {
		t.Fatal("ask_user offered without an Ask handler")
	}
	o.Ask = func(string, []string) string { return "" }
	ag, _ = New(o)
	raw, _ = json.Marshal(ag.Tools)
	if !strings.Contains(string(raw), "ask_user") {
		t.Fatal("ask_user missing with an Ask handler")
	}
}

func TestModelsAreUsedAsGivenAndOnlyGroqGetsTheFallbackChain(t *testing.T) {
	s := &script{replies: []func(map[string]any) (int, any){reply(map[string]any{"role": "assistant", "content": "ok"})}}
	o, _ := setup(t, s)
	o.Model, o.Small = "anthropic/claude-sonnet-5-5", "anthropic/claude-haiku-4-5-20251001"
	ag, _ := New(o)
	if ag.Model != "anthropic/claude-haiku-4-5-20251001" || ag.Large != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("models: %s %s", ag.Model, ag.Large)
	}
	if len(ag.Fallbacks) != 0 {
		t.Fatalf("non-Groq models must not inherit Groq fallbacks: %v", ag.Fallbacks)
	}
	if _, err := ag.Run("explain this?", 15); err != nil {
		t.Fatal(err)
	}
	if s.models[0] != "anthropic/claude-haiku-4-5-20251001" {
		t.Fatalf("sent to %s", s.models[0])
	}
	if _, hasFallbacks := s.requests[0]["fallbacks"]; hasFallbacks {
		t.Fatal("request carried fallbacks")
	}
}
