package main

// chart renders results as a self-contained SVG, so GitHub shows it without running anything.

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	chartW, chartH = 1000, 870
	colLean        = "#10b981" // emerald: slendertoken
	colNaive       = "#94a3b8" // slate: naive agent
	colFail        = "#f87171" // red: run failed
	colInk         = "#111827"
	colMuted       = "#6b7280"
	colGrid        = "#eef1f4"
	colCard        = "#ffffff"
	colBorder      = "#e5e7eb"
)

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func svgRect(x, y, w, h float64, fill string, rx int) string {
	return fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="%d" fill="%s"/>`, x, y, w, h, rx, fill)
}

func svgText(x, y float64, s string, size int, fill, anchor, weight string) string {
	return fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="%d" fill="%s" text-anchor="%s" font-weight="%s">%s</text>`,
		x, y, size, fill, anchor, weight, esc(s))
}

func svgLine(x1, y1, x2, y2 float64, stroke, dash string) string {
	d := ""
	if dash != "" {
		d = fmt.Sprintf(` stroke-dasharray="%s"`, dash)
	}
	return fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"%s/>`, x1, y1, x2, y2, stroke, d)
}

func commas(n int) string {
	if n < 0 {
		return "-" + commas(-n)
	}
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// scale picks an axis top and gridline step that leave room for the value labels above the tallest bar.
func scale(vmax int) (top, step int) {
	step = 10000
	for _, s := range []int{1000, 2000, 5000, 10000, 20000, 50000, 100000, 200000, 500000} {
		step = s
		if float64(vmax)*1.15 <= float64(s*6) {
			break
		}
	}
	top = int(math.Ceil(float64(vmax)*1.15/float64(step))) * step
	return max(top, step), step
}

func axis(x0, x1, base, top float64, vmax, step int) string {
	var b strings.Builder
	for v := 0; v <= vmax; v += step {
		y := base - (base-top)*float64(v)/float64(vmax)
		c := colGrid
		if v == 0 {
			c = colBorder
		}
		b.WriteString(svgLine(x0, y, x1, y, c, ""))
		if v > 0 {
			label := fmt.Sprintf("%dk", v/1000)
			if step < 1000 || v%1000 != 0 {
				label = commas(v)
			}
			b.WriteString(svgText(x0-12, y+4, label, 11, colMuted, "end", "400"))
		}
	}
	return b.String()
}

type pair struct {
	lean, naive int
	hasLean     bool
	hasNaive    bool
	naiveFailed bool
}

// bars draws one lean/naive pair per slot and returns the slot centres.
func bars(x0, x1, base, top float64, vals []pair, vmax int) (string, []float64) {
	var b strings.Builder
	slot := (x1 - x0) / float64(len(vals))
	bw := math.Min(46, slot*0.32)
	gap := bw * 0.18
	var centers []float64
	h := func(v int) float64 { return (base - top) * float64(v) / float64(vmax) }
	for i, p := range vals {
		cx := x0 + slot*(float64(i)+0.5)
		centers = append(centers, cx)
		lx, nx := cx-gap/2-bw, cx+gap/2
		if p.hasLean {
			b.WriteString(svgRect(lx, base-h(p.lean), bw, h(p.lean), colLean, 4))
			b.WriteString(svgText(lx+bw/2, base-h(p.lean)-8, commas(p.lean), 11, colInk, "middle", "600"))
		} else {
			b.WriteString(svgText(lx+bw/2, base-26, "n/a", 11, colMuted, "middle", "400"))
		}
		if p.hasNaive {
			fill, ink := colNaive, colInk
			if p.naiveFailed {
				fill, ink = colFail, colFail
			}
			b.WriteString(svgRect(nx, base-h(p.naive), bw, h(p.naive), fill, 4))
			b.WriteString(svgText(nx+bw/2, base-h(p.naive)-8, commas(p.naive), 11, ink, "middle", "600"))
		} else {
			b.WriteString(svgText(nx+bw/2, base-26, "n/a", 11, colMuted, "middle", "400"))
		}
	}
	return b.String(), centers
}

// codingTasks lists the tasks both arms ran, in the benchmark's own order.
func codingTasks(r results) []string {
	lean, naive := r.Coding["lean"], r.Coding["naive"]
	var out []string
	seen := map[string]bool{}
	for _, b := range bugNames() {
		_, l := lean[b]
		_, n := naive[b]
		if l && n {
			out = append(out, b)
			seen[b] = true
		}
	}
	var extra []string
	for b := range lean {
		if _, n := naive[b]; n && !seen[b] {
			extra = append(extra, b)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func failReason(rows []row) string {
	for i := len(rows) - 1; i >= 0; i-- {
		e := rows[i].Error
		switch {
		case e == "":
			continue
		case strings.Contains(e, "413"):
			return "request too large (413)"
		case strings.Contains(e, "429"):
			return "rate limited (429)"
		default:
			return cut(e, 40)
		}
	}
	return "answer missed"
}

func chart(r results, source string) (string, error) {
	tasks := codingTasks(r)
	leanRuns, naiveRuns := r.Session["lean"], r.Session["naive"]
	if len(tasks) == 0 || len(leanRuns) == 0 || len(naiveRuns) == 0 {
		return "", errors.New("the chart needs coding and session runs from both arms: run `go run ./bench run` without --only or --arms")
	}

	// coding panel
	var coding []pair
	tl, tn, passL, passN := 0, 0, 0, 0
	var cl, cn float64
	for _, t := range tasks {
		l, n := r.Coding["lean"][t], r.Coding["naive"][t]
		coding = append(coding, pair{lean: l.Tokens, naive: n.Tokens, hasLean: true, hasNaive: true})
		tl, tn, cl, cn = tl+l.Tokens, tn+n.Tokens, cl+l.CostUSD, cn+n.CostUSD
		if l.Passed {
			passL++
		}
		if n.Passed {
			passN++
		}
	}
	coding = append(coding, pair{lean: tl, naive: tn, hasLean: true, hasNaive: true})

	// session panel: cumulative tokens per turn
	turns := max(len(leanRuns), len(naiveRuns))
	var session []pair
	a, b := 0, 0
	for i := range turns {
		p := pair{}
		if i < len(leanRuns) {
			a += leanRuns[i].Tokens
			p.lean, p.hasLean = a, true
		}
		if i < len(naiveRuns) {
			b += naiveRuns[i].Tokens
			p.naive, p.hasNaive = b, true
			p.naiveFailed = naiveRuns[i].Error != ""
		}
		session = append(session, p)
	}

	x0, x1 := 76.0, 940.0
	p1Base, p1Top := 395.0, 170.0
	p2Base, p2Top := 755.0, 580.0
	p1Max, p1Step := scale(max(tl, tn))
	p2Max, p2Step := scale(max(a, b))

	font := "-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif"
	var s strings.Builder
	fmt.Fprintf(&s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" font-family="%s" role="img" aria-label="slendertoken token efficiency versus a naive agent">`,
		chartW, chartH, chartW, chartH, font)
	s.WriteString(`<title>slendertoken vs naive agent: tokens per task</title>`)
	fmt.Fprintf(&s, `<rect x="0" y="0" width="%d" height="%d" rx="16" fill="%s" stroke="%s"/>`, chartW, chartH, colCard, colBorder)
	s.WriteString(svgText(40, 52, "Token efficiency vs a naive agent", 24, colInk, "start", "700"))
	s.WriteString(svgText(40, 76, "Same model, same tasks. Tokens are the API's own usage.", 13, colMuted, "start", "400"))
	lx := 742.0
	s.WriteString(svgRect(lx, 44, 12, 12, colLean, 3))
	s.WriteString(svgText(lx+20, 55, "slendertoken", 13, colInk, "start", "400"))
	s.WriteString(svgRect(lx+112, 44, 12, 12, colNaive, 3))
	s.WriteString(svgText(lx+132, 55, "naive", 13, colInk, "start", "400"))

	s.WriteString(svgText(40, 118, "Coding: fix a seeded bug (lower is better)", 15, colInk, "start", "600"))
	s.WriteString(svgText(40, 138, fmt.Sprintf("slendertoken fixed %d of %d, naive %d of %d; a fix counts when the project's tests pass afterwards.",
		passL, len(tasks), passN, len(tasks)), 12, colMuted, "start", "400"))
	s.WriteString(axis(x0, x1, p1Base, p1Top, p1Max, p1Step))
	svgBars, centers := bars(x0, x1, p1Base, p1Top, coding, p1Max)
	s.WriteString(svgBars)
	labels := append(append([]string{}, tasks...), "total")
	for i, cx := range centers {
		weight := "500"
		if labels[i] == "total" {
			weight = "700"
		}
		s.WriteString(svgText(cx, p1Base+22, labels[i], 12, colInk, "middle", weight))
		l, n := coding[i].lean, coding[i].naive
		if n > 0 {
			// floor, not round: stays conservative
			pct := (n - l) * 100 / n
			word, ink := "fewer", colLean
			if pct < 0 {
				pct, word, ink = -pct, "more", colFail
			}
			s.WriteString(svgText(cx, p1Base+38, fmt.Sprintf("%d%% %s", pct, word), 11, ink, "middle", "600"))
		}
	}
	s.WriteString(svgLine(x0, p1Base, x1, p1Base, colBorder, ""))
	s.WriteString(svgText(40, 478, fmt.Sprintf("Coding total: %s vs %s tokens  ·  $%.4f vs $%.4f", commas(tl), commas(tn), cl, cn), 13, colInk, "start", "600"))

	s.WriteString(svgText(40, 525, fmt.Sprintf("Session: %d follow-up questions (cumulative tokens, lower is better)", turns), 15, colInk, "start", "600"))
	s.WriteString(svgText(40, 545, "The naive arm resends the whole transcript each turn, so it grows faster.", 12, colMuted, "start", "400"))
	s.WriteString(axis(x0, x1, p2Base, p2Top, p2Max, p2Step))
	svgBars2, centers2 := bars(x0, x1, p2Base, p2Top, session, p2Max)
	s.WriteString(svgBars2)
	for i, cx := range centers2 {
		s.WriteString(svgText(cx, p2Base+22, fmt.Sprintf("turn %d", i+1), 12, colMuted, "middle", "400"))
	}
	s.WriteString(svgLine(x0, p2Base, x1, p2Base, colBorder, ""))

	summary := fmt.Sprintf("By turn %d: %s tokens vs the naive arm's %s", turns, commas(a), commas(b))
	if len(naiveRuns) < turns {
		// dashed divider where the naive arm stopped, annotated in the empty space after it
		n := len(naiveRuns)
		stopX := (centers2[n-1] + centers2[n]) / 2
		s.WriteString(svgLine(stopX, p2Top-6, stopX, p2Base, colMuted, "4 4"))
		noteX := (centers2[n] + centers2[len(centers2)-1]) / 2
		s.WriteString(svgText(noteX, p2Top+34, "naive stopped", 11, colFail, "middle", "600"))
		s.WriteString(svgText(noteX, p2Top+50, failReason(naiveRuns), 10, colMuted, "middle", "400"))
		summary += fmt.Sprintf(" before it failed at turn %d", n)
	}
	s.WriteString(svgText(40, 805, summary, 13, colInk, "start", "600"))
	s.WriteString(svgText(40, 827, fmt.Sprintf("Source: %s  ·  one run per arm, %s  ·  indicative, not general", source, r.Model), 11, colMuted, "start", "400"))
	s.WriteString("</svg>")
	return s.String(), nil
}
