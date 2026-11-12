package tui

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Ani07-05/slendertoken/internal/art"
)

// rowColDiacritics are the first entries of the Kitty graphics protocol's row/column table: a placeholder cell carries
// one for its row and one for its column, which is how a text renderer can lay an image out cell by cell.
var rowColDiacritics = []rune{0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F, 0x0346, 0x034A}

const (
	placeholder    = 0x10EEEE
	framesPerScene = 48
	chunk          = 4096
)

// images shows the animals as real images in terminals that speak the Kitty graphics protocol (Ghostty, kitty).
// Each frame is uploaded once, as a virtual placement, and then drawn by printing placeholder characters whose
// foreground color names the image. That keeps the picture inside ordinary text, so the renderer, scrolling and the
// painted background all keep working.
type images struct {
	on   bool
	out  io.Writer
	ids  map[string]int
	next int
}

func newImages() *images {
	return &images{on: detectImages(), out: os.Stdout, ids: map[string]int{}, next: 1}
}

func detectImages() bool {
	switch os.Getenv("SLENDERTOKEN_IMAGES") {
	case "0", "off":
		return false
	case "1", "on":
		return true
	}
	if os.Getenv("TMUX") != "" || os.Getenv("STY") != "" {
		return false
	}
	term := os.Getenv("TERM")
	return os.Getenv("TERM_PROGRAM") == "ghostty" || strings.Contains(term, "ghostty") || term == "xterm-kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

// upload sends one PNG frame and returns its image id. The write is a single call so other terminal output cannot
// land between its chunks.
func (im *images) upload(scene string, t int) int {
	id := im.next
	im.next++
	data := base64.StdEncoding.EncodeToString(art.PNG(scene, t))
	var b strings.Builder
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		more := 0
		if end < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,t=d,i=%d,U=1,c=%d,r=%d,q=2,m=%d;%s\x1b\\", id, art.ImageCols, art.ImageRows, more, data[i:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, data[i:end])
		}
	}
	_, _ = io.WriteString(im.out, b.String())
	return id
}

// lines are the placeholder rows for one animation step, uploading the frame the first time it is needed.
func (im *images) lines(scene string, t int) []string {
	t %= framesPerScene
	key := fmt.Sprintf("%s:%d", scene, t)
	id, ok := im.ids[key]
	if !ok {
		id = im.upload(scene, t)
		im.ids[key] = id
	}
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&255, (id>>8)&255, id&255)
	rows := make([]string, art.ImageRows)
	for r := range rows {
		var b strings.Builder
		b.WriteString(color)
		for c := 0; c < art.ImageCols; c++ {
			b.WriteRune(placeholder)
			b.WriteRune(rowColDiacritics[r])
			b.WriteRune(rowColDiacritics[c])
		}
		b.WriteString("\x1b[0m")
		rows[r] = b.String()
	}
	return rows
}

// clear frees every uploaded image.
func (im *images) clear() {
	if im.on {
		_, _ = io.WriteString(im.out, "\x1b_Ga=d,d=A,q=2\x1b\\")
	}
}
