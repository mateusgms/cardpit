package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	cfg     config.Config
	verbose bool
	log     *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := runApp(ctx, p.cfg, p.log); err != nil && ctx.Err() == nil {
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
func runApp(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	// Remove leftover .old exe from any previous self-update swap.
	update.Recover(log)

	a, err := app.New(cfg, log)
	if err != nil {
		return err
	}
	defer a.Close()

	exe, _ := os.Executable()
	upd := update.New("mateusgms/cardpit", version, exe, a.DB, log)

	srv := httpapi.New(a.DB, a.Bus, a.Watcher, a.Manager, a.Secrets, cfg.Listen, log)
	srv.Version = version
	srv.CheckNow = upd.TriggerCheck
	disp := notify.NewDispatcher(a.DB, a.Bus, a.Secrets, log)
	srv.TgTest = disp.Test
	a.AddRunner(srv.Run)
	a.AddRunner(disp.Run)
	a.AddRunner(upd.Run)
	return a.Run(ctx)
}

// runCmd handles `cardpit run` for both the console and the SCM: kardianos
// Run() blocks on signals interactively and on SCM control otherwise.
func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "path to the bootstrap config file")
	verbose := fs.Bool("v", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := logging.Setup(cfg.LogPath, level)
	log.Info("cardpit starting", "version", version, "config", *cfgPath,
		"interactive", service.Interactive())

	abs, err := filepath.Abs(*cfgPath)
	if err != nil {
		abs = *cfgPath
	}
	prg := &program{cfg: cfg, verbose: *verbose, log: log}
	s, err := service.New(prg, svcConfig(abs))
	if err != nil {
		return err
	}
	return s.Run()
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
