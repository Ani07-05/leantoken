"""Voxel-style pixel animals, one per tool. Sprites are tiny grids; bevels and outlines are derived from neighbouring pixels,
drawn as SVG rects and rasterised with resvg so the terminal can show them as crisp images."""

import io
import math
from functools import lru_cache

import resvg_py
from PIL import Image as PILImage

W, H = 40, 16
RENDER_SCALE = 10
OX, OY = 2, 3  # animal origin, leaving room for the outline and the bob

SCENE_ANIMAL = {"think": "bunny", "type": "frog", "search": "cat", "read": "owl", "shell": "penguin", "web": "octopus"}
TOOL_SCENE = {
    "read_file": "read", "search": "search", "list_files": "search", "web_search": "web",
    "edit_file": "type", "write_file": "type", "run_shell": "shell",
}
VERBS = {
    "think": ("Lollygagging", "Dillydallying", "Noodling", "Pondering", "Percolating", "Mulling"),
    "type": ("Typing", "Hammering keys", "Scribbling"),
    "search": ("Rummaging", "Sniffing around", "Hunting"),
    "read": ("Reading", "Skimming", "Squinting at"),
    "shell": ("Running", "Poking the shell", "Executing"),
    "web": ("Surfing the web", "Spinning the globe", "Googling"),
}

# sprite rows plus palette; K (eyes) stays flat, everything else gets bevels
SPRITES = {
    "frog": (("..GGG..GGG..", ".GWKG..GWKG.", ".GGGGGGGGGG.", "GGGGGGGGGGGG", "GPGGGGGGGGPG", "GGGMMMMMMGGG",
              "GGGGGGGGGGGG", ".GGGGGGGGGG.", ".GG..GG..GG.", ".GG......GG."),
             {"G": "#5fbf7f", "W": "#f4f4f4", "K": "#1b1f2a", "P": "#ff9ab3", "M": "#2b6b43"}, "G"),
    "bunny": (("..BB....BB..", "..BI....IB..", "..BI....IB..", ".BBBBBBBBBB.", "BBKBBBBBBKBB", "BPBBBPPBBBPB",
               "BBBBBBBBBBBB", ".BBBBBBBBBB.", ".BB.BBBB.BB.", ".BB......BB."),
              {"B": "#f1ebe5", "I": "#ffb3c6", "K": "#1b1f2a", "P": "#ff8fab"}, "B"),
    "cat": (("A..........A", "AIA......AIA", "AAAAAAAAAAAA", "AAYKAAAAKYAA", "AAAAAPPAAAAA", "AAPAAAAAAAPA",
             "AAAAAAAAAAAA", ".AAAAAAAAAA.", ".AA.AAAA.AA.", ".AA......AA."),
            {"A": "#8d9bb5", "I": "#ffb3c6", "Y": "#f5d76e", "K": "#1b1f2a", "P": "#ff8fab"}, "A"),
    "owl": (("TT........TT", "TTTTTTTTTTTT", "TWWWTTTTWWWT", "TWKWTTTTWKWT", "TTTTTTOOTTTT", "TTSSSSSSSSTT",
             "TTSDSDSDSDTT", ".TSSSSSSSST.", ".TT......TT.", "..OO....OO.."),
            {"T": "#c9a26b", "W": "#fff6e2", "K": "#1b1f2a", "O": "#f5a742", "S": "#e6c48f", "D": "#b68d54"}, "T"),
    "penguin": (("...NNNNNN...", "..NNNNNNNN..", ".NNWKNNWKNN.", ".NNNNOONNNN.", "NNNWWWWWWNNN", "NNWWWWWWWWNN",
                 "NNWWWWWWWWNN", ".NWWWWWWWWN.", "..NWWWWWWN..", "..OOO..OOO.."),
                {"N": "#4a5fa8", "W": "#f4f0ea", "K": "#1b1f2a", "O": "#f5a742"}, "N"),
    "octopus": (("...VVVVVV...", "..VVLLVVVV..", ".VVLVVVVVVV.", "VVVVVVVVVVVV", "VVKVVVVVVKVV", "VPVVVVVVVVPV",
                 "VVVVVMMVVVVV", "VVVVVVVVVVVV", "V.VV.VV.VV.V", "V.V..V..V..V"),
                {"V": "#9b7be0", "L": "#c7b3f5", "K": "#1b1f2a", "P": "#ff8fab", "M": "#4a2f96"}, "V"),
}
BODY = {"bunny": "#f1ebe5", "frog": "#5fbf7f", "cat": "#8d9bb5", "owl": "#c9a26b", "penguin": "#4a5fa8", "octopus": "#9b7be0"}
FLAT = "#1b1f2a"


