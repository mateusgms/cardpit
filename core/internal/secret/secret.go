// Package secret seals small secrets (bot token, API token) before they are
// stored in SQLite. On Windows the DPAPI implementation (platform/win) is
// used; elsewhere PlainBox provides a non-protecting fallback for development.
package secret

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// SecretBox seals and opens small secrets.
type SecretBox interface {
	Seal(plain []byte) (string, error)
	Open(sealed string) ([]byte, error)
}

// PlainBox is the development fallback: base64 with a marker prefix, no
// actual protection. Callers should log a warning when selecting it.
type PlainBox struct{}

const plainPrefix = "plain:"

func (PlainBox) Seal(plain []byte) (string, error) {
	return plainPrefix + base64.StdEncoding.EncodeToString(plain), nil
}

func (PlainBox) Open(sealed string) ([]byte, error) {
	s, ok := strings.CutPrefix(sealed, plainPrefix)
	if !ok {
		return nil, fmt.Errorf("secret: blob was not sealed by PlainBox")
	}
	return base64.StdEncoding.DecodeString(s)
}
