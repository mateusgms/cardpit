package main

import (
	"context"

	"github.com/mateusgms/cardpit/core/internal/config"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// apiTokenString best-effort reads and unseals the API token from the
// database so the launcher can auto-log-in the browser. Returns "" on any
// failure (e.g. first boot before the token exists), in which case the UI
// falls back to prompting for the token. Safe to call while another instance
// holds the database open (SQLite allows concurrent readers).
func apiTokenString(cfg config.Config) string {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return ""
	}
	defer db.Close()
	sealed, ok, err := db.Settings.Get(context.Background(), store.SetAPIToken)
	if err != nil || !ok {
		return ""
	}
	plain, err := apiTokenBox().Open(sealed)
	if err != nil {
		return ""
	}
	return string(plain)
}
