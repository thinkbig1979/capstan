package config

import (
	"bytes"
	"log"
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

// TestLoad_AuthDisabledAllowedNetworksIsIndependentOfTrustedNetworks guards
// agent-os-0s4 vector 1: AuthDisabledAllowedNetworks and TrustedNetworks used
// to be the same field (Load only ever set TrustedNetworks, and main.go fed
// that single value to both Gin's SetTrustedProxies and the AUTH_DISABLED
// bypass allowlist). That meant every host an operator added to
// TRUSTED_NETWORKS for correct client-IP attribution was automatically
// allow-listed for the AUTH_DISABLED bypass too, no header spoofing required.
//
// AuthDisabledAllowedNetworks did not exist on Config before this fix, so
// this test fails to even compile against the pre-fix source — the closest
// "seen failing" state available for a defect whose root cause is "one config
// value answers two different trust questions" rather than a bad conditional.
// Once the field exists, it must default to loopback-only (empty) regardless
// of TrustedNetworks, and must not silently mirror it.
func TestLoad_AuthDisabledAllowedNetworksIsIndependentOfTrustedNetworks(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("TRUSTED_NETWORKS", "10.0.0.0/24")
	t.Setenv("AUTH_DISABLED_ALLOWED_NETWORKS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.TrustedNetworks != "10.0.0.0/24" {
		t.Fatalf("expected TrustedNetworks to be loaded from TRUSTED_NETWORKS, got %q", cfg.TrustedNetworks)
	}
	if cfg.AuthDisabledAllowedNetworks != "" {
		t.Errorf("expected an unset AUTH_DISABLED_ALLOWED_NETWORKS to default to loopback-only (empty) even though TRUSTED_NETWORKS is set, got %q", cfg.AuthDisabledAllowedNetworks)
	}
	if cfg.AuthDisabledAllowedNetworks == cfg.TrustedNetworks {
		t.Errorf("AuthDisabledAllowedNetworks must never silently mirror TrustedNetworks — that is exactly the defect agent-os-0s4 closes")
	}
}

// TestLoad_HonoursAuthDisabledAllowedNetworksFromEnvironment is the positive
// half: an operator who deliberately widens the AUTH_DISABLED bypass beyond
// loopback must be able to, via a variable that is not TRUSTED_NETWORKS.
func TestLoad_HonoursAuthDisabledAllowedNetworksFromEnvironment(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("TRUSTED_NETWORKS", "")
	t.Setenv("AUTH_DISABLED_ALLOWED_NETWORKS", "10.1.0.0/16")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.AuthDisabledAllowedNetworks != "10.1.0.0/16" {
		t.Errorf("expected Load() to honour AUTH_DISABLED_ALLOWED_NETWORKS=10.1.0.0/16, got %q", cfg.AuthDisabledAllowedNetworks)
	}
}

// captureSlog redirects the process-wide slog default to a buffer for the
// duration of the test and restores the previous default on cleanup. Mirrors
// internal/services/git_credentials_test.go's helper of the same name.
//
// The stdlib log package must be restored explicitly, and its writer and flags
// read BEFORE the swap: slog.SetDefault also does log.SetOutput(handlerWriter{})
// and log.SetFlags(0), and slog.SetDefault(prev) undoes neither, so restoring
// slog alone leaks the redirect and every later stdlib-log write in this test
// binary lands in a dead buffer (agent-os-ac0o). TestCaptureSlog_RestoresStdlibLog
// below is the ratchet that keeps this from being simplified back to one line.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevSlog := slog.Default()
	prevWriter, prevFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
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

// An unset RATE_LIMIT_API_PER_MIN must leave the API budget at exactly the
// pre-existing 300/min. This is the constraint that matters: the variable was
// added for CI, and every deployment that ignores it must be unaffected.
func TestLoad_DefaultsAPIRateLimitWhenUnset(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("RATE_LIMIT_API_PER_MIN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.APIRateLimitPerMin != 300 {
		t.Errorf("expected unset RATE_LIMIT_API_PER_MIN to default to 300, got %d", cfg.APIRateLimitPerMin)
	}
}

// The other side: the variable is actually read. Without this, the assignment
// in Load() could be deleted and the default test above would stay green.
func TestLoad_HonoursAPIRateLimitFromEnvironment(t *testing.T) {
	setBaseLoadEnv(t)
	t.Setenv("RATE_LIMIT_API_PER_MIN", "2000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.APIRateLimitPerMin != 2000 {
		t.Errorf("expected Load() to honour RATE_LIMIT_API_PER_MIN=2000, got %d", cfg.APIRateLimitPerMin)
	}
}

// A typo in a security control fails startup rather than silently reverting to
// the default, matching PORT and LOG_LEVEL. Zero and negatives are rejected for
// the same reason from the other direction: a 0 budget fails CLOSED and refuses
// every request, which reads as an outage with no obvious cause. That is
// measured, not assumed — see
// middleware.TestInitRateLimiters_RejectsNonPositiveBudget, which drives the
// limiter at 0 and pins the resulting panic. This check is the outer of two
// guards; middleware.InitRateLimiters panics if a non-positive budget reaches
// it by any other route.
func TestLoad_RejectsImplausibleAPIRateLimit(t *testing.T) {
	for _, value := range []string{"not-a-number", "0", "-1", "300.5"} {
		t.Run(value, func(t *testing.T) {
			setBaseLoadEnv(t)
			t.Setenv("RATE_LIMIT_API_PER_MIN", value)

			_, err := Load()
			if err == nil {
				t.Fatalf("expected Load() to reject RATE_LIMIT_API_PER_MIN=%s, got nil error", value)
			}
			if !strings.Contains(err.Error(), "RATE_LIMIT_API_PER_MIN") {
				t.Errorf("error should name the offending variable, got: %v", err)
			}
		})
	}
}

// TestCaptureSlog_RestoresStdlibLog guards the helper above, not the
// production code. slog.SetDefault silently re-points the stdlib log package
// (log.SetOutput + log.SetFlags(0)), and slog.SetDefault(prev) does not undo
// either, because prev's handler is slog's internal defaultHandler and the
// restore path skips the re-pointing branch. So a cleanup that restores only
// slog leaks the redirect for the rest of this test binary: every later
// stdlib-log write lands in a dead buffer instead of stderr.
//
// Blast radius in THIS package is small and the comment should not pretend
// otherwise: internal/config stands up no httptest server (OBSERVED: 0 hits
// for httptest.NewServer under internal/config), so nothing here is currently
// writing to the stdlib logger after a capture. The ratchet exists because the
// helper is declared a mirror of internal/services' one, whose blast radius is
// real, and a mirror that silently stops matching is how the fixed form gets
// copied back out of existence.
func TestCaptureSlog_RestoresStdlibLog(t *testing.T) {
	writerBefore, flagsBefore := log.Writer(), log.Flags()

	t.Run("capture", func(t *testing.T) { _ = captureSlog(t) })

	if log.Writer() != writerBefore {
		t.Errorf("stdlib log writer not restored: the capture leaked its redirect, so later log output in this binary is swallowed")
	}
	if log.Flags() != flagsBefore {
		t.Errorf("stdlib log flags not restored: got %d, want %d", log.Flags(), flagsBefore)
	}
}
