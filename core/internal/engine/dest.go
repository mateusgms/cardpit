package engine

import (
	"fmt"
	"strings"
	"time"
)

// DefaultTemplate organizes files by capture date (mtime).
const DefaultTemplate = "{YYYY-MM-DD}"

// expandTemplate renders the destination sub-path for one file.
// Supported tokens: {YYYY-MM-DD}, {YYYY}, {MM}, {DD}, {card_alias}.
// The file's mtime is interpreted in the machine's local timezone (cameras
// set mtime to capture time; see docs for the DST caveat).
func expandTemplate(tpl string, mtime time.Time, cardAlias string) string {
	if tpl == "" {
		tpl = DefaultTemplate
	}
	local := mtime.Local()
	r := strings.NewReplacer(
		"{YYYY-MM-DD}", local.Format("2006-01-02"),
		"{YYYY}", local.Format("2006"),
		"{MM}", local.Format("01"),
		"{DD}", local.Format("02"),
		"{card_alias}", sanitizePathComponent(cardAlias),
	)
	return r.Replace(tpl)
}

// sanitizePathComponent keeps card aliases from injecting separators or
// characters Windows rejects in file names.
func sanitizePathComponent(s string) string {
	if s == "" {
		return "sem-nome"
	}
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-", "?", "-",
		"\"", "-", "<", "-", ">", "-", "|", "-",
	)
	out := strings.TrimSpace(replacer.Replace(s))
	if out == "" {
		return "sem-nome"
	}
	return out
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
