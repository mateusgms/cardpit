// Package logging configures the process-wide structured JSON logger.
package logging

import (
	"io"
	"log/slog"
	"os"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Setup returns a JSON slog.Logger writing to stderr and, when path is
// non-empty, to a size-rotated file as well.
func Setup(path string, level slog.Level) *slog.Logger {
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
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger
}
