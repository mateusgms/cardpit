// cardpit — automatic memory-card ingestion station.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mateusgms/cardpit/core/internal/app"
	"github.com/mateusgms/cardpit/core/internal/config"
	"github.com/mateusgms/cardpit/core/internal/logging"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run":
		if err := runCmd(args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("cardpit", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: cardpit [run|version] [flags]\n", cmd)
		os.Exit(2)
	}
}

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
	log.Info("cardpit starting", "version", version, "config", *cfgPath)

	a, err := app.New(cfg, log)
	if err != nil {
		return err
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.Run(ctx)
}
