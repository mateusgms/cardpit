// cardpit — automatic memory-card ingestion station.
//
// Subcommands:
//
//	run        run the service (console or under the Windows SCM)
//	install    register as a Windows service (auto-start)
//	uninstall  remove the Windows service
//	start/stop/restart/status
//	setup      one-command install: write config, install + start service, print token (Windows, admin)
//	token      print the DPAPI-unsealed API token (Windows)
//	tray       per-user tray icon (Windows; talks to the service via the API)
//	version
package main

import (
	"fmt"
	"os"
	"strings"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "run":
		err = runCmd(args)
	case "install", "uninstall", "start", "stop", "restart", "status":
		err = svcCmd(cmd, args)
	case "setup":
		err = setupCmd(args)
	case "token":
		err = tokenCmd(args)
	case "tray":
		err = trayCmd(args)
	case "version":
		fmt.Println("cardpit", version)
	default:
		fmt.Fprintf(os.Stderr,
			"uso: cardpit [run|install|uninstall|start|stop|restart|status|setup|token|tray|version] [flags]\n")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
