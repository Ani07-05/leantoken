package main

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Ani07-05/slendertoken/internal/agent"
	"github.com/Ani07-05/slendertoken/internal/budget"
)

const codingPrompt = "The tests in this project are failing. Run them, find the bug and fix it."

type question struct {
	text string
	// every group must appear (any word within a group) in the lowercased answer
	expect [][]string
}

var sessionQuestions = []question{
	{"Which file computes discounts, and which function?", [][]string{{"pricing"}, {"applydiscount"}}},
	{"What does that function return for a price of 100 and a discount of 0.2?", [][]string{{"80"}}},
	{"Where is the tax rate defined and what is it?", [][]string{{"0.18", "18%"}}},
	{"How does the cart compute its total?", [][]string{{"qty", "quantity"}}},
	{"When does the inventory say an item needs restocking?", [][]string{{"threshold"}}},
	{"Which file groups orders by month?", [][]string{{"reports"}}},
	{"What format are the keys of the monthly summary?", [][]string{{"year"}, {"month"}}},
	{"Summarise what you told me about pricing in two bullets.", [][]string{{"pricing"}, {"discount", "tax"}}},
}

type row struct {
	Tokens   int      `json:"tokens"`
	In       int      `json:"in"`
	Out      int      `json:"out"`
	Calls    int      `json:"calls"`
	CostUSD  float64  `json:"cost_usd"`
	Seconds  float64  `json:"seconds"`
	Models   []string `json:"models"`
	Error    string   `json:"error"`
	Answer   string   `json:"answer"`
	Question string   `json:"question,omitempty"`
	Passed   bool     `json:"passed"`
}

type results struct {
	Model   string                    `json:"model"`
	Coding  map[string]map[string]row `json:"coding"`
	Session map[string][]row          `json:"session"`
}

type runner struct {
	model string
	pause time.Duration
	steps int // 0: no limit
	ctx   context.Context
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// passes runs the shop's own tests.
func passes(repo string) bool {
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = repo
	return cmd.Run() == nil
}

func answered(answer string, expect [][]string) bool {
	low := strings.ToLower(answer)
	for _, group := range expect {
		hit := false
		for _, w := range group {
			hit = hit || strings.Contains(low, w)
		}
		if !hit {
			return false
		}
	}
	return true
}

func measure(ag *agent.Agent, started time.Time, result string, err error) row {
	t := ag.FP.Totals()
	models := make([]string, 0, len(t.ByModel))
	for m := range t.ByModel {
		models = append(models, m)
	}
	sort.Strings(models)
	r := row{Tokens: t.In + t.Out, In: t.In, Out: t.Out, Calls: t.Calls, CostUSD: math.Round(t.CostUSD*1e5) / 1e5,
		Seconds: math.Round(time.Since(started).Seconds()*10) / 10, Models: models, Answer: cut(result, 300)}
	if err != nil {
		r.Error = cut(err.Error(), 200)
	}
	return r
}

func (r runner) newAgent(repo string, lean bool, o agent.Options) (*agent.Agent, error) {
	dir, err := os.MkdirTemp("", "bench-budget")
	if err != nil {
		return nil, err
	}
	o.Repo, o.Model, o.Small, o.Lean, o.Ctx = repo, r.model, r.model, lean, r.ctx
	o.Budget = budget.New(filepath.Join(dir, "b.json"), 50)
	o.Approve = func(string) bool { return true }
	return agent.New(o)
}

func (r runner) coding(bugName string, lean bool) (row, error) {
	dir, err := os.MkdirTemp("", "bench-shop")
	if err != nil {
		return row{}, err
	}
	defer os.RemoveAll(dir)
	repo, err := build(filepath.Join(dir, "shop"), bugName)
	if err != nil {
		return row{}, err
	}
	ag, err := r.newAgent(repo, lean, agent.Options{})
	if err != nil {
		return row{}, err
	}
	started := time.Now()
	result, runErr := ag.Run(codingPrompt, r.steps)
	out := measure(ag, started, result, runErr)
	out.Passed = passes(repo)
	return out, nil
}

// session asks the follow-up questions in order. The lean arm carries a short memory note between turns;
// the naive arm resends the whole transcript. It stops at the first error, as a user would.
func (r runner) session(lean bool) ([]row, error) {
	dir, err := os.MkdirTemp("", "bench-shop")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	repo, err := build(filepath.Join(dir, "shop"), "")
	if err != nil {
		return nil, err
	}
	var turns []agent.Turn
	var history []agent.Msg
	prior := 0
	var rows []row
	for i, q := range sessionQuestions {
		o := agent.Options{History: history}
		if lean {
			o = agent.Options{Memory: agent.MemoryNote(turns, 1800), PriorNaive: prior}
		}
		ag, err := r.newAgent(repo, lean, o)
		if err != nil {
			return rows, err
		}
		started := time.Now()
		result, runErr := ag.Run(q.text, r.steps)
		out := measure(ag, started, result, runErr)
		out.Question = q.text
		out.Passed = runErr == nil && answered(result, q.expect)
		rows = append(rows, out)
		if runErr != nil {
			break
		}
		turns = append(turns, ag.Note(result))
		prior += ag.TurnNaive
		if !lean {
			history = ag.Messages[1:]
		}
		if i < len(sessionQuestions)-1 {
			r.sleep()
		}
	}
	return rows, nil
}

func (r runner) sleep() {
	select {
	case <-time.After(r.pause):
	case <-r.ctx.Done():
	}
}
