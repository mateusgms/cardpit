//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachParentConsole re-attaches std streams to the launching console, if
// any. The release binary is built for the GUI subsystem so a double-click
// shows no console; this lets CLI subcommands (token/status/version) still
// print when the exe is run from an existing cmd/PowerShell window. When there
// is no parent console (double-click) AttachConsole fails and we leave the
// std handles untouched.
func attachParentConsole() {
	const attachParentProcess = ^uint32(0) // (DWORD)-1
	attachConsole := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := attachConsole.Call(uintptr(attachParentProcess)); r == 0 {
		return // no parent console — bare GUI launch
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
}
