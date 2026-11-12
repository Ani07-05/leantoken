package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLineDeltaIgnoresUnchangedEdges(t *testing.T) {
	cases := []struct {
		name, args string
		add, del   int
	}{
		{"edit_file", `{"old":"a\nb\nc","new":"a\nB\nX\nc"}`, 2, 1},
		{"edit_file", `{"old":"return total - pct","new":"return total * (1 - pct / 100)"}`, 1, 1},
		{"edit_file", `{"old":"x","new":""}`, 1, 1},
		{"write_file", `{"path":"f","content":"a\nb\nc\n"}`, 3, 0},
		{"write_file", `{"path":"f","content":"a"}`, 1, 0},
	}
	for _, c := range cases {
		if add, del := lineDelta(c.name, c.args); add != c.add || del != c.del {
			t.Errorf("%s %s: got +%d -%d, want +%d -%d", c.name, c.args, add, del, c.add, c.del)
		}
	}
}

func TestEveryThemeResolvesByName(t *testing.T) {
	for _, p := range Palettes {
		if got, ok := PaletteNamed(p.Name); !ok || got.Accent != p.Accent {
			t.Errorf("%s not found", p.Name)
		}
	}
	if _, ok := PaletteNamed("nope"); ok {
		t.Error("unknown theme should not resolve")
	}
}

func TestEditRowsNumberTheChangeAndKeepContext(t *testing.T) {
	dir := t.TempDir()
	src := "import os\n\ndef f(total, pct):\n    return total * (1 - pct / 100)\n\nprint(f(1, 2))\n"
	if err := os.WriteFile(filepath.Join(dir, "p.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := editRows(dir, "p.py", "def f(total, pct):\n    return total - pct", "def f(total, pct):\n    return total * (1 - pct / 100)")
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%c%d", r.kind, r.num))
	}
	want := []string{" 2", " 3", "-4", "+4", " 5", " 6"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows %v, want %v", got, want)
	}
	if rows[2].text != "    return total - pct" || rows[3].text != "    return total * (1 - pct / 100)" {
		t.Fatalf("texts: %q %q", rows[2].text, rows[3].text)
	}
}

func TestEditRowsShowWholeLinesForFragmentEdits(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.py"), []byte("def f(total, pct):\n    return total * 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range editRows(dir, "p.py", "total - pct", "total * 2") {
		if r.kind != ' ' {
			got = append(got, fmt.Sprintf("%c%d%s", r.kind, r.num, r.text))
		}
	}
	want := "-2    return total - pct,+2    return total * 2"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %q want %q", strings.Join(got, ","), want)
	}
}
