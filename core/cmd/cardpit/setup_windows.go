//go:build windows

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kardianos/service"

	"github.com/mateusgms/cardpit/core/internal/config"
)

// setupCmd installs and starts the Windows service in one step.
// Must be run as administrator (setup.bat handles UAC elevation).
func setupCmd(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Write a default config next to the exe if one doesn't exist yet.
	if _, err := os.Stat(*cfgPath); os.IsNotExist(err) {
		content := "platform: windows\nlog_path: cardpit.log\n"
		if err := os.WriteFile(*cfgPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("criando config.yaml: %w", err)
		}
		fmt.Println("Config criado:", *cfgPath)
	}

	abs, err := filepath.Abs(*cfgPath)
	if err != nil {
		return err
	}

	// Install (idempotent — ignore "already installed").
	s, err := service.New(&program{log: slog.Default()}, svcConfig(abs))
	if err != nil {
		return err
	}
	if err := service.Control(s, "install"); err != nil {
		fmt.Println("Aviso ao instalar serviço (talvez já instalado):", err)
	} else {
		fmt.Println("Serviço instalado.")
	}

	// Start the service.
	if err := service.Control(s, "start"); err != nil {
		return fmt.Errorf("iniciando serviço: %w", err)
	}
	fmt.Println("Serviço iniciado. Aguardando primeiro boot...")

	// Poll for the API token (generated on first boot).
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	var token string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		t, err := readAPIToken(cfg.DBPath)
		if err == nil && t != "" {
			token = t
			break
		}
	}

	if token == "" {
		fmt.Println("\nNão foi possível ler o token automaticamente.")
		fmt.Println("Execute `cardpit.exe token` após o serviço iniciar.")
	} else {
		fmt.Printf("\n=== Token de acesso (guarde-o) ===\n%s\n==================================\n", token)
	}

	fmt.Println("\nAbrindo http://localhost:8532 ...")
	exec.Command("cmd", "/c", "start", "http://localhost:8532").Start()
	return nil
}

// tokenCmd prints the DPAPI-unsealed API token from the database.
func tokenCmd(args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	token, err := readAPIToken(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("lendo token: %w", err)
	}
	fmt.Println(token)
	return nil
}
