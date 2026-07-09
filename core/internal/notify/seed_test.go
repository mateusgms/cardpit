package notify

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
)

func newSeedDB(t *testing.T) (*store.DB, context.Context) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, context.Background()
}

func storedToken(t *testing.T, ctx context.Context, db *store.DB) (string, bool) {
	t.Helper()
	sealed, ok, err := db.Settings.Get(ctx, store.SetTelegramToken)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return "", false
	}
	plain, err := secret.PlainBox{}.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	return string(plain), true
}

func TestSeedTelegramToken(t *testing.T) {
	box := secret.PlainBox{}

	t.Run("seeds when nothing configured", func(t *testing.T) {
		db, ctx := newSeedDB(t)
		seedTelegramToken(ctx, db, box, discard, "tok-env")
		if got, ok := storedToken(t, ctx, db); !ok || got != "tok-env" {
			t.Fatalf("token = %q, %v; want tok-env", got, ok)
		}
		if src := db.Settings.GetString(ctx, store.SetTelegramTokenSrc, ""); src != store.TokenSourceEnv {
			t.Fatalf("source = %q; want env", src)
		}
	})

	t.Run("re-seeds when env value rotates", func(t *testing.T) {
		db, ctx := newSeedDB(t)
		seedTelegramToken(ctx, db, box, discard, "tok-old")
		seedTelegramToken(ctx, db, box, discard, "tok-new")
		if got, _ := storedToken(t, ctx, db); got != "tok-new" {
			t.Fatalf("token = %q; want tok-new", got)
		}
	})

	t.Run("does not overwrite ui-configured token", func(t *testing.T) {
		db, ctx := newSeedDB(t)
		sealed, _ := box.Seal([]byte("tok-ui"))
		db.Settings.Set(ctx, store.SetTelegramToken, sealed)
		db.Settings.Set(ctx, store.SetTelegramTokenSrc, store.TokenSourceUI)
		seedTelegramToken(ctx, db, box, discard, "tok-env")
		if got, _ := storedToken(t, ctx, db); got != "tok-ui" {
			t.Fatalf("token = %q; want tok-ui", got)
		}
	})

	t.Run("does not touch pre-existing token without marker", func(t *testing.T) {
		db, ctx := newSeedDB(t)
		sealed, _ := box.Seal([]byte("tok-legacy"))
		db.Settings.Set(ctx, store.SetTelegramToken, sealed)
		seedTelegramToken(ctx, db, box, discard, "tok-env")
		if got, _ := storedToken(t, ctx, db); got != "tok-legacy" {
			t.Fatalf("token = %q; want tok-legacy", got)
		}
		if src := db.Settings.GetString(ctx, store.SetTelegramTokenSrc, ""); src != "" {
			t.Fatalf("source = %q; want empty", src)
		}
	})

	t.Run("empty env is a no-op", func(t *testing.T) {
		db, ctx := newSeedDB(t)
		seedTelegramToken(ctx, db, box, discard, "")
		if _, ok := storedToken(t, ctx, db); ok {
			t.Fatal("token stored for empty env value")
		}
	})
}

func TestSeedSourcePrecedence(t *testing.T) {
	box := secret.PlainBox{}
	old := buildTelegramKey
	t.Cleanup(func() { buildTelegramKey = old })

	t.Run("build-time key used when env unset", func(t *testing.T) {
		db, ctx := newSeedDB(t)
		t.Setenv(EnvTelegramKey, "")
		buildTelegramKey = "tok-build"
		SeedTelegramTokenFromEnv(ctx, db, box, discard)
		if got, _ := storedToken(t, ctx, db); got != "tok-build" {
			t.Fatalf("token = %q; want tok-build", got)
		}
	})

	t.Run("env var wins over build-time key", func(t *testing.T) {
		db, ctx := newSeedDB(t)
		t.Setenv(EnvTelegramKey, "tok-env")
		buildTelegramKey = "tok-build"
		SeedTelegramTokenFromEnv(ctx, db, box, discard)
		if got, _ := storedToken(t, ctx, db); got != "tok-env" {
			t.Fatalf("token = %q; want tok-env", got)
		}
	})
}
