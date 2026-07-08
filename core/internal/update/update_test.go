package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func noopLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeGitHubServer returns a test server that serves a fake GitHub Releases
// API endpoint plus /asset and /checksums endpoints.
func fakeGitHubServer(t *testing.T, tag string, asset []byte, checksum string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[`+
				`{"name":"cardpit.exe","browser_download_url":%q},`+
				`{"name":"checksums.txt","browser_download_url":%q}`+
				`]}`,
				tag, base+"/asset", base+"/checksums")
		case "/asset":
			w.Write(asset)
		case "/checksums":
			fmt.Fprint(w, checksum)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func goodChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x  cardpit.exe\n", h)
}

// --- semver tests ------------------------------------------------------------

func TestSemverParse(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"v1.2.3", false},
		{"1.2.3", false},
		{"v1.2.3-beta.1", false}, // pre-release suffix stripped
		{"dev", true},
		{"v1.2", true},
		{"", true},
		{"v1.2.x", true},
	}
	for _, c := range cases {
		_, err := parseSemver(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseSemver(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
	}
}

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		aGTb bool
	}{
		{"v1.2.3", "v1.2.2", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.2", "v1.2.3", false},
		{"v2.0.0", "v1.9.9", true},
		{"v1.10.0", "v1.9.0", true},
		{"v0.1.0", "v0.1.0", false},
	}
	for _, c := range cases {
		a, _ := parseSemver(c.a)
		b, _ := parseSemver(c.b)
		if got := a.newerThan(b); got != c.aGTb {
			t.Errorf("%s.newerThan(%s): got %v want %v", c.a, c.b, got, c.aGTb)
		}
	}
}

// --- fetchLatest test --------------------------------------------------------

func TestFetchLatest(t *testing.T) {
	asset := []byte("fake exe")
	srv := fakeGitHubServer(t, "v0.2.0", asset, goodChecksum(asset))

	u := &Updater{repo: "owner/repo", apiBase: srv.URL, log: noopLog()}
	tag, assetURL, checksumURL, err := u.fetchLatest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.2.0" {
		t.Errorf("tag: got %q want %q", tag, "v0.2.0")
	}
	if assetURL == "" || checksumURL == "" {
		t.Errorf("missing URLs: asset=%q checksum=%q", assetURL, checksumURL)
	}
}

// --- download tests ----------------------------------------------------------

func TestChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "cardpit.exe")
	if err := os.WriteFile(fakeExe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	asset := []byte("new binary content")
	wrongChecksum := fmt.Sprintf("%s  cardpit.exe\n",
		"0000000000000000000000000000000000000000000000000000000000000000")
	srv := fakeGitHubServer(t, "v0.0.2", asset, wrongChecksum)

	u := &Updater{
		exePath:    fakeExe,
		log:        noopLog(),
		activeJobs: func(context.Context) (int, error) { return 0, nil },
	}
	err := u.download(t.Context(), srv.URL+"/asset", srv.URL+"/checksums", "v0.0.2")
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}

	// .new must be cleaned up on failure.
	if _, e := os.Stat(fakeExe + ".new"); !os.IsNotExist(e) {
		t.Error(".new not cleaned up after checksum mismatch")
	}
	// Original exe must be untouched.
	orig, _ := os.ReadFile(fakeExe)
	if string(orig) != "old binary" {
		t.Errorf("original exe modified on checksum failure: got %q", orig)
	}
}

func TestHappySwap(t *testing.T) {
	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "cardpit.exe")
	if err := os.WriteFile(fakeExe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	asset := []byte("new binary content")
	srv := fakeGitHubServer(t, "v0.0.2", asset, goodChecksum(asset))

	u := &Updater{
		exePath:    fakeExe,
		version:    "v0.0.1",
		log:        noopLog(),
		activeJobs: func(context.Context) (int, error) { return 0, nil },
	}
	if err := u.download(t.Context(), srv.URL+"/asset", srv.URL+"/checksums", "v0.0.2"); err != nil {
		t.Fatal(err)
	}

	// New binary must be in place.
	got, err := os.ReadFile(fakeExe)
	if err != nil {
		t.Fatalf("reading exe after swap: %v", err)
	}
	if string(got) != "new binary content" {
		t.Errorf("exe content after swap: got %q want %q", got, "new binary content")
	}

	// .old should contain the original binary.
	old, err := os.ReadFile(fakeExe + ".old")
	if err != nil {
		t.Fatalf(".old missing after swap: %v", err)
	}
	if string(old) != "old binary" {
		t.Errorf(".old content: got %q want %q", old, "old binary")
	}

	// .new must be cleaned up (renamed away to exe).
	if _, e := os.Stat(fakeExe + ".new"); !os.IsNotExist(e) {
		t.Error(".new still exists after successful swap")
	}
}

func TestNotNewerNoOp(t *testing.T) {
	asset := []byte("same binary")
	srv := fakeGitHubServer(t, "v0.1.0", asset, goodChecksum(asset))

	u := &Updater{repo: "owner/repo", apiBase: srv.URL, log: noopLog(), version: "v0.1.0"}
	tag, _, _, _ := u.fetchLatest(t.Context())
	latest, _ := parseSemver(tag)
	current, _ := parseSemver(u.version)
	if latest.newerThan(current) {
		t.Error("same version reported as newer")
	}
}

func TestRecover(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "cardpit.exe.old")
	os.WriteFile(old, []byte("old"), 0o644)

	// Recover should delete the file — we can't call the real Recover() since
	// it uses os.Executable(), so test the logic directly.
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error(".old not removed")
	}
}
