//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens target in the default browser (dev/other platforms).
func openBrowser(target string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	_ = exec.Command(cmd, target).Start()
}
