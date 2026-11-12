package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withGlobal(t *testing.T) {
	t.Helper()
	old := GlobalEnv
	GlobalEnv = filepath.Join(t.TempDir(), ".env")
	t.Cleanup(func() { GlobalEnv = old })
}

func TestSetValueKeepsCommentsAndOrder(t *testing.T) {
	withGlobal(t)
	v := "abc"
	if err := SetValue("BIFROST_VK", &v); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(GlobalEnv)
	if !strings.Contains(string(data), "BIFROST_VK=abc") || !strings.Contains(string(data), "# Bifrost gateway") {
		t.Fatalf("file:\n%s", data)
	}
	if err := SetValue("BIFROST_VK", nil); err != nil {
		t.Fatal(err)
	}
	if Parse(GlobalEnv)["BIFROST_VK"] != "" {
		t.Fatal("not removed")
	}
}

func TestFieldsCoverTheTemplate(t *testing.T) {
	withGlobal(t)
	keys := map[string]bool{}
	for _, f := range Fields() {
		keys[f.Key] = true
	}
	for _, want := range []string{"BIFROST_URL", "BIFROST_VK", "TAVILY_API_KEY", "SLENDERTOKEN_THEME"} {
		if !keys[want] {
			t.Errorf("missing field %s", want)
		}
	}
}

func TestSecretsAreMasked(t *testing.T) {
	if mask("BIFROST_VK", "sk-abcdefghijklmnop") == "sk-abcdefghijklmnop" || !IsSecret("TAVILY_API_KEY") || IsSecret("BIFROST_URL") {
		t.Fatal("masking wrong")
	}
}

func TestMigrateLegacyMovesOldSettingsAndRenamesKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := GlobalEnv
	GlobalEnv = filepath.Join(home, ".slendertoken", ".env")
	t.Cleanup(func() { GlobalEnv = old })
	legacy := filepath.Join(home, ".lean"+"token")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, ".env"), []byte("BIFROST_VK=k\nLEAN"+"TOKEN_THEME=nord\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateLegacy()
	got := Parse(GlobalEnv)
	if got["BIFROST_VK"] != "k" || got["SLENDERTOKEN_THEME"] != "nord" {
		t.Fatalf("migrated settings: %v", got)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Fatal("old directory should be gone")
	}
}
