package budget

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestChargePersistsAndBinds(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	b := New(p, 0.001)
	if err := b.Check(); err != nil {
		t.Fatal(err)
	}
	b.Charge("groq/openai/gpt-oss-120b", 10_000, 1_000, false)
	if err := New(p, 0.001).Check(); !errors.Is(err, ErrExceeded) {
		t.Fatalf("want ErrExceeded after reload, got %v", err)
	}
}

func TestLocalCallsAreFree(t *testing.T) {
	b := New(filepath.Join(t.TempDir(), "b.json"), 1)
	if cost := b.Charge("ollama/x", 1e6, 1e6, true); cost != 0 {
		t.Fatalf("cost = %v", cost)
	}
}

func TestPriceOverride(t *testing.T) {
	t.Setenv("SLENDERTOKEN_PRICE", "1.5,2.5")
	if in, out := Price("anything"); in != 1.5 || out != 2.5 {
		t.Fatalf("got %v,%v", in, out)
	}
}

func TestPriceLookupIgnoresTheProviderPrefix(t *testing.T) {
	t.Setenv("SLENDERTOKEN_PRICE", "")
	for _, m := range []string{"groq/openai/gpt-oss-120b", "openrouter/openai/gpt-oss-120b", "openai/gpt-oss-120b"} {
		if in, out := Price(m); in != 0.15 || out != 0.60 {
			t.Errorf("%s: %v,%v", m, in, out)
		}
	}
	if in, out := Price("anthropic/claude-sonnet-5-5"); in != defaultPrice[0] || out != defaultPrice[1] {
		t.Errorf("unknown model should get the default price, got %v,%v", in, out)
	}
}
