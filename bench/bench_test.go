package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func needGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
}

func TestCleanShopPassesAndEveryBugFails(t *testing.T) {
	needGo(t)
	root, err := build(filepath.Join(t.TempDir(), "clean"), "")
	if err != nil {
		t.Fatal(err)
	}
	vet := exec.Command("go", "vet", "./...")
	vet.Dir = root
	if out, err := vet.CombinedOutput(); err != nil {
		t.Fatalf("clean shop does not vet:\n%s", out)
	}
	if !passes(root) {
		t.Fatal("clean shop fails its own tests")
	}
	for _, b := range bugs {
		t.Run(b.name, func(t *testing.T) {
			root, err := build(filepath.Join(t.TempDir(), b.name), b.name)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(root, b.file))
			if !strings.Contains(string(data), b.buggy) {
				t.Fatalf("%s does not contain the seeded bug", b.file)
			}
			if passes(root) {
				t.Fatalf("bug %s does not break any test", b.name)
			}
		})
	}
}

func TestBuildIsDeterministicAndRejectsUnknownBug(t *testing.T) {
	a, _ := build(filepath.Join(t.TempDir(), "a"), "cart")
	b, _ := build(filepath.Join(t.TempDir(), "b"), "cart")
	entries, _ := os.ReadDir(a)
	if len(entries) != 1+len(modules)+len(tests) {
		t.Fatalf("got %d files", len(entries))
	}
	for _, e := range entries {
		x, _ := os.ReadFile(filepath.Join(a, e.Name()))
		y, _ := os.ReadFile(filepath.Join(b, e.Name()))
		if !bytes.Equal(x, y) {
			t.Fatalf("%s differs between builds", e.Name())
		}
	}
	if _, err := build(t.TempDir(), "nope"); err == nil {
		t.Fatal("unknown bug accepted")
	}
}

func TestAnswered(t *testing.T) {
	q := sessionQuestions[0]
	if !answered("It is ApplyDiscount in pricing.go", q.expect) {
		t.Fatal("correct answer rejected")
	}
	if answered("pricing.go", q.expect) {
		t.Fatal("half answer accepted")
	}
}

// gateway is a fake Bifrost: each request gets the reply its handler picks, with fixed usage.
type gateway struct {
	mu       sync.Mutex
	requests []map[string]any
	reply    func(req map[string]any) map[string]any
	status   int
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	g.mu.Lock()
	g.requests = append(g.requests, req)
	g.mu.Unlock()
	w.Header().Set("content-type", "application/json")
	if g.status != 0 {
		w.WriteHeader(g.status)
		io.WriteString(w, `{"error":{"message":"Request too large"}}`)
		return
	}
	msg := g.reply(req)
	_ = json.NewEncoder(w).Encode(map[string]any{"model": req["model"], "choices": []any{map[string]any{"message": msg}},
		"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 50}})
}

func toolCall(name string, args any) map[string]any {
	a, _ := json.Marshal(args)
	return map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": name + "1", "type": "function",
		"function": map[string]any{"name": name, "arguments": string(a)}}}}
}

func messages(req map[string]any) []map[string]any {
	var out []map[string]any
	raw, _ := json.Marshal(req["messages"])
	_ = json.Unmarshal(raw, &out)
	return out
}

func fakeGateway(t *testing.T, g *gateway) runner {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	t.Setenv("BIFROST_URL", srv.URL)
	t.Setenv("BIFROST_VK", "k")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("SLENDERTOKEN_FALLBACKS", "")
	// keep `run`'s config loading away from the real home folder
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return runner{model: "groq/openai/gpt-oss-120b", steps: 15, ctx: context.Background()}
}

// fixer edits the seeded bug back to the clean text, then reports done.
func fixer(b bug) func(map[string]any) map[string]any {
	return func(req map[string]any) map[string]any {
		if last := messages(req); last[len(last)-1]["role"] == "tool" {
			return map[string]any{"role": "assistant", "content": "fixed"}
		}
		return toolCall("edit_file", map[string]string{"path": b.file, "old": b.buggy, "new": b.clean})
	}
}

func TestCodingRunFixesAndMeasures(t *testing.T) {
	needGo(t)
	for _, lean := range []bool{true, false} {
		b, _ := findBug("restock")
		g := &gateway{reply: fixer(b)}
		r := fakeGateway(t, g)
		got, err := r.coding("restock", lean)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Passed || got.Error != "" || got.Answer != "fixed" {
			t.Fatalf("lean=%v: %+v", lean, got)
		}
		if got.Calls != 2 || got.In != 2000 || got.Out != 100 || got.Tokens != 2100 {
			t.Fatalf("lean=%v: usage not summed: %+v", lean, got)
		}
		if len(got.Models) != 1 || got.Models[0] != r.model {
			t.Fatalf("models %v", got.Models)
		}
	}
}

