package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

const longAnswer = "## Summary\n\nThis repo is a **small shop library**. The main pieces are `pricing.py`, which applies discounts, and `cart.py`, which totals an order. The discount code takes a percentage and subtracts it from the total, which is wrong when the percentage is meant to scale the price.\n\n1. Fixed `apply_discount` so it multiplies by `(1 - pct / 100)`.\n2. Added a test for a 10 percent discount on 100.\n\n- Bullet one with a long line that goes on and on to check that wrapping inside a list item stays aligned under the first word\n- Bullet two\n\n```python\ndef apply_discount(total, pct):\n    return total * (1 - pct / 100)\n```\n\nDone. Run `pytest -q` to confirm."

func TestAnswerWrapsOnceToTheTranscriptColumn(t *testing.T) {
	for _, width := range []int{80, 100, 130, 200} {
		m := &Model{width: width, height: 40, side: true}
		block := m.answerBlock(longAnswer)
		col := m.mainW() - 2
		rendered := lipgloss.NewStyle().Width(max(20, col)).Render(block)
		if got, want := strings.Count(rendered, "\n"), strings.Count(block, "\n"); got != want {
			t.Errorf("width %d: the transcript wrapped the answer again (%d lines became %d)", width, want+1, got+1)
		}
		for _, line := range strings.Split(block, "\n") {
			if w := lipgloss.Width(line); w > col {
				t.Errorf("width %d: line is %d wide, column is %d: %q", width, w, col, line)
				break
			}
		}
	}
}

func TestInlineCodeHasNoPaddingSpaces(t *testing.T) {
	m := &Model{width: 120, height: 40}
	if out := ansi.Strip(m.answerBlock("Found `apply_discount` in pricing.py.")); !strings.Contains(out, "Found apply_discount in pricing.py.") {
		t.Fatalf("got %q", out)
	}
}
