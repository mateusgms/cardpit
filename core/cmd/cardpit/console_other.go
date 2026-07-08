//go:build !windows

package main

// attachParentConsole is a no-op off Windows: dev/other builds use the console
// subsystem and already have stdio attached.
func attachParentConsole() {}