func TestCodingRunWithoutFixFails(t *testing.T) {
	needGo(t)
	g := &gateway{reply: func(map[string]any) map[string]any { return map[string]any{"role": "assistant", "content": "done"} }}
	r := fakeGateway(t, g)
	got, err := r.coding("month", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Passed {
		t.Fatal("unfixed bug reported as passing")
	}
}

// answerer answers each question with the words it expects, so every turn passes.
func answerer(req map[string]any) map[string]any {
	msgs := messages(req)
	q, _ := msgs[len(msgs)-1]["content"].(string)
	for _, sq := range sessionQuestions {
		if strings.HasSuffix(q, sq.text) {
			var words []string
			for _, g := range sq.expect {
				words = append(words, g[0])
			}
			return map[string]any{"role": "assistant", "content": strings.Join(words, " ")}
		}
	}
	return map[string]any{"role": "assistant", "content": "?"}
}

func TestSessionArms(t *testing.T) {
	for _, lean := range []bool{true, false} {
		g := &gateway{reply: answerer}
		r := fakeGateway(t, g)
		rows, err := r.session(lean)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(sessionQuestions) {
			t.Fatalf("lean=%v: %d turns", lean, len(rows))
		}
		for i, row := range rows {
			if !row.Passed || row.Question != sessionQuestions[i].text {
				t.Fatalf("lean=%v turn %d: %+v", lean, i+1, row)
			}
		}
		last := messages(g.requests[len(g.requests)-1])
		var sys, user int
		for _, m := range last {
			switch m["role"] {
			case "system":
				sys++
			case "user":
				user++
			}
		}
		if sys != 1 {
			t.Fatalf("lean=%v: %d system prompts in the last request", lean, sys)
		}
		if lean {
			// one user message carrying a memory note of earlier turns
			c, _ := last[len(last)-1]["content"].(string)
			if user != 1 || !strings.Contains(c, "Earlier in this session") || !strings.Contains(c, sessionQuestions[len(sessionQuestions)-2].text) {
				t.Fatalf("lean arm did not carry the memory note: %d users, %q", user, c)
			}
		} else if user != len(sessionQuestions) || len(last) != 1+2*len(sessionQuestions)-1 {
			// the naive arm resends every earlier question and answer
			t.Fatalf("naive arm did not resend the transcript: %d users, %d messages", user, len(last))
		}
	}
}

func TestSessionStopsAtFirstError(t *testing.T) {
	g := &gateway{status: 413}
	r := fakeGateway(t, g)
	rows, err := r.session(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Passed || !strings.Contains(rows[0].Error, "413") {
		t.Fatalf("%+v", rows)
	}
}

func TestRunWritesResultsAndChart(t *testing.T) {
	needGo(t)
	g := &gateway{reply: func(req map[string]any) map[string]any {
		msgs := messages(req)
		if strings.Contains(msgs[1]["content"].(string), "tests in this project") || len(msgs) > 2 && msgs[len(msgs)-1]["role"] == "tool" {
			return fixer(bugs[0])(req)
		}
		return answerer(req)
	}}
	fakeGateway(t, g)
	dir := t.TempDir()
	out := filepath.Join(dir, "results.json")
	var log bytes.Buffer
	if err := run([]string{"run", "--pause", "0", "--bugs", "discount", "--out", out}, &log); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[coding/lean] discount", "[coding/naive] discount", "[session/lean] turn 8", "[session/naive] turn 8", "saved"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("log missing %q:\n%s", want, log.String())
		}
	}
	res, err := readResults(out)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Coding["lean"]["discount"].Passed || !res.Coding["naive"]["discount"].Passed || len(res.Session["naive"]) != 8 {
		t.Fatalf("%+v", res)
	}
	svg := filepath.Join(dir, "c.svg")
	if err := run([]string{"chart", "--in", out, "--out", svg}, io.Discard); err != nil {
		t.Fatal(err)
	}
	checkSVG(t, svg, "fixed 1 of 1, naive 1 of 1")
}

func TestRunRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{{"run", "--only", "x"}, {"run", "--bugs", "nope"}, {"run", "--arms", "fast"}, {"nope"}} {
		if err := run(args, io.Discard); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func checkSVG(t *testing.T, path string, want ...string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := d.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("invalid SVG: %v", err)
		}
	}
	for _, w := range want {
		if !strings.Contains(string(data), w) {
			t.Fatalf("SVG missing %q", w)
		}
	}
	if strings.Contains(string(data), "NaN") || strings.Contains(string(data), "Inf") {
		t.Fatal("SVG has a NaN or Inf coordinate")
	}
	return string(data)
}

func TestChartOfCommittedResults(t *testing.T) {
	r, err := readResults("results.json")
	if err != nil {
		t.Fatal(err)
	}
	svg, err := chart(r, "bench/results.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "c.svg")
	os.WriteFile(path, []byte(svg), 0o644)
	checkSVG(t, path, r.Model, "Coding total")
}

func TestChartWhenNaiveStopsEarly(t *testing.T) {
	r := results{Model: "m", Coding: map[string]map[string]row{
		"lean":  {"discount": {Tokens: 5000, Passed: true}},
		"naive": {"discount": {Tokens: 20000, Passed: false}},
	}, Session: map[string][]row{}}
	for i := range 8 {
		r.Session["lean"] = append(r.Session["lean"], row{Tokens: 1000 + i})
	}
	r.Session["naive"] = []row{{Tokens: 5000}, {Tokens: 9000}, {Tokens: 40000, Error: "Bifrost 413: too large"}}
	svg, err := chart(r, "x")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "c.svg")
	os.WriteFile(path, []byte(svg), 0o644)
	checkSVG(t, path, "naive stopped", "request too large (413)", "failed at turn 3", "fixed 1 of 1, naive 0 of 1", "75% fewer", colFail)
}

func TestChartNeedsBothArms(t *testing.T) {
	if _, err := chart(results{Coding: map[string]map[string]row{"lean": {"cart": {}}}}, "x"); err == nil {
		t.Fatal("chart of one arm accepted")
	}
}

func TestScaleAndCommas(t *testing.T) {
	for _, v := range []int{1, 999, 9287, 45000, 60000, 1234567} {
		top, step := scale(v)
		if float64(top) < float64(v)*1.15 || top%step != 0 || top/step > 7 {
			t.Fatalf("scale(%d) = %d, %d", v, top, step)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := commas(n); got != want {
			t.Fatalf("commas(%d) = %q", n, got)
		}
	}
}

func TestInterruptStopsBetweenRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := runner{pause: time.Hour, ctx: ctx}
	cancel()
	done := make(chan struct{})
	go func() { r.sleep(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sleep ignored cancellation")
	}
}
