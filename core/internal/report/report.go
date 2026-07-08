// Package report renders the end-of-job PNG attached to the Telegram
// completion message: per-type totals, a proportion bar and the largest
// files — pure Go drawing via fogleman/gg, no headless browser.
package report

import (
	"bytes"
	"fmt"
	"image/color"
	"path/filepath"
	"time"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/mateusgms/cardpit/core/internal/store"
)

type TypeStat struct {
	Count int
	Bytes int64
}

// Input is everything the renderer needs, pre-fetched by the caller.
type Input struct {
	Job       store.Job
	CardAlias string
	SlotAlias string
	Stats     map[string]TypeStat // keyed photo|video|other
	Largest   []store.IngestedFile
}

const (
	width   = 900
	margin  = 40.0
	rowH    = 34.0
	maxRows = 10
)

var (
	bgColor    = color.RGBA{16, 20, 24, 255}
	panelColor = color.RGBA{26, 32, 39, 255}
	textColor  = color.RGBA{232, 237, 242, 255}
	mutedColor = color.RGBA{139, 152, 165, 255}
	photoColor = color.RGBA{77, 163, 255, 255}
	videoColor = color.RGBA{62, 207, 116, 255}
	otherColor = color.RGBA{245, 184, 61, 255}
	okColor    = color.RGBA{62, 207, 116, 255}
	warnColor  = color.RGBA{240, 86, 74, 255}
)

var typeLabels = map[string]string{
	"photo": "fotos", "video": "vídeos", "other": "outros",
}

func face(data []byte, size float64) (font.Face, error) {
	f, err := truetype.Parse(data)
	if err != nil {
		return nil, err
	}
	return truetype.NewFace(f, &truetype.Options{Size: size}), nil
}

// Render produces the PNG bytes.
func Render(in Input) ([]byte, error) {
	rows := len(in.Largest)
	if rows > maxRows {
		rows = maxRows
	}
	height := int(300 + rowH*float64(rows) + 90)
	dc := gg.NewContext(width, height)

	regular16, err := face(goregular.TTF, 16)
	if err != nil {
		return nil, err
	}
	bold22, err := face(gobold.TTF, 22)
	if err != nil {
		return nil, err
	}
	bold16, err := face(gobold.TTF, 16)
	if err != nil {
		return nil, err
	}
	mono14, err := face(goregular.TTF, 14)
	if err != nil {
		return nil, err
	}

	dc.SetColor(bgColor)
	dc.Clear()

	y := margin + 8

	// Header.
	dc.SetFontFace(bold22)
	dc.SetColor(textColor)
	dc.DrawString("cardpit — relatório de ingestão", margin, y)
	y += 30
	dc.SetFontFace(regular16)
	dc.SetColor(mutedColor)
	dc.DrawString(fmt.Sprintf("Cartão %s · %s · %s", in.CardAlias, in.SlotAlias,
		formatWhen(in.Job.StartedAt)), margin, y)
	y += 34

	// Totals line.
	dc.SetFontFace(bold16)
	dc.SetColor(textColor)
	dc.DrawString(summaryLine(in.Stats), margin, y)
	y += 14

	// Proportion bar by media type.
	total := int64(0)
	for _, s := range in.Stats {
		total += s.Bytes
	}
	barW := float64(width) - 2*margin
	x := margin
	if total > 0 {
		for _, mt := range []string{"photo", "video", "other"} {
			s := in.Stats[mt]
			if s.Bytes == 0 {
				continue
			}
			w := barW * float64(s.Bytes) / float64(total)
			dc.SetColor(typeColor(mt))
			dc.DrawRoundedRectangle(x, y, w, 14, 3)
			dc.Fill()
			x += w
		}
	} else {
		dc.SetColor(panelColor)
		dc.DrawRoundedRectangle(x, y, barW, 14, 3)
		dc.Fill()
	}
	y += 40

	// Duration / throughput / dedup / failures.
	dc.SetFontFace(regular16)
	dc.SetColor(mutedColor)
	dur, thr := durationThroughput(in.Job)
	line := fmt.Sprintf("Duração %s · %s/s · %d pulados (dedup)", dur, thr, in.Job.FilesSkipped)
	dc.DrawString(line, margin, y)
	if in.Job.FilesFailed > 0 {
		dc.SetColor(warnColor)
		dc.DrawString(fmt.Sprintf("   %d arquivos falharam", in.Job.FilesFailed),
			margin+measureW(dc, line), y)
	} else {
		dc.SetColor(okColor)
		dc.DrawString("   ✓ íntegro", margin+measureW(dc, line), y)
	}
	y += 36

	// Largest-files table.
	dc.SetFontFace(bold16)
	dc.SetColor(textColor)
	dc.DrawString("Maiores arquivos", margin, y)
	y += 12

	for i := 0; i < rows; i++ {
		f := in.Largest[i]
		rowY := y + float64(i)*rowH
		if i%2 == 0 {
			dc.SetColor(panelColor)
			dc.DrawRectangle(margin-8, rowY+6, barW+16, rowH-4)
			dc.Fill()
		}
		dc.SetFontFace(mono14)
		dc.SetColor(typeColor(f.MediaType))
		dc.DrawString("●", margin, rowY+rowH*0.72)
		dc.SetColor(textColor)
		name := filepath.Base(f.DstPath)
		if len(name) > 58 {
			name = name[:55] + "…"
		}
		dc.DrawString(name, margin+22, rowY+rowH*0.72)
		size := formatBytes(f.Size)
		dc.SetColor(mutedColor)
		dc.DrawString(size, float64(width)-margin-measureW(dc, size), rowY+rowH*0.72)
	}
	y += rowH*float64(rows) + 36

	dc.SetFontFace(regular16)
	dc.SetColor(mutedColor)
	dc.DrawString(fmt.Sprintf("job #%d · cardpit", in.Job.ID), margin, y)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func typeColor(mt string) color.RGBA {
	switch mt {
	case "photo":
		return photoColor
	case "video":
		return videoColor
	default:
		return otherColor
	}
}

func summaryLine(stats map[string]TypeStat) string {
	out := ""
	for _, mt := range []string{"photo", "video", "other"} {
		s, ok := stats[mt]
		if !ok || s.Count == 0 {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += fmt.Sprintf("%d %s (%s)", s.Count, typeLabels[mt], formatBytes(s.Bytes))
	}
	if out == "" {
		out = "nenhum arquivo novo (tudo já ingerido)"
	}
	return out
}

func durationThroughput(j store.Job) (string, string) {
	start, err1 := time.Parse(time.RFC3339, j.StartedAt)
	end, err2 := time.Parse(time.RFC3339, j.FinishedAt)
	if err1 != nil || err2 != nil || !end.After(start) {
		return "—", "—"
	}
	d := end.Sub(start).Round(time.Second)
	if d <= 0 {
		d = time.Second
	}
	return d.String(), formatBytes(int64(float64(j.BytesCopied) / d.Seconds()))
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatWhen(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format("02/01/2006 15:04")
}

func measureW(dc *gg.Context, s string) float64 {
	w, _ := dc.MeasureString(s)
	return w
}
