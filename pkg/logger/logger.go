package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// New creates a JSON-format structured logger that writes to stdout and
// <configDir>/logs/<YYYY-MM-DD>.json. Pass an empty configDir to skip file logging.
func New(level, configDir string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: l}
	writers := []io.Writer{os.Stdout}

	if configDir != "" {
		if f, err := openLogFile(configDir); err == nil {
			writers = append(writers, f)
		}
	}

	handler := slog.NewJSONHandler(io.MultiWriter(writers...), opts)
	return slog.New(handler)
}

// openLogFile opens (or creates) <configDir>/logs/<YYYY-MM-DD>.json.
func openLogFile(configDir string) (*os.File, error) {
	dir := filepath.Join(configDir, "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	name := time.Now().Format("2006-01-02") + ".json"
	return os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
}
