//go:build !windows

package main

import "fmt"

func trayCmd([]string) error {
	return fmt.Errorf("o ícone de bandeja está disponível apenas no Windows")
}
