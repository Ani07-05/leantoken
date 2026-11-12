// Package art draws the pixel animals. Sprites are tiny grids; outlines and bevels come from neighbouring pixels.
// PNG renders a crisp frame for terminals with an image protocol; Frame is the half-block fallback for the rest.
package art

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
)

const (
	W, H   = 40, 16
	OX, OY = 2, 3 // animal origin, leaving room for the outline
	eyes   = "#1b1f2a"
)

var sceneAnimal = map[string]string{"think": "bunny", "type": "frog", "search": "cat", "read": "owl", "shell": "penguin", "web": "octopus"}

// ToolScene maps a tool name to the scene (animal and prop) that plays while it runs.
var ToolScene = map[string]string{
	"read_file": "read", "search": "search", "list_files": "search", "web_search": "web", "ask_user": "think",
	"edit_file": "type", "write_file": "type", "run_shell": "shell",
}

var verbs = map[string][]string{
	"think":  {"Lollygagging", "Dillydallying", "Noodling", "Pondering", "Percolating", "Mulling"},
	"type":   {"Typing", "Hammering keys", "Scribbling"},
	"search": {"Rummaging", "Sniffing around", "Hunting"},
	"read":   {"Reading", "Skimming", "Squinting at"},
	"shell":  {"Running", "Poking the shell", "Executing"},
	"web":    {"Surfing the web", "Spinning the globe", "Googling"},
}

type sprite struct {
	rows    []string
	palette map[byte]string
	face    byte
}

var sprites = map[string]sprite{
	"frog": {[]string{"..GGG..GGG..", ".GWKG..GWKG.", ".GGGGGGGGGG.", "GGGGGGGGGGGG", "GPGGGGGGGGPG", "GGGMMMMMMGGG", "GGGGGGGGGGGG", ".GGGGGGGGGG.", ".GG..GG..GG.", ".GG......GG."},
		map[byte]string{'G': "#5fbf7f", 'W': "#f4f4f4", 'K': eyes, 'P': "#ff9ab3", 'M': "#2b6b43"}, 'G'},
	"bunny": {[]string{"..BB....BB..", "..BI....IB..", "..BI....IB..", ".BBBBBBBBBB.", "BBKBBBBBBKBB", "BPBBBPPBBBPB", "BBBBBBBBBBBB", ".BBBBBBBBBB.", ".BB.BBBB.BB.", ".BB......BB."},
		map[byte]string{'B': "#f1ebe5", 'I': "#ffb3c6", 'K': eyes, 'P': "#ff8fab"}, 'B'},
	"cat": {[]string{"A..........A", "AIA......AIA", "AAAAAAAAAAAA", "AAYKAAAAKYAA", "AAAAAPPAAAAA", "AAPAAAAAAAPA", "AAAAAAAAAAAA", ".AAAAAAAAAA.", ".AA.AAAA.AA.", ".AA......AA."},
		map[byte]string{'A': "#8d9bb5", 'I': "#ffb3c6", 'Y': "#f5d76e", 'K': eyes, 'P': "#ff8fab"}, 'A'},
	"owl": {[]string{"TT........TT", "TTTTTTTTTTTT", "TWWWTTTTWWWT", "TWKWTTTTWKWT", "TTTTTTOOTTTT", "TTSSSSSSSSTT", "TTSDSDSDSDTT", ".TSSSSSSSST.", ".TT......TT.", "..OO....OO.."},
		map[byte]string{'T': "#c9a26b", 'W': "#fff6e2", 'K': eyes, 'O': "#f5a742", 'S': "#e6c48f", 'D': "#b68d54"}, 'T'},
	"penguin": {[]string{"...NNNNNN...", "..NNNNNNNN..", ".NNWKNNWKNN.", ".NNNNOONNNN.", "NNNWWWWWWNNN", "NNWWWWWWWWNN", "NNWWWWWWWWNN", ".NWWWWWWWWN.", "..NWWWWWWN..", "..OOO..OOO.."},
		map[byte]string{'N': "#4a5fa8", 'W': "#f4f0ea", 'K': eyes, 'O': "#f5a742"}, 'N'},
	"octopus": {[]string{"...VVVVVV...", "..VVLLVVVV..", ".VVLVVVVVVV.", "VVVVVVVVVVVV", "VVKVVVVVVKVV", "VPVVVVVVVVPV", "VVVVVMMVVVVV", "VVVVVVVVVVVV", "V.VV.VV.VV.V", "V.V..V..V..V"},
		map[byte]string{'V': "#9b7be0", 'L': "#c7b3f5", 'K': eyes, 'P': "#ff8fab", 'M': "#4a2f96"}, 'V'},
}

type rgb [3]int

func parse(hex string) rgb {
	var c rgb
	fmt.Sscanf(hex, "#%02x%02x%02x", &c[0], &c[1], &c[2])
	return c
}

// shade lightens toward white when k > 0 and darkens toward black when k < 0.
func shade(c rgb, k float64) rgb {
	target := 0.0
	if k > 0 {
		target = 255
	}
	var out rgb
	for i := range c {
		out[i] = int(math.Round(float64(c[i]) + (target-float64(c[i]))*math.Abs(k)))
	}
	return out
}

