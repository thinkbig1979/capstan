package handlers

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// readinessProbeTimeout bounds the dependency check. A hung Docker daemon
// otherwise parks a goroutine per probe, and repeated probes pile them up.
const readinessProbeTimeout = 2 * time.Second

// DockerPinger is the readiness handler's view of the Docker service: one cheap,
// context-aware round-trip. Narrow on purpose, so the handler is testable
// without a daemon.
type DockerPinger interface {
	Ping(ctx context.Context) error
}

// HealthHandler serves liveness and readiness.
//
// The split exists because Capstan is a separate process from Docker. The old
// combined endpoint returned 503 whenever the daemon was unreachable, so a
// daemon restart marked the container unhealthy and anything that restarts on
// failed health checks would bounce Capstan — which does nothing to fix Docker
// (agent-os-69a).
//
//	GET /health       liveness  — the process is up and serving. No Docker call.
//	GET /health/ready readiness — reports dependencies; 503 names what is degraded.
type HealthHandler struct {
	// docker is nil when the Docker service could not be created at startup.
	// That is a legitimate degraded state, not an error to hide.
	docker          DockerPinger
	allowedNetworks string
	probeTimeout    time.Duration
}

// NewHealthHandler builds the handler. Pass a nil docker when the Docker service
// is unavailable; readiness then reports Docker as degraded rather than
// pretending to be ready.
//
// allowedNetworks is HEALTH_ALLOWED_NETWORKS: comma-separated CIDRs, beyond
// loopback, permitted to reach either endpoint. Empty means loopback only, which
// is the pre-split behaviour — an upgrade must not silently widen exposure.
func NewHealthHandler(docker DockerPinger, allowedNetworks string) *HealthHandler {
	return &HealthHandler{
		docker:          docker,
		allowedNetworks: allowedNetworks,
		probeTimeout:    readinessProbeTimeout,
	}
}

// RegisterRoutes mounts both endpoints on the root router. They sit outside the
// protected group deliberately: a probe has no session. Both paths are listed in
// middleware.PublicPaths so auth and CSRF agree with that.
func (h *HealthHandler) RegisterRoutes(r gin.IRoutes) {
	r.GET("/health", h.Live)
	r.GET("/health/ready", h.Ready)
}

// allowed enforces the network policy shared by both endpoints, writing the 403
// itself. Loopback always passes, so the container's own HEALTHCHECK needs no
// configuration.
func (h *HealthHandler) allowed(c *gin.Context) bool {
	clientIP := c.ClientIP()

	// The whole loopback range, not just the literal 127.0.0.1/::1 that
	// middleware.IsTrustedIP matches. The handler this replaced used
	// net.IP.IsLoopback, and narrowing that here would deny a probe bound to,
	// say, 127.0.0.2 that works today.
	if ip := net.ParseIP(clientIP); ip != nil && ip.IsLoopback() {
		return true
	}

	if middleware.IsTrustedIP(clientIP, h.allowedNetworks) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error": "Health endpoint restricted; add this network to HEALTH_ALLOWED_NETWORKS to permit it",
	})
	return false
}

// Live answers whether the process is up and serving. It must not touch any
// dependency: the container HEALTHCHECK points here, and a dependency failure
// answered from this endpoint gets the container restarted for someone else's
// outage.
//
// The body keeps the pre-split {"status":"healthy"} so existing monitors that
// match on it keep working. What changed is that it no longer returns 503.
func (h *HealthHandler) Live(c *gin.Context) {
	if !h.allowed(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

// Ready reports the dependencies, 503 when any is degraded, naming which.
func (h *HealthHandler) Ready(c *gin.Context) {
	if !h.allowed(c) {
		return
	}

	timeout := h.probeTimeout
	if timeout <= 0 {
		timeout = readinessProbeTimeout
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	dockerCheck := gin.H{"status": "ok"}
	var degraded []string
	var dockerErr error

	switch h.docker {
	case nil:
		degraded = append(degraded, "docker")
		dockerCheck = gin.H{"status": "unavailable", "error": "docker service not initialized at startup"}
	default:
		if err := h.docker.Ping(ctx); err != nil {
			degraded = append(degraded, "docker")
			// N12 (agent-os-4pa.6): return a fixed operator-facing string, not the
			// raw Docker client error. The raw error can carry socket paths and
			// internal detail, and this body is returned to the caller. The detail
			// is logged below instead, where it is retained for diagnosis.
			dockerCheck = gin.H{"status": "unavailable", "error": "Docker daemon unreachable"}
			dockerErr = err
		}
	}

	body := gin.H{
		"status": "ready",
		"checks": gin.H{"docker": dockerCheck},
	}

	if len(degraded) > 0 {
		body["status"] = "degraded"
		body["degraded"] = degraded
		slog.Warn("Readiness probe degraded", "degraded", degraded, "error", dockerErr)
		// Deliberately NOT routed through handleError (agent-os-ua4y): that would
		// replace this endpoint's documented {status, checks, degraded} body with
		// the generic AppError {code, message} shape, breaking
		// TestReadinessNamesDockerWhenDegraded and TestReadinessRedactsRawDockerError,
		// for a probe /health/ready that no browser client reads .code from (it is
		// outside the protected group and unused by frontend/src). logServerFault is
		// called directly instead, so this 503 still gets the same structured ERROR
		// line (request_id/status/code/cause) as every handleError site, without
		// touching the wire body. The slog.Warn above stays too, double logging is
		// an accepted outcome elsewhere in this bead.
		logServerFault(c, http.StatusServiceUnavailable, models.ErrDockerUnavailable, dockerErr)
		c.JSON(http.StatusServiceUnavailable, body)
		return
	}

	c.JSON(http.StatusOK, body)
}
