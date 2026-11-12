package bifrost

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// Spec is what the gateway needs to be useful: the user's provider keys behind one budgeted virtual key.
type Spec struct {
	Providers  []KeyedProvider
	VirtualKey string
	BudgetUSD  float64
}

func ConfigPath() string { return filepath.Join(DataDir(), "config.json") }

// NewVirtualKey makes a key in Bifrost's sk-bf- format.
func NewVirtualKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sk-bf-" + hex.EncodeToString(b)
}

// WriteConfig writes the declarative gateway config: one entry per provider, and a virtual key that may use all of them
// with a daily budget. Bifrost reads this on start, so setup needs no calls to its management API. The file holds the
// provider keys, so it is private to the user.
func WriteConfig(s Spec) error {
	providers := map[string]any{}
	var configs []any
	for _, p := range s.Providers {
		providers[p.ID] = map[string]any{"keys": []any{map[string]any{"name": p.ID + "-main", "value": p.Key, "models": []string{"*"}, "weight": 1.0}}}
		configs = append(configs, map[string]any{"provider": p.ID, "weight": 1.0, "allowed_models": []string{"*"}, "key_ids": []string{"*"}})
	}
	cfg := map[string]any{
		"$schema":      "https://www.getbifrost.ai/schema",
		"config_store": map[string]any{"enabled": true, "type": "sqlite", "config": map[string]any{"path": filepath.Join(DataDir(), "config.db")}},
		"providers":    providers,
		"governance": map[string]any{
			"virtual_keys": []any{map[string]any{
				"id": "vk-slendertoken", "name": "slendertoken", "value": s.VirtualKey, "is_active": true, "provider_configs": configs,
			}},
			"budgets": []any{map[string]any{"id": "budget-slendertoken", "virtual_key_id": "vk-slendertoken", "max_limit": s.BudgetUSD, "reset_duration": "1d"}},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(DataDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), append(data, '\n'), 0o600)
}

// ExistingVirtualKey returns the key already in the config, so re-running setup keeps the same one.
func ExistingVirtualKey() string {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return ""
	}
	var cfg struct {
		Governance struct {
			VirtualKeys []struct{ Value string } `json:"virtual_keys"`
		}
	}
	if json.Unmarshal(data, &cfg) != nil || len(cfg.Governance.VirtualKeys) == 0 {
		return ""
	}
	return cfg.Governance.VirtualKeys[0].Value
}

// FreePort returns the first port from 8080 up that nothing is listening on.
func FreePort() int {
	for p := 8080; p < 8100; p++ {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			l.Close()
			return p
		}
	}
	return 8080
}
