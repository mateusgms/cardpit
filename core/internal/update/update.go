// Package update checks GitHub Releases for newer versions and swaps the
// running executable in place, then signals the service manager to restart.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kardianos/service"

	"github.com/mateusgms/cardpit/core/internal/store"
)

const (
	// PublicReleaseRepo is the credential-free public repository used for
	// update discovery and downloads. Keep this separate from the source repo.
	PublicReleaseRepo = "mateusgms/cardpit-releases"
	defaultAPIBase    = "https://api.github.com"
	checkInterval     = 24 * time.Hour
	startupDelay      = 30 * time.Second
	busyRetry         = 10 * time.Minute
)

// Updater checks for newer GitHub Releases and swaps the running executable.
type Updater struct {
	repo    string // e.g. "mateusgms/cardpit-releases"
	version string // ldflags-injected, e.g. "v0.1.0"
	exePath string // from os.Executable()
	db      *store.DB
	log     *slog.Logger
	apiBase string // injectable for tests; default defaultAPIBase

	checkNow chan bool // buffered(1); true = manual/forced (bypasses auto_update), false = automatic

	// activeJobs returns the number of running/pending jobs. Injected for tests.
	activeJobs func(ctx context.Context) (int, error)
}

// New returns a configured Updater. exePath should be os.Executable().
func New(repo, version, exePath string, db *store.DB, log *slog.Logger) *Updater {
	u := &Updater{
		repo:     repo,
		version:  version,
		exePath:  exePath,
		db:       db,
		log:      log,
		apiBase:  defaultAPIBase,
		checkNow: make(chan bool, 1),
	}
	u.activeJobs = func(ctx context.Context) (int, error) {
		jobs, err := db.Jobs.Active(ctx)
		return len(jobs), err
	}
	return u
}

// TriggerCheck asks the updater to run an immediate manual check, bypassing
// the auto_update setting. Non-blocking.
func (u *Updater) TriggerCheck() {
	select {
	case u.checkNow <- true:
	default:
	}
}

// Run blocks until ctx is done. Register it via app.AddRunner.
func (u *Updater) Run(ctx context.Context) error {
	if _, err := parseSemver(u.version); err != nil {
		u.log.Info("update: skipping (non-semver build)", "version", u.version)
		return nil
	}

	// Wait for startup delay or a manual trigger before the first check.
	forced := false
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(startupDelay):
	case forced = <-u.checkNow:
	}

	u.tryUpdate(ctx, forced)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			u.tryUpdate(ctx, false)
		case forced = <-u.checkNow:
			u.tryUpdate(ctx, forced)
		}
	}
}

func (u *Updater) tryUpdate(ctx context.Context, force bool) {
	if !force && !u.db.Settings.GetBool(ctx, store.SetAutoUpdate, true) {
		u.log.Debug("update: auto_update disabled, skipping")
		return
	}
	latestTag, assetURL, checksumURL, err := u.fetchLatest(ctx)
	if err != nil {
		u.log.Warn("update: release check failed", "err", err)
		return
	}
	latest, err := parseSemver(latestTag)
	if err != nil {
		u.log.Warn("update: unreadable remote tag", "tag", latestTag)
		return
	}
	current, _ := parseSemver(u.version)
	if !latest.newerThan(current) {
		u.log.Info("update: already up to date", "version", u.version)
		return
	}
	u.log.Info("update: newer version available", "current", u.version, "latest", latestTag)
	if err := u.download(ctx, assetURL, checksumURL, latestTag); err != nil {
		u.log.Error("update: download/swap failed", "err", err)
	}
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (u *Updater) fetchLatest(ctx context.Context) (tag, assetURL, checksumURL string, err error) {
	url := u.apiBase + "/repos/" + u.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", "", err
	}
	for _, a := range rel.Assets {
		switch a.Name {
		case "cardpit.exe":
			assetURL = a.BrowserDownloadURL
		case "checksums.txt":
			checksumURL = a.BrowserDownloadURL
		}
	}
	if assetURL == "" || checksumURL == "" {
		return "", "", "", fmt.Errorf("release %s missing required assets", rel.TagName)
	}
	return rel.TagName, assetURL, checksumURL, nil
}

func (u *Updater) download(ctx context.Context, assetURL, checksumURL, newTag string) error {
	newPath := u.exePath + ".new"
	defer os.Remove(newPath)

	if err := u.downloadFile(ctx, assetURL, newPath); err != nil {
		return fmt.Errorf("downloading exe: %w", err)
	}

	wantHash, err := u.fetchChecksum(ctx, checksumURL, "cardpit.exe")
	if err != nil {
		return fmt.Errorf("fetching checksum: %w", err)
	}
	gotHash, err := sha256File(newPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(gotHash, wantHash) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", gotHash, wantHash)
	}
	u.log.Info("update: checksum verified", "sha256", gotHash[:16]+"…")

	// Wait until no jobs are running before swapping the binary.
	for {
		count, err := u.activeJobs(ctx)
		if err != nil {
			return fmt.Errorf("checking active jobs: %w", err)
		}
		if count == 0 {
			break
		}
		u.log.Info("update: waiting for jobs to finish before restart", "active", count)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(busyRetry):
		}
	}

	// Atomic swap: rename current → .old, .new → current.
	// Windows allows renaming a running exe; .old is cleaned up on next boot.
	oldPath := u.exePath + ".old"
	os.Remove(oldPath) // best-effort cleanup of leftovers
	if err := os.Rename(u.exePath, oldPath); err != nil {
		return fmt.Errorf("renaming current exe: %w", err)
	}
	if err := os.Rename(newPath, u.exePath); err != nil {
		os.Rename(oldPath, u.exePath) // rollback
		return fmt.Errorf("placing new exe (rolled back): %w", err)
	}
	u.log.Info("update: exe swapped successfully", "from", u.version, "to", newTag)

	if !service.Interactive() {
		// Service recovery (OnFailure=restart) will relaunch the new binary.
		u.log.Info("update: exiting for service recovery restart")
		os.Exit(3)
	}
	u.log.Info("update: atualizado para " + newTag + "; reinicie o cardpit para usar a nova versão")
	return nil
}

// Recover removes <exe>.old left by a previous update. Call at startup.
func Recover(log *slog.Logger) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	old := exe + ".old"
	if err := os.Remove(old); err == nil {
		log.Info("update: removed leftover from previous update", "path", old)
	}
}

func (u *Updater) downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	f.Close()
	return err
}

func (u *Updater) fetchChecksum(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums download: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// sha256sum format: "<hash>  <filename>" one per line
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == name {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %q not found in checksums.txt", name)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
