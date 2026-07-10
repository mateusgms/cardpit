package httpapi

import (
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mateusgms/cardpit/core/internal/httpapi/webui"
)

// handleLogs returns recent in-memory log records (oldest first).
// Query params: limit (default 500), level (debug|info|warn|error).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.ring == nil {
		writeJSON(w, http.StatusOK, map[string]any{"records": []any{}})
		return
	}
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	min := parseLevel(r.URL.Query().Get("level"), slog.LevelDebug)
	writeJSON(w, http.StatusOK, map[string]any{
		"records": s.ring.Snapshot(limit, min),
	})
}

// handleSetLevel raises or lowers the file/stderr log threshold at runtime.
// The in-memory ring always keeps Debug regardless of this setting.
func (s *Server) handleSetLevel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	switch body.Level {
	case "debug", "info", "warn", "error":
	default:
		writeErr(w, http.StatusBadRequest, "level deve ser debug, info, warn ou error")
		return
	}
	lvl := parseLevel(body.Level, slog.LevelInfo)
	if s.level != nil {
		s.level.Set(lvl)
	}
	s.log.Info("httpapi: log level changed", "level", body.Level)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "level": body.Level})
}

type diagnosticsResponse struct {
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	GoVersion     string `json:"go_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Listen        string `json:"listen"`
	DBPath        string `json:"db_path"`
	LogPath       string `json:"log_path"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	UIPlaceholder bool   `json:"ui_placeholder"`
	Interactive   bool   `json:"interactive"`
	CanShutdown   bool   `json:"can_shutdown"`
	LogLevel      string `json:"log_level"`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	level := "info"
	if s.level != nil {
		level = strings.ToLower(s.level.Level().String())
	}
	writeJSON(w, http.StatusOK, diagnosticsResponse{
		Version:       s.Version,
		Platform:      s.Platform,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Listen:        s.listen,
		DBPath:        s.DBPath,
		LogPath:       s.LogPath,
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		UIPlaceholder: webui.IsPlaceholder(),
		Interactive:   s.Interactive,
		CanShutdown:   s.Shutdown != nil,
		LogLevel:      level,
	})
}

// handleShutdown gracefully stops a user-launched (in-process) worker. When
// cardpit runs under the service manager, Shutdown is nil and stopping is the
// service manager's job.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if s.Shutdown == nil {
		writeErr(w, http.StatusConflict, "cardpit está sob o gerenciador de serviços; pare pelo serviço")
		return
	}
	s.log.Info("httpapi: shutdown requested via UI")
	writeJSON(w, http.StatusOK, map[string]string{"status": "encerrando"})
	// Trigger after the response is flushed so the UI sees the confirmation.
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.Shutdown()
	}()
}

func parseLevel(name string, fallback slog.Level) slog.Level {
	switch strings.ToLower(name) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return fallback
	}
}
