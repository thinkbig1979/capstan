package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/handlers"
	"github.com/thinkbig1979/capstan/backend/internal/logging"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/version"
)

// backupDrainTimeout bounds how long graceful shutdown waits for in-flight
// durable backup/restore/sync/dr-restore/prune runs to finish after
// srv.Shutdown returns. 15s (srv.Shutdown's own bound, below) + this stays
// well under systemd's default TimeoutStopSec of 90s. See agent-os-7a5.
const backupDrainTimeout = 30 * time.Second

// timeoutMiddleware bounds the request context to timeout, guarding a slow
// dependency (a hung Docker daemon, for instance) from letting a request run
// forever. Callers MUST attach it via group.Use(timeoutMiddleware(...)) BEFORE
// registering routes on that group: gin@v1.12.0's RouterGroup.combineHandlers
// (routergroup.go:88) snapshots the middleware chain at ROUTE REGISTRATION
// time, so a Use() added after a group's routes are registered never applies
// to them. Hoisted to package scope (was a local closure in main()) so
// wireStacksGroup below can share it without capturing main()'s locals
// (agent-os-qru.1).
func timeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// wireStacksGroup registers the /stacks route group's own routes (via
// stacksHandler), its env and compose sub-routes, and its request timeout, all
// through one call — so a future change can no longer separate the Use() from
// the RegisterRoutes() calls it must precede the way agent-os-qru.1 found
// them: the timeout was being registered AFTER the routes it was meant to
// bound, so it silently never applied to them. Extracted out of main() (rather
// than left inline) so a test can invoke the real wiring instead of a
// hand-rolled replica of it.
func wireStacksGroup(
	protected *gin.RouterGroup,
	stacksHandler *handlers.StacksHandler,
	envHandler *handlers.EnvHandler,
	composeHandler *handlers.ComposeHandler,
	timeout time.Duration,
) *gin.RouterGroup {
	stacksGroup := protected.Group("/stacks")
	stacksGroup.Use(timeoutMiddleware(timeout))
	stacksHandler.RegisterRoutes(stacksGroup)
	envHandler.RegisterRoutes(stacksGroup)
	composeHandler.RegisterRoutes(stacksGroup)
	return stacksGroup
}

// buildConnectSrc constructs the CSP connect-src directive from the configured
// CORS origins so a cross-origin (reverse-proxy) deployment is not silently
// blocked by a localhost-only policy (M6). localhost ws/wss variants are only
// added in dev (AUTH_DISABLED) rather than baked into production.
func buildConnectSrc(cfg *config.Config) string {
	parts := []string{"'self'"}
	for _, o := range config.NormalizeOrigins(cfg.CORSOrigins) {
		ws := strings.Replace(o, "https://", "wss://", 1)
		ws = strings.Replace(ws, "http://", "ws://", 1)
		parts = append(parts, o, ws)
	}
	if cfg.AuthDisabled {
		parts = append(parts, "ws://localhost:*", "wss://localhost:*")
	}
	return strings.Join(parts, " ")
}

