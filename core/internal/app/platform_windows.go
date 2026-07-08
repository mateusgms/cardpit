//go:build windows

package app

import (
	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/platform/win"
	"github.com/mateusgms/cardpit/core/internal/secret"
)

func newWindowsPlatform() (platform.Platform, secret.SecretBox, error) {
	return win.New(), win.DPAPIBox{}, nil
}
