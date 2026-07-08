//go:build !windows

package app

import (
	"fmt"

	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/secret"
)

func newWindowsPlatform() (platform.Platform, secret.SecretBox, error) {
	return platform.Platform{}, nil,
		fmt.Errorf(`app: platform "windows" is only available in Windows builds; use platform "fake"`)
}
