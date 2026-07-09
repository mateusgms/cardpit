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

// buildTelegramKey is stamped into release binaries by the pipeline via
// -ldflags "-X .../notify.buildTelegramKey=<token>" (see the Makefile), so a
// distributed exe comes with Telegram pre-configured. The TELEGRAM_KEY env
// var, when set, takes precedence over the stamped value.
var buildTelegramKey string

// SeedTelegramTokenFromEnv seals the pre-configured token (TELEGRAM_KEY env
// var, falling back to the build-time stamp) into the settings on boot so a
// fresh install comes up with Telegram already configured. A token entered
// through the UI always wins: once telegram_token_source is "ui" (or a token
// predates the marker) the pre-configured value is ignored.
func SeedTelegramTokenFromEnv(ctx context.Context, db *store.DB, secrets secret.SecretBox, log *slog.Logger) {
	token := strings.TrimSpace(os.Getenv(EnvTelegramKey))
	if token == "" {
		token = strings.TrimSpace(buildTelegramKey)
	}
	seedTelegramToken(ctx, db, secrets, log, token)
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
	log.Info("notify: telegram token pré-configurado aplicado (env " +
		EnvTelegramKey + " ou build)")
}
