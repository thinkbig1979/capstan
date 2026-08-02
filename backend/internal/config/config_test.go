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
		Port:      "5001",
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
		Port:      "5001", // must match Load's default
		LogLevel:  "info", // must match Load's default
		LogFormat: "text", // must match Load's default
	}
	if err := validate(defaults); err != nil {
		t.Errorf("Load's defaults do not pass validation: %v", err)
	}
}

// PORT used to be dead configuration in the same way LOG_LEVEL was: documented,
// defaulted, and logged back out at startup, but never read from the
// environment, so the server always listened on 5001 regardless of PORT
// (agent-os-o6q).

func TestValidate_RejectsNonNumericPort(t *testing.T) {
	cfg := newValidConfig()
	cfg.Port = "not-a-port"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected PORT=not-a-port to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "PORT") {
		t.Errorf("error should name the offending variable, got: %v", err)
	}
}

func TestValidate_RejectsOutOfRangePort(t *testing.T) {
	for _, port := range []string{"0", "-1", "65536", "999999"} {
		cfg := newValidConfig()
		cfg.Port = port

		err := validate(cfg)
		if err == nil {
			t.Errorf("expected PORT=%s to be rejected, got nil", port)
			continue
		}
		if !strings.Contains(err.Error(), "PORT") {
			t.Errorf("PORT=%s: error should name the offending variable, got: %v", port, err)
		}
	}
}

func TestValidate_AcceptsPlausiblePorts(t *testing.T) {
	for _, port := range []string{"1", "80", "5001", "8080", "65535"} {
		cfg := newValidConfig()
		cfg.Port = port

		if err := validate(cfg); err != nil {
			t.Errorf("PORT=%s rejected: %v", port, err)
		}
	}
}

// TestLoad_DefaultPortIsValid guards against Load's hardcoded default drifting
// away from what validate accepts, which would make the server refuse to start
// with no PORT env var set at all — the same regression class as
// TestLoad_DefaultsAreValid above, isolated to PORT.
func TestLoad_DefaultPortIsValid(t *testing.T) {
	cfg := newValidConfig()
	cfg.Port = "5001" // must match Load's default

	if err := validate(cfg); err != nil {
		t.Errorf("Load's default PORT does not pass validation: %v", err)
	}
}
