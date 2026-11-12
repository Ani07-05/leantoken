package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteConfigDeclaresProviderKeyAndBudget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := WriteConfig(Spec{Providers: []KeyedProvider{{"groq", "gsk_abc"}, {"openai", "sk-oa"}}, VirtualKey: "sk-bf-123", BudgetUSD: 3}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	groq := cfg["providers"].(map[string]any)["groq"].(map[string]any)["keys"].([]any)[0].(map[string]any)
	if groq["value"] != "gsk_abc" || groq["name"] != "groq-main" {
		t.Fatalf("groq key: %v", groq)
	}
	if oa := cfg["providers"].(map[string]any)["openai"]; oa == nil {
		t.Fatal("second provider missing")
	}
	gov := cfg["governance"].(map[string]any)
	if vk := gov["virtual_keys"].([]any)[0].(map[string]any); vk["value"] != "sk-bf-123" || vk["is_active"] != true {
		t.Fatalf("virtual key: %v", vk)
	}
	if n := len(gov["virtual_keys"].([]any)[0].(map[string]any)["provider_configs"].([]any)); n != 2 {
		t.Fatalf("virtual key allows %d providers, want 2", n)
	}
	if b := gov["budgets"].([]any)[0].(map[string]any); b["max_limit"] != 3.0 || b["virtual_key_id"] != "vk-slendertoken" {
		t.Fatalf("budget: %v", b)
	}
	store := cfg["config_store"].(map[string]any)["config"].(map[string]any)["path"].(string)
	if !strings.HasPrefix(store, DataDir()) {
		t.Fatalf("database path %q must live in the data dir", store)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(ConfigPath()); st.Mode().Perm() != 0o600 {
			t.Fatalf("config holds the Groq key, mode is %v", st.Mode().Perm())
		}
	}
	if ExistingVirtualKey() != "sk-bf-123" || !Managed() {
		t.Fatal("rerunning setup must find and keep the same virtual key")
	}
}

func TestNewVirtualKeyFormat(t *testing.T) {
	a, b := NewVirtualKey(), NewVirtualKey()
	if !strings.HasPrefix(a, "sk-bf-") || len(a) != len("sk-bf-")+32 || a == b {
		t.Fatalf("keys: %s %s", a, b)
	}
}

func TestPortOf(t *testing.T) {
	for url, want := range map[string]int{"http://localhost:8081": 8081, "http://127.0.0.1:9000/": 9000, "http://localhost": 8080, "::bad": 8080} {
		if got := PortOf(url); got != want {
			t.Errorf("%s: %d, want %d", url, got, want)
		}
	}
}

func TestFreePortSkipsBusyOnes(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skip("8080 already taken, which is the case being tested")
	}
	defer l.Close()
	if p := FreePort(); p == 8080 {
		t.Fatal("picked a busy port")
	}
}

func TestInstallDownloadsOnceAndMakesTheBinaryExecutable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	downloads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			_, _ = w.Write([]byte(`{"name":"v9.9.9"}`))
		case strings.HasPrefix(r.URL.Path, "/bifrost/v9.9.9/") && strings.HasSuffix(r.URL.Path, "bifrost-http") || strings.HasSuffix(r.URL.Path, "bifrost-http.exe"):
			downloads++
			_, _ = w.Write([]byte("#!/bin/sh\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldL, oldD := LatestURL, DownloadBase
	LatestURL, DownloadBase = srv.URL+"/latest", srv.URL
	defer func() { LatestURL, DownloadBase = oldL, oldD }()

	for range 2 {
		bin, err := Install(context.Background(), func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		if st, err := os.Stat(bin); err != nil || st.Size() == 0 {
			t.Fatalf("binary: %v", err)
		}
		if runtime.GOOS != "windows" {
			if st, _ := os.Stat(bin); st.Mode().Perm()&0o100 == 0 {
				t.Fatal("binary is not executable")
			}
		}
	}
	if downloads != 1 {
		t.Fatalf("downloaded %d times, want 1", downloads)
	}
	if v, _ := os.ReadFile(filepath.Join(Home(), "VERSION")); strings.TrimSpace(string(v)) != "v9.9.9" {
		t.Fatalf("version file: %q", v)
	}
}

func TestInstallReportsAMissingBuild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			_, _ = w.Write([]byte(`{"name":"v1.0.0"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	oldL, oldD := LatestURL, DownloadBase
	LatestURL, DownloadBase = srv.URL+"/latest", srv.URL
	defer func() { LatestURL, DownloadBase = oldL, oldD }()
	if _, err := Install(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyExplainsAProviderRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-bf-vk") != "sk-bf-1" {
			t.Errorf("virtual key header: %q", r.Header.Get("x-bf-vk"))
		}
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API Key"}}`))
	}))
	defer srv.Close()
	err := verify(context.Background(), srv.URL, "sk-bf-1", "groq/x")
	if err == nil || !strings.Contains(err.Error(), "Invalid API Key") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadSpecRoundTripsAndEditsProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := WriteConfig(Spec{Providers: []KeyedProvider{{"openai", "k1"}}, VirtualKey: "sk-bf-9", BudgetUSD: 4}); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSpec()
	if err != nil || s.VirtualKey != "sk-bf-9" || s.BudgetUSD != 4 || len(s.Providers) != 1 {
		t.Fatalf("spec %+v err %v", s, err)
	}
	s = s.With(KeyedProvider{"anthropic", "k2"}).With(KeyedProvider{"openai", "k1b"})
	if len(s.Providers) != 2 || s.Providers[0].Key != "k1b" {
		t.Fatalf("with: %+v", s.Providers)
	}
	if got := s.Without("openai").Providers; len(got) != 1 || got[0].ID != "anthropic" {
		t.Fatalf("without: %+v", got)
	}
}

func TestProvidersFromEnvironment(t *testing.T) {
	for _, p := range Providers {
		t.Setenv(p.EnvVar, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", "a")
	t.Setenv("OPENROUTER_API_KEY", "o")
	got := FromEnvironment()
	if len(got) != 2 || got[0].ID != "anthropic" || got[1].ID != "openrouter" {
		t.Fatalf("got %+v", got)
	}
}

func TestEveryProviderHasUniqueIDAndEnvVar(t *testing.T) {
	ids, envs := map[string]bool{}, map[string]bool{}
	for _, p := range Providers {
		if p.ID == "" || p.EnvVar == "" || ids[p.ID] || envs[p.EnvVar] {
			t.Errorf("bad or duplicate provider %+v", p)
		}
		ids[p.ID], envs[p.EnvVar] = true, true
		if (p.Small == "") != (p.Large == "") {
			t.Errorf("%s: set both default models or neither", p.ID)
		}
	}
}

func TestCollectProvidersWithoutTerminalNeedsAKeyVariable(t *testing.T) {
	for _, p := range Providers {
		t.Setenv(p.EnvVar, "")
	}
	if _, err := collectProviders("", io.Discard); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	if _, err := collectProviders("nope", io.Discard); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("MISTRAL_API_KEY", "m")
	got, err := collectProviders("mistral", io.Discard)
	if err != nil || len(got) != 1 || got[0].Key != "m" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestVerifyToleratesAnUnknownTestModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"message":"The model gpt-x does not exist"}}`))
	}))
	defer srv.Close()
	if err := verify(context.Background(), srv.URL, "k", "openai/gpt-x"); !errors.Is(err, errNoTestModel) {
		t.Fatalf("err = %v", err)
	}
	if err := verify(context.Background(), srv.URL, "k", ""); !errors.Is(err, errNoTestModel) {
		t.Fatalf("err = %v", err)
	}
}
