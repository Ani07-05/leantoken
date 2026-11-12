// Package footprint measures one task: tokens, cost, latency, energy (kWh) and carbon (gCO2e).
//
// Energy follows the EcoLogits LLM inference method (ecologits.ai/latest/methodology/llm_inference,
// constants from mlco2/ecologits impacts/llm.py), simplified:
//
//	E = PUE * [ dT/3600 * 1.2kW * GPUs/8 / B  +  GPUs * T_out * (a*e^(b*B)*P_active + c) / 1000 ]
//
// EcoLogits models H100 servers, not Groq LPUs, and ignores input tokens for GPU energy; Groq is not an
// EcoLogits provider. Treat the numbers as comparable estimates, not measurements.
package footprint

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ani07-05/slendertoken/internal/budget"
)

const (
	a, bExp, c    = 1.1665273170451914e-06, -0.011205921025579175, 4.052928146734005e-05
	batch         = 64
	serverKW      = 1.2
	gpusPerServer = 8
	gpuGB         = 80
	PUE           = 1.2
)

// gCO2e/kWh, Our World in Data 2023; override with SLENDERTOKEN_GRID_G.
var (
	gridGPerKWh = map[string]float64{"IN": 713, "US": 369}
	regionGrid  = map[string]string{"bom": "IN"}
	params      = map[string][2]float64{"openai/gpt-oss-120b": {117, 5.1}, "openai/gpt-oss-20b": {21, 3.6}}
	sizeRe      = regexp.MustCompile(`(\d+(?:\.\d+)?)b\b`)
)

func modelParams(model string) (total, active float64) {
	m := model
	if p, ok := params[m]; ok {
		return p[0], p[1]
	}
	if _, rest, ok := strings.Cut(model, "/"); ok {
		m = rest
		if p, ok := params[m]; ok {
			return p[0], p[1]
		}
	}
	p := 70.0
	if all := sizeRe.FindAllStringSubmatch(strings.ToLower(m), -1); len(all) > 0 {
		p, _ = strconv.ParseFloat(all[len(all)-1][1], 64)
	}
	return p, p
}

func gpus(totalB float64) int {
	memGB := 1.2 * totalB * 16 / 8
	need := math.Max(1, math.Ceil(memGB/gpuGB))
	return int(math.Pow(2, math.Ceil(math.Log2(need))))
}

func EnergyKWh(model string, outTokens int, latencyS float64) float64 {
	total, active := modelParams(model)
	n := float64(gpus(total))
	gpu := float64(outTokens) * (a*math.Exp(bExp*batch)*active + c) / 1000
	server := latencyS / 3600 * serverKW * n / gpusPerServer / batch
	return PUE * (server + n*gpu)
}

func Grid(region string) float64 {
	if o := os.Getenv("SLENDERTOKEN_GRID_G"); o != "" {
		if v, err := strconv.ParseFloat(o, 64); err == nil {
			return v
		}
	}
	key, ok := regionGrid[region]
	if !ok {
		key = "US"
	}
	return gridGPerKWh[key]
}

type Call struct {
	Model      string  `json:"model"`
	Prompt     int     `json:"prompt"`
	Completion int     `json:"completion"`
	LatencyS   float64 `json:"latency_s"`
	Cost       float64 `json:"cost"`
	KWh        float64 `json:"kwh"`
	Region     string  `json:"region"`
	GCO2e      float64 `json:"gco2e"`
	Local      bool    `json:"local"`
}

