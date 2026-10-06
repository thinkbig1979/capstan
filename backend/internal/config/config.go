package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/logging"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
)

// DefaultAPIRateLimitPerMin is the API budget, in requests per rolling minute,
// applied to every deployment that does not set RATE_LIMIT_API_PER_MIN. It is
// the single source of truth for that number: middleware.InitRateLimiters takes
// the budget as an argument rather than holding its own copy, so there is no
// second 300 to drift out of step with this one.
//
// The constant lives here, not next to the auth limiter constants in
// internal/middleware, because middleware already depends on this package
// (transitively, via internal/services) and the reverse edge would be an import
// cycle. Verified 2026-09-01 with `go list -deps ./internal/middleware`.
const DefaultAPIRateLimitPerMin = 300

type StacksDirEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

type Config struct {
	StacksDir       string
	HostStacksDir   string
	DataDir         string
	Port            string
	JWTSecret       string
	StorageKey      string
	LogLevel        string
	LogFormat       string
	GitSSHKey       string
	GitHTTPSToken   string
	GitHTTPSUser    string
	AuthDisabled    bool
	CORSOrigins     string
	TrustedNetworks string
	// APIRateLimitPerMin is the per-caller budget for the authenticated API
	// surface, in requests per rolling minute. Raising it weakens a real
	// abuse control, so it exists for one purpose: an end-to-end test run
	// drives far more traffic through one bucket than any human does. See
	// RATE_LIMIT_API_PER_MIN in docs/reference/configuration.md.
	APIRateLimitPerMin int
	// HealthNetworks lists the CIDRs, beyond loopback, that may reach /health
	// and /health/ready. Deliberately separate from TrustedNetworks: that value
	// is Gin's trusted-proxy list, so reusing it would force an operator to
	// grant an uptime monitor X-Forwarded-For spoofing just to read a health
	// endpoint (agent-os-69a). Empty means loopback only, which is the
	// pre-split behaviour. (TrustedNetworks stopped also being the
	// AUTH_DISABLED bypass list in agent-os-0s4 — see
	// AuthDisabledAllowedNetworks below.)
	HealthNetworks string
	// AuthDisabledAllowedNetworks lists the CIDRs, beyond loopback, that may
	// use the AUTH_DISABLED admin bypass. Deliberately separate from
	// TrustedNetworks: that value is Gin's trusted-proxy list, needed for
	// correct client-IP attribution and rate limiting, and reusing it here
	// would mean trusting a reverse proxy for IP attribution silently widens
	// who can skip authentication entirely (agent-os-0s4). Empty means
	// loopback only, which is the narrowest and safest default.
	AuthDisabledAllowedNetworks string
	ExtraStacksDirs             []string

	// Backup / restic env-var fallbacks (DB settings take precedence at runtime).
	ResticRepository       string
	ResticPassword         string
	BackupKeepDaily        string
	BackupKeepWeekly       string
	BackupKeepMonthly      string
	BackupKeepYearly       string
	BackupAutoPrune        string
	BackupScheduleInterval string
	BackupScheduleMode     string
	BackupScheduleTime     string
	BackupScheduleDays     string
	BackupSyncAfter        string
	RcloneRemote           string
	RclonePath             string
	RcloneTransfers        string
	BackupHostname         string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:                        "5001",
		APIRateLimitPerMin:          DefaultAPIRateLimitPerMin,
		LogLevel:                    logging.DefaultLevel,
		LogFormat:                   logging.FormatText,
		GitSSHKey:                   filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa"),
		GitHTTPSUser:                "git",
		AuthDisabled:                os.Getenv("AUTH_DISABLED") == "true",
		TrustedNetworks:             os.Getenv("TRUSTED_NETWORKS"),
		HealthNetworks:              os.Getenv("HEALTH_ALLOWED_NETWORKS"),
		AuthDisabledAllowedNetworks: os.Getenv("AUTH_DISABLED_ALLOWED_NETWORKS"),
	}

	if stacksDir := os.Getenv("STACKS_DIR"); stacksDir != "" {
		cfg.StacksDir = stacksDir
	} else if dockgeStacksDir := os.Getenv("DOCKGE_STACKS_DIR"); dockgeStacksDir != "" {
		cfg.StacksDir = dockgeStacksDir
	} else {
		cfg.StacksDir = "/opt/stacks"
	}

	if hostStacksDir := os.Getenv("HOST_STACKS_DIR"); hostStacksDir != "" {
		cfg.HostStacksDir = hostStacksDir
	}

	if dataDir := os.Getenv("DATA_DIR"); dataDir != "" {
		cfg.DataDir = dataDir
	} else {
		cfg.DataDir = "/app/data"
	}

	cfg.JWTSecret = os.Getenv("JWT_SECRET")

	// STORAGE_KEY derives the at-rest encryption key independently of JWT_SECRET
	// (H2). Optional: when unset the encryptor falls back to JWT_SECRET so
	// existing deployments keep working.
	cfg.StorageKey = os.Getenv("STORAGE_KEY")

	if port := os.Getenv("PORT"); port != "" {
		cfg.Port = port
	}

	// Absent or empty leaves DefaultAPIRateLimitPerMin in place, so an existing
	// deployment that never sets this sees byte-identical limiter behaviour. A
	// malformed or non-positive value is rejected at startup rather than
	// falling back, matching PORT and LOG_LEVEL: silently ignoring a typo in a
	// security control is how an operator comes to believe a limit is in force
	// when it is not.
	if apiRateLimit := os.Getenv("RATE_LIMIT_API_PER_MIN"); apiRateLimit != "" {
		parsed, err := strconv.Atoi(apiRateLimit)
		if err != nil {
			return nil, &ConfigError{Field: "RATE_LIMIT_API_PER_MIN", Message: "must be a number, got " + strconv.Quote(apiRateLimit)}
		}
		if parsed < 1 {
			return nil, &ConfigError{Field: "RATE_LIMIT_API_PER_MIN", Message: "must be at least 1, got " + apiRateLimit}
		}
		cfg.APIRateLimitPerMin = parsed
	}

	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.LogLevel = logLevel
	}

	if logFormat := os.Getenv("LOG_FORMAT"); logFormat != "" {
		cfg.LogFormat = logFormat
	}

	if gitSSHKey := os.Getenv("GIT_SSH_KEY"); gitSSHKey != "" {
		cfg.GitSSHKey = gitSSHKey
	}

	cfg.GitHTTPSToken = os.Getenv("GIT_HTTPS_TOKEN")

	if gitHTTPSUser := os.Getenv("GIT_HTTPS_USER"); gitHTTPSUser != "" {
		cfg.GitHTTPSUser = gitHTTPSUser
	}

	cfg.CORSOrigins = os.Getenv("CORS_ORIGINS")

	if extraDirs := os.Getenv("EXTRA_STACKS_DIRS"); extraDirs != "" {
		for _, d := range strings.Split(extraDirs, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				cfg.ExtraStacksDirs = append(cfg.ExtraStacksDirs, d)
			}
		}
	}

	// Backup env-var fallbacks (DB settings override these at runtime via resolveBackupConfig).
	cfg.ResticRepository = os.Getenv("RESTIC_REPOSITORY")
	cfg.ResticPassword = os.Getenv("RESTIC_PASSWORD")
	cfg.BackupKeepDaily = os.Getenv("BACKUP_KEEP_DAILY")
	cfg.BackupKeepWeekly = os.Getenv("BACKUP_KEEP_WEEKLY")
	cfg.BackupKeepMonthly = os.Getenv("BACKUP_KEEP_MONTHLY")
	cfg.BackupKeepYearly = os.Getenv("BACKUP_KEEP_YEARLY")
	cfg.BackupAutoPrune = os.Getenv("BACKUP_AUTO_PRUNE")
	cfg.BackupScheduleInterval = os.Getenv("BACKUP_SCHEDULE_INTERVAL")
	cfg.BackupScheduleMode = os.Getenv("BACKUP_SCHEDULE_MODE")
	cfg.BackupScheduleTime = os.Getenv("BACKUP_SCHEDULE_TIME")
	cfg.BackupScheduleDays = os.Getenv("BACKUP_SCHEDULE_DAYS")
	cfg.BackupSyncAfter = os.Getenv("BACKUP_SYNC_AFTER")
	cfg.RcloneRemote = os.Getenv("RCLONE_REMOTE")
	cfg.RclonePath = os.Getenv("RCLONE_PATH")
	cfg.RcloneTransfers = os.Getenv("RCLONE_TRANSFERS")
	cfg.BackupHostname = os.Getenv("BACKUP_HOSTNAME")

	if err := validate(cfg); err != nil {
		return nil, err
	}

	warnWeakStorageKey(cfg)

	//nolint:gosec // cfg.StacksDir is read from the STACKS_DIR env var at process startup, set by whoever deploys the container — never request input
	if err := os.MkdirAll(cfg.StacksDir, 0755); err != nil {
		return nil, err
	}

	//nolint:gosec // cfg.DataDir is read from the DATA_DIR env var at process startup, set by whoever deploys the container — never request input
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, err
	}

	validateVolumePathIdentity(cfg)

	//nolint:gosec // slog's structured key-value logging stores each field separately rather than concatenating into the message text, so a value can't forge a new log line the way string-built log messages can
	slog.Info("Configuration loaded",
		"stacks_dir", cfg.StacksDir,
		"data_dir", cfg.DataDir,
		"port", cfg.Port,
		"jwt_secret", "[REDACTED]",
		"log_level", cfg.LogLevel,
		"log_format", cfg.LogFormat,
		"auth_disabled", cfg.AuthDisabled,
	)

	return cfg, nil
}

