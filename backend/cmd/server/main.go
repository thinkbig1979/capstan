package main

import (
	"context"
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
	"github.com/gin-gonic/gin"
)

func main() {
	slog.Info("Starting Docker Manager backend")
	startTime := time.Now()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	db, err := database.New(cfg.DataDir)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	if err := database.RunMigrations(db); err != nil {
		log.Fatal("Failed to run migrations:", err)
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

	dockerService, err := services.NewDockerService(cfg)
	if err != nil {
		slog.Warn("Docker service unavailable", "error", err)
	}

	scannerService := services.NewScannerService(cfg, db)

	hasGlobalEnv, _ := scannerService.ScanAll()
	if hasGlobalEnv {
		slog.Info("Global .env file detected")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(middleware.RecoveryMiddleware())
	r.Use(middleware.LoggingMiddleware())
	r.Use(middleware.CORSMiddleware(cfg.CORSOrigins))

	r.GET("/health", func(c *gin.Context) {
		dockerStatus := "disconnected"
		if dockerService != nil {
			dockerStatus = "connected"
		}

		c.JSON(http.StatusOK, gin.H{
			"status":         "ok",
			"docker":         dockerStatus,
			"database":       "connected",
			"version":        "1.0.0",
			"uptime_seconds": time.Since(startTime).Seconds(),
		})
	})

	api := r.Group("/api/v1")

	authHandler := handlers.NewAuthHandler(db, cfg.JWTSecret, cfg.AuthDisabled)
	authGroup := api.Group("/auth")
	authHandler.RegisterRoutes(authGroup)

	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware(db, cfg.JWTSecret, cfg.AuthDisabled))

	directoriesHandler := handlers.NewDirectoriesHandler(scannerService, db)
	directoriesGroup := protected.Group("/directories")
	directoriesHandler.RegisterRoutes(directoriesGroup)

	stacksHandler := handlers.NewStacksHandler(dockerService, scannerService, db)
	stacksGroup := protected.Group("/stacks")
	stacksHandler.RegisterRoutes(stacksGroup)

	r.Static("/static", "./frontend")

	r.NoRoute(func(c *gin.Context) {
		if c.Request.URL.Path != "/" && !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.File("./frontend/index.html")
		}
		c.Next()
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
