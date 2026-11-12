package footprint

import "testing"

func TestLocalCallsHaveNoEnergy(t *testing.T) {
	f := New("t")
	f.Add("ollama/x", 100, 50, 1, true, "")
	if tot := f.Totals(); tot.KWh != 0 || !f.AllLocal() {
		t.Fatalf("%+v", tot)
	}
}

func TestSavedScalesEnergyByTokens(t *testing.T) {
	f := New("t")
	f.Add("groq/openai/gpt-oss-120b", 1000, 100, 1, false, "")
	f.SavedPrompt = 1100
	tot := f.Totals()
	if tot.SavedPct != 50 || tot.SavedKWh <= 0 || tot.SavedKWh > tot.KWh*1.01 {
		t.Fatalf("%+v", tot)
	}
}

func TestGridOverride(t *testing.T) {
	t.Setenv("SLENDERTOKEN_GRID_G", "100")
	if Grid("bom") != 100 {
		t.Fatal("override ignored")
	}
}