// minSecretLength is the length floor JWT_SECRET enforces as a hard startup
// failure. warnWeakStorageKey reuses it for STORAGE_KEY below rather than
// inventing a second number.
const minSecretLength = 32

func validate(cfg *Config) error {
	if !cfg.AuthDisabled {
		if cfg.JWTSecret == "" {
			return &ConfigError{Field: "JWT_SECRET", Message: "required when AUTH_DISABLED is not set"}
		}
		if len(cfg.JWTSecret) < minSecretLength {
			return &ConfigError{Field: "JWT_SECRET", Message: "must be at least 32 characters"}
		}
		if cfg.JWTSecret == "change-this-secret-in-production" {
			return &ConfigError{Field: "JWT_SECRET", Message: "must be changed from default value"}
		}
	}

	if cfg.StacksDir == "" {
		return &ConfigError{Field: "STACKS_DIR", Message: "required"}
	}

	if cfg.DataDir == "" {
		return &ConfigError{Field: "DATA_DIR", Message: "required"}
	}

	// An implausible PORT (non-numeric, 0, or outside the TCP port range) is
	// rejected here rather than reaching net/http.Server.ListenAndServe, which
	// would either fail with an opaque "listen tcp: address ..." error or,
	// worse, silently take the platform's ephemeral-port behaviour for an empty
	// port string. Caught at startup like every other malformed variable.
	if portNum, err := strconv.Atoi(cfg.Port); err != nil {
		return &ConfigError{Field: "PORT", Message: "must be a number, got " + strconv.Quote(cfg.Port)}
	} else if portNum < 1 || portNum > 65535 {
		return &ConfigError{Field: "PORT", Message: "must be between 1 and 65535, got " + cfg.Port}
	}

	// A typo here is caught at startup rather than silently defaulting to info.
	// Silently falling back is how an operator turns logging up during an
	// incident, sees no change, and concludes the problem is elsewhere.
	if _, err := logging.ParseLevel(cfg.LogLevel); err != nil {
		return &ConfigError{Field: "LOG_LEVEL", Message: err.Error()}
	}

	if _, err := logging.ParseFormat(cfg.LogFormat); err != nil {
		return &ConfigError{Field: "LOG_FORMAT", Message: err.Error()}
	}

	return nil
}

