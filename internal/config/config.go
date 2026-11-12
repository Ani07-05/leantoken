// Package config reads settings from the real environment, ./.env and ~/.slendertoken/.env, in that order of precedence.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

var (
	realEnv   = snapshotEnv()
	GlobalEnv = filepath.Join(Home(), ".slendertoken", ".env")
	secrets   = []string{"KEY", "VK", "TOKEN", "PASSWORD"}
)

const Template = `# slendertoken settings, used from any folder. A .env in the current folder overrides these.
# Edit with ` + "`slendertoken config edit` or `slendertoken config set KEY VALUE`" + `.

# Bifrost gateway: every model call goes through it
BIFROST_URL=http://localhost:8080
# your virtual key (budget + rate limit), created in the Bifrost UI
BIFROST_VK=
# Ollama-only virtual key for --private runs
BIFROST_PRIVATE_VK=
# optional: skip the picker; otherwise run ` + "`slendertoken --pick-model`" + `
# SLENDERTOKEN_MODEL=groq/openai/gpt-oss-120b
# optional: small model tried first (default groq/openai/gpt-oss-20b)
# SLENDERTOKEN_SMALL_MODEL=groq/openai/gpt-oss-20b
# optional: grid intensity in gCO2e/kWh instead of the Groq-region default
# SLENDERTOKEN_GRID_G=369
# optional: provider/model list Bifrost tries if Groq fails
# SLENDERTOKEN_FALLBACKS=openai/gpt-4o-mini
# optional: local model for --private (served by Ollama through Bifrost)
# SLENDERTOKEN_LOCAL_MODEL=qwen2.5-coder:7b
# optional: backup budget price in USD per 1M tokens "in,out"
# SLENDERTOKEN_PRICE=0.59,0.79
# optional: enables the web_search tool (app.tavily.com); never used in private mode
# TAVILY_API_KEY=tvly-
# optional: color theme: mono, slender, ember, nord, gruvbox, dracula or paper; change with /theme
# SLENDERTOKEN_THEME=mono
# optional: 1 leaves the background to your terminal (blur/opacity shows through); toggle with /transparent
# SLENDERTOKEN_TRANSPARENT=0
`

const usage = `usage: slendertoken config [list | get KEY | set KEY VALUE | unset KEY | edit | path]
  list            show settings in effect and where each comes from (secrets masked)
  get KEY         print one value
  set KEY VALUE   save to the global file
  unset KEY       remove from the global file
  edit            open the global file in your editor
  path            print the global file's location`

func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func snapshotEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func Parse(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if line == "" || strings.HasPrefix(line, "#") || !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"'")
	}
	return out
}

func loadEnv(path string) {
	for k, v := range Parse(path) {
		if _, set := os.LookupEnv(k); v != "" && !set {
			os.Setenv(k, v)
		}
	}
}

// migrateLegacy moves the settings of the project's old name (~/.leantoken) to the new home once, and renames
// its LEANTOKEN_* keys so saved settings keep working.
func migrateLegacy() {
	dir := filepath.Dir(GlobalEnv)
	old := filepath.Join(Home(), ".lean"+"token")
	if _, err := os.Stat(old); err != nil {
		return
	}
	if _, err := os.Stat(dir); err == nil {
		return
	}
	if os.Rename(old, dir) != nil {
		return
	}
	if data, err := os.ReadFile(GlobalEnv); err == nil {
		_ = os.WriteFile(GlobalEnv, []byte(strings.ReplaceAll(string(data), "LEAN"+"TOKEN_", "SLENDERTOKEN_")), 0o600)
	}
}

func LoadAll() {
	migrateLegacy()
	cwd, _ := os.Getwd()
	loadEnv(filepath.Join(cwd, ".env"))
	loadEnv(GlobalEnv)
}

func EnsureGlobal() string {
	if _, err := os.Stat(GlobalEnv); err != nil {
		_ = os.MkdirAll(filepath.Dir(GlobalEnv), 0o755)
		_ = os.WriteFile(GlobalEnv, []byte(Template), 0o600)
	}
	return GlobalEnv
}

