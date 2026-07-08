// Package webui embeds the built React UI. A committed placeholder
// index.html keeps `go build` green without a web build; `make web` stages
// the real Vite output into dist/ before a release build.
package webui

import "embed"

//go:embed all:dist
var Dist embed.FS

// IsPlaceholder reports whether the embedded UI is the committed stub
// (useful to log at startup which UI shipped in this binary).
func IsPlaceholder() bool {
	entries, err := Dist.ReadDir("dist")
	if err != nil {
		return true
	}
	return len(entries) == 1 && entries[0].Name() == "index.html"
}
