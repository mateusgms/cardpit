// Package config loads the bootstrap configuration (config.yaml).
// Only settings needed before the database is open live here; everything
// else is stored in SQLite and edited through the web UI.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen       string        `yaml:"listen"`
	DBPath       string        `yaml:"db_path"`
	Platform     string        `yaml:"platform"` // "windows" | "fake"
	FakeRoot     string        `yaml:"fake_root"`
	FakeDest     string        `yaml:"fake_dest"`
	LogPath      string        `yaml:"log_path"`
	PollInterval time.Duration `yaml:"poll_interval"`
	Debounce     time.Duration `yaml:"debounce"`
}

func Default() Config {
	plat := "fake"
	if runtime.GOOS == "windows" {
		plat = "windows"
	}
	return Config{
		Listen:       "0.0.0.0:8532",
		DBPath:       "cardpit.db",
		Platform:     plat,
		FakeRoot:     "devcards",
		FakeDest:     "devout",
		PollInterval: 2 * time.Second,
		Debounce:     3 * time.Second,
	}
}

// Load reads path (if it exists) over the defaults. Relative paths in the
// file are resolved against the file's directory; if the file is absent,
// defaults are returned unchanged.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	base := filepath.Dir(path)
	cfg.DBPath = resolve(base, cfg.DBPath)
	cfg.FakeRoot = resolve(base, cfg.FakeRoot)
	cfg.FakeDest = resolve(base, cfg.FakeDest)
	cfg.LogPath = resolve(base, cfg.LogPath)
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Platform {
	case "windows", "fake":
	default:
		return fmt.Errorf("config: platform must be \"windows\" or \"fake\", got %q", c.Platform)
	}
	if c.PollInterval <= 0 || c.Debounce < 0 {
		return fmt.Errorf("config: poll_interval must be > 0 and debounce >= 0")
	}
	return nil
}

func resolve(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
