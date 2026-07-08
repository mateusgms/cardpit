//go:build windows

package app

import (
	"fmt"

	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/secret"
)

// TODO(fase 4): return the real platform/win implementation + DPAPI box.
func newWindowsPlatform() (platform.Platform, secret.SecretBox, error) {
	return platform.Platform{}, nil, fmt.Errorf("app: windows platform not wired yet")
}
