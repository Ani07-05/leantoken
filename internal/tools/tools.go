// Package tools is the agent's workspace: ranged reads, capped search, exact-match edits and clipped shell output.
package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ani07-05/slendertoken/internal/web"
)

const (
	MaxOut   = 1600
	MaxLines = 150 // per read_file call; every later turn pays for what is read now
	MaxFiles = 200
)

var skipDirs = map[string]bool{".git": true, ".slendertoken": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, "dist": true, "build": true, ".mypy_cache": true, ".pytest_cache": true}

// Clip keeps the head and tail of s when it is longer than n.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n/2] + fmt.Sprintf("\n...[%d chars cut]...\n", len(s)-n) + s[len(s)-n/2:]
}

type Workspace struct {
	Root string
	// Lean false gives the benchmark's naive agent: whole files, unclipped output.
	Lean bool
	// Naive is the size of the last call's output for an agent with no limits, or -1 when it equals the real output.
	Naive int
	// Sources are the links the last web_search returned, for the UI.
	Sources []web.Source
	// Ctx cancels a running shell command.
	Ctx context.Context
}

func NewWorkspace(root string, lean bool) *Workspace {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return &Workspace{Root: abs, Lean: lean, Naive: -1}
}

func (w *Workspace) path(p string) (string, error) {
	full := filepath.Join(w.Root, p)
	if filepath.IsAbs(p) {
		full = filepath.Clean(p)
	}
	rel, err := filepath.Rel(w.Root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repo: %s", p)
	}
	return full, nil
}

func (w *Workspace) clip(s string) string {
	if w.Lean {
		return Clip(s, MaxOut)
	}
	return s
}

func str(a map[string]any, k, def string) string {
	if v, ok := a[k].(string); ok {
		return v
	}
	return def
}

func num(a map[string]any, k string, def int) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

// Call runs one tool by name. Errors that the model can act on come back as "error: ..." text, like the other tools.
func (w *Workspace) Call(name string, a map[string]any) (string, error) {
	w.Naive, w.Sources = -1, nil
	switch name {
	case "read_file":
		return w.readFile(a)
	case "search":
		return w.search(str(a, "pattern", ""), str(a, "path", "."))
	case "list_files":
		return w.listFiles(str(a, "path", "."))
	case "write_file":
		return w.writeFile(str(a, "path", ""), str(a, "content", ""))
	case "edit_file":
		return w.editFile(str(a, "path", ""), str(a, "old", ""), str(a, "new", ""))
	case "run_shell":
		return w.runShell(a["cmd"], num(a, "timeout", 120))
	case "web_search":
		out, sources := web.Search(str(a, "query", ""), num(a, "max_results", 3))
		w.Sources = sources
		return Clip(out, MaxOut), nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

func (w *Workspace) readFile(a map[string]any) (string, error) {
	path := str(a, "path", "")
	if path == "" {
		return "", errors.New("missing path")
	}
	start, end := num(a, "start", 1), -1
	if _, ok := a["end"]; ok {
		end = num(a, "end", -1)
	}
	// models guess names like line_start/end_line; a wrong guess would cost a round trip
	for k := range a {
		switch {
		case k == "path" || k == "start" || k == "end":
		case strings.Contains(k, "start"):
			start = num(a, k, start)
		case strings.Contains(k, "end"):
			end = num(a, k, end)
		default:
			return "", fmt.Errorf("unexpected argument %s; use path, start, end", k)
		}
	}
	full, err := w.path(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	text := strings.ToValidUTF8(string(data), "�")
	w.Naive = len(text)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	start = max(start, 1)
	stop := end
	if end < 0 {
		stop = len(lines)
		if w.Lean {
			stop = start + 99
		}
	}
	if w.Lean {
		stop = min(stop, start-1+MaxLines)
	}
	var b strings.Builder
	for i := start; i <= min(stop, len(lines)); i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i, lines[i-1])
	}
	return w.clip(strings.TrimSuffix(b.String(), "\n") + fmt.Sprintf("\n[%d lines total]", len(lines))), nil
}

func (w *Workspace) search(pattern, path string) (string, error) {
	target, err := w.path(path)
	if err != nil {
		return "", err
	}
	if rg, err := exec.LookPath("rg"); err == nil {
		cmd := exec.Command(rg, "-n", "--no-heading", "--no-messages", "--sort", "path", "-m", "5", "--max-columns", "200",
			"--max-columns-preview", "-g", "!.slendertoken", pattern, target)
		cmd.Dir = w.Root
		out, _ := cmd.Output()
		w.Naive = len(out)
		s := strings.ReplaceAll(string(out), w.Root+string(filepath.Separator), "")
		return w.clip(orDefault(s, "no matches")), nil
	}
	out := w.goSearch(pattern, target)
	w.Naive = len(out)
	return w.clip(orDefault(out, "no matches")), nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (w *Workspace) walk(base string, visit func(path string)) {
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		visit(p)
		return nil
	})
}

