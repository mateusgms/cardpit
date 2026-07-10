// Package logging configures the process-wide structured JSON logger.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Record is a captured log line, retained in memory for the diagnostics UI.
type Record struct {
	Time  time.Time      `json:"time"`
	Level string         `json:"level"`
	Msg   string         `json:"msg"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Ring is a fixed-capacity, thread-safe circular buffer of recent log
// records. It always keeps Debug-level lines (regardless of the file/stderr
// level) so the UI can surface debug detail without a restart.
type Ring struct {
	mu   sync.Mutex
	buf  []Record
	next int
	full bool
}

// NewRing returns a Ring retaining up to capacity records (min 1).
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]Record, capacity)}
}

func (r *Ring) append(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = rec
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// Snapshot returns the most recent records (oldest first) whose level is at
// least min. limit <= 0 means "no limit".
func (r *Ring) Snapshot(limit int, min slog.Level) []Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.next
	if r.full {
		n = len(r.buf)
	}
	out := make([]Record, 0, n)
	// Walk in chronological order.
	for i := 0; i < n; i++ {
		var idx int
		if r.full {
			idx = (r.next + i) % len(r.buf)
		} else {
			idx = i
		}
		rec := r.buf[idx]
		if levelValue(rec.Level) < min {
			continue
		}
		out = append(out, rec)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func levelValue(name string) slog.Level {
	switch name {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ringHandler captures every record into a Ring. It is always enabled (at
// Debug) so the buffer keeps debug lines even when the file handler filters
// them out.
type ringHandler struct {
	ring  *Ring
	attrs []slog.Attr
	group string
}

func (h *ringHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *ringHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs()+len(h.attrs))
	for _, a := range h.attrs {
		attrs[a.Key] = attrValue(a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = attrValue(a.Value)
		return true
	})
	if len(attrs) == 0 {
		attrs = nil
	}
	h.ring.append(Record{
		Time:  r.Time,
		Level: r.Level.String(),
		Msg:   r.Message,
		Attrs: attrs,
	})
	return nil
}

// attrValue makes attrs JSON-friendly for the diagnostics UI: an error value
// would marshal to "{}", so it is stored as its message instead.
func attrValue(v slog.Value) any {
	if v.Kind() == slog.KindAny {
		if err, ok := v.Any().(error); ok {
			return err.Error()
		}
	}
	return v.Any()
}

func (h *ringHandler) WithAttrs(as []slog.Attr) slog.Handler {
	nh := &ringHandler{ring: h.ring, group: h.group}
	nh.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return nh
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	return &ringHandler{ring: h.ring, attrs: h.attrs, group: name}
}

// teeHandler fans each record to the JSON handler (file/stderr, gated by the
// configured level) and the ring (always).
type teeHandler struct {
	json slog.Handler
	ring slog.Handler
}

func (t teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return t.json.Enabled(ctx, l) || t.ring.Enabled(ctx, l)
}

func (t teeHandler) Handle(ctx context.Context, r slog.Record) error {
	if t.json.Enabled(ctx, r.Level) {
		if err := t.json.Handle(ctx, r); err != nil {
			return err
		}
	}
	return t.ring.Handle(ctx, r)
}

func (t teeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return teeHandler{json: t.json.WithAttrs(as), ring: t.ring.WithAttrs(as)}
}

func (t teeHandler) WithGroup(name string) slog.Handler {
	return teeHandler{json: t.json.WithGroup(name), ring: t.ring.WithGroup(name)}
}

// Setup returns a JSON slog.Logger writing to stderr and, when path is
// non-empty, to a size-rotated file as well. Every line is also mirrored into
// an in-memory Ring (surfaced by the diagnostics UI). The returned LevelVar
// lets callers change the file/stderr threshold at runtime (the "debug mode"
// toggle); the ring always retains Debug regardless.
func Setup(path string, level slog.Level) (*slog.Logger, *slog.LevelVar, *Ring) {
	var w io.Writer = os.Stderr
	if path != "" {
		w = io.MultiWriter(os.Stderr, &lumberjack.Logger{
			Filename:   path,
			MaxSize:    10, // MiB
			MaxBackups: 5,
			MaxAge:     60, // days
			Compress:   true,
		})
	}
	lvl := new(slog.LevelVar)
	lvl.Set(level)
	ring := NewRing(1000)

	json := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})
	logger := slog.New(teeHandler{json: json, ring: &ringHandler{ring: ring}})
	slog.SetDefault(logger)
	return logger, lvl, ring
}
