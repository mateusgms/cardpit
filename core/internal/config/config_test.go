package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:8532" || cfg.PollInterval != 2*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadResolvesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "db_path: data/cardpit.db\nplatform: fake\npoll_interval: 500ms\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "data", "cardpit.db"); cfg.DBPath != want {
		t.Fatalf("db_path = %q, want %q", cfg.DBPath, want)
	}
	if cfg.PollInterval != 500*time.Millisecond {
		t.Fatalf("poll_interval = %v", cfg.PollInterval)
	}
}

func TestLoadRejectsBadPlatform(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("platform: macos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid platform")
	}
}
