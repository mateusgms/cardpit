package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Well-known setting keys. Secrets are sealed with secret.SecretBox before
// landing here.
const (
	SetDestVolumeGUID    = "dest_volume_guid"
	SetDestTemplate      = "dest_template" // default "{YYYY-MM-DD}"
	SetMaxConcurrent     = "max_concurrent_jobs"
	SetVerifyMode        = "verify_mode" // "fast" | "paranoid"
	SetEjectAfterCopy    = "eject_after_copy"
	SetUnknownCardPolicy = "unknown_card_policy" // "ask" | "copy" | "ignore"
	SetRequireDCIM       = "require_dcim"
	SetWatcherPaused     = "watcher_paused"
	SetTelegramToken     = "telegram_bot_token"    // sealed
	SetTelegramChatIDs   = "telegram_chat_ids"     // comma-separated allowlist
	SetTelegramTokenSrc  = "telegram_token_source" // TokenSourceEnv | TokenSourceUI
	SetAPIToken          = "api_token"             // sealed
	SetAutoUpdate        = "auto_update"           // "true" | "false", default true
)

// Values for SetTelegramTokenSrc. A token seeded from the TELEGRAM_KEY env
// var (TokenSourceEnv) is re-seeded on every boot so rotating the variable
// propagates; once the UI writes a token (TokenSourceUI) the env var no
// longer touches it.
const (
	TokenSourceEnv = "env"
	TokenSourceUI  = "ui"
)

type SettingsRepo struct{ db *sql.DB }

func (r *SettingsRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (r *SettingsRepo) GetString(ctx context.Context, key, def string) string {
	v, ok, err := r.Get(ctx, key)
	if err != nil || !ok {
		return def
	}
	return v
}

func (r *SettingsRepo) GetInt(ctx context.Context, key string, def int) int {
	v, ok, err := r.Get(ctx, key)
	if err != nil || !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (r *SettingsRepo) GetBool(ctx context.Context, key string, def bool) bool {
	v, ok, err := r.Get(ctx, key)
	if err != nil || !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// All returns every non-secret setting (secret keys are filtered out so the
// API never leaks sealed blobs).
func (r *SettingsRepo) All(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	secret := map[string]bool{SetTelegramToken: true, SetAPIToken: true}
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if !secret[k] {
			out[k] = v
		}
	}
	return out, rows.Err()
}
