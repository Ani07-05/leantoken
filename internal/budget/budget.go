// Package budget is a local USD backup to the Bifrost virtual-key budget, which is the real limit.
package budget

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Groq USD per 1M tokens (input, output), console.groq.com/docs/models, Nov 2026.
// Unpublished prices get defaultPrice so the limit still binds; override with SLENDERTOKEN_PRICE="in,out".
var prices = map[string][2]float64{
	"openai/gpt-oss-120b": {0.15, 0.60},
	"openai/gpt-oss-20b":  {0.075, 0.30},
	"qwen/qwen3.8-27b":    {0.80, 4.00},
}

var defaultPrice = [2]float64{1.00, 4.00}

var ErrExceeded = errors.New("budget exceeded")

// Price returns USD per 1M input and output tokens.
func Price(model string) (in, out float64) {
	if o := os.Getenv("SLENDERTOKEN_PRICE"); o != "" {
		a, b, _ := strings.Cut(o, ",")
		in, _ = strconv.ParseFloat(strings.TrimSpace(a), 64)
		out, _ = strconv.ParseFloat(strings.TrimSpace(b), 64)
		return in, out
	}
	if p, ok := prices[strings.TrimPrefix(model, "groq/")]; ok {
		return p[0], p[1]
	}
	return defaultPrice[0], defaultPrice[1]
}

type Budget struct {
	mu    sync.Mutex
	path  string
	Limit float64
	spent float64
}

func New(path string, limit float64) *Budget {
	b := &Budget{path: path, Limit: limit}
	if data, err := os.ReadFile(path); err == nil {
		var v struct{ Spent float64 }
		_ = json.Unmarshal([]byte(strings.ToLower(string(data))), &v)
		b.spent = v.Spent
	}
	return b
}

func (b *Budget) Spent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}

func (b *Budget) SetLimit(v float64) {
	b.mu.Lock()
	b.Limit = v
	b.mu.Unlock()
}

func (b *Budget) Check() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spent >= b.Limit {
		return fmt.Errorf("%w: spent $%.4f of $%.2f", ErrExceeded, b.spent, b.Limit)
	}
	return nil
}

func (b *Budget) Charge(model string, prompt, completion int, local bool) float64 {
	var pin, pout float64
	if !local {
		pin, pout = Price(model)
	}
	cost := (float64(prompt)*pin + float64(completion)*pout) / 1e6
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spent += cost
	_ = os.MkdirAll(filepath.Dir(b.path), 0o755)
	data, _ := json.Marshal(map[string]float64{"spent": b.spent})
	_ = os.WriteFile(b.path, data, 0o644)
	return cost
}
