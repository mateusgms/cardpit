//go:build !windows

package engine

func isDiskFullOS(error) bool { return false }
