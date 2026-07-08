//go:build !windows

package main

import "github.com/mateusgms/cardpit/core/internal/secret"

// apiTokenBox uses the plaintext box on the dev/fake platform, matching how
// the worker sealed settings there.
func apiTokenBox() secret.SecretBox { return secret.PlainBox{} }