// warnWeakStorageKey warns, but does not block boot, when STORAGE_KEY is set
// but shorter than minSecretLength — the same floor JWT_SECRET enforces as a
// hard failure.
//
// STORAGE_KEY is deliberately NOT held to that same hard-failure floor.
// STORAGE_KEY already encrypts existing deployments' stored git tokens
// (crypto.go's HKDF expansion into an AES-256 key). A server that refuses to
// boot on a short key strands that data: the operator cannot simply lengthen
// the key, because changing STORAGE_KEY changes the HKDF-derived AES key and
// makes everything already encrypted unreadable. A hard failure would
// therefore turn a weak-key warning into data loss. JWT_SECRET does not have
// this problem — it only signs sessions, so rotating it just logs everyone
// out. So: a prominent startup warning, boot continues (agent-os-yqf).
//
// An unset STORAGE_KEY is not warned about here: NewTokenEncryptor
// (crypto.go) falls back to JWTSecret when StorageKey is empty, and
// JWTSecret is already held to minSecretLength as a hard failure above, so
// an unset STORAGE_KEY inherits a key of adequate strength rather than a
// weak one.
func warnWeakStorageKey(cfg *Config) {
	if cfg.StorageKey == "" || len(cfg.StorageKey) >= minSecretLength {
		return
	}

	slog.Warn("WARNING: STORAGE_KEY is short and provides little effective encryption strength for at-rest secrets (git tokens, restic passwords). Boot continues because rotating STORAGE_KEY makes previously encrypted data unreadable.",
		"storage_key_length", len(cfg.StorageKey),
		"minimum_recommended", minSecretLength,
		"hint", "Set STORAGE_KEY to a random string at least 32 characters long for new deployments; existing deployments should plan a coordinated rotation.")
}

