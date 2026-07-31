package config

import (
	"strings"
	"testing"
)

// LOG_LEVEL used to be dead configuration — read, logged back out, never
// consumed and never validated. A typo silently produced info-level logging,
// which is how an operator turns logging up during an incident, sees no change,
// and concludes the problem is elsewhere (agent-os-7li).

func newValidConfig() *Config {
	return &Config{
		JWTSecret: strings.Repeat("k", 32),
		StacksDir: "/opt/stacks",
		DataDir:   "/app/data",
		LogLevel:  "info",
		LogFormat: "text",
	}
}

func TestValidate_RejectsUnknownLogLevel(t *testing.T) {
	cfg := newValidConfig()
	cfg.LogLevel = "verbose"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected LOG_LEVEL=verbose to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Errorf("error should name the offending variable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "verbose") {
		t.Errorf("error should quote the offending value, got: %v", err)
	}
}

func TestValidate_RejectsUnknownLogFormat(t *testing.T) {
	cfg := newValidConfig()
	cfg.LogFormat = "logfmt"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected LOG_FORMAT=logfmt to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "LOG_FORMAT") {
		t.Errorf("error should name the offending variable, got: %v", err)
	}
}

func TestValidate_AcceptsEveryDocumentedCombination(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		for _, format := range []string{"text", "json"} {
			cfg := newValidConfig()
			cfg.LogLevel = level
			cfg.LogFormat = format
			if err := validate(cfg); err != nil {
				t.Errorf("LOG_LEVEL=%s LOG_FORMAT=%s rejected: %v", level, format, err)
			}
		}
	}
}

// TestLoad_DefaultsAreValid guards against the defaults drifting away from the
// values validate accepts, which would make the server refuse to start with no
// logging env vars set at all.
func TestLoad_DefaultsAreValid(t *testing.T) {
	cfg := newValidConfig()
	cfg.LogLevel = ""
	cfg.LogFormat = ""

	if err := validate(cfg); err == nil {
		t.Fatal("empty values should not validate; Load fills them with defaults")
	}

	defaults := &Config{
		JWTSecret: strings.Repeat("k", 32),
		StacksDir: "/opt/stacks",
		DataDir:   "/app/data",
		LogLevel:  "info", // must match Load's default
		LogFormat: "text", // must match Load's default
	}
	if err := validate(defaults); err != nil {
		t.Errorf("Load's defaults do not pass validation: %v", err)
	}
}
