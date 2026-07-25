package sharecard

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type Data struct {
	ChallengeTitle string
	Handle         string
	DisplayName    string
	Score          float64
	VoteCount      int
	Rank           int
	CurrentStreak  int
}

func Render(writer io.Writer, data Data) error {
	canvas := image.NewRGBA(image.Rect(0, 0, 1080, 1920))
	fill(canvas, canvas.Bounds(), color.RGBA{R: 245, G: 244, B: 239, A: 255})
	fill(canvas, image.Rect(0, 0, 1080, 420), color.RGBA{R: 238, G: 79, B: 67, A: 255})
	fill(canvas, image.Rect(0, 1640, 1080, 1920), color.RGBA{R: 29, G: 31, B: 35, A: 255})
	fill(canvas, image.Rect(72, 480, 1008, 492), color.RGBA{R: 29, G: 31, B: 35, A: 255})
	fill(canvas, image.Rect(72, 1490, 1008, 1502), color.RGBA{R: 29, G: 31, B: 35, A: 255})
	fill(canvas, image.Rect(72, 1120, 1008, 1150), color.RGBA{R: 190, G: 226, B: 69, A: 255})

	bold, err := face(gobold.TTF, 82)
	if err != nil {
		return err
	}
	defer bold.Close()
	regular, err := face(goregular.TTF, 42)
	if err != nil {
		return err
	}
	defer regular.Close()
	metric, err := face(gobold.TTF, 170)
	if err != nil {
		return err
	}
	defer metric.Close()
	smallBold, err := face(gobold.TTF, 44)
	if err != nil {
		return err
	}
	defer smallBold.Close()

	dark := color.RGBA{R: 29, G: 31, B: 35, A: 255}
	light := color.RGBA{R: 245, G: 244, B: 239, A: 255}
	drawText(canvas, bold, dark, 72, 125, "HEATCHECK")
	drawText(canvas, regular, dark, 72, 210, "DAILY CHALLENGE RESULT")
	y := 600
	for _, line := range wrapToWidth(bold, data.ChallengeTitle, 936, 3) {
		drawText(canvas, bold, dark, 72, y, strings.ToUpper(line))
		y += 94
	}
	identity := "@" + data.Handle + "  /  " + data.DisplayName
	drawText(canvas, regular, dark, 72, y+30, truncateToWidth(regular, identity, 936))

	drawText(canvas, smallBold, dark, 72, 1215, "STYLE SCORE")
	drawText(canvas, metric, dark, 72, 1405, fmt.Sprintf("%.2f", data.Score))
	drawText(canvas, regular, dark, 600, 1270, fmt.Sprintf("%d VOTES", data.VoteCount))
	drawText(canvas, regular, dark, 600, 1340, fmt.Sprintf("RANK #%d", data.Rank))
	drawText(canvas, regular, dark, 600, 1410, fmt.Sprintf("%d DAY STREAK", data.CurrentStreak))

	drawText(canvas, bold, light, 72, 1745, "PROVE IT. RATE IT.")
	drawText(canvas, regular, light, 72, 1825, "heatcheck.dogi.watch")
	return png.Encode(writer, canvas)
}

func face(ttf []byte, size float64) (font.Face, error) {
	parsed, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

func drawText(destination draw.Image, face font.Face, ink color.Color, x, y int, text string) {
	drawer := font.Drawer{
		Dst:  destination,
		Src:  image.NewUniform(ink),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	drawer.DrawString(text)
}

func fill(destination draw.Image, rectangle image.Rectangle, value color.Color) {
	draw.Draw(destination, rectangle, image.NewUniform(value), image.Point{}, draw.Src)
}

func wrapToWidth(face font.Face, value string, maximum int, maxLines int) []string {
	maximumWidth := fixed.I(maximum)
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{"CHALLENGE"}
	}
	var lines []string
	line := ""
	for _, word := range words {
		chunks := splitToWidth(face, word, maximum)
		for _, chunk := range chunks {
			candidate := chunk
			if line != "" {
				candidate = line + " " + chunk
			}
			if font.MeasureString(face, candidate) <= maximumWidth {
				line = candidate
				continue
			}
			if line != "" {
				lines = append(lines, line)
			}
			line = chunk
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = truncateToWidth(face, lines[maxLines-1]+"...", maximum)
	}
	return lines
}

func splitToWidth(face font.Face, value string, maximum int) []string {
	maximumWidth := fixed.I(maximum)
	if font.MeasureString(face, value) <= maximumWidth {
		return []string{value}
	}
	var chunks []string
	var chunk []rune
	for _, character := range []rune(value) {
		candidate := string(append(chunk, character))
		if len(chunk) > 0 && font.MeasureString(face, candidate) > maximumWidth {
			chunks = append(chunks, string(chunk))
			chunk = chunk[:0]
		}
		chunk = append(chunk, character)
	}
	if len(chunk) > 0 {
		chunks = append(chunks, string(chunk))
	}
	return chunks
}

func truncateToWidth(face font.Face, value string, maximum int) string {
	maximumWidth := fixed.I(maximum)
	if font.MeasureString(face, value) <= maximumWidth {
		return value
	}
	const suffix = "..."
	runes := []rune(value)
	for len(runes) > 0 &&
		font.MeasureString(face, string(runes)+suffix) > maximumWidth {
		runes = runes[:len(runes)-1]
	}
	return strings.TrimSpace(string(runes)) + suffix
}