// stacksMountVerdict is what /proc/self/mountinfo can say about STACKS_DIR.
type stacksMountVerdict int

const (
	// No usable mount entry: not in a container, or STACKS_DIR is not on a
	// bind mount. The caller falls back to comparing the two env strings.
	stacksMountNotInspected stacksMountVerdict = iota
	// The mount's source path is STACKS_DIR exactly.
	stacksMountVerified
	// The mount's source ends in STACKS_DIR's path but lives on a filesystem
	// mounted elsewhere on the host, so equality cannot be proven from here.
	stacksMountConsistent
	// The mount's source cannot be STACKS_DIR: Volume Path Identity is broken.
	stacksMountMismatch
)

// mountinfoPath is a var so tests can point it at a fixture.
var mountinfoPath = "/proc/self/mountinfo"

// inspectStacksMount finds the mount holding stacksDir and checks that its
// host source can be stacksDir itself. It also returns the source path it
// derived, for the log line.
//
// mountinfo's field 4 is the path relative to the SOURCE FILESYSTEM's root,
// not the host path: `-v /home/u/stacks:/opt/stacks` with /home on its own
// partition shows as "/u/stacks" (observed, see volume_identity_test.go's
// fixtures). So a match is only provable when the source is on the root
// filesystem; otherwise the best available check is that the source is a
// path-suffix of stacksDir, and anything that is not a suffix is wrong.
func inspectStacksMount(stacksDir, mountinfo string) (stacksMountVerdict, string) {
	stacksDir = filepath.Clean(stacksDir)
	var root, mountPoint string
	for _, line := range strings.Split(mountinfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mp := unescapeMountinfo(fields[4])
		// "/" is the container's own overlay root (or the host root when not
		// in a container): STACKS_DIR on it means no bind mount to inspect.
		if mp == "/" || (stacksDir != mp && !strings.HasPrefix(stacksDir, mp+"/")) {
			continue
		}
		// Later lines win on equal length: they stack on top of earlier ones.
		if len(mp) >= len(mountPoint) {
			root, mountPoint = unescapeMountinfo(fields[3]), mp
		}
	}
	if mountPoint == "" {
		return stacksMountNotInspected, ""
	}

	source := filepath.Join(root, strings.TrimPrefix(stacksDir, mountPoint))
	switch {
	case source == stacksDir:
		return stacksMountVerified, source
	// source always starts with "/", so HasSuffix only matches on a path
	// boundary: "/stacks" is not a suffix of "/opt/mystacks".
	case source == "/" || strings.HasSuffix(stacksDir, source):
		return stacksMountConsistent, source
	default:
		return stacksMountMismatch, source
	}
}

