package gitignore

import (
	"os"
	"path/filepath"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEnsureAddsAtGitRootFromSubfolder(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	sub := filepath.Join(root, "a", "b")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/"), 0o644)
	if err := Ensure(sub); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, ".gitignore")); got != "node_modules/\n.slendertoken/\n" {
		t.Fatalf("got %q", got)
	}
	if err := Ensure(sub); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, ".gitignore")); got != "node_modules/\n.slendertoken/\n" {
		t.Fatalf("added twice: %q", got)
	}
}

func TestEnsureCreatesFileAndRespectsExisting(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere"), 0o644)
	if err := Ensure(root); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, ".gitignore")); got != ".slendertoken/\n" {
		t.Fatalf("got %q", got)
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.slendertoken\n"), 0o644)
	Ensure(root)
	if got := read(t, filepath.Join(root, ".gitignore")); got != "/.slendertoken\n" {
		t.Fatalf("changed existing entry: %q", got)
	}
}

func TestEnsureOutsideGitDoesNothing(t *testing.T) {
	dir := t.TempDir()
	if Root(dir) != "" {
		t.Skip("temp dir is inside a git project")
	}
	if err := Ensure(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("created .gitignore outside git")
	}
}