// SetValue sets KEY in the global file, or removes it when value is nil, keeping comments and order.
func SetValue(key string, value *string) error {
	path := EnsureGlobal()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var lines []string
	found := false
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		k, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(k) == key && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			found = true
			if value != nil {
				lines = append(lines, key+"="+*value)
			}
			continue
		}
		lines = append(lines, line)
	}
	if value != nil && !found {
		lines = append(lines, key+"="+*value)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func IsSecret(key string) bool {
	up := strings.ToUpper(key)
	for _, s := range secrets {
		if strings.Contains(up, s) {
			return true
		}
	}
	return false
}

func mask(key, value string) string {
	if value == "" || !IsSecret(key) {
		return value
	}
	if len(value) > 12 {
		return value[:4] + "..." + value[len(value)-4:]
	}
	return "****"
}

// Overridden names where a value that beats the global file comes from, or "".
func Overridden(key string) string {
	if realEnv[key] != "" {
		return "environment variable"
	}
	cwd, _ := os.Getwd()
	if Parse(filepath.Join(cwd, ".env"))[key] != "" {
		return "./.env"
	}
	return ""
}

type Field struct{ Key, Desc, Example, Value string }

// Fields lists every setting with its description and saved global value, taken from Template.
func Fields() []Field {
	glob := Parse(GlobalEnv)
	var out []Field
	desc := ""
	for _, line := range strings.Split(Template, "\n") {
		body := strings.TrimLeft(line, "# ")
		key, example, ok := strings.Cut(body, "=")
		switch {
		case ok && key != "" && key == strings.ToUpper(key) && !strings.ContainsAny(key, " `"):
			out = append(out, Field{key, strings.TrimPrefix(desc, "optional: "), example, glob[key]})
		case strings.HasPrefix(line, "#"):
			desc = body
		}
	}
	return out
}

func rows() [][3]string {
	cwd, _ := os.Getwd()
	local, glob := Parse(filepath.Join(cwd, ".env")), Parse(GlobalEnv)
	seen := map[string]bool{}
	var keys []string
	for _, f := range Fields() {
		keys, seen[f.Key] = append(keys, f.Key), true
	}
	var extra []string
	for _, m := range []map[string]string{glob, local} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				extra = append(extra, k)
			}
		}
	}
	sort.Strings(extra)
	var out [][3]string
	for _, k := range append(keys, extra...) {
		row := [3]string{k, "", "not set"}
		for _, s := range []struct {
			src  string
			vals map[string]string
		}{{"environment", realEnv}, {"./.env", local}, {"global", glob}} {
			if s.vals[k] != "" {
				row = [3]string{k, mask(k, s.vals[k]), s.src}
				break
			}
		}
		out = append(out, row)
	}
	return out
}

func Main(args []string) error {
	cmd, rest := "list", []string(nil)
	if len(args) > 0 {
		cmd, rest = args[0], args[1:]
	}
	switch {
	case cmd == "list":
		width := 0
		for _, r := range rows() {
			width = max(width, len(r[0]))
		}
		for _, r := range rows() {
			v := r[1]
			if v == "" {
				v = "-"
			}
			fmt.Printf("%-*s  %-24s  %s\n", width, r[0], v, r[2])
		}
		fmt.Printf("\nglobal file: %s\n", GlobalEnv)
	case cmd == "get" && len(rest) == 1:
		fmt.Println(os.Getenv(rest[0]))
	case cmd == "set" && len(rest) >= 2:
		v := strings.Join(rest[1:], " ")
		if err := SetValue(rest[0], &v); err != nil {
			return err
		}
		fmt.Printf("saved %s to %s\n", rest[0], GlobalEnv)
	case cmd == "unset" && len(rest) == 1:
		if err := SetValue(rest[0], nil); err != nil {
			return err
		}
		fmt.Printf("removed %s from %s\n", rest[0], GlobalEnv)
	case cmd == "edit":
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = map[bool]string{true: "notepad", false: "nano"}[runtime.GOOS == "windows"]
		}
		c := exec.Command(editor, EnsureGlobal())
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	case cmd == "path":
		fmt.Println(EnsureGlobal())
	default:
		return fmt.Errorf("%s", usage)
	}
	return nil
}
