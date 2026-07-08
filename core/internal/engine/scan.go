package engine

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// fileEntry is one source file discovered on the card.
type fileEntry struct {
	src   string // absolute source path
	name  string // base name
	size  int64
	mtime time.Time
	media string // "photo" | "video" | "other"

	// knownHash is filled during dedup planning when the source had to be
	// hashed to disambiguate a (size, mtime) collision, so the copy phase
	// does not hash it twice.
	knownHash string
}

var photoExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".heic": true, ".heif": true,
	".dng": true, ".cr2": true, ".cr3": true, ".nef": true, ".arw": true,
	".orf": true, ".rw2": true, ".raf": true, ".gpr": true, ".tif": true,
	".tiff": true, ".webp": true, ".avif": true,
}

var videoExt = map[string]bool{
	".mp4": true, ".mov": true, ".avi": true, ".mts": true, ".m2ts": true,
	".mxf": true, ".mkv": true, ".braw": true, ".r3d": true, ".insv": true,
	".360": true, ".lrv": true, ".m4v": true, ".mpg": true, ".mpeg": true,
}

var skipDirs = map[string]bool{
	"system volume information": true,
	"$recycle.bin":              true,
	".trashes":                  true,
	".spotlight-v100":           true,
}

var skipFiles = map[string]bool{
	"desktop.ini": true,
	"thumbs.db":   true,
	".ds_store":   true,
}

func classify(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case photoExt[ext]:
		return "photo"
	case videoExt[ext]:
		return "video"
	default:
		return "other"
	}
}

// scanSource walks the card and returns every media candidate file.
// System/hidden entries and cardpit's own artifacts are skipped.
func scanSource(root string) ([]fileEntry, error) {
	var out []fileEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // card yanked mid-scan surfaces here
		}
		name := d.Name()
		lower := strings.ToLower(name)
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[lower]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || skipFiles[lower] || strings.HasSuffix(lower, tmpSuffix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fileEntry{
			src:   path,
			name:  name,
			size:  info.Size(),
			mtime: info.ModTime(),
			media: classify(name),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// hasDCIM implements the optional "only copy camera cards" heuristic.
func hasDCIM(root string) bool {
	entries, err := filepath.Glob(filepath.Join(root, "*"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.EqualFold(filepath.Base(e), "DCIM") {
			return true
		}
	}
	return false
}

// mtimeKey is the canonical string form of a file mtime used in the dedup
// index. UTC + RFC3339Nano keeps full filesystem precision and compares
// bytewise.
func mtimeKey(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
