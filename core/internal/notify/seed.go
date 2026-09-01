package notify

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// EnvTelegramKey is the runtime environment variable that pre-configures the
// Telegram bot token.
const EnvTelegramKey = "TELEGRAM_KEY"

// EnvTelegramChatID is the runtime environment variable that pre-configures
// the Telegram chat ID allowlist (comma-separated).
const EnvTelegramChatID = "TELEGRAM_CHAT_ID"

// SeedTelegramTokenFromEnv seals the runtime TELEGRAM_KEY into the settings on
// boot. A token entered through the UI always wins: once telegram_token_source
// is "ui" (or a token predates the marker) the environment value is ignored.
func SeedTelegramTokenFromEnv(ctx context.Context, db *store.DB, secrets secret.SecretBox, log *slog.Logger) {
	token := strings.TrimSpace(os.Getenv(EnvTelegramKey))
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
	log.Info("notify: telegram token pré-configurado aplicado (env " + EnvTelegramKey + ")")
}

// SeedTelegramChatIDsFromEnv writes the runtime TELEGRAM_CHAT_ID allowlist
// into the settings on boot. Chat IDs entered through the UI always win.
func SeedTelegramChatIDsFromEnv(ctx context.Context, db *store.DB, log *slog.Logger) {
	chatIDs := strings.TrimSpace(os.Getenv(EnvTelegramChatID))
	seedTelegramChatIDs(ctx, db, log, chatIDs)
}

func seedTelegramChatIDs(ctx context.Context, db *store.DB, log *slog.Logger, chatIDs string) {
	if chatIDs == "" {
		return
	}
	if len(parseChatIDs(chatIDs)) == 0 {
		log.Error("notify: chat IDs pré-configurados inválidos; ignorando", "value", chatIDs)
		return
	}
	existing, hasChatIDs, err := db.Settings.Get(ctx, store.SetTelegramChatIDs)
	if err != nil {
		log.Error("notify: reading telegram chat ids for env seed", "err", err)
		return
	}
	source := db.Settings.GetString(ctx, store.SetTelegramChatIDsSrc, "")
	if source == store.TokenSourceUI || (hasChatIDs && existing != "" && source == "") {
		return // manually configured — never overwrite
	}
	if source == store.TokenSourceEnv && existing == chatIDs {
		return // unchanged since the last seed
	}
	if err := db.Settings.Set(ctx, store.SetTelegramChatIDs, chatIDs); err != nil {
		log.Error("notify: storing telegram chat ids from env", "err", err)
		return
	}
	if err := db.Settings.Set(ctx, store.SetTelegramChatIDsSrc, store.TokenSourceEnv); err != nil {
		log.Error("notify: storing telegram chat ids source", "err", err)
		return
	}
	log.Info("notify: telegram chat IDs pré-configurados aplicados (env " + EnvTelegramChatID + ")")
}