def shade(hex_color: str, k: float) -> str:
    """k > 0 lightens toward white, k < 0 darkens toward black."""
    r, g, b = (int(hex_color[i:i + 2], 16) for i in (1, 3, 5))
    target = 255 if k > 0 else 0
    return "#%02x%02x%02x" % tuple(round(c + (target - c) * abs(k)) for c in (r, g, b))


Cells = dict[tuple[int, int], str]


def animal_cells(name: str, t: int) -> Cells:
    rows, palette, face = SPRITES[name]
    blink = t % 40 > 37
    bob = 1 if (t // 8) % 2 else 0
    cells: Cells = {}
    for y, row in enumerate(rows):
        for x, ch in enumerate(row):
            if ch == ".":
                continue
            if blink and ch in "KY":
                ch = face
            if name == "octopus" and y >= 8 and (t // 6) % 2 and ch == "V":
                x += 1 if y == 9 else 0
            cells[(OX + x, OY + y + bob)] = palette[ch]
    return cells


def rect(x: float, y: float, w: float, h: float, fill: str, opacity: float = 1) -> str:
    return f'<rect x="{x}" y="{y}" width="{w}" height="{h}" fill="{fill}"' + (f' opacity="{opacity}"' if opacity < 1 else "") + "/>"


def voxels(cells: Cells, outline: bool = True) -> str:
    """Each pixel gets a lit top edge when nothing lighter sits above it and a shaded bottom edge when nothing sits below."""
    out = []
    if outline:
        for (x, y), c in cells.items():
            for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                n = (x + dx, y + dy)
                if n not in cells:
                    out.append(rect(n[0], n[1], 1, 1, shade(c, -0.62)))
    for (x, y), c in sorted(cells.items(), key=lambda i: (i[0][1], i[0][0])):
        out.append(rect(x, y, 1, 1, c))
        if c == FLAT:
            continue
        if cells.get((x, y - 1)) != c:
            out.append(rect(x, y, 1, 0.3, shade(c, 0.32)))
        if cells.get((x, y + 1)) != c:
            out.append(rect(x, y + 0.7, 1, 0.3, shade(c, -0.28)))
        if cells.get((x - 1, y)) is None:
            out.append(rect(x, y, 0.2, 1, shade(c, 0.18)))
        if cells.get((x + 1, y)) is None:
            out.append(rect(x + 0.8, y, 0.2, 1, shade(c, -0.18)))
    return "".join(out)


def fill(cells: Cells, x0: int, y0: int, w: int, h: int, color: str) -> None:
    for y in range(y0, y0 + h):
        for x in range(x0, x0 + w):
            cells[(x, y)] = color


def prop_cells(scene: str, t: int) -> Cells:
    c: Cells = {}
    if scene == "type":
        fill(c, 19, 3, 12, 8, "#343f63")
        fill(c, 20, 4, 10, 6, "#58b8ec")
        for i in range(3):
            fill(c, 21, 5 + i * 2, 3 + (t // 3 + i * 2) % 5, 1, "#e4f8ff")
        fill(c, 17, 11, 16, 2, "#b3bdd6")
        fill(c, 18, 13, 14, 1, "#8892ac")
        k = (t // 2) % 6
        c[(19 + k * 2, 11)] = "#ffffff"
    elif scene == "search":
        fill(c, 19, 3, 14, 11, "#eef1f9")
        for i, wd in enumerate((10, 7, 10, 6, 9)):
            fill(c, 21, 4 + i * 2, wd, 1, "#b8c2da")
        sx = 22 + round(5 * math.sin(t / 4))
        ring = ((1, 0), (2, 0), (3, 0), (0, 1), (4, 1), (0, 2), (4, 2), (0, 3), (4, 3), (1, 4), (2, 4), (3, 4))
        for dx, dy in ring:
            c[(sx + dx, 5 + dy)] = "#f0b93a"
        for dx in range(1, 4):
            for dy in range(1, 4):
                c[(sx + dx, 5 + dy)] = "#bfe7ff"
        c[(sx + 5, 10)] = c[(sx + 6, 11)] = c[(sx + 7, 12)] = "#8a6a3a"
    elif scene == "read":
        flip = (t // 8) % 2
        fill(c, 17, 5, 7, 9, "#fffdf6")
        fill(c, 25, 5, 7, 9, "#f2ecdc")
        fill(c, 24, 5, 1, 9, "#c9a26b")
        for i in range(4):
            fill(c, 18, 7 + i * 2, 5, 1, "#c3ccdf")
            if not flip or i < 2:
                fill(c, 26, 7 + i * 2, 5, 1, "#c3ccdf")
        if flip:
            fill(c, 25, 5, 7, 9, "#fffdf6")
            for i in range(2):
                fill(c, 26, 7 + i * 2, 5, 1, "#c3ccdf")
    elif scene == "shell":
        fill(c, 18, 3, 15, 11, "#1b2238")
        fill(c, 18, 3, 15, 2, "#2c3554")
        c[(19, 3)], c[(21, 3)], c[(23, 3)] = "#ff6b6b", "#ffd166", "#5ad08a"
        for i in range(2):
            fill(c, 20, 6 + i * 2, 4 + (t // 4 + i * 3) % 7, 1, "#6e7aa6")
        c[(20, 10)], c[(21, 11)], c[(20, 12)] = "#5ad08a", "#5ad08a", "#5ad08a"
        fill(c, 23, 12, 3, 1, "#5ad08a")
        if t % 8 < 5:
            fill(c, 27, 10, 2, 3, "#e7ffe9")
    elif scene == "web":
        cx, cy, r = 26, 8, 5.5
        shift = (t // 2) % 12
        for y in range(16):
            for x in range(40):
                if (x - cx) ** 2 + (y - cy) ** 2 <= r * r:
                    land = math.sin((x + shift) * 0.9) + math.cos(y * 0.85) > 0.85
                    c[(x, y)] = "#5ad08a" if land else "#3f72e8"
        c[(23, 4)] = "#a8c8ff"
        if t % 12 < 6:
            c[(34, 3)] = "#f5d76e"
    else:
        for i in range(3):
            size = 1 + i
            phase = (t // 5 + i) % 3
            fill(c, 19 + i * 4, 10 - i * 3 - phase // 2, size, size, "#ffffff" if i == 2 else "#cfd6e6")
        if t % 12 < 8:
            c[(31, 3)], c[(30, 4)], c[(31, 4)], c[(32, 4)], c[(31, 5)] = ("#f5d76e",) * 5
    return c


SPARK = ("#ffffff", "#f5d76e", "#7fd4ff", "#b79cf0", "#ff9ab3")


def materialise(cells: Cells, grow: float) -> Cells:
    """Dungeon-spawner reveal: the sprite builds from the feet up behind a white-hot scan line."""
    if grow >= 1:
        return cells
    top, bottom = OY - 1, OY + 11
    edge = bottom - grow * (bottom - top)
    out: Cells = {}
    for (x, y), c in cells.items():
        if y + 1 <= edge:
            continue
        out[(x, y)] = shade(c, 0.75) if y <= edge + 1 else c
    return out


def sparks(t: int, grow: float) -> str:
    """A burst of motes around the spawn point that thins out as the sprite solidifies."""
    out = []
    for i in range(round(14 * (1 - grow))):
        h = (i * 2654435761 + t * 40503) & 0xFFFFFFFF
        x = OX + (h % 17) - 2
        y = OY + 11 - ((h >> 8) % 14) - (1 - grow) * 2
        out.append(rect(x, round(y), 1, 1, SPARK[(h >> 16) % len(SPARK)], 0.9))
    return "".join(out)


def svg(scene: str, t: int, grow: float = 1.0) -> str:
    """One frame at step t. grow < 1 is the spawn (or, run backwards, despawn) effect."""
    shadow = f'<ellipse cx="{OX + 6}" cy="{OY + 11.2}" rx="6.5" ry="0.9" fill="#000" opacity="{0.35 * grow}"/>'
    sprite = materialise(animal_cells(SCENE_ANIMAL[scene], t), grow)
    props = prop_cells(scene, t) if grow > 0.5 else {}
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" shape-rendering="crispEdges">'
            f'{shadow}{voxels(sprite)}{voxels(props, outline=False)}{sparks(t, grow)}</svg>')


@lru_cache(maxsize=512)
def frame(scene: str, t: int, grow: float = 1.0) -> PILImage.Image:
    png = resvg_py.svg_to_bytes(svg_string=svg(scene, t, grow), width=W * RENDER_SCALE, height=H * RENDER_SCALE)
    return PILImage.open(io.BytesIO(png)).convert("RGBA")


def verb(scene: str, tick: int) -> str:
    words = VERBS[scene]
    return words[(tick // 30) % len(words)]


def color(scene: str) -> str:
    return BODY[SCENE_ANIMAL[scene]]