type pt struct{ x, y int }
type cells map[pt]rgb

func animalCells(name string, t int) cells {
	sp := sprites[name]
	blink := t%40 > 37
	bob := 0
	if (t/8)%2 == 1 {
		bob = 1
	}
	out := cells{}
	for y, row := range sp.rows {
		for x := 0; x < len(row); x++ {
			ch := row[x]
			if ch == '.' {
				continue
			}
			if blink && (ch == 'K' || ch == 'Y') {
				ch = sp.face
			}
			px := x
			if name == "octopus" && y == 9 && (t/6)%2 == 1 && ch == 'V' {
				px++
			}
			out[pt{OX + px, OY + y + bob}] = parse(sp.palette[ch])
		}
	}
	return out
}

func fill(c cells, x0, y0, w, h int, hex string) {
	col := parse(hex)
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			c[pt{x, y}] = col
		}
	}
}

func set(c cells, x, y int, hex string) { c[pt{x, y}] = parse(hex) }

func propCells(scene string, t int) cells {
	c := cells{}
	switch scene {
	case "type":
		fill(c, 19, 3, 12, 8, "#343f63")
		fill(c, 20, 4, 10, 6, "#58b8ec")
		for i := 0; i < 3; i++ {
			fill(c, 21, 5+i*2, 3+(t/3+i*2)%5, 1, "#e4f8ff")
		}
		fill(c, 17, 11, 16, 2, "#b3bdd6")
		fill(c, 18, 13, 14, 1, "#8892ac")
		set(c, 19+((t/2)%6)*2, 11, "#ffffff")
	case "search":
		fill(c, 19, 3, 14, 11, "#eef1f9")
		for i, wd := range []int{10, 7, 10, 6, 9} {
			fill(c, 21, 4+i*2, wd, 1, "#b8c2da")
		}
		sx := 22 + int(math.Round(5*math.Sin(float64(t)/4)))
		for _, d := range [][2]int{{1, 0}, {2, 0}, {3, 0}, {0, 1}, {4, 1}, {0, 2}, {4, 2}, {0, 3}, {4, 3}, {1, 4}, {2, 4}, {3, 4}} {
			set(c, sx+d[0], 5+d[1], "#f0b93a")
		}
		for dx := 1; dx < 4; dx++ {
			for dy := 1; dy < 4; dy++ {
				set(c, sx+dx, 5+dy, "#bfe7ff")
			}
		}
		set(c, sx+5, 10, "#8a6a3a")
		set(c, sx+6, 11, "#8a6a3a")
		set(c, sx+7, 12, "#8a6a3a")
	case "read":
		flip := (t / 8) % 2
		fill(c, 17, 5, 7, 9, "#fffdf6")
		fill(c, 25, 5, 7, 9, "#f2ecdc")
		fill(c, 24, 5, 1, 9, "#c9a26b")
		for i := 0; i < 4; i++ {
			fill(c, 18, 7+i*2, 5, 1, "#c3ccdf")
			if flip == 0 || i < 2 {
				fill(c, 26, 7+i*2, 5, 1, "#c3ccdf")
			}
		}
		if flip == 1 {
			fill(c, 25, 5, 7, 9, "#fffdf6")
			for i := 0; i < 2; i++ {
				fill(c, 26, 7+i*2, 5, 1, "#c3ccdf")
			}
		}
	case "shell":
		fill(c, 18, 3, 15, 11, "#1b2238")
		fill(c, 18, 3, 15, 2, "#2c3554")
		set(c, 19, 3, "#ff6b6b")
		set(c, 21, 3, "#ffd166")
		set(c, 23, 3, "#5ad08a")
		for i := 0; i < 2; i++ {
			fill(c, 20, 6+i*2, 4+(t/4+i*3)%7, 1, "#6e7aa6")
		}
		set(c, 20, 10, "#5ad08a")
		set(c, 21, 11, "#5ad08a")
		set(c, 20, 12, "#5ad08a")
		fill(c, 23, 12, 3, 1, "#5ad08a")
		if t%8 < 5 {
			fill(c, 27, 10, 2, 3, "#e7ffe9")
		}
	case "web":
		cx, cy, r := 26.0, 8.0, 5.5
		shift := float64((t / 2) % 12)
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				if math.Pow(float64(x)-cx, 2)+math.Pow(float64(y)-cy, 2) <= r*r {
					if math.Sin((float64(x)+shift)*0.9)+math.Cos(float64(y)*0.85) > 0.85 {
						set(c, x, y, "#5ad08a")
					} else {
						set(c, x, y, "#3f72e8")
					}
				}
			}
		}
		set(c, 23, 4, "#a8c8ff")
		if t%12 < 6 {
			set(c, 34, 3, "#f5d76e")
		}
	default:
		for i := 0; i < 3; i++ {
			size, phase := 1+i, (t/5+i)%3
			col := "#cfd6e6"
			if i == 2 {
				col = "#ffffff"
			}
			fill(c, 19+i*4, 10-i*3-phase/2, size, size, col)
		}
		if t%12 < 8 {
			for _, p := range []pt{{31, 3}, {30, 4}, {31, 4}, {32, 4}, {31, 5}} {
				set(c, p.x, p.y, "#f5d76e")
			}
		}
	}
	return c
}

