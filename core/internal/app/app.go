// Package app wires every component together and runs them.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/config"
	"github.com/mateusgms/cardpit/core/internal/engine"
	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/platform/fake"
	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
	"github.com/mateusgms/cardpit/core/internal/watcher"
)

type App struct {
	Cfg      config.Config
	Log      *slog.Logger
	DB       *store.DB
	Bus      *bus.Bus
	Platform platform.Platform
	Secrets  secret.SecretBox
	Watcher  *watcher.Watcher
	Manager  *engine.Manager

	// extraRunners lets later layers (HTTP server, notifier) join Run's
	// lifecycle without app depending on them at construction time.
	extraRunners []func(ctx context.Context) error
}

func New(cfg config.Config, log *slog.Logger) (*App, error) {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", cfg.DBPath, err)
	}

	var plat platform.Platform
	var secrets secret.SecretBox
	switch cfg.Platform {
	case "windows":
		plat, secrets, err = newWindowsPlatform()
		if err != nil {
			db.Close()
			return nil, err
		}
	default:
		log.Warn("app: using FAKE platform (development mode)",
			"fake_root", cfg.FakeRoot, "fake_dest", cfg.FakeDest)
		log.Warn("app: secrets are NOT protected on this platform (PlainBox)")
		plat = fake.New(cfg.FakeRoot, cfg.FakeDest)
		secrets = secret.PlainBox{}
	}

	a := &App{
		Cfg:      cfg,
		Log:      log,
		DB:       db,
		Bus:      bus.New(),
		Platform: plat,
		Secrets:  secrets,
	}
	a.Watcher = watcher.New(plat, a.Bus, watcher.Options{
		PollInterval: cfg.PollInterval,
		Debounce:     cfg.Debounce,
	}, log)
	a.Manager = engine.NewManager(db, plat, a.Bus, log)
	return a, nil
}

// AddRunner registers an additional long-running component (HTTP server,
// notifier dispatcher). Must be called before Run.
func (a *App) AddRunner(fn func(ctx context.Context) error) {
	a.extraRunners = append(a.extraRunners, fn)
}

// Run performs boot recovery and runs every component until ctx is
// cancelled or one of them fails.
func (a *App) Run(ctx context.Context) error {
	if err := a.Manager.Recover(ctx); err != nil {
		return fmt.Errorf("boot recovery: %w", err)
	}
	if a.DB.Settings.GetBool(ctx, store.SetWatcherPaused, false) {
		a.Watcher.SetPaused(true)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return ignoreCancel(a.Watcher.Run(gctx)) })
	g.Go(func() error { return ignoreCancel(a.Manager.Run(gctx)) })
	for _, fn := range a.extraRunners {
		fn := fn
		g.Go(func() error { return ignoreCancel(fn(gctx)) })
	}
	a.Log.Info("cardpit running",
		"platform", a.Cfg.Platform, "db", a.Cfg.DBPath, "listen", a.Cfg.Listen)
	return g.Wait()
}

func (a *App) Close() error { return a.DB.Close() }

// ignoreCancel keeps a clean shutdown from being reported as an error.
func ignoreCancel(err error) error {
	if err == context.Canceled {
		return nil
	}
	return err
}
