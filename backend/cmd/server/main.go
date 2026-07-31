package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net"
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
)

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

func isLocalhost(c *gin.Context) bool {
	ip := net.ParseIP(c.ClientIP())
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func main() {
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

	slog.Info("Starting Capstan backend",
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
				db.DeleteExpiredSessions()
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

	if cfg.TrustedNetworks != "" {
		proxies := strings.Split(cfg.TrustedNetworks, ",")
		var trimmed []string
		for _, p := range proxies {
			p = strings.TrimSpace(p)
			if p != "" {
				trimmed = append(trimmed, p)
			}
		}
		if len(trimmed) > 0 {
			r.SetTrustedProxies(trimmed)
		}
	} else {
		r.SetTrustedProxies([]string{"127.0.0.1", "::1"})
	}

	r.Use(middleware.RecoveryMiddleware())
	// Before LoggingMiddleware: the HTTP log line carries the request ID.
	r.Use(middleware.RequestID())
	r.Use(middleware.LoggingMiddleware())
	r.Use(middleware.BodySizeLimit())
	r.Use(middleware.CORSMiddleware(cfg.CORSOrigins))
	r.Use(middleware.ValidateInput())
	r.Use(SecurityHeaders(cfg))

	r.GET("/health", func(c *gin.Context) {
		if !isLocalhost(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Health endpoint restricted to localhost"})
			return
		}

		dockerHealthy := false

		if dockerService != nil {
			_, err := dockerService.GetContainerList("")
			if err == nil {
				dockerHealthy = true
			}
		}

		if !dockerHealthy {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
		})
	})

	api := r.Group("/api/v1")

	authHandler := handlers.NewAuthHandler(db, cfg.JWTSecret, cfg.AuthDisabled)
	authGroup := api.Group("/auth")
	authHandler.RegisterPublicRoutes(authGroup)
	authGroup.Use(middleware.RateLimitByIP())
	authHandler.RegisterRoutes(authGroup)

	settingsHandler := handlers.NewSettingsHandler(db, cfg.StacksDir, cfg.JWTSecret, cfg.AuthDisabled, schedulerService, cfg)

	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware(db, cfg.JWTSecret, cfg.AuthDisabled, cfg.TrustedNetworks))
	protected.Use(middleware.RateLimitByUser())
	protected.Use(middleware.CSRFMiddleware())
	authHandler.RegisterProtectedRoutes(protected)
	settingsHandler.RegisterRoutes(protected)

	directoriesHandler := handlers.NewDirectoriesHandler(scannerService, db)
	directoriesGroup := protected.Group("/directories")
	directoriesHandler.RegisterRoutes(directoriesGroup)

	stacksHandler := handlers.NewStacksHandler(dockerService, scannerService, services.NewLinterService(), db, cfg, services.NewActionLogger(db), opLock)
	stacksGroup := protected.Group("/stacks")
	stacksHandler.RegisterRoutes(stacksGroup)

	composeGroup := protected.Group("/compose")
	composeGroup.POST("/lint", stacksHandler.Lint)

	envHandler := handlers.NewEnvHandler(db, cfg)
	envHandler.RegisterRoutes(stacksGroup)

	composeHandler := handlers.NewComposeHandler(services.NewLinterService(), db, cfg)
	composeHandler.RegisterRoutes(stacksGroup)

	gitHandler := handlers.NewGitHandler(services.NewGitService(cfg, db), dockerService, db, cfg)
	gitGroup := protected.Group("/git")
	gitHandler.RegisterRoutes(gitGroup)

	connectionManager := handlers.NewConnectionManager(10)
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
		if intervalStr != "" {
			if minutes, err := strconv.Atoi(intervalStr); err == nil && minutes > 0 {
				schedulerService.Start(time.Duration(minutes) * time.Minute)
				slog.Info("Update scheduler started", "interval_minutes", minutes)
			}
		}
	}

	if monitorService != nil {
		go handlers.StartEventBroadcaster(ctx, monitorService, eventBus)
	}

	r.Static("/assets", "./frontend/assets")
	r.Static("/fonts", "./frontend/fonts")
	r.StaticFile("/vite.svg", "./frontend/vite.svg")

	timeoutMiddleware := func(timeout time.Duration) gin.HandlerFunc {
		return func(c *gin.Context) {
			ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		}
	}

	stacksGroup.Use(timeoutMiddleware(120 * time.Second))
	wsGroup := protected.Group("")
	wsGroup.Use(timeoutMiddleware(300 * time.Second))

	terminalHandler := handlers.NewTerminalHandler(terminalService, db)
	terminalHandler.RegisterRoutes(wsGroup, cfg.JWTSecret, cfg.AuthDisabled)

	operationsHandler := handlers.NewOperationsHandler(dockerService, db, opLock)
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

	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			return
		}
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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server exited")
}
