package main

import (
	"flag"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/mateusgms/cardpit/core/internal/config"
)

// openCmd is the double-click default: if cardpit is already running it just
// opens the browser to the UI (never binding a second time, so no "port in
// use" crash); otherwise it starts the worker and opens the UI when ready.
func openCmd(args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "path to the bootstrap config file")
	verbose := fs.Bool("v", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if alreadyRunning(cfg.Listen) {
		openBrowser(uiURL(cfg.Listen, apiTokenString(cfg)))
		return nil
	}
	return startService(*cfgPath, *verbose, true)
}

// alreadyRunning probes the local UI to see if an instance is serving it.
func alreadyRunning(listen string) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get(baseURL(listen) + "/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// baseURL turns a listen address (host:port, possibly 0.0.0.0) into a
// browser-reachable localhost URL.
func baseURL(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		port = "8532"
	}
	return "http://localhost:" + port
}

// uiURL is the UI entry point; when a token is known it is passed so the
// browser logs in automatically instead of prompting for a paste.
func uiURL(listen, token string) string {
	base := baseURL(listen)
	if token != "" {
		return base + "/token?t=" + url.QueryEscape(token)
	}
	return base + "/"
}