type Escalation struct {
	Step   int    `json:"step"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

type TraceEntry struct {
	Step  int    `json:"step"`
	Model string `json:"model"`
	Tool  string `json:"tool"`
	Args  string `json:"args"`
	Out   string `json:"out"`
}

type Footprint struct {
	Task           string
	Calls          []Call
	Escalations    []Escalation
	Fallbacks      int
	RateLimitWaitS float64
	// SavedPrompt is the prompt tokens a naive agent would have sent on top of ours.
	SavedPrompt float64
	Trace       []TraceEntry
	Started     time.Time
}

func New(task string) *Footprint { return &Footprint{Task: task, Started: time.Now()} }

func (f *Footprint) Add(model string, prompt, completion int, latencyS float64, local bool, region string) Call {
	var pin, pout, kwh float64
	if !local {
		pin, pout = budget.Price(model)
		kwh = EnergyKWh(model, completion, latencyS)
	}
	cl := Call{model, prompt, completion, math.Round(latencyS*1000) / 1000,
		(float64(prompt)*pin + float64(completion)*pout) / 1e6, kwh, region, kwh * Grid(region), local}
	f.Calls = append(f.Calls, cl)
	return cl
}

type ModelTotals struct {
	Calls, In, Out int
	Cost, KWh      float64
}

type Totals struct {
	SavedKWh, SavedGCO2e     float64
	SavedPrompt, NaiveTotal  int
	SavedPct, Calls, In, Out int
	CostUSD, LatencyS        float64
	KWh, GCO2e               float64
	Regions                  []string
	ByModel                  map[string]*ModelTotals
}

func (f *Footprint) Totals() Totals {
	t := Totals{ByModel: map[string]*ModelTotals{}, Calls: len(f.Calls)}
	regions := map[string]bool{}
	for _, cl := range f.Calls {
		m := t.ByModel[cl.Model]
		if m == nil {
			m = &ModelTotals{}
			t.ByModel[cl.Model] = m
		}
		m.Calls++
		m.In += cl.Prompt
		m.Out += cl.Completion
		m.Cost += cl.Cost
		m.KWh += cl.KWh
		t.In += cl.Prompt
		t.Out += cl.Completion
		t.CostUSD += cl.Cost
		t.LatencyS += cl.LatencyS
		t.KWh += cl.KWh
		t.GCO2e += cl.GCO2e
		if cl.Region != "" {
			regions[cl.Region] = true
		}
	}
	for r := range regions {
		t.Regions = append(t.Regions, r)
	}
	sort.Strings(t.Regions)
	used := t.In + t.Out
	t.SavedPrompt = int(math.Round(f.SavedPrompt))
	t.NaiveTotal = used + t.SavedPrompt
	if t.NaiveTotal > 0 {
		t.SavedPct = int(math.Round(100 * float64(t.SavedPrompt) / float64(t.NaiveTotal)))
	}
	// the energy model ignores input tokens, so the saving is scaled from tokens: energy per token stays constant
	if used > 0 {
		scale := float64(t.SavedPrompt) / float64(used)
		t.SavedKWh, t.SavedGCO2e = t.KWh*scale, t.GCO2e*scale
	}
	return t
}

func (f *Footprint) AllLocal() bool {
	for _, cl := range f.Calls {
		if !cl.Local {
			return false
		}
	}
	return len(f.Calls) > 0
}

func (f *Footprint) Report(result, verified string) string {
	t := f.Totals()
	short := func(s string, n int) string {
		if len(s) > n {
			return s[:n]
		}
		return s
	}
	lines := []string{
		"", "-- footprint " + strings.Repeat("-", 47),
		"result     " + short(result, 70),
		"verified   " + verified,
		fmt.Sprintf("calls      %d  (fallbacks %d, escalations %d)", t.Calls, f.Fallbacks, len(f.Escalations)),
		fmt.Sprintf("tokens     %d  (in %d / out %d)", t.In+t.Out, t.In, t.Out),
		fmt.Sprintf("saved      ~%d tokens (%d%% fewer than a naive agent; estimate, see README)", t.SavedPrompt, t.SavedPct),
		fmt.Sprintf("cost       $%.5f  (price table; Bifrost logs are authoritative)", t.CostUSD),
	}
	wait := ""
	if f.RateLimitWaitS > 0 {
		wait = fmt.Sprintf(", %.0fs rate-limit wait", f.RateLimitWaitS)
	}
	lines = append(lines, fmt.Sprintf("latency    %.2fs model time%s", t.LatencyS, wait))
	if f.AllLocal() {
		lines = append(lines, "energy     n/a  (local model, not estimated)", "carbon     n/a  (local model, not estimated)")
	} else {
		regions := strings.Join(t.Regions, ",")
		if regions == "" {
			regions = "?"
		}
		lines = append(lines, fmt.Sprintf("energy     %.4f Wh  (EcoLogits estimate, PUE %.1f)", t.KWh*1000, PUE),
			fmt.Sprintf("carbon     %.3f mgCO2e  (region %s, grid %.0f g/kWh)", t.GCO2e*1000, regions, Grid(first(t.Regions))))
	}
	names := make([]string, 0, len(t.ByModel))
	for m := range t.ByModel {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		v := t.ByModel[m]
		lines = append(lines, fmt.Sprintf("  %-28s %d calls  %7d tok  $%.5f  %.4f Wh", m, v.Calls, v.In+v.Out, v.Cost, v.KWh*1000))
	}
	for _, e := range f.Escalations {
		lines = append(lines, fmt.Sprintf("  escalated at step %d: %s", e.Step, e.Reason))
	}
	return strings.Join(lines, "\n")
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func (f *Footprint) Save(dir, result, verified string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, f.Started.Format("20060102-150405")+".json")
	data, err := json.MarshalIndent(map[string]any{
		"task": f.Task, "result": result, "verified": verified, "pue": PUE, "rate_limit_wait_s": f.RateLimitWaitS,
		"totals": f.Totals(), "escalations": f.Escalations, "fallbacks": f.Fallbacks, "calls": f.Calls, "trace": f.Trace,
	}, "", " ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}