func SecurityHeaders(cfg *config.Config) gin.HandlerFunc {
	connectSrc := buildConnectSrc(cfg)
	return func(c *gin.Context) {
		nonceBytes := make([]byte, 16)
		if _, err := rand.Read(nonceBytes); err != nil {
			slog.Error("Failed to generate CSP nonce", "error", err)
			nonceBytes = []byte("fallback-nonce")
		}
		nonce := hex.EncodeToString(nonceBytes)
		c.Set("csp_nonce", nonce)

		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		// X-XSS-Protection is deprecated and "1; mode=block" can introduce
		// cross-site leaks; with a strong CSP present, 0 is the recommended value (L5).
		c.Header("X-XSS-Protection", "0")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// Only assert HSTS over HTTPS — sending it (esp. with includeSubDomains)
		// on plaintext/dev access can pin sibling services to HTTPS (L4).
		if middleware.IsSecureRequest(c) {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Header("Content-Security-Policy", fmt.Sprintf(
			// 'unsafe-eval' is required by the charting bundle (recharts) on the
			// dashboard. With 'strict-dynamic' + a per-request nonce, only
			// nonce-authorized scripts run, so this only permits eval *within*
			// already-trusted scripts — an acceptable trade-off for a first-party,
			// single-origin app. Tighten by replacing the eval-using lib if needed.
			"default-src 'self'; script-src 'self' 'nonce-%s' 'strict-dynamic' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src %s; frame-ancestors 'none';",
			nonce, connectSrc,
		))
		c.Next()
	}
}

// registerStaticAssets wires the Vite-built, content-hashed asset routes
// with long-lived immutable caching: a new build ships under a new
// filename, so a cached copy of an old filename is simply never requested
// again (agent-os-k6k). /vite.svg is NOT content-hashed — it keeps whatever
// default caching http.FileServer applies rather than being marked
// immutable, since an unhashed filename can legitimately change content.
func registerStaticAssets(r *gin.Engine) {
	immutable := func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	assets := r.Group("/assets")
	assets.Use(immutable)
	assets.Static("", "./frontend/assets")

	fonts := r.Group("/fonts")
	fonts.Use(immutable)
	fonts.Static("", "./frontend/fonts")

	r.StaticFile("/vite.svg", "./frontend/vite.svg")
}

// registerIndexRoute wires the SPA fallback that serves index.html with a
// per-request CSP nonce spliced into its <script>/<link> tags (see
// SecurityHeaders above). That splice makes the response body unique on
// every request, so it is served with Cache-Control: no-store and
// deliberately NO ETag or Last-Modified validator (agent-os-k6k):
//   - An ETag over the response body would change every request, so it
//     could never produce a 304 — pure overhead.
//   - An ETag over the source file WOULD be stable and WOULD 304. That is
//     the dangerous case: a 304 tells the browser to reuse its cached body
//     (old nonce) while the 304's own headers carry SecurityHeaders'
//     freshly-minted nonce. The nonces mismatch, 'strict-dynamic' trusts
//     nothing, and every script on the page is blocked — a blank app, not
//     a stale one. no-store is the honest policy for a body that cannot be
//     cached by construction, and it is what keeps a stale bundle from
//     outliving a rollback.
//
// Do not add an ETag/Last-Modified validator back to this route.
func registerIndexRoute(r *gin.Engine, indexHTML string) {
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			return
		}
		c.Header("Cache-Control", "no-store")
		if indexHTML == "" {
			c.File("./frontend/index.html")
			return
		}
		nonce, exists := c.Get("csp_nonce")
		if !exists {
			c.File("./frontend/index.html")
			return
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		html := strings.ReplaceAll(indexHTML, "<script", fmt.Sprintf("<script nonce=\"%s\"", nonce))
		html = strings.ReplaceAll(html, "<link", fmt.Sprintf("<link nonce=\"%s\"", nonce))
		c.String(http.StatusOK, html)
	})
}

