package bifrost

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Provider is one model provider Bifrost can route to with nothing but an API key. These are the ones its config accepts
// without extra settings (cloud platforms such as Bedrock, Azure and Vertex need more than a key and are added in
// Bifrost's own config).
type Provider struct {
	ID     string
	Name   string
	EnvVar string
	// Small and Large are the models used when the user has not picked any: a cheap first model and a stronger one for
	// when a task gets hard. Empty means the app asks the user to pick from the provider's list on first run.
	Small, Large string
}

var Providers = []Provider{
	{ID: "openai", Name: "OpenAI", EnvVar: "OPENAI_API_KEY"},
	{ID: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY", Small: "anthropic/claude-haiku-4-5-20251001", Large: "anthropic/claude-sonnet-5-5"},
	{ID: "gemini", Name: "Google Gemini", EnvVar: "GEMINI_API_KEY"},
	{ID: "openrouter", Name: "OpenRouter", EnvVar: "OPENROUTER_API_KEY"},
	{ID: "groq", Name: "Groq", EnvVar: "GROQ_API_KEY", Small: "groq/openai/gpt-oss-20b", Large: "groq/openai/gpt-oss-120b"},
	{ID: "mistral", Name: "Mistral", EnvVar: "MISTRAL_API_KEY"},
	{ID: "deepseek", Name: "DeepSeek", EnvVar: "DEEPSEEK_API_KEY"},
	{ID: "xai", Name: "xAI", EnvVar: "XAI_API_KEY"},
	{ID: "cerebras", Name: "Cerebras", EnvVar: "CEREBRAS_API_KEY"},
	{ID: "fireworks", Name: "Fireworks AI", EnvVar: "FIREWORKS_API_KEY"},
	{ID: "perplexity", Name: "Perplexity", EnvVar: "PERPLEXITY_API_KEY"},
	{ID: "cohere", Name: "Cohere", EnvVar: "COHERE_API_KEY"},
	{ID: "nebius", Name: "Nebius", EnvVar: "NEBIUS_API_KEY"},
	{ID: "parasail", Name: "Parasail", EnvVar: "PARASAIL_API_KEY"},
	{ID: "huggingface", Name: "Hugging Face", EnvVar: "HUGGINGFACE_API_KEY"},
	{ID: "minimax", Name: "MiniMax", EnvVar: "MINIMAX_API_KEY"},
}

func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// KeyedProvider is a provider with the user's key for it.
type KeyedProvider struct{ ID, Key string }

// FromEnvironment finds a key for every provider whose usual environment variable is set.
func FromEnvironment() []KeyedProvider {
	var out []KeyedProvider
	for _, p := range Providers {
		if k := os.Getenv(p.EnvVar); k != "" {
			out = append(out, KeyedProvider{p.ID, k})
		}
	}
	return out
}

// LoadSpec reads back what WriteConfig wrote, so a provider can be added or removed without asking for every key again.
func LoadSpec() (Spec, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return Spec{}, errNotManaged
	}
	var cfg struct {
		Providers map[string]struct {
			Keys []struct{ Value string }
		}
		Governance struct {
			VirtualKeys []struct{ Value string } `json:"virtual_keys"`
			Budgets     []struct {
				MaxLimit float64 `json:"max_limit"`
			}
		}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Spec{}, fmt.Errorf("cannot read %s: %w", ConfigPath(), err)
	}
	var s Spec
	for id, p := range cfg.Providers {
		if len(p.Keys) > 0 {
			s.Providers = append(s.Providers, KeyedProvider{id, p.Keys[0].Value})
		}
	}
	sort.Slice(s.Providers, func(i, j int) bool { return s.Providers[i].ID < s.Providers[j].ID })
	if len(cfg.Governance.VirtualKeys) > 0 {
		s.VirtualKey = cfg.Governance.VirtualKeys[0].Value
	}
	if len(cfg.Governance.Budgets) > 0 {
		s.BudgetUSD = cfg.Governance.Budgets[0].MaxLimit
	}
	return s, nil
}

// With returns the spec with the provider added, or its key replaced.
func (s Spec) With(p KeyedProvider) Spec {
	out := s
	out.Providers = nil
	replaced := false
	for _, e := range s.Providers {
		if e.ID == p.ID {
			e, replaced = p, true
		}
		out.Providers = append(out.Providers, e)
	}
	if !replaced {
		out.Providers = append(out.Providers, p)
	}
	return out
}

// Without returns the spec with the provider removed.
func (s Spec) Without(id string) Spec {
	out := s
	out.Providers = nil
	for _, e := range s.Providers {
		if e.ID != id {
			out.Providers = append(out.Providers, e)
		}
	}
	return out
}
