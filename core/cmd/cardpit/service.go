package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/mateusgms/cardpit/core/internal/app"
	"github.com/mateusgms/cardpit/core/internal/config"
	"github.com/mateusgms/cardpit/core/internal/httpapi"
	"github.com/mateusgms/cardpit/core/internal/logging"
	"github.com/mateusgms/cardpit/core/internal/notify"
	"github.com/mateusgms/cardpit/core/internal/update"
)

func svcConfig(absConfigPath string) *service.Config {
	return &service.Config{
		Name:        "cardpit",
		DisplayName: "cardpit — ingestão de cartões de memória",
		Description: "Detecta cartões de memória, copia para o SSD com verificação de integridade e notifica via Telegram.",
		Arguments:   []string{"run", "--config", absConfigPath},
		Option: service.KeyValue{
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
			"OnFailureResetPeriod":   10,
		},
	}
}

// program adapts the app to kardianos/service. Start must return
// immediately (SCM contract); the app runs in a goroutine.
type program struct {
	cfg      config.Config
	verbose  bool
	log      *slog.Logger
	ring     *logging.Ring
	level    *slog.LevelVar
	autoOpen bool // open the browser once the server is listening

	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := p.runApp(ctx, cancel); err != nil && ctx.Err() == nil {
			if errors.Is(err, httpapi.ErrAddrInUse) {
				// Another instance already owns the port: not a crash.
				p.log.Info("cardpit já está em execução nesta porta; nada a fazer")
				return
			}
			p.log.Error("cardpit terminated unexpectedly", "err", err)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		p.log.Warn("service stop timed out waiting for clean shutdown")
	}
	return nil
}

// runApp wires and runs every component (shared by console and service).
// cancel backs the in-UI "Encerrar" button when running interactively.
func (p *program) runApp(ctx context.Context, cancel context.CancelFunc) error {
	// Remove leftover .old exe from any previous self-update swap.
	update.Recover(p.log)

	a, err := app.New(p.cfg, p.log)
	if err != nil {
		return err
	}
	defer a.Close()

	exe, _ := os.Executable()
	upd := update.New("mateusgms/cardpit", version, exe, a.DB, p.log)

	srv := httpapi.New(a.DB, a.Bus, a.Watcher, a.Manager, a.Secrets, p.cfg.Listen,
		p.log, p.ring, p.level)
	srv.Version = version
	srv.CheckNow = upd.TriggerCheck
	srv.Platform = p.cfg.Platform
	srv.DBPath = p.cfg.DBPath
	srv.Interactive = service.Interactive()
	if srv.Interactive {
		srv.Shutdown = cancel // let the UI stop a user-launched worker
	}
	if p.autoOpen {
		srv.OnReady = func(tok string) { go openBrowser(uiURL(p.cfg.Listen, tok)) }
	}
	// Seed the Telegram token from TELEGRAM_KEY before the dispatcher's first
	// supervise tick, so a fresh install starts with the notifier configured.
	notify.SeedTelegramTokenFromEnv(ctx, a.DB, a.Secrets, p.log)
	disp := notify.NewDispatcher(a.DB, a.Bus, a.Secrets, p.log)
	disp.SetListenAddr(p.cfg.Listen)
	srv.TgTest = disp.Test
	a.AddRunner(srv.Run)
	a.AddRunner(disp.Run)
	a.AddRunner(upd.Run)
	return a.Run(ctx)
}

// runInteractive runs the worker in the foreground process (console double-
// click or `cardpit open`), owning its own signal handling so the UI's
// "Encerrar" button and Ctrl-C both stop the process cleanly.
func (p *program) runInteractive() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := p.runApp(ctx, cancel)
	if errors.Is(err, httpapi.ErrAddrInUse) {
		// A sibling instance already serves the UI — open it instead of failing.
		p.log.Info("cardpit já está em execução; abrindo o painel")
		if p.autoOpen {
			openBrowser(uiURL(p.cfg.Listen, apiTokenString(p.cfg)))
		}
		return nil
	}
	return err
}

// startService loads config, sets up logging, and runs the worker either in
// the foreground (interactive) or under the service manager (SCM).
func startService(cfgPath string, verbose, autoOpen bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	log, levelVar, ring := logging.Setup(cfg.LogPath, level)
	log.Info("cardpit starting", "version", version, "config", cfgPath,
		"interactive", service.Interactive())

	prg := &program{cfg: cfg, verbose: verbose, log: log, ring: ring,
		level: levelVar, autoOpen: autoOpen}

	if service.Interactive() {
		return prg.runInteractive()
	}

	// Under the service manager: kardianos drives Start/Stop via the SCM.
	abs, err := filepath.Abs(cfgPath)
	if err != nil {
		abs = cfgPath
	}
	s, err := service.New(prg, svcConfig(abs))
	if err != nil {
		return err
	}
	return s.Run()
}

// runCmd handles `cardpit run` for both the console and the SCM.
func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "path to the bootstrap config file")
	verbose := fs.Bool("v", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Interactive `run` opens the browser too, matching the double-click flow.
	return startService(*cfgPath, *verbose, service.Interactive())
}

// svcCmd handles install/uninstall/start/stop/status.
func svcCmd(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "config file the service will use (stored absolute)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, err := filepath.Abs(*cfgPath)
	if err != nil {
		return err
	}
	s, err := service.New(&program{log: slog.Default()}, svcConfig(abs))
	if err != nil {
		return err
	}
	switch action {
	case "status":
		st, err := s.Status()
		if err != nil {
			return err
		}
		names := map[service.Status]string{
			service.StatusRunning: "running",
			service.StatusStopped: "stopped",
			service.StatusUnknown: "unknown",
		}
		fmt.Println("cardpit:", names[st])
		return nil
	case "install":
		if err := service.Control(s, "install"); err != nil {
			return err
		}
		fmt.Printf("serviço instalado (config: %s)\nuse `cardpit start` para iniciar\n", abs)
		return nil
	default:
		if err := service.Control(s, action); err != nil {
			return err
		}
		fmt.Println("ok:", action)
		return nil
	}
}

// defaultConfigPath is config.yaml next to the executable — sensible for a
// service whose cwd is System32.
func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml")
}
