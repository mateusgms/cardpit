// Package httpapi serves the REST API, the SSE event stream and the
// embedded web UI from a single listener.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/engine"
	"github.com/mateusgms/cardpit/core/internal/httpapi/webui"
	"github.com/mateusgms/cardpit/core/internal/logging"
	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
	"github.com/mateusgms/cardpit/core/internal/watcher"
)

// ErrAddrInUse is returned by Run when the listen address is already bound —
// almost always another cardpit instance. Callers treat it as "already
// running" (open the browser) rather than a fatal crash.
var ErrAddrInUse = errors.New("httpapi: endereço já em uso")

// TelegramTester is injected by the notify layer; nil means "not configured".
type TelegramTester func(ctx context.Context) error

type Server struct {
	db      *store.DB
	bus     *bus.Bus
	watcher *watcher.Watcher
	manager *engine.Manager
	secrets secret.SecretBox
	log     *slog.Logger
	listen  string
	ring    *logging.Ring
	level   *slog.LevelVar

	TgTest   TelegramTester
	Version  string // ldflags-injected build version, surfaced in /api/status
	CheckNow func() // triggers an immediate update check; nil if updater absent

	// DestCandidates lists the fixed disks offered by the destination picker;
	// nil-safe (the endpoint answers an empty list).
	DestCandidates platform.DestCandidateLister

	// Diagnostics context, set by the wiring layer after construction.
	Platform    string       // "windows" | "fake"
	DBPath      string       // absolute path to the SQLite database
	LogPath     string       // log file path; empty means in-memory only
	Interactive bool         // true when user-launched (not under the service manager)
	Shutdown    func()       // graceful stop; nil when managed by the service manager
	OnReady     func(string) // called with the token once the listener is bound
	startedAt   time.Time

	token       string // plaintext API token, loaded/generated at startup
	calibration atomic.Pointer[pendingCalibration]
}

type pendingCalibration struct {
	Alias    string    `json:"alias"`
	ArmedAt  time.Time `json:"armed_at"`
	Deadline time.Time `json:"deadline"`
}

func New(db *store.DB, b *bus.Bus, w *watcher.Watcher, m *engine.Manager,
	secrets secret.SecretBox, listen string, log *slog.Logger,
	ring *logging.Ring, level *slog.LevelVar) *Server {
	return &Server{
		db: db, bus: b, watcher: w, manager: m,
		secrets: secrets, listen: listen, log: log,
		ring: ring, level: level, startedAt: time.Now(),
	}
}

// Run initializes the auth token, watches calibration attaches and serves
// HTTP until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if err := s.initToken(ctx); err != nil {
		return err
	}
	go s.watchCalibration(ctx)

	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	// Bind explicitly so we can turn "address already in use" into a clean
	// "already running" signal instead of a fatal crash.
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return ErrAddrInUse
		}
		return fmt.Errorf("httpapi: %w", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	s.log.Info("httpapi: listening", "addr", s.listen, "ui_placeholder", webui.IsPlaceholder())
	if s.OnReady != nil {
		s.OnReady(s.token)
	}

	select {
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shCtx)
		return ctx.Err()
	case err := <-errCh:
		return fmt.Errorf("httpapi: %w", err)
	}
}

// initToken loads the sealed API token or generates one on first boot.
func (s *Server) initToken(ctx context.Context) error {
	sealed, ok, err := s.db.Settings.Get(ctx, store.SetAPIToken)
	if err != nil {
		return err
	}
	if ok {
		plain, err := s.secrets.Open(sealed)
		if err != nil {
			return fmt.Errorf("httpapi: unsealing api token: %w", err)
		}
		s.token = string(plain)
		return nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	s.token = hex.EncodeToString(buf)
	sealedNew, err := s.secrets.Seal([]byte(s.token))
	if err != nil {
		return err
	}
	if err := s.db.Settings.Set(ctx, store.SetAPIToken, sealedNew); err != nil {
		return err
	}
	// Surfaced once, on first boot — the user pastes this into the UI.
	// It goes to the log too because a Windows service has no console; the
	// log file is admin-readable only (ProgramData), same trust boundary as
	// the DPAPI-sealed settings.
	fmt.Printf("\n=== cardpit: token de acesso gerado (primeiro boot) ===\n%s\n=======================================================\n\n", s.token)
	s.log.Info("httpapi: first-boot API token generated", "token", s.token)
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/events", s.handleSSE)
	mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/jobs/{id}/files", s.handleJobFiles)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleCancelJob)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("GET /api/cards", s.handleListCards)
	mux.HandleFunc("POST /api/cards", s.handleCreateCard)
	mux.HandleFunc("PUT /api/cards/{id}", s.handleUpdateCard)
	mux.HandleFunc("DELETE /api/cards/{id}", s.handleDeleteCard)
	mux.HandleFunc("POST /api/cards/decision", s.handleCardDecision)
	mux.HandleFunc("GET /api/volumes/dest-candidates", s.handleDestCandidates)
	mux.HandleFunc("GET /api/slots", s.handleListSlots)
	mux.HandleFunc("PUT /api/slots/{id}", s.handleUpdateSlot)
	mux.HandleFunc("DELETE /api/slots/{id}", s.handleDeleteSlot)
	mux.HandleFunc("POST /api/slots/calibrate", s.handleCalibrate)
	mux.HandleFunc("DELETE /api/slots/calibrate", s.handleCancelCalibrate)
	mux.HandleFunc("POST /api/telegram/test", s.handleTelegramTest)
	mux.HandleFunc("POST /api/update/check", s.handleUpdateCheck)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("POST /api/logs/level", s.handleSetLevel)
	mux.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)

	dist, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		panic(err) // embed is broken at build time, not a runtime condition
	}
	mux.Handle("/", spaHandler(dist))

	return s.authMiddleware(mux)
}

// authMiddleware guards /api/* with a constant-time bearer check. The SSE
// endpoint also accepts ?token= because EventSource cannot set headers.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r) // static UI is public; all data flows via /api
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" && r.URL.Path == "/api/events" {
			got = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "token inválido")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves the embedded UI with an index.html fallback for
// client-side routes.
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// --- calibration ----------------------------------------------------------

// watchCalibration binds the next attached volume to the armed alias.
func (s *Server) watchCalibration(ctx context.Context) {
	sub := s.bus.Subscribe(64, bus.TopicVolumeAttached)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-sub.C:
			pc := s.calibration.Load()
			if pc == nil {
				continue
			}
			if time.Now().After(pc.Deadline) {
				s.calibration.Store(nil)
				continue
			}
			va := e.Payload.(bus.VolumeAttached)
			if va.SlotLocationPath == "" {
				s.log.Warn("httpapi: calibration attach had no location path; still waiting")
				continue
			}
			slot, err := s.db.Slots.Upsert(ctx, va.SlotLocationPath, va.SlotLUN, pc.Alias)
			if err != nil {
				s.log.Error("httpapi: calibration upsert", "err", err)
				continue
			}
			s.calibration.Store(nil)
			s.log.Info("httpapi: slot calibrated", "alias", slot.Alias,
				"location_path", slot.LocationPath, "lun", slot.LUN)
			s.bus.Publish(bus.Event{Topic: bus.TopicSlotCalibrated, Payload: bus.SlotCalibrated{
				SlotID: slot.ID, Alias: slot.Alias,
				LocationPath: slot.LocationPath, LUN: slot.LUN,
			}})
		}
	}
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("corpo JSON inválido: " + err.Error())
	}
	return nil
}