// goSearch is the plain grep for machines without rg.
func (w *Workspace) goSearch(pattern, base string) string {
	rx, err := regexp.Compile(pattern)
	if err != nil {
		rx = regexp.MustCompile(regexp.QuoteMeta(pattern))
	}
	var hits []string
	w.walk(base, func(p string) {
		info, err := os.Stat(p)
		if err != nil || info.Size() > 1_000_000 {
			return
		}
		data, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(data) {
			return
		}
		rel, _ := filepath.Rel(w.Root, p)
		n := 0
		for i, line := range strings.Split(string(data), "\n") {
			if rx.MatchString(line) {
				line = strings.TrimSpace(line)
				hits = append(hits, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), i+1, line[:min(len(line), 200)]))
				if n++; n == 5 {
					break
				}
			}
		}
	})
	return strings.Join(hits, "\n")
}

func (w *Workspace) listFiles(path string) (string, error) {
	base, err := w.path(path)
	if err != nil {
		return "", err
	}
	var files []string
	w.walk(base, func(p string) {
		rel, _ := filepath.Rel(w.Root, p)
		files = append(files, filepath.ToSlash(rel))
	})
	sort.Strings(files)
	for _, f := range files {
		w.Naive += len(f) + 1
	}
	w.Naive++
	more := ""
	shown := files
	if w.Lean && len(files) > MaxFiles {
		more = fmt.Sprintf("\n[%d more; narrow the path]", len(files)-MaxFiles)
		shown = files[:MaxFiles]
	}
	return orDefault(strings.Join(shown, "\n"), "no files") + more, nil
}

func (w *Workspace) writeFile(path, content string) (string, error) {
	full, err := w.path(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s (%d bytes)", path, len(content)), nil
}

func (w *Workspace) editFile(path, old, new string) (string, error) {
	full, err := w.path(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	text := string(data)
	if strings.Count(text, old) == 0 && strings.Contains(old, "\t") { // models often re-indent with tabs
		old, new = strings.ReplaceAll(old, "\t", "    "), strings.ReplaceAll(new, "\t", "    ")
	}
	if n := strings.Count(text, old); n != 1 || old == "" {
		return fmt.Sprintf("error: old string matched %d times, need exactly 1", strings.Count(text, old)), nil
	}
	if err := os.WriteFile(full, []byte(strings.Replace(text, old, new, 1)), 0o644); err != nil {
		return "", err
	}
	return "edited " + path, nil
}

func (w *Workspace) runShell(cmd any, timeoutS int) (string, error) {
	command := ShellCmd(map[string]any{"cmd": cmd})
	if timeoutS <= 0 || timeoutS > 120 {
		timeoutS = 120
	}
	parent := w.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutS)*time.Second)
	defer cancel()
	var c *exec.Cmd
	if argv, ok := Unwrap(cmd).([]any); ok && len(argv) > 0 {
		parts := make([]string, len(argv))
		for i, p := range argv {
			parts[i] = fmt.Sprint(p)
		}
		c = exec.CommandContext(ctx, parts[0], parts[1:]...)
	} else if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", command)
	}
	c.Dir = w.Root
	out, err := c.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		code = 124
	case err != nil:
		code = 127
		out = append(out, err.Error()...)
	}
	raw := fmt.Sprintf("exit=%d\n%s", code, strings.ToValidUTF8(string(out), "�"))
	w.Naive = len(raw)
	return w.clip(raw), nil
}
