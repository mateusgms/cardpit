//go:build !windows

package main

import "fmt"

func setupCmd([]string) error {
	return fmt.Errorf("setup: disponível apenas no Windows")
}

func tokenCmd([]string) error {
	return fmt.Errorf("token: disponível apenas no Windows")
}
