// Package gitignore keeps slendertoken's per-repo state folder out of git.
package gitignore

import (
	"os"
	"path/filepath"
	"strings"
)

// Entry is the line added to .gitignore; without a leading slash it matches the folder at any depth.
const Entry = ".slendertoken/"

// Root returns the nearest folder at or above dir that holds a .git entry (a folder, or a file for worktrees
// and submodules), or "" when dir is not inside a git project.
func Root(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Ensure adds Entry to the .gitignore at the git root above dir unless it is already listed.
// It does nothing outside a git project.
func Ensure(dir string) error {
	root := Root(dir)
	if root == "" {
		return nil
	}
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	for _, line := range strings.Split(text, "\n") {
		switch strings.TrimSpace(line) {
		case ".slendertoken", ".slendertoken/", "/.slendertoken", "/.slendertoken/":
			return nil
		}
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(path, []byte(text+Entry+"\n"), 0o644)
}