func main() {
	// Offline admin commands are dispatched before anything else, deliberately
	// ahead of config.Load: config treats JWT_SECRET as a hard startup
	// requirement, and resetting a password neither mints nor verifies tokens.
	// Requiring a secret in order to recover an account would be a needless way
	// for the recovery path to fail (agent-os-8pa). See admin.go.
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		os.Exit(runAdminCommand(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}

	// Bootstrap the logger from the environment before config.Load, so that
	// config's own startup lines — including the volume-path-identity warning —
	// go through the configured handler instead of slog's default. An
	// unrecognised value stops the process here rather than silently becoming
	// info (agent-os-7li).
	if err := logging.ConfigureFromEnv(os.Stderr); err != nil {
		log.Fatal("Failed to configure logging:", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	// Re-install from the validated config, which is the authoritative source.
	if err := logging.Configure(os.Stderr, cfg.LogLevel, cfg.LogFormat); err != nil {
		log.Fatal("Failed to configure logging:", err)
	}

	// Build identity goes on the very first line, so it is present even if the
	// process dies later in startup (agent-os-r7e).
	build := version.Get()
	slog.Info("Starting Capstan backend",
		"version", build.Version,
		"commit", build.Commit,
		"built", build.BuildDate,
		"log_level", cfg.LogLevel,
		"log_format", cfg.LogFormat,
	)

	db, err := database.NewWithMigrationsAndEncryptor(cfg.DataDir, services.NewTokenEncryptorOrDefault(cfg.StorageKey, cfg.JWTSecret))
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer db.Close()

	if err := db.MigrateStackIDsToRootPrefixed(cfg.StacksDir); err != nil {
		slog.Warn("Failed to migrate stack IDs", "error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// A single failed sweep just means expired sessions linger
				// until the next tick, but a sweep that keeps failing every
				// hour, forever, would otherwise never surface anywhere.
				if err := db.DeleteExpiredSessions(); err != nil {
					slog.Warn("Failed to delete expired sessions", "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		// Prune shortly after startup as well as on the tick. The ticker's first
		// fire is 24h away, so an instance that restarts daily — a container on a
		// nightly compose pull, say — never reached it and the cleanup never ran
		// at all. The short delay keeps the prune off the startup critical path.
		startup := time.NewTimer(2 * time.Minute)
		defer startup.Stop()

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-startup.C:
				db.PruneHistory()
			case <-ticker.C:
				db.PruneHistory()
			case <-ctx.Done():
				return
			}
		}
	}()

	// dockerService is nil when the daemon was unreachable at startup — the
	// server still boots so the UI can report the outage. Two rules keep that
	// nil from panicking anything downstream (agent-os-xay):
	//
	//  1. Every *DockerService method guards its own nil receiver and returns
	//     services.ErrDockerUnavailable. Handlers therefore receive the concrete
	//     pointer directly, and consumer-side interfaces (stackDocker,
	//     dockerStopper) keep working even though a nil pointer inside an
	//     interface is a non-nil interface value.
	//  2. The seams below that convert to an UNTYPED nil (dockerPinger,
	//     containerLister, operationStreamer) are exactly those whose consumer
	//     checks `== nil` before calling. Never hand an untyped nil to a consumer
	//     that calls straight through: a method call on a nil interface panics,
	//     where a nil *DockerService refuses cleanly.
	dockerService, err := services.NewDockerService(cfg)
	if err != nil {
		slog.Warn("Docker service unavailable", "error", err)
	}

	var schedulerService *services.SchedulerService
	if dockerService != nil {
		schedulerService = services.NewSchedulerService(dockerService, db, slog.Default(), handlers.BroadcastEvent)
	}

	scannerService := services.NewScannerService(cfg, db)

	opLock := services.NewOperationLock()

	hasGlobalEnv, _ := scannerService.ScanAll()
	if hasGlobalEnv {
		slog.Info("Global .env file detected")
	}

	watcherService := services.NewWatcherService(scannerService, cfg)
	if err := watcherService.Start(); err != nil {
		slog.Warn("Failed to start file watcher", "error", err)
	}
	defer watcherService.Stop()

	middleware.InitRateLimiters()
	handlers.InitUpgrader(cfg.CORSOrigins, cfg.AuthDisabled)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// TRUSTED_NETWORKS doubles as Gin's trusted-proxy list, which is not obvious
	// from its name, so log what it resolved to (agent-os-boe).
	trustedProxies := []string{"127.0.0.1", "::1"}
	fromConfig := false
	if cfg.TrustedNetworks != "" {
		var trimmed []string
		for _, p := range strings.Split(cfg.TrustedNetworks, ",") {
			if p = strings.TrimSpace(p); p != "" {
				trimmed = append(trimmed, p)
			}
		}
		if len(trimmed) > 0 {
			trustedProxies = trimmed
			fromConfig = true
		}
	}
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		slog.Warn("Invalid trusted proxy configuration", "error", err, "proxies", trustedProxies)
	}
	middleware.LogTrustedProxies(trustedProxies, fromConfig)
	// Feed the X-Forwarded-Proto trust gate the SAME effective list gin's
	// trusted-proxy machinery just received: gin's SetTrustedProxies only
	// governs X-Forwarded-For/X-Real-IP (RemoteIPHeaders), never
	// X-Forwarded-Proto, so without this call that header was honoured from
	// any peer whatsoever (agent-os-ab9). Feeding both from one computed list
	// is meant to keep the two "which peers do we trust" answers in
	// agreement, but does NOT guarantee it on a malformed TRUSTED_NETWORKS
	// entry: gin's trusted-proxy parser drops everything after the FIRST bad
	// entry, while IsTrustedIP (internal/middleware/auth.go) skips a bad
	// entry and keeps evaluating the rest — VERIFIED 2026-08-05, see the
	// comment on trustedProxyNetworks in proxytrust.go for the exact
	// divergence this produces. TestTrustedProxyProtoGateIsWired (this
	// package) pins that both calls below receive the identical identifier,
	// which is the most this file does to keep them from silently diverging
	// onto two different variables — it does not fix the parser-disagreement
	// case above.
	middleware.InitTrustedProxyNetworks(trustedProxies)

	r.Use(middleware.RecoveryMiddleware())
	r.Use(middleware.TrustedProxyWarning())
	// Before LoggingMiddleware: the HTTP log line carries the request ID.
	r.Use(middleware.RequestID())
	r.Use(middleware.LoggingMiddleware())
	r.Use(middleware.BodySizeLimit())
	r.Use(middleware.CORSMiddleware(cfg.CORSOrigins))
	r.Use(middleware.ValidateInput())
	r.Use(SecurityHeaders(cfg))

	// Liveness and readiness. dockerService is a typed nil when the daemon was
	// unreachable at startup; hand the handler an untyped nil so it reports the
	// outage rather than calling through a non-nil interface holding a nil
	// pointer (agent-os-69a).
	var dockerPinger handlers.DockerPinger
	if dockerService != nil {
		dockerPinger = dockerService
	}
	handlers.NewHealthHandler(dockerPinger, cfg.HealthNetworks).RegisterRoutes(r)

	api := r.Group("/api/v1")

	// Public by choice: build identity answers "what is running here?" for
	// monitoring and support without a session. Mirrored in
	// middleware.PublicPaths, which both auth and CSRF consult (agent-os-r7e).
	handlers.NewVersionHandler().RegisterVersionRoutes(api)

	// One store shared by the minting side (POST /auth/verify-password) and the
	// validating side (middleware.EnvUnlock). Two instances would mint tokens
	// nothing can validate, which is why this is constructed once, here.
	envUnlockStore := services.NewEnvUnlockStore()

	authHandler := handlers.NewAuthHandler(db, cfg.JWTSecret, cfg.AuthDisabled)
	authHandler.SetEnvUnlockStore(envUnlockStore)
	authGroup := api.Group("/auth")
	authHandler.RegisterPublicRoutes(authGroup)
	authGroup.Use(middleware.RateLimitAuth())
	authHandler.RegisterRoutes(authGroup)

	settingsHandler := handlers.NewSettingsHandler(db, cfg.StacksDir, cfg.JWTSecret, cfg.AuthDisabled, schedulerService, cfg)

	// AuthDisabledAllowedNetworks, not TrustedNetworks: that value is Gin's
	// trusted-proxy list, and reusing it here would mean an operator adding a
	// reverse proxy for correct client-IP attribution silently widens who can
	// skip authentication (agent-os-0s4). An operator who relied on the old
	// shared behaviour loses access after upgrading, so warn rather than fail
	// silently: unset AuthDisabledAllowedNetworks defaults to loopback only,
	// which is narrower than a non-empty TrustedNetworks used to grant here.
	if cfg.AuthDisabled && cfg.AuthDisabledAllowedNetworks == "" && cfg.TrustedNetworks != "" {
		slog.Warn("AUTH_DISABLED bypass is now restricted to loopback only",
			"reason", "AUTH_DISABLED_ALLOWED_NETWORKS is unset while TRUSTED_NETWORKS is set; these are separate lists as of agent-os-0s4",
			"trusted_networks", cfg.TrustedNetworks,
			"fix", "set AUTH_DISABLED_ALLOWED_NETWORKS explicitly if hosts beyond loopback need the AUTH_DISABLED bypass")
	}

	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware(db, cfg.JWTSecret, cfg.AuthDisabled, cfg.AuthDisabledAllowedNetworks))
	protected.Use(middleware.RateLimitByUser())
	protected.Use(middleware.CSRFMiddleware())
	// After AuthMiddleware, which is what publishes the userID this gate binds
	// the token to. It never rejects a request — it only records whether the
	// caller re-entered their password recently, and the secret-reveal handlers
	// redact when it did not.
	protected.Use(middleware.EnvUnlock(envUnlockStore, cfg.AuthDisabled))
	authHandler.RegisterProtectedRoutes(protected)
	settingsHandler.RegisterRoutes(protected)

	directoriesHandler := handlers.NewDirectoriesHandler(scannerService, db)
	directoriesGroup := protected.Group("/directories")
	directoriesHandler.RegisterRoutes(directoriesGroup)

	stacksHandler := handlers.NewStacksHandler(dockerService, scannerService, services.NewLinterService(), db, cfg, services.NewActionLogger(db), opLock)
	envHandler := handlers.NewEnvHandler(db, cfg)
	composeHandler := handlers.NewComposeHandler(services.NewLinterService(), db, cfg)
	wireStacksGroup(protected, stacksHandler, envHandler, composeHandler, 120*time.Second)

	composeGroup := protected.Group("/compose")
	composeGroup.POST("/lint", stacksHandler.Lint)

	gitHandler := handlers.NewGitHandler(services.NewGitService(cfg, db), dockerService, db, cfg)
	gitGroup := protected.Group("/git")
	gitHandler.RegisterRoutes(gitGroup)

	connectionManager := handlers.NewConnectionManager(10)

	// Terminals get their own, lower cap. Every terminal connection is a real
	// `docker exec` process with a PTY held for up to 30 minutes, which is
	// materially more expensive than a log or metrics stream, and five
	// concurrent shells is already generous for an interactive tool
	// (agent-os-a0y). services.MaxConcurrentSessions is the host-wide ceiling
	// behind it.
	terminalConnections := handlers.NewConnectionManager(5)

	logsHandler := handlers.NewLogsHandler(dockerService, db, cfg.JWTSecret, cfg.AuthDisabled, cfg.DataDir, connectionManager)
	logsHandler.RegisterRoutes(protected)

	terminalService := services.NewTerminalService(cfg)
	terminalService.StartReaper(ctx)

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.Warn("Docker client unavailable for monitoring", "error", err)
	}
	monitorService := services.NewMonitorServiceWithDB(dockerClient, db)
	eventBus := handlers.DefaultEventBus()
	monitorHandler := handlers.NewMonitoringHandler(monitorService, dockerService, db, connectionManager, eventBus)
	monitorHandler.RegisterRoutes(protected, cfg.JWTSecret, cfg.AuthDisabled)

	dashboardHandler := handlers.NewDashboardHandler(monitorService, dockerService, db, connectionManager)
	dashboardHandler.RegisterRoutes(protected, cfg.JWTSecret, cfg.AuthDisabled)

	updateJobManager := services.NewUpdateJobManager(15 * time.Minute)

	resourcesHandler := handlers.NewResourcesHandlerWithJobManager(dockerService, db, schedulerService, updateJobManager)
	resourcesHandler.RegisterRoutes(protected)

	if schedulerService != nil {
		intervalStr, _ := db.GetSetting("update_scan_interval")
		scanIntervalMinutes := 0
		if intervalStr != "" {
			if minutes, err := strconv.Atoi(intervalStr); err == nil {
				scanIntervalMinutes = minutes
			}
		}

		if scanIntervalMinutes > 0 {
			// Start also arms the scheduled-apply timer, reading update_apply_*
			// itself, so a configured apply schedule is live from boot.
			schedulerService.Start(time.Duration(scanIntervalMinutes) * time.Minute)
			slog.Info("Update scheduler started", "interval_minutes", scanIntervalMinutes)
		} else if applyMode, _ := db.GetSetting("update_apply_mode"); applyMode == "scheduled" {
			// The apply timer lives inside the scan scheduler, because applying
			// from a cache no scan ever refreshes is worse than not applying.
			// Say so rather than letting a configured schedule sit silently dead.
			applyTime, _ := db.GetSetting("update_apply_time")
			slog.Warn("Auto-update apply is scheduled but the update scan interval is 0, so nothing will be applied",
				"apply_time", applyTime)
		}
	}

	if monitorService != nil {
		go handlers.StartEventBroadcaster(ctx, monitorService, eventBus)
	}

	registerStaticAssets(r)

	// stacksGroup's own timeout is already wired by wireStacksGroup above, at
	// the point the group and its routes were registered — not here, which is
	// exactly the ordering mistake agent-os-qru.1 fixed.
	wsGroup := protected.Group("")
	wsGroup.Use(timeoutMiddleware(300 * time.Second))

	// Untyped nil when the daemon was unreachable at startup: a typed nil in an
	// interface is non-nil, and the handler's nil check is what makes it deny
	// rather than skip the container-membership check (agent-os-7u5).
	var containerLister handlers.ContainerLister
	if dockerService != nil {
		containerLister = dockerService
	}
	terminalHandler := handlers.NewTerminalHandler(terminalService, containerLister, db, terminalConnections, services.NewActionLogger(db))
	terminalHandler.RegisterRoutes(wsGroup, cfg.JWTSecret, cfg.AuthDisabled)

	// Untyped nil again: OperationsHandler now takes an interface, and its own
	// nil check is what keeps a Docker outage from panicking the process.
	var operationStreamer handlers.OperationStreamer
	if dockerService != nil {
		operationStreamer = dockerService
	}
	operationsHandler := handlers.NewOperationsHandler(operationStreamer, db, opLock, connectionManager)
	operationsHandler.RegisterRoutes(wsGroup, cfg.JWTSecret, cfg.AuthDisabled)

	updateJobsWSHandler := handlers.NewUpdateJobsWSHandler(updateJobManager, db, cfg.JWTSecret, cfg.AuthDisabled, connectionManager)
	updateJobsWSHandler.RegisterRoutes(wsGroup)

	// ── Backup engine ──────────────────────────────────────────────────────────
	//
	// Wire BackupService, BackupScheduler, and BackupHandler mirroring the
	// SchedulerService + OperationsHandler pattern above. Graceful degradation:
	// if restic/rclone are absent the service still starts and returns
	// ErrBackupUnavailable on write operations; the UI shows its own banner.
	actionLogger := services.NewActionLogger(db)
	backupSvc := services.NewBackupService(cfg, db, dockerService, opLock, actionLogger)
	backupSched := services.NewBackupScheduler(backupSvc, db, slog.Default())
	backupSvc.SetScheduler(backupSched)
	backupHandler := handlers.NewBackupHandler(backupSvc, db, slog.Default())

	// REST routes sit under the same protected group as StacksHandler/ResourcesHandler.
	backupHandler.RegisterRoutes(protected)
	// WS streaming routes sit under the same wsGroup as OperationsHandler.
	backupHandler.RegisterWSRoutes(wsGroup, cfg.JWTSecret, cfg.AuthDisabled)

	// Log availability so operators know at startup whether backup is functional.
	if av := backupSvc.Available(); !av.Available {
		slog.Warn("Backup engine degraded: backup features disabled until tools are installed",
			"reason", av.Message,
		)
	}

	// Start the scheduler only if an interval has been configured.
	backupSvc.StartScheduler()

	indexHTMLBytes, indexErr := os.ReadFile("./frontend/index.html")
	if indexErr != nil {
		slog.Warn("Failed to preload index.html, will fall back to disk read per request", "error", indexErr)
	}
	indexHTML := string(indexHTMLBytes)

	registerIndexRoute(r, indexHTML)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("Server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	cancel()

	if schedulerService != nil {
		schedulerService.Stop()
	}

	updateJobManager.Stop()

	backupSvc.StopScheduler()

	watcherService.Stop()

	if connectionManager != nil {
		connectionManager.CloseAll()
	}

	// Terminals live in their own manager, so shutdown has to close both or
	// their PTY sessions outlive the shutdown that was meant to end them.
	if terminalConnections != nil {
		terminalConnections.CloseAll()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	// Drain in-flight durable backup/restore/sync/dr-restore/prune runs only
	// AFTER srv.Shutdown has returned. LaunchX methods DO refuse a new run
	// once this drain has begun (BackupRunnerRegistry.registerAndAdd checks
	// reg.stopped, set by beginStop before StopWithTimeout ever waits — see
	// agent-os-7a5), so the guard alone would tolerate either placement. The
	// reason for placing it here rather than before srv.Shutdown is
	// different: draining first would reject legitimate in-flight requests
	// with 503 while the server is still meant to be serving them. After
	// srv.Shutdown returns, no new HTTP request can arrive at all, so nothing
	// legitimate is lost by refusing launches from this point on — the guard
	// is belt-and-braces here, not the thing doing the work.
	//
	// Bounded rather than unbounded: an in-flight run is not fast, and an
	// unbounded wait here would make graceful shutdown hang indefinitely. If
	// the bound expires, any still-running goroutines are left running
	// detached (as by design) and the process proceeds to exit; nothing is
	// stranded because the startup sweeper (database.SweepInterruptedBackupRuns,
	// called on every boot) marks any row still "running" as "interrupted".
	// See agent-os-7a5.
	if !backupHandler.StopWithTimeout(backupDrainTimeout) {
		slog.Warn("Timed out waiting for in-flight backup runs to finish; exiting anyway",
			"timeout", backupDrainTimeout)
	}

	slog.Info("Server exited")
}
