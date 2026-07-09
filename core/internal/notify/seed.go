package notify

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// EnvTelegramKey is the environment variable that pre-configures the Telegram
// bot token: in CI it comes from the TELEGRAM_KEY secret of the GitHub
// environment; locally it is a plain process/service env var.
const EnvTelegramKey = "TELEGRAM_KEY"

// SeedTelegramTokenFromEnv seals TELEGRAM_KEY into the settings on boot so a
// fresh install comes up with Telegram already configured. A token entered
// through the UI always wins: once telegram_token_source is "ui" (or a token
// predates the marker) the env var is ignored.
func SeedTelegramTokenFromEnv(ctx context.Context, db *store.DB, secrets secret.SecretBox, log *slog.Logger) {
	seedTelegramToken(ctx, db, secrets, log, strings.TrimSpace(os.Getenv(EnvTelegramKey)))
}

func seedTelegramToken(ctx context.Context, db *store.DB, secrets secret.SecretBox, log *slog.Logger, token string) {
	if token == "" {
		return
	}
	sealed, hasToken, err := db.Settings.Get(ctx, store.SetTelegramToken)
	if err != nil {
		log.Error("notify: reading telegram token for env seed", "err", err)
		return
	}
	source := db.Settings.GetString(ctx, store.SetTelegramTokenSrc, "")
	if source == store.TokenSourceUI || (hasToken && source == "") {
		return // manually configured — never overwrite
	}
	if hasToken && source == store.TokenSourceEnv {
		if plain, err := secrets.Open(sealed); err == nil && string(plain) == token {
			return // unchanged since the last seed
		}
	}
	newSealed, err := secrets.Seal([]byte(token))
	if err != nil {
		log.Error("notify: sealing telegram token from env", "err", err)
		return
	}
	if err := db.Settings.Set(ctx, store.SetTelegramToken, newSealed); err != nil {
		log.Error("notify: storing telegram token from env", "err", err)
		return
	}
	if err := db.Settings.Set(ctx, store.SetTelegramTokenSrc, store.TokenSourceEnv); err != nil {
		log.Error("notify: storing telegram token source", "err", err)
		return
	}
	log.Info("notify: telegram token configurado a partir de " + EnvTelegramKey)
}