// unescapeMountinfo reverses the kernel's octal escaping of space, tab,
// newline and backslash in mountinfo paths.
func unescapeMountinfo(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

func validateVolumePathIdentity(cfg *Config) {
	// A read error (not Linux, no procfs) leaves mountinfo empty, which
	// inspectStacksMount reports as not inspected.
	mountinfo, err := os.ReadFile(mountinfoPath)
	if err != nil {
		slog.Debug("Volume path identity: cannot read mounts", "path", mountinfoPath, "error", err)
	}
	verdict, source := inspectStacksMount(cfg.StacksDir, string(mountinfo))

	switch verdict {
	case stacksMountMismatch:
		//nolint:gosec // slog's structured key-value logging stores each field separately rather than concatenating into the message text, so a value can't forge a new log line the way string-built log messages can
		slog.Error("Volume path identity broken: the host directory mounted at STACKS_DIR is not STACKS_DIR. Relative bind mounts in managed stacks will resolve to the wrong host path.",
			"stacks_dir", cfg.StacksDir,
			"host_stacks_dir", cfg.HostStacksDir,
			"mount_source", source,
			"mount_source_note", "path relative to the source filesystem's root, from "+mountinfoPath,
			"hint", "Mount the stacks directory at the same path on both sides, e.g. /opt/stacks:/opt/stacks, and set STACKS_DIR and HOST_STACKS_DIR to that path")
		return
	case stacksMountVerified:
		slog.Info("Volume path identity verified from the container's mounts",
			"stacks_dir", cfg.StacksDir,
			"mount_source", source)
	case stacksMountConsistent:
		slog.Info("Volume path identity consistent: the mount source matches STACKS_DIR as far as the container can see, but lives on a separate host filesystem so cannot be fully proven",
			"stacks_dir", cfg.StacksDir,
			"mount_source", source)
	}

	notInspected := ""
	if verdict == stacksMountNotInspected {
		notInspected = " (no bind mount found for STACKS_DIR in " + mountinfoPath + "; compared STACKS_DIR and HOST_STACKS_DIR as strings only)"
	}

	if cfg.HostStacksDir == "" {
		slog.Warn("Volume path identity: Set HOST_STACKS_DIR to verify path matching. STACKS_DIR must be the same path inside and outside the container for Docker Compose operations to work correctly."+notInspected,
			"stacks_dir", cfg.StacksDir,
			"host_stacks_dir", "not set",
			"hint", "Add HOST_STACKS_DIR environment variable matching your docker-compose.yaml volume path")
		return
	}

	if cfg.HostStacksDir != cfg.StacksDir {
		slog.Warn("Volume path identity mismatch: STACKS_DIR and HOST_STACKS_DIR do not match. Docker Compose operations may fail."+notInspected,
			"stacks_dir", cfg.StacksDir,
			"host_stacks_dir", cfg.HostStacksDir,
			"hint", "Ensure both variables use the same path (e.g., STACKS_DIR=/opt/stacks and HOST_STACKS_DIR=/opt/stacks)")
		return
	}

	if verdict == stacksMountNotInspected {
		slog.Info("Volume path identity verified"+notInspected,
			"stacks_dir", cfg.StacksDir,
			"host_stacks_dir", cfg.HostStacksDir)
	}
}

type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return e.Field + ": " + e.Message
}

func NormalizeOrigins(origins string) []string {
	if origins == "" {
		return nil
	}

	originList := strings.Split(origins, ",")
	result := make([]string, 0, len(originList))
	for _, origin := range originList {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			result = append(result, origin)
		}
	}
	return result
}

func (c *Config) GetAllStacksDirs() []string {
	dirs := []string{c.StacksDir}
	dirs = append(dirs, c.ExtraStacksDirs...)
	return dirs
}

// MatchStacksRoot returns the configured stacks root that resolves to the same
// real directory as path, following symlinks on both sides. Only a root
// matches: a subdirectory of one does not, because making it the default would
// nest one root inside another at the next boot (agent-os-a1ye.6).
func (c *Config) MatchStacksRoot(path string) (string, bool, error) {
	for _, root := range c.GetAllStacksDirs() {
		same, err := sameDir(root, path)
		if err != nil {
			return "", false, err
		}
		if same {
			return root, true, nil
		}
	}
	return "", false, nil
}

func sameDir(a, b string) (bool, error) {
	inside, err := pathutil.IsContained(a, b)
	if err != nil || !inside {
		return false, err
	}
	return pathutil.IsContained(b, a)
}

// ApplyPersistedDefaultStacksDir makes the persisted default_stacks_dir setting
// the primary root. It runs once at boot, before cfg is shared with any
// goroutine; the settings endpoint only persists the choice and never writes
// cfg, which is read without a lock (agent-os-a1ye.6).
//
// It REORDERS the roots rather than replacing StacksDir: the env STACKS_DIR
// root moves to the front of ExtraStacksDirs, so the set of scanned and allowed
// roots is unchanged and nothing on disk drops out of view. A persisted value
// that matches no configured root (the env changed since it was saved) is
// ignored with a warning and the env value stays.
func ApplyPersistedDefaultStacksDir(cfg *Config, persisted string) {
	if persisted == "" {
		return
	}
	root, ok, err := cfg.MatchStacksRoot(persisted)
	if err != nil || !ok {
		slog.Warn("Ignoring persisted default stacks directory: it is not a configured stacks root",
			"default_stacks_dir", persisted,
			"stacks_dir", cfg.StacksDir,
			"error", err,
		)
		return
	}
	if root == cfg.StacksDir {
		return
	}

	extras := []string{cfg.StacksDir}
	for _, extra := range cfg.ExtraStacksDirs {
		if extra != root {
			extras = append(extras, extra)
		}
	}
	slog.Info("Using persisted default stacks directory over STACKS_DIR",
		"default_stacks_dir", root,
		"env_stacks_dir", cfg.StacksDir,
	)
	cfg.StacksDir = root
	cfg.ExtraStacksDirs = extras
}
