"""Render bench/results.json as bench/token-efficiency.svg (no dependencies).

  .venv/bin/python bench/chart.py

The SVG is committed and embedded in the README, so GitHub renders it without
running anything. Re-run this after a new benchmark.
"""

import json
from pathlib import Path

HERE = Path(__file__).parent
OUT = HERE / "token-efficiency.svg"

W, H = 1000, 870
LEAN = "#10b981"      # emerald: leantoken
NAIVE = "#94a3b8"     # slate: naive agent
FAIL = "#f87171"      # red: request rejected (413)
INK = "#111827"
MUTED = "#6b7280"
GRID = "#eef1f4"
CARD = "#ffffff"
BORDER = "#e5e7eb"


def esc(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def rect(x, y, w, h, fill, rx=0, extra=""):
    return f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="{rx}" fill="{fill}" {extra}/>'


def text(x, y, s, size=12, fill=INK, anchor="start", weight="400", extra=""):
    return (f'<text x="{x:.1f}" y="{y:.1f}" font-size="{size}" fill="{fill}" '
            f'text-anchor="{anchor}" font-weight="{weight}" {extra}>{esc(s)}</text>')


def line(x1, y1, x2, y2, stroke, width=1, dash=""):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    return (f'<line x1="{x1:.1f}" y1="{y1:.1f}" x2="{x2:.1f}" y2="{y2:.1f}" '
            f'stroke="{stroke}" stroke-width="{width}"{d}/>')


def fmt(n: int) -> str:
    return f"{n:,}"


def axis(x0, x1, y_base, y_top, vmax, step):
    """Horizontal gridlines plus left-hand value labels."""
    out = []
    v = 0
    while v <= vmax:
        y = y_base - (y_base - y_top) * (v / vmax)
        out.append(line(x0, y, x1, y, GRID if v else BORDER, 1))
        if v:
            out.append(text(x0 - 12, y + 4, f"{v // 1000}k", 11, MUTED, anchor="end"))
        v += step
    return "".join(out)


def grouped_bars(x0, x1, y_base, y_top, values, vmax, colors, fail=None):
    """values: list of (lean, naive-or-None). Returns (svg, centers, bar_width)."""
    n = len(values)
    slot = (x1 - x0) / n
    bw = min(46, slot * 0.32)
    gap = bw * 0.18
    out = []
    centers = []
    for i, (lean, naive) in enumerate(values):
        cx = x0 + slot * (i + 0.5)
        centers.append(cx)
        # leantoken bar (left)
        lh = (y_base - y_top) * (lean / vmax)
        lx = cx - gap / 2 - bw
        out.append(rect(lx, y_base - lh, bw, lh, colors[0], rx=4))
        out.append(text(lx + bw / 2, y_base - lh - 8, fmt(lean), 11, INK, anchor="middle", weight="600"))
        # naive bar (right)
        if naive is None:
            out.append(text(cx + gap / 2 + bw / 2, y_base - 26, "n/a", 11, MUTED, anchor="middle"))
        else:
            nh = (y_base - y_top) * (naive / vmax)
            nx = cx + gap / 2
            bad = bool(fail and i in fail)
            out.append(rect(nx, y_base - nh, bw, nh, colors[2] if bad else colors[1], rx=4))
            out.append(text(nx + bw / 2, y_base - nh - 8, fmt(naive), 11,
                            FAIL if bad else INK, anchor="middle", weight="600"))
    return "".join(out), centers, bw


def main() -> None:
    r = json.loads((HERE / "results.json").read_text())
    coding = r["coding"]
    tasks = list(coding["lean"].keys())

    # ---- totals ----
    c_lean = [coding["lean"][t]["tokens"] for t in tasks]
    c_naive = [coding["naive"][t]["tokens"] for t in tasks]
    tl, tn = sum(c_lean), sum(c_naive)
    cl = sum(coding["lean"][t]["cost_usd"] for t in tasks)
    cn = sum(coding["naive"][t]["cost_usd"] for t in tasks)

    lean_runs = r["session"]["lean"]
    naive_runs = r["session"]["naive"]
    turns = max(len(lean_runs), len(naive_runs))
    lean_cum, naive_cum = [], []
    a = b = 0
    for i in range(turns):
        a += lean_runs[i]["tokens"] if i < len(lean_runs) else 0
        lean_cum.append(a)
        if i < len(naive_runs):
            b += naive_runs[i]["tokens"]
            naive_cum.append(b)
        else:
            naive_cum.append(None)
    fail_idx = {i for i, run in enumerate(naive_runs) if not run["passed"]}

    # ---- layout ----
    x0, x1 = 76, 940
    p1_base, p1_top, p1_vmax = 395, 170, 60000
    p2_base, p2_top, p2_vmax = 755, 580, 50000

    font = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif"
    svg = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" '
           f'width="{W}" height="{H}" font-family="{font}" '
           f'role="img" aria-label="leantoken token efficiency versus a naive agent">',
           '<title>leantoken vs naive agent: tokens per task</title>',
           f'<rect x="0" y="0" width="{W}" height="{H}" rx="16" fill="{CARD}" stroke="{BORDER}"/>']

    svg.append(text(40, 52, "Token efficiency vs a naive agent", 24, INK, weight="700"))
    svg.append(text(40, 76, "Same model, same tasks. Tokens are the API's own usage.", 13, MUTED))

    # legend (top right, clear of the subtitle)
    lx = 742
    svg.append(rect(lx, 44, 12, 12, LEAN, rx=3))
    svg.append(text(lx + 20, 55, "leantoken", 13, INK))
    svg.append(rect(lx + 112, 44, 12, 12, NAIVE, rx=3))
    svg.append(text(lx + 132, 55, "naive", 13, INK))

    # ---- panel 1: coding ----
    svg.append(text(40, 118, "Coding: fix a seeded bug (lower is better)", 15, INK, weight="600"))
    svg.append(text(40, 138, "Both arms fixed 4 of 4; pass means pytest is green afterwards.", 12, MUTED))
    svg.append(axis(x0, x1, p1_base, p1_top, p1_vmax, 10000))

    vals = list(zip(c_lean, c_naive)) + [(tl, tn)]
    bars, centers, _ = grouped_bars(x0, x1, p1_base, p1_top, vals, p1_vmax, (LEAN, NAIVE, NAIVE))
    svg.append(bars)

    for i, (cx, lab) in enumerate(zip(centers, tasks + ["total"])):
        weight = "700" if lab == "total" else "500"
        svg.append(text(cx, p1_base + 22, lab, 12, INK, anchor="middle", weight=weight))
        l, n = vals[i]
        # floor, not round: matches the README and stays conservative
        svg.append(text(cx, p1_base + 38, f"{int((n - l) / n * 100)}% fewer", 11, LEAN,
                        anchor="middle", weight="600"))
    svg.append(line(x0, p1_base, x1, p1_base, BORDER, 1))
    svg.append(text(40, 478, f"Coding total: {fmt(tl)} vs {fmt(tn)} tokens  ·  ${cl:.4f} vs ${cn:.4f}",
                    13, INK, weight="600"))

    # ---- panel 2: session ----
    svg.append(text(40, 525, "Session: 8 follow-up questions (cumulative tokens, lower is better)",
                    15, INK, weight="600"))
    svg.append(text(40, 545, "The naive arm resends the whole transcript each turn, so it grows faster and hits the limit first.",
                    12, MUTED))
    svg.append(axis(x0, x1, p2_base, p2_top, p2_vmax, 10000))

    bars2, centers2, _ = grouped_bars(x0, x1, p2_base, p2_top,
                                      list(zip(lean_cum, naive_cum)), p2_vmax,
                                      (LEAN, NAIVE, FAIL), fail=fail_idx)
    svg.append(bars2)

    for i, cx in enumerate(centers2):
        svg.append(text(cx, p2_base + 22, f"turn {i + 1}", 12, MUTED, anchor="middle"))
    svg.append(line(x0, p2_base, x1, p2_base, BORDER, 1))

    # dashed divider where the naive arm stopped, annotated in the empty right corner
    stop_x = (centers2[len(naive_runs) - 1] + centers2[len(naive_runs)]) / 2
    svg.append(line(stop_x, p2_top - 6, stop_x, p2_base, MUTED, 1, dash="4 4"))
    note_x = (centers2[len(naive_runs)] + centers2[-1]) / 2
    svg.append(text(note_x, p2_top + 34, "naive stopped", 11, FAIL, anchor="middle", weight="600"))
    svg.append(text(note_x, p2_top + 50, "Groq 413: request too large", 10, MUTED, anchor="middle"))

    svg.append(text(40, 805, f"By turn 8: {fmt(lean_cum[-1])} tokens vs the naive arm's {fmt(naive_cum[5])} before it failed",
                    13, INK, weight="600"))
    svg.append(text(40, 827, "Source: bench/results.json  ·  one run per arm, groq/openai/gpt-oss-120b, free tier  ·  indicative, not general",
                    11, MUTED))

    svg.append("</svg>")
    OUT.write_text("".join(svg))
    print("wrote", OUT)


if __name__ == "__main__":
    main()
