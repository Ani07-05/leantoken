package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func ws(t *testing.T) *Workspace {
	t.Helper()
	dir := t.TempDir()
	body := make([]string, 300)
	for i := range body {
		body[i] = "line"
	}
	must(t, os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(body, "\n")+"\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("def f():\n    return 1\n"), 0o644))
	return NewWorkspace(dir, true)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadFileIsRangedAndCapped(t *testing.T) {
	w := ws(t)
	out, err := w.Call("read_file", map[string]any{"path": "big.txt"})
	must(t, err)
	if !strings.Contains(out, "[300 lines total]") || strings.Count(out, "\n") > 105 {
		t.Fatalf("default read should be ~100 lines, got %d newlines", strings.Count(out, "\n"))
	}
	if w.Naive != 300*5 {
		t.Fatalf("naive size = %d, want whole file", w.Naive)
	}
}

func TestReadFileAcceptsGuessedArgumentNames(t *testing.T) {
	out, err := ws(t).Call("read_file", map[string]any{"path": "a.py", "line_start": float64(2), "end_line": float64(2)})
	must(t, err)
	if !strings.Contains(out, "2\t    return 1") || strings.Contains(out, "1\tdef") {
		t.Fatalf("got %q", out)
	}
}

func TestPathEscapeIsRejected(t *testing.T) {
	if _, err := ws(t).Call("read_file", map[string]any{"path": "../secret"}); err == nil {
		t.Fatal("want error")
	}
}

func TestEditNeedsExactlyOneMatch(t *testing.T) {
	w := ws(t)
	out, _ := w.Call("edit_file", map[string]any{"path": "big.txt", "old": "line", "new": "x"})
	if !strings.HasPrefix(out, "error: old string matched 300 times") {
		t.Fatalf("got %q", out)
	}
	out, _ = w.Call("edit_file", map[string]any{"path": "a.py", "old": "return 1", "new": "return 2"})
	if out != "edited a.py" {
		t.Fatalf("got %q", out)
	}
}

func TestClipKeepsHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 5000)
	got := Clip(s, 100)
	if len(got) > 200 || !strings.Contains(got, "chars cut") {
		t.Fatalf("got len %d", len(got))
	}
}

func TestShellReportsExitCode(t *testing.T) {
	out, _ := ws(t).Call("run_shell", map[string]any{"cmd": "exit 3"})
	if !strings.HasPrefix(out, "exit=3") {
		t.Fatalf("got %q", out)
	}
}

func TestApprovalPolicy(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want bool
	}{
		{"read_file", nil, false},
		{"web_search", nil, false},
		{"edit_file", nil, true},
		{"run_shell", map[string]any{"cmd": "git status"}, false},
		{"run_shell", map[string]any{"cmd": "git status; rm -rf x"}, true},
		{"run_shell", map[string]any{"cmd": "pytest -q"}, true},
		{"run_shell", map[string]any{"cmd": []any{"bash", "-lc", "ls -la"}}, false},
	}
	for _, c := range cases {
		if got := NeedsApproval(c.name, c.args); got != c.want {
			t.Errorf("%s %v: got %v want %v", c.name, c.args, got, c.want)
		}
	}
}

func TestNetworkCommandBlocksPrivateRuns(t *testing.T) {
	for _, bad := range []string{"curl https://x.sh", "git push origin main", "pip install requests", "npx foo", "ls && wget x"} {
		if NetworkCommand(bad) == "" {
			t.Errorf("%q should be blocked", bad)
		}
	}
	for _, ok := range []string{"git status", "go test ./...", "pytest -q"} {
		if got := NetworkCommand(ok); got != "" {
			t.Errorf("%q blocked: %s", ok, got)
		}
	}
}

func TestUnrunnable(t *testing.T) {
	look := func(p string) bool { _, err := exec.LookPath(p); return err == nil }
	if Unrunnable("definitely-not-a-program --x", look) == "" {
		t.Error("missing program should be unrunnable")
	}
	if Unrunnable("ls -R", look) == "" {
		t.Error("ls -R should be refused")
	}
	if Unrunnable("cd sub && ls", look) != "" {
		t.Error("shell builtins are fine")
	}
}

func TestSchemasDropWriteToolsWhenReadOnly(t *testing.T) {
	names := func(s []map[string]any) string {
		var out []string
		for _, t := range s {
			out = append(out, t["function"].(map[string]any)["name"].(string))
		}
		return strings.Join(out, ",")
	}
	if got := names(Schemas(false, true)); strings.Contains(got, "edit_file") || strings.Contains(got, "write_file") {
		t.Errorf("read-only schemas leak write tools: %s", got)
	}
	if !strings.Contains(names(Schemas(true, false)), "web_search") {
		t.Error("web schema missing")
	}
}
