package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/logging"
)

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
	// HealthNetworks lists the CIDRs, beyond loopback, that may reach /health
	// and /health/ready. Deliberately separate from TrustedNetworks: that value
	// is Gin's trusted-proxy list *and* the AUTH_DISABLED bypass list, so
	// reusing it would force an operator to grant an uptime monitor
	// X-Forwarded-For spoofing and auth bypass just to read a health endpoint
	// (agent-os-69a). Empty means loopback only, which is the pre-split
	// behaviour.
	HealthNetworks  string
	ExtraStacksDirs []string

	// Backup / restic env-var fallbacks (DB settings take precedence at runtime).
	ResticRepository       string
	ResticPassword         string
	BackupKeepDaily        string
	BackupKeepWeekly       string
	BackupKeepMonthly      string
	BackupKeepYearly       string
	BackupAutoPrune        string
	BackupScheduleInterval string
	BackupSyncAfter        string
	RcloneRemote           string
	RclonePath             string
	RcloneTransfers        string
	BackupHostname         string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:            "5001",
		LogLevel:        logging.DefaultLevel,
		LogFormat:       logging.FormatText,
		GitSSHKey:       filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa"),
		GitHTTPSUser:    "git",
		AuthDisabled:    os.Getenv("AUTH_DISABLED") == "true",
		TrustedNetworks: os.Getenv("TRUSTED_NETWORKS"),
		HealthNetworks:  os.Getenv("HEALTH_ALLOWED_NETWORKS"),
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
	cfg.BackupSyncAfter = os.Getenv("BACKUP_SYNC_AFTER")
	cfg.RcloneRemote = os.Getenv("RCLONE_REMOTE")
	cfg.RclonePath = os.Getenv("RCLONE_PATH")
	cfg.RcloneTransfers = os.Getenv("RCLONE_TRANSFERS")
	cfg.BackupHostname = os.Getenv("BACKUP_HOSTNAME")

	if err := validate(cfg); err != nil {
		return nil, err
	}

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

func validate(cfg *Config) error {
	if !cfg.AuthDisabled {
		if cfg.JWTSecret == "" {
			return &ConfigError{Field: "JWT_SECRET", Message: "required when AUTH_DISABLED is not set"}
		}
		if len(cfg.JWTSecret) < 32 {
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

func validateVolumePathIdentity(cfg *Config) {
	if cfg.HostStacksDir == "" {
		slog.Warn("Volume path identity: Set HOST_STACKS_DIR to verify path matching. STACKS_DIR must be the same path inside and outside the container for Docker Compose operations to work correctly.",
			"stacks_dir", cfg.StacksDir,
			"host_stacks_dir", "not set",
			"hint", "Add HOST_STACKS_DIR environment variable matching your docker-compose.yaml volume path")
		return
	}

	if cfg.HostStacksDir != cfg.StacksDir {
		slog.Warn("Volume path identity mismatch: STACKS_DIR and HOST_STACKS_DIR do not match. Docker Compose operations may fail.",
			"stacks_dir", cfg.StacksDir,
			"host_stacks_dir", cfg.HostStacksDir,
			"hint", "Ensure both variables use the same path (e.g., STACKS_DIR=/opt/stacks and HOST_STACKS_DIR=/opt/stacks)")
		return
	}

	slog.Info("Volume path identity verified",
		"stacks_dir", cfg.StacksDir,
		"host_stacks_dir", cfg.HostStacksDir)
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
