// Package gateway holds Bifrost connection settings, Groq model discovery and the saved model choice.
package gateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ani07-05/slendertoken/internal/config"
)

const DefaultSmall = "groq/openai/gpt-oss-20b"

// speech and classifier models can't run the agent
var nonChat = []string{"whisper", "tts", "orpheus", "playai", "guard"}

var (
	configFile  = filepath.Join(config.Home(), ".slendertoken", "config.json")
	modelsCache = filepath.Join(config.Home(), ".slendertoken", "models.json")
)

func URL() string {
	u := os.Getenv("BIFROST_URL")
	if u == "" {
		u = "http://localhost:8080"
	}
	return strings.TrimRight(u, "/")
}

func Headers(private bool) (map[string]string, error) {
	name := "BIFROST_VK"
	if private {
		name = "BIFROST_PRIVATE_VK"
	}
	vk := os.Getenv(name)
	if private && vk == "" {
		return nil, errors.New("BIFROST_PRIVATE_VK is not set: private runs need the Ollama-only virtual key")
	}
	if vk == "" {
		return map[string]string{}, nil
	}
	return map[string]string{"x-bf-vk": vk}, nil
}

func LocalModel() string {
	m := os.Getenv("SLENDERTOKEN_LOCAL_MODEL")
	if m == "" {
		m = "qwen2.5-coder:7b"
	}
	return "ollama/" + m
}

func GroqModel(m string) string {
	if strings.HasPrefix(m, "groq/") {
		return m
	}
	return "groq/" + m
}

func ListModels() ([]string, error) {
	h, _ := Headers(false)
	req, _ := http.NewRequest(http.MethodGet, URL()+"/v1/models", nil)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Bifrost at %s: start it with `npx -y @maximhq/bifrost`", URL())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e struct{ Error struct{ Message string } }
		msg := string(body)
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return nil, fmt.Errorf("Bifrost refused to list models (%d): %s", resp.StatusCode, msg[:min(len(msg), 200)])
	}
	var parsed struct{ Data []struct{ ID string } }
	_ = json.Unmarshal(body, &parsed)
	var models []string
	for _, m := range parsed.Data {
		if strings.HasPrefix(m.ID, "groq/") && !hasAny(m.ID, nonChat) {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	if len(models) > 0 {
		_ = os.MkdirAll(filepath.Dir(modelsCache), 0o755)
		data, _ := json.Marshal(models)
		_ = os.WriteFile(modelsCache, data, 0o644)
	}
	return models, nil
}

func hasAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// CachedModels is the last list Bifrost returned, so the picker opens instantly.
func CachedModels() []string {
	var out []string
	if data, err := os.ReadFile(modelsCache); err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

type saved struct {
	Model *string `json:"model,omitempty"`
	Small *string `json:"small,omitempty"`
}

func load() saved {
	var s saved
	if data, err := os.ReadFile(configFile); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func SavedModel() string {
	if m := load().Model; m != nil {
		return *m
	}
	return ""
}

// SavedSmall returns the saved first model and whether one was saved; "" with true means no routing.
func SavedSmall() (string, bool) {
	if s := load().Small; s != nil {
		return *s, true
	}
	return "", false
}

func SaveModel(model, small *string) error {
	s := load()
	if model != nil {
		s.Model = model
	}
	if small != nil {
		s.Small = small
	}
	_ = os.MkdirAll(filepath.Dir(configFile), 0o755)
	data, _ := json.Marshal(s)
	return os.WriteFile(configFile, data, 0o644)
}

func choose(in *bufio.Reader, models []string, prompt string, allowNone bool) string {
	for i, m := range models {
		fmt.Printf("%2d. %s\n", i+1, m)
	}
	if allowNone {
		fmt.Println(" 0. none: use the large model only")
	}
	for {
		fmt.Print(prompt)
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if allowNone && line == "0" {
			return ""
		}
		if n, convErr := strconv.Atoi(line); convErr == nil && n >= 1 && n <= len(models) {
			return models[n-1]
		}
		if err != nil {
			return models[0]
		}
		fmt.Println("invalid choice")
	}
}

// PickModel asks on the terminal for the large and first models and saves both.
func PickModel() (string, error) {
	models, err := ListModels()
	if err != nil {
		return "", err
	}
	if len(models) == 0 {
		return "", errors.New("Bifrost lists no Groq models: add the Groq provider in the Bifrost UI first")
	}
	in := bufio.NewReader(os.Stdin)
	model := choose(in, models, "large model number: ", false)
	small := choose(in, models, "first model number (tried before the large one, 0 for none): ", true)
	if err := SaveModel(&model, &small); err != nil {
		return "", err
	}
	fmt.Printf("saved %s to %s\n", model, configFile)
	return model, nil
}

// ResolveModel: --model flag > SLENDERTOKEN_MODEL > saved choice > interactive pick.
func ResolveModel(flag string) (string, error) {
	for _, m := range []string{flag, os.Getenv("SLENDERTOKEN_MODEL"), SavedModel()} {
		if m != "" {
			return m, nil
		}
	}
	return PickModel()
}

// ResolveSmall: --small flag > SLENDERTOKEN_SMALL_MODEL > saved choice > default.
func ResolveSmall(flag *string) string {
	if flag != nil {
		return *flag
	}
	if m := os.Getenv("SLENDERTOKEN_SMALL_MODEL"); m != "" {
		return m
	}
	if m, ok := SavedSmall(); ok {
		return m
	}
	return DefaultSmall
}
