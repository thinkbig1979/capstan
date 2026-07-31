// Package logging installs the process-wide slog handler.
//
// Before this existed, LOG_LEVEL was dead configuration: config.Load read it,
// logged it back out at startup and nothing consumed it. The process ran on
// slog's default handler for its entire life, so every slog.Debug call in the
// WebSocket, terminal and git paths was permanently invisible — including the
// ones that are the whole evidence base when a user reports a dropped terminal
// (agent-os-7li).
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
)

// Format names the encoder used for log output.
const (
	FormatText = "text"
	FormatJSON = "json"
)

var levels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// ParseLevel maps a LOG_LEVEL value to a slog.Level.
//
// An unrecognised value is an error rather than a silent fallback to info: a
// typo in LOG_LEVEL should be visible at startup, not discovered at 3am when
// the debug lines someone is waiting for never arrive.
func ParseLevel(name string) (slog.Level, error) {
	level, ok := levels[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return 0, fmt.Errorf("unrecognised log level %q (want one of %s)", name, LevelNames())
	}
	return level, nil
}

// ParseFormat validates a LOG_FORMAT value.
func ParseFormat(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case FormatText:
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unrecognised log format %q (want %s or %s)", name, FormatText, FormatJSON)
	}
}

// LevelNames returns the accepted LOG_LEVEL values, sorted, for error messages.
func LevelNames() string {
	names := make([]string, 0, len(levels))
	for name := range levels {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// NewHandler builds a slog handler for the given level and format.
func NewHandler(w io.Writer, level slog.Level, format string) (slog.Handler, error) {
	opts := &slog.HandlerOptions{Level: level}
	switch format {
	case FormatJSON:
		return slog.NewJSONHandler(w, opts), nil
	case FormatText:
		return slog.NewTextHandler(w, opts), nil
	default:
		return nil, fmt.Errorf("unrecognised log format %q (want %s or %s)", format, FormatText, FormatJSON)
	}
}

// DefaultLevel is the LOG_LEVEL applied when the variable is unset.
const DefaultLevel = "info"

// ConfigureFromEnv installs the logger straight from LOG_LEVEL / LOG_FORMAT.
//
// main() calls this before config.Load so that config's own startup lines —
// including the volume-path-identity warning — are emitted through the
// configured handler rather than slog's default. Load then re-installs the
// logger from the validated config, which stays the authoritative source.
func ConfigureFromEnv(w io.Writer) error {
	level := os.Getenv("LOG_LEVEL")
	if level == "" {
		level = DefaultLevel
	}
	format := os.Getenv("LOG_FORMAT")
	if format == "" {
		format = FormatText
	}
	return Configure(w, level, format)
}

// Configure installs the process-wide default logger.
func Configure(w io.Writer, levelName, formatName string) error {
	level, err := ParseLevel(levelName)
	if err != nil {
		return err
	}
	format, err := ParseFormat(formatName)
	if err != nil {
		return err
	}
	handler, err := NewHandler(w, level, format)
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(handler))
	return nil
}