const (
	// scale is output pixels per sprite pixel in the PNG.
	scale = 4
	// ImageCols and ImageRows are the terminal cells the PNG is shown in.
	ImageCols, ImageRows = 10, 2
)

// PNG renders one animation step as a transparent image with outlines, bevels and a soft shadow.
func PNG(scene string, t int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, W*scale, H*scale))
	paintShadow(img)
	paintVoxels(img, animalCells(sceneAnimal[scene], t), true)
	paintVoxels(img, propCells(scene, t), false)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	_ = enc.Encode(&buf, img)
	return buf.Bytes()
}

func rect(img *image.NRGBA, x, y, w, h int, c rgb, alpha uint8) {
	col := color.NRGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), alpha}
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			if image.Pt(xx, yy).In(img.Rect) {
				img.SetNRGBA(xx, yy, col)
			}
		}
	}
}

func paintShadow(img *image.NRGBA) {
	cx, cy, rx, ry := float64(OX+6)*scale, (float64(OY)+11.2)*scale, 6.5*scale, 0.9*scale
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			dx, dy := (float64(x)-cx)/rx, (float64(y)-cy)/ry
			if dx*dx+dy*dy <= 1 {
				img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 90})
			}
		}
	}
}

// paintVoxels draws each pixel with a lit top edge when the pixel above differs and a shaded bottom edge when the one
// below differs, plus a dark outline around the whole shape.
func paintVoxels(img *image.NRGBA, c cells, outline bool) {
	if outline {
		for p, col := range c {
			for _, d := range []pt{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				n := pt{p.x + d.x, p.y + d.y}
				if _, ok := c[n]; !ok {
					rect(img, n.x*scale, n.y*scale, scale, scale, shade(col, -0.62), 255)
				}
			}
		}
	}
	flat := parse(eyes)
	for p, col := range c {
		x, y := p.x*scale, p.y*scale
		rect(img, x, y, scale, scale, col, 255)
		if col == flat {
			continue
		}
		if c[pt{p.x, p.y - 1}] != col {
			rect(img, x, y, scale, 1, shade(col, 0.32), 255)
		}
		if c[pt{p.x, p.y + 1}] != col {
			rect(img, x, y+scale-1, scale, 1, shade(col, -0.28), 255)
		}
		if _, ok := c[pt{p.x - 1, p.y}]; !ok {
			rect(img, x, y, 1, scale, shade(col, 0.18), 255)
		}
		if _, ok := c[pt{p.x + 1, p.y}]; !ok {
			rect(img, x+scale-1, y, 1, scale, shade(col, -0.18), 255)
		}
	}
}

// Fallback frames are the animal alone, shrunk to a half-block pixel per 2x2 sprite pixels.
const (
	shrink = 2
	// Rows and Cols are the fallback frame's size in terminal cells.
	Rows, Cols = 3, 6
)

// Frame renders the half-block fallback: flat colors, eyes kept, no props.
func Frame(scene string, t int) []string {
	c := halve(animalCells(sceneAnimal[scene], t))
	lines := make([]string, 0, Rows)
	for y := 1; y < 1+2*Rows; y += 2 {
		var b strings.Builder
		for x := 1; x <= Cols; x++ {
			top, hasTop := c[pt{x, y}]
			bot, hasBot := c[pt{x, y + 1}]
			switch {
			case hasTop && hasBot:
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀\x1b[0m", top[0], top[1], top[2], bot[0], bot[1], bot[2])
			case hasTop:
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm▀\x1b[0m", top[0], top[1], top[2])
			case hasBot:
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm▄\x1b[0m", bot[0], bot[1], bot[2])
			default:
				b.WriteByte(' ')
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

// halve turns each 2x2 block into one pixel: eyes win so faces survive, otherwise the most common color wins.
func halve(c cells) cells {
	groups := map[pt][]rgb{}
	for p, col := range c {
		k := pt{floorDiv(p.x, shrink), floorDiv(p.y, shrink)}
		groups[k] = append(groups[k], col)
	}
	eye := parse(eyes)
	out := cells{}
	for k, cols := range groups {
		if len(cols) < (shrink*shrink+1)/2 {
			continue
		}
		count := map[rgb]int{}
		best, bestN := cols[0], 0
		for _, col := range cols {
			if col == eye {
				best, bestN = col, 99
				break
			}
			if count[col]++; count[col] > bestN {
				best, bestN = col, count[col]
			}
		}
		out[k] = best
	}
	return out
}

func floorDiv(a, b int) int { return int(math.Floor(float64(a) / float64(b))) }

func Verb(scene string, tick int) string {
	w, ok := verbs[scene]
	if !ok {
		w = verbs["think"]
	}
	return w[(tick/30)%len(w)]
}
