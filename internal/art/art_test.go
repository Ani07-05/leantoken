package art

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestEveryScenePlaysAFullFrame(t *testing.T) {
	for _, scene := range []string{"think", "type", "search", "read", "shell", "web"} {
		lines := Frame(scene, 7)
		if len(lines) != Rows || !strings.Contains(strings.Join(lines, ""), "▀") {
			t.Errorf("%s: fallback frame wrong: %d rows", scene, len(lines))
		}
		img, err := png.Decode(bytes.NewReader(PNG(scene, 7)))
		if err != nil {
			t.Fatalf("%s: png: %v", scene, err)
		}
		if b := img.Bounds(); b.Dx() != W*scale || b.Dy() != H*scale {
			t.Errorf("%s: image is %v", scene, b)
		}
	}
}

func TestPNGFitsOneProtocolChunk(t *testing.T) {
	// a frame sent in one write is never split by other terminal output; base64 grows data by a third
	for _, scene := range []string{"think", "type", "search", "read", "shell", "web"} {
		for tick := 0; tick < 48; tick++ {
			if n := len(PNG(scene, tick)); n > 3000 {
				t.Errorf("%s t=%d: %d bytes", scene, tick, n)
			}
		}
	}
}

func TestEveryToolHasAScene(t *testing.T) {
	for tool, scene := range ToolScene {
		if _, ok := sceneAnimal[scene]; !ok {
			t.Errorf("%s maps to unknown scene %s", tool, scene)
		}
	}
}
