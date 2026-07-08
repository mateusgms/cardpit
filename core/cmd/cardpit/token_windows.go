//go:build windows

package main

import (
	"github.com/mateusgms/cardpit/core/internal/platform/win"
	"github.com/mateusgms/cardpit/core/internal/secret"
)

// apiTokenBox unseals settings with Windows DPAPI (LOCAL_MACHINE), matching
// how the worker sealed them.
func apiTokenBox() secret.SecretBox { return win.DPAPIBox{} }
