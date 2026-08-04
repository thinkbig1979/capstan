package config

import (
	"bytes"
	"log/slog"
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

// setBaseLoadEnv pins every env var validate() cares about to a known-good
// value, via t.Setenv (auto-restored, and safe here since these tests aren't
// parallel). Without this, Load() reading the ambient process environment
// would make these tests depend on whatever happens to be set on the machine
// running them. AUTH_DISABLED=true sidesteps the JWT_SECRET requirement,
// which is irrelevant to what these tests are pinning.
func setBaseLoadEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_DISABLED", "true")
	t.Setenv("STACKS_DIR", t.TempDir())
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
}

// TestLoad_HonoursPortFromEnvironment is the test that actually pins the bug
// this ticket fixed: PORT was read into a struct field with a validation rule
// that nothing ever exercised, because Load() never assigned the environment
// value to cfg.Port in the first place. Every other test above calls
// validate() directly and would stay green even if the os.Getenv("PORT") read
// in Load() were deleted.
func TestLoad_HonoursPortFromEnvironment(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("PORT", "9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("expected Load() to honour PORT=9090, got Port=%q", cfg.Port)
	}
}

// TestLoad_DefaultsPortWhenUnset guards the other half of the same fix: an
// unset PORT must keep the 5001 default rather than becoming a required
// variable or an empty string reaching the listen address.
func TestLoad_DefaultsPortWhenUnset(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("PORT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.Port != "5001" {
		t.Errorf("expected unset PORT to default to 5001, got Port=%q", cfg.Port)
	}
}

// TestLoad_RejectsImplausiblePort proves the rejection happens at startup
// (Load returning an error), not merely inside validate() called in
// isolation by the tests above.
func TestLoad_RejectsImplausiblePort(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("PORT", "not-a-port")

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load() to reject PORT=not-a-port, got nil error")
	}
	if !strings.Contains(err.Error(), "PORT") {
		t.Errorf("error should name the offending variable, got: %v", err)
	}
}

// captureSlog redirects the process-wide slog default to a buffer for the
// duration of the test and restores the previous default on cleanup. Mirrors
// internal/services/git_credentials_test.go's helper of the same name.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// STORAGE_KEY derives the at-rest encryption key (crypto.go's HKDF expansion)
// but, unlike JWT_SECRET, was never held to any length floor: a 3-character
// STORAGE_KEY silently produced a structurally valid but near-zero-entropy
// AES-256 key with no operator warning (agent-os-yqf). The fix is a loud
// startup warning, not a hard failure — see warnWeakStorageKey's comment for
// why a hard failure would strand already-encrypted deployments.

func TestLoad_ShortStorageKeyWarnsAtStartup(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("STORAGE_KEY", "short")

	buf := captureSlog(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error for a short STORAGE_KEY: %v — a short key must warn, not block boot", err)
	}
	if cfg.StorageKey != "short" {
		t.Fatalf("expected cfg.StorageKey to be %q, got %q", "short", cfg.StorageKey)
	}

	if !strings.Contains(buf.String(), "STORAGE_KEY") {
		t.Errorf("expected a startup warning naming STORAGE_KEY for a short key, got log output: %s", buf.String())
	}
}

// TestLoad_ShortStorageKeyDoesNotBlockBoot pins the "warn, don't block boot"
// decision structurally: a future change to hard-fail on a short STORAGE_KEY
// breaks this test rather than silently stranding deployments whose already-
// encrypted data would become unreadable if STORAGE_KEY had to change.
func TestLoad_ShortStorageKeyDoesNotBlockBoot(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("STORAGE_KEY", "x")

	if _, err := Load(); err != nil {
		t.Fatalf("a short STORAGE_KEY must not block boot, got error: %v", err)
	}
}

func TestLoad_AdequateStorageKeyDoesNotWarn(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("STORAGE_KEY", strings.Repeat("k", 32))

	buf := captureSlog(t)

	if _, err := Load(); err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if strings.Contains(buf.String(), "STORAGE_KEY") {
		t.Errorf("did not expect a STORAGE_KEY warning for a 32-character key, got: %s", buf.String())
	}
}

// An unset STORAGE_KEY is not warned about: NewTokenEncryptor (crypto.go)
// falls back to JWT_SECRET, which validate() already holds to the same
// minSecretLength floor as a hard startup failure, so the inherited key is
// never weak by omission.
func TestLoad_UnsetStorageKeyDoesNotWarn(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("STORAGE_KEY", "")

	buf := captureSlog(t)

	if _, err := Load(); err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if strings.Contains(buf.String(), "STORAGE_KEY") {
		t.Errorf("did not expect a STORAGE_KEY warning when unset, got: %s", buf.String())
	}
}
