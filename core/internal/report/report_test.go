package report

import (
	"bytes"
	"fmt"
	"image/png"
	"testing"

	"github.com/mateusgms/cardpit/core/internal/store"
)

func TestRenderProducesDecodablePNG(t *testing.T) {
	var largest []store.IngestedFile
	for i := 0; i < 25; i++ {
		largest = append(largest, store.IngestedFile{
			DstPath: fmt.Sprintf("D:/2026-07-08/CLIP_%04d.MP4", i),
			Size:    int64(1<<30) - int64(i)*1000, MediaType: "video",
		})
	}
	in := Input{
		Job: store.Job{
			ID: 7, StartedAt: "2026-07-08T10:00:00Z", FinishedAt: "2026-07-08T10:12:30Z",
			BytesCopied: 60 << 30, FilesSkipped: 3,
		},
		CardAlias: "SanDisk 128 A",
		SlotAlias: "Leitor esquerdo",
		Stats: map[string]TypeStat{
			"photo": {Count: 247, Bytes: 18 << 30},
			"video": {Count: 12, Bytes: 42 << 30},
		},
		Largest: largest,
	}
	data, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a decodable png: %v", err)
	}
	if img.Bounds().Dx() != 900 || img.Bounds().Dy() < 300 {
		t.Fatalf("bounds: %v", img.Bounds())
	}
}

func TestRenderEmptyJob(t *testing.T) {
	data, err := Render(Input{
		Job:       store.Job{ID: 1, StartedAt: "2026-07-08T10:00:00Z"},
		CardAlias: "X", SlotAlias: "Y",
		Stats: map[string]TypeStat{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
}
