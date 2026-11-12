package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	safeCmd     = regexp.MustCompile("^(git (status|diff|log|show|branch|ls-files)|ls|dir|pwd|cat|type|head|tail|wc)\\b[^;&|><`$]*$")
	assignRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	shellSplit  = regexp.MustCompile(`[;&|\n]+`)
	builtins    = map[string]bool{"cd": true, "export": true, "source": true, ".": true, "set": true, "unset": true, "test": true, "[": true, "true": true, "false": true, "exit": true, "type": true, "alias": true, "ulimit": true, "umask": true}
	winBuiltins = []string{"dir", "echo", "copy", "del", "move", "ren", "md", "mkdir", "rd", "cls", "ver", "where", "more"}
)

// Shell is the dialect the model is told it is writing for.
func Shell() string {
	if runtime.GOOS == "windows" {
		return "Windows cmd.exe"
	}
	return "POSIX sh"
}

// Unwrap turns ["bash","-lc","pytest"] into "pytest": models assume Linux, but on Windows bash is WSL without the repo's tools.
func Unwrap(cmd any) any {
	argv, ok := cmd.([]any)
	if !ok {
		return cmd
	}
	if len(argv) == 3 {
		if sh := filepath.Base(fmt.Sprint(argv[0])); (sh == "bash" || sh == "sh" || sh == "zsh") && (argv[1] == "-c" || argv[1] == "-lc") {
			return fmt.Sprint(argv[2])
		}
	}
	// a whole command line inside one argv element, e.g. ["pytest -q"], is not an executable name
	if len(argv) > 0 && strings.Contains(fmt.Sprint(argv[0]), " ") {
		parts := make([]string, len(argv))
		for i, p := range argv {
			parts[i] = fmt.Sprint(p)
		}
		return strings.Join(parts, " ")
	}
	return cmd
}

func ShellCmd(args map[string]any) string {
	switch c := Unwrap(args["cmd"]).(type) {
	case []any:
		parts := make([]string, len(c))
		for i, p := range c {
			parts[i] = fmt.Sprint(p)
		}
		return strings.Join(parts, " ")
	case string:
		return c
	}
	return ""
}

// Split is a small POSIX word splitter; it fails on unbalanced quotes.
func Split(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words, inWord = append(words, cur.String()), false
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, true
}

func stripAssignments(words []string) []string {
	var out []string
	for _, w := range words {
		if !assignRe.MatchString(w) {
			out = append(out, w)
		}
	}
	return out
}

// Unrunnable is an error when the command's program does not exist, so a garbled call never reaches the approval prompt.
func Unrunnable(cmd string, lookPath func(string) bool) string {
	words, ok := Split(cmd)
	if !ok {
		return ""
	}
	if len(words) > 0 && strings.Contains(words[0], "=") {
		words = stripAssignments(words)
	}
	if len(words) > 0 && words[0] == "ls" {
		for _, w := range words[1:] {
			if w == "--recursive" || (strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.Contains(w, "R")) {
				return "error: ls -R prints the whole tree. Use list_files, or search to find one."
			}
		}
	}
	if len(words) == 0 || words[0] == "" {
		return "error: empty command. Send a real command line such as pytest -q or git status."
	}
	win := false
	for _, b := range winBuiltins {
		win = win || (runtime.GOOS == "windows" && b == words[0])
	}
	if builtins[words[0]] || win || strings.Contains(words[0], "/") || lookPath(words[0]) {
		return ""
	}
	return fmt.Sprintf("error: command not found: %s. Send a real command line such as pytest -q or git status.", words[0])
}

