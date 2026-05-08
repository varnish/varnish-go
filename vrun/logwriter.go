package vrun

import (
	"bufio"
	"context"
	"log/slog"
	"strings"
)

// logWriter is an io.Writer adapter that routes varnishd output through structured logging
type logWriter struct {
	logger *slog.Logger
	source string
}

// newLogWriter creates a new log writer for varnishd output
func newLogWriter(logger *slog.Logger, source string) *logWriter {
	return &logWriter{
		logger: logger,
		source: source,
	}
}

// Write implements io.Writer interface and logs each line through slog
func (lw *logWriter) Write(p []byte) (n int, err error) {
	// Split the input by newlines and log each line
	scanner := bufio.NewScanner(strings.NewReader(string(p)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Determine log level based on line prefix
		var level slog.Level
		switch {
		case line == "Child launched OK":
			// Info from manager process, treat as debug
			level = slog.LevelDebug
		case strings.HasPrefix(line, "Info: Child") && strings.Contains(line, "said Child starts"):
			// Info from child process about starting, treat as debug
			level = slog.LevelDebug

			// MILESTONE
			lw.logger.Log(context.Background(), slog.LevelInfo, "Varnish is ready to receive traffic")
		case strings.HasPrefix(line, "Debug:"):
			level = slog.LevelDebug
			line = strings.TrimSpace(strings.TrimPrefix(line, "Debug:"))
		case strings.HasPrefix(line, "Info:"):
			level = slog.LevelInfo
			line = strings.TrimSpace(strings.TrimPrefix(line, "Info:"))
		case strings.HasPrefix(line, "Warning:") || strings.HasPrefix(line, "Warn:"):
			level = slog.LevelWarn
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "Warning:"), "Warn:"))
		case strings.HasPrefix(line, "Error:"):
			level = slog.LevelError
			line = strings.TrimSpace(strings.TrimPrefix(line, "Error:"))
		default:
			// Default to info level for other varnishd output
			level = slog.LevelInfo
		}
		// Log with source attribution
		lw.logger.Log(context.Background(), level, line, "source", lw.source)
	}
	// Always return the full length written to satisfy io.Writer interface
	return len(p), nil
}
