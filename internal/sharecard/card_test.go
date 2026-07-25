package sharecard

import (
	"bytes"
	"image/png"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/math/fixed"
)

func TestRenderProducesVerticalPNG(t *testing.T) {
	var output bytes.Buffer
	if err := Render(&output, Data{
		ChallengeTitle: "Land the longest jump",
		Handle:         "player_one",
		DisplayName:    "Player One",
		Score:          4.75,
		VoteCount:      32,
		Rank:           2,
		CurrentStreak:  7,
	}); err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != 1080 || image.Bounds().Dy() != 1920 {
		t.Fatalf("dimensions = %v, want 1080x1920", image.Bounds())
	}
}

func TestWrapToWidthBoundsLongWords(t *testing.T) {
	bold, err := face(gobold.TTF, 82)
	if err != nil {
		t.Fatal(err)
	}
	defer bold.Close()
	lines := wrapToWidth(
		bold,
		"ANEXTREMELYLONGUNBROKENCHALLENGETITLETHATMUSTNOTOVERFLOW",
		936,
		3,
	)
	if len(lines) == 0 || len(lines) > 3 {
		t.Fatalf("unexpected lines: %#v", lines)
	}
	for _, line := range lines {
		if font.MeasureString(bold, line) > fixed.I(936) {
			t.Fatalf("line %q exceeds card width", line)
		}
	}
}