// A private run promises nothing leaves the machine; this is a best-effort block on common network tools, not a sandbox.
var (
	networkPrograms = map[string]bool{"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true, "rsync": true, "nc": true, "ncat": true, "telnet": true, "ftp": true, "dig": true, "nslookup": true, "ping": true, "npx": true}
	networkSubs     = map[string][]string{
		"git": {"push", "pull", "fetch", "clone"}, "pip": {"install", "download"}, "pip3": {"install", "download"}, "uv": {"add", "sync", "pip"},
		"npm": {"install", "i", "ci", "publish", "exec"}, "pnpm": {"install", "i", "add", "dlx"}, "yarn": {"install", "add"},
		"go": {"get", "install", "mod"}, "cargo": {"install", "add", "fetch"}, "brew": {"install", "upgrade", "update"},
	}
)

// NetworkCommand is an error when a command would use the network, for private runs.
func NetworkCommand(cmd string) string {
	for _, part := range shellSplit.Split(cmd, -1) {
		words, ok := Split(part)
		if !ok {
			continue
		}
		words = stripAssignments(words)
		if len(words) == 0 {
			continue
		}
		prog := filepath.Base(words[0])
		blocked := networkPrograms[prog]
		for _, w := range words[1:min(len(words), 3)] {
			for _, s := range networkSubs[prog] {
				blocked = blocked || w == s
			}
		}
		if blocked {
			return fmt.Sprintf("error: private mode blocks network commands (%s). Work offline with the files already in the repo.", prog)
		}
	}
	return ""
}

func NeedsApproval(name string, args map[string]any) bool {
	switch name {
	case "read_file", "search", "list_files", "web_search", "ask_user":
		return false
	case "run_shell":
		return !safeCmd.MatchString(ShellCmd(args))
	}
	return true
}

// Describe is the approval prompt text: first line is the action, the rest is the diff.
func Describe(name string, args map[string]any) string {
	switch name {
	case "edit_file":
		return fmt.Sprintf("edit %v\n- %v\n+ %v", args["path"], args["old"], args["new"])
	case "write_file":
		return fmt.Sprintf("write %v (%d bytes)", args["path"], len(fmt.Sprint(args["content"])))
	}
	return "shell: " + ShellCmd(args)
}

func fn(name, desc string, props map[string]any, required []string) map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name": name, "description": desc,
		"parameters": map[string]any{"type": "object", "properties": props, "required": required},
	}}
}

var (
	s = map[string]any{"type": "string"}
	i = map[string]any{"type": "integer"}
)

var writeTools = map[string]bool{"edit_file": true, "write_file": true}

// Schemas are the tool definitions sent to the model. read-only drops the write tools; web adds web_search.
func Schemas(web, readOnly bool) []map[string]any {
	all := []map[string]any{
		fn("list_files", "List repo files (skips .git, node_modules, venvs).", map[string]any{"path": s}, []string{}),
		fn("read_file", "Read lines start..end (max 150).", map[string]any{"path": s, "start": i, "end": i}, []string{"path"}),
		fn("search", "Regex search, 5 hits per file.", map[string]any{"pattern": s, "path": s}, []string{"pattern"}),
		fn("edit_file", "Replace one unique exact string.", map[string]any{"path": s, "old": s, "new": s}, []string{"path", "old", "new"}),
		fn("write_file", "Create or overwrite a file.", map[string]any{"path": s, "content": s}, []string{"path", "content"}),
		fn("run_shell", "Run a "+Shell()+" command (tests, git). Use list_files, not ls or dir.",
			map[string]any{"cmd": map[string]any{"anyOf": []any{s, map[string]any{"type": "array", "items": s}}}}, []string{"cmd"}),
	}
	var out []map[string]any
	for _, t := range all {
		name := t["function"].(map[string]any)["name"].(string)
		if !(readOnly && writeTools[name]) {
			out = append(out, t)
		}
	}
	if web {
		out = append(out, fn("web_search", "Web search for current docs. Only if the repo cannot answer.", map[string]any{"query": s, "max_results": i}, []string{"query"}))
	}
	return out
}

// AskSchema lets the model put a multiple-choice question to the user instead of guessing.
func AskSchema() map[string]any {
	return fn("ask_user", "Ask the user to choose between options when the task is ambiguous.",
		map[string]any{"question": s, "options": map[string]any{"type": "array", "items": s}}, []string{"question", "options"})
}
