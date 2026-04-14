package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/handlers"
	"github.com/docker-manager/backend/internal/middleware"
	"github.com/docker-manager/backend/internal/services"
	"github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
)

func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self' ws://localhost:* wss://localhost:*; frame-ancestors 'none';")
		c.Next()
	}
}

func isLocalhost(c *gin.Context) bool {
	host := c.Request.Host
	return host == "localhost:5001" || host == "127.0.0.1:5001" || strings.HasPrefix(host, "127.0.0.1:") || strings.HasPrefix(host, "localhost:")
}

func main() {
	slog.Info("Starting Docker Manager backend")

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	db, err := database.NewWithMigrations(cfg.DataDir)
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer db.Close()

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
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				retentionStr, err := db.GetSetting("max_log_retention_days")
				if err != nil {
					slog.Error("Failed to get log retention setting", "error", err)
					continue
				}
				retentionDays := 90
				if retentionStr != "" {
					if _, err := fmt.Sscanf(retentionStr, "%d", &retentionDays); err != nil {
						slog.Error("Failed to parse log retention days", "error", err)
						continue
					}
				}
				if retentionDays < 7 {
					retentionDays = 7
				}
				if err := db.DeleteOldActionLogs(retentionDays); err != nil {
					slog.Error("Failed to delete old action logs", "error", err)
				} else {
					slog.Info("Cleaned up old action logs", "retention_days", retentionDays)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	dockerService, err := services.NewDockerService(cfg)
	if err != nil {
		slog.Warn("Docker service unavailable", "error", err)
	}

	scannerService := services.NewScannerService(cfg, db)

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

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(middleware.RecoveryMiddleware())
	r.Use(middleware.LoggingMiddleware())
	r.Use(middleware.CORSMiddleware(cfg.CORSOrigins))
	r.Use(middleware.ValidateInput())
	r.Use(gin.CustomRecovery(nil))
	r.Use(SecurityHeaders())

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
	authGroup.Use(middleware.RateLimitByIP())
	authHandler.RegisterRoutes(authGroup)

	settingsHandler := handlers.NewSettingsHandler(db, cfg.StacksDir, cfg.JWTSecret, cfg.AuthDisabled)

	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware(db, cfg.JWTSecret, cfg.AuthDisabled, cfg.TrustedNetworks))
	protected.Use(middleware.RateLimitByUser())
	settingsHandler.RegisterRoutes(protected)

	directoriesHandler := handlers.NewDirectoriesHandler(scannerService, db)
	directoriesGroup := protected.Group("/directories")
	directoriesHandler.RegisterRoutes(directoriesGroup)

	stacksHandler := handlers.NewStacksHandler(dockerService, scannerService, services.NewLinterService(), db, cfg)
	stacksGroup := protected.Group("/stacks")
	stacksHandler.RegisterRoutes(stacksGroup)

	composeGroup := protected.Group("/compose")
	composeGroup.POST("/lint", stacksHandler.Lint)

	envHandler := handlers.NewEnvHandler(db, cfg)
	envHandler.RegisterRoutes(stacksGroup)

	composeHandler := handlers.NewComposeHandler(services.NewLinterService(), db, cfg)
	composeHandler.RegisterRoutes(stacksGroup)

	gitHandler := handlers.NewGitHandler(services.NewGitService(cfg), dockerService, db, cfg)
	gitGroup := protected.Group("/git")
	gitHandler.RegisterRoutes(gitGroup)

	logsHandler := handlers.NewLogsHandler(dockerService, db, cfg.JWTSecret, cfg.AuthDisabled)
	logsHandler.RegisterRoutes(protected)

	terminalService := services.NewTerminalService(cfg)
	terminalService.StartReaper(ctx)

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.Warn("Docker client unavailable for monitoring", "error", err)
	}
	monitorService := services.NewMonitorServiceWithDB(dockerClient, db)
	connectionManager := handlers.NewConnectionManager(10)
	monitorHandler := handlers.NewMonitoringHandler(monitorService, dockerService, db, connectionManager)
	monitorHandler.RegisterRoutes(protected, cfg.JWTSecret, cfg.AuthDisabled)
	if monitorService != nil {
		go handlers.StartEventBroadcaster(ctx, monitorService)
	}

	r.Static("/assets", "./frontend/assets")
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
	terminalGroup := protected.Group("/terminal")
	terminalGroup.Use(timeoutMiddleware(300 * time.Second))

	terminalHandler := handlers.NewTerminalHandler(terminalService, db)
	terminalHandler.RegisterRoutes(terminalGroup)

	r.NoRoute(func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.File("./frontend/index.html")
		}
	})

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
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

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	slog.Info("Server exited")
}
