//go:build windows

package main

import "golang.org/x/sys/windows"

// openBrowser opens target in the default browser via ShellExecute — no
// transient `cmd /c start` window, unlike the older approach.
func openBrowser(target string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return
	}
	// SW_SHOWNORMAL = 1 — show the browser window normally.
	_ = windows.ShellExecute(0, verb, file, nil, nil, 1)
}
