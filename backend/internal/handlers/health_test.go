package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// fakePinger records how often it was called, so a test can assert that
// liveness touched no dependency at all.
type fakePinger struct {
	calls atomic.Int32
	err   error
	// block, when non-nil, holds Ping until ctx is done or block is closed —
	// the stand-in for a hung daemon.
	block chan struct{}
	// sawDeadline / probeBudget record the budget Ready put on the probe's
	// context, sampled at entry. TestReadinessTimesOutOnAHungDaemon asserts on
	// this instead of on how long the request took (agent-os-jar5): elapsed
	// time is a wall-clock bound a loaded runner can turn red, whereas the
	// budget the handler SET is a property of the code under test. See the
	// comment on that test for what it discriminates.
	sawDeadline atomic.Bool
	probeBudget atomic.Int64 // nanoseconds remaining when Ping was entered
}

func (f *fakePinger) Ping(ctx context.Context) error {
	f.calls.Add(1)
	if d, ok := ctx.Deadline(); ok {
		f.probeBudget.Store(int64(time.Until(d)))
		f.sawDeadline.Store(true)
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func healthRouter(h *HealthHandler) *gin.Engine {
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

// requestFrom issues a GET to path as if it came from remoteIP. IPv6 literals
// need bracketing in RemoteAddr or net.SplitHostPort cannot parse them, and gin
// falls back to an empty ClientIP.
func requestFrom(r *gin.Engine, path, remoteIP string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if strings.Contains(remoteIP, ":") {
		req.RemoteAddr = "[" + remoteIP + "]:40000"
	} else {
		req.RemoteAddr = remoteIP + ":40000"
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeHealthBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	return body
}

// ── Liveness ────────────────────────────────────────────────────────────────

// TestLivenessMakesNoDockerCall is the core of the split. The container
// HEALTHCHECK points here; if liveness consulted Docker, a daemon restart would
// mark the container unhealthy and get Capstan bounced for someone else's
// outage.
func TestLivenessMakesNoDockerCall(t *testing.T) {
	docker := &fakePinger{}
	w := requestFrom(healthRouter(NewHealthHandler(docker, "")), "/health", "127.0.0.1")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := docker.calls.Load(); got != 0 {
		t.Errorf("liveness called Docker %d time(s); it must call it none", got)
	}
	if body := decodeHealthBody(t, w); body["status"] != "healthy" {
		t.Errorf("status = %v, want %q", body["status"], "healthy")
	}
}

// TestLivenessStays200WhenDockerIsDown is the behaviour change the bead asks
// for: the old combined endpoint returned 503 here.
func TestLivenessStays200WhenDockerIsDown(t *testing.T) {
	docker := &fakePinger{err: errors.New("cannot connect to the Docker daemon")}
	w := requestFrom(healthRouter(NewHealthHandler(docker, "")), "/health", "127.0.0.1")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d — a Docker outage must not mark the process dead", w.Code, http.StatusOK)
	}
}

// TestLivenessStays200WhenDockerServiceIsAbsent covers the nil case: the daemon
// was already unreachable when the process started.
func TestLivenessStays200WhenDockerServiceIsAbsent(t *testing.T) {
	w := requestFrom(healthRouter(NewHealthHandler(nil, "")), "/health", "127.0.0.1")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ── Readiness ───────────────────────────────────────────────────────────────

func TestReadinessReportsReadyWhenDockerAnswers(t *testing.T) {
	docker := &fakePinger{}
	w := requestFrom(healthRouter(NewHealthHandler(docker, "")), "/health/ready", "127.0.0.1")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, http.StatusOK, w.Body.String())
	}
	body := decodeHealthBody(t, w)
	if body["status"] != "ready" {
		t.Errorf("status = %v, want %q", body["status"], "ready")
	}
	if docker.calls.Load() != 1 {
		t.Errorf("Docker pinged %d time(s), want 1", docker.calls.Load())
	}
}

// TestReadinessNamesDockerWhenDegraded — 503 alone is not enough; an operator
// reading the probe output has to be able to tell *what* is degraded.
func TestReadinessNamesDockerWhenDegraded(t *testing.T) {
	docker := &fakePinger{err: errors.New("cannot connect to the Docker daemon socket")}
	w := requestFrom(healthRouter(NewHealthHandler(docker, "")), "/health/ready", "127.0.0.1")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	body := decodeHealthBody(t, w)
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want %q", body["status"], "degraded")
	}

	names, ok := body["degraded"].([]any)
	if !ok || len(names) != 1 || names[0] != "docker" {
		t.Fatalf("degraded = %v, want [docker] — 503 must name the failing dependency", body["degraded"])
	}

	checks, _ := body["checks"].(map[string]any)
	dockerCheck, _ := checks["docker"].(map[string]any)
	if dockerCheck["status"] != "unavailable" {
		t.Errorf("checks.docker.status = %v, want %q", dockerCheck["status"], "unavailable")
	}
	if msg, _ := dockerCheck["error"].(string); msg == "" {
		t.Error("checks.docker.error is empty; the reason is what makes the probe actionable")
	}
}

// TestReadinessRedactsRawDockerError pins N12 (agent-os-4pa.6): the readiness
// body must carry a fixed operator-facing string, never the raw Docker client
// error — which can leak socket paths and internal detail — and the raw error
// belongs in the log instead. Seen failing first against the pre-fix code, which
// echoed err.Error() straight into the response body.
func TestReadinessRedactsRawDockerError(t *testing.T) {
	raw := "cannot connect to the Docker daemon at unix:///run/secret-internal/docker.sock"
	docker := &fakePinger{err: errors.New(raw)}
	w := requestFrom(healthRouter(NewHealthHandler(docker, "")), "/health/ready", "127.0.0.1")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(w.Body.String(), "secret-internal") {
		t.Errorf("readiness body leaked the raw Docker error: %s", w.Body.String())
	}
	body := decodeHealthBody(t, w)
	checks, _ := body["checks"].(map[string]any)
	dockerCheck, _ := checks["docker"].(map[string]any)
	if msg, _ := dockerCheck["error"].(string); msg != "Docker daemon unreachable" {
		t.Errorf("checks.docker.error = %q, want the fixed operator-facing string", msg)
	}
}

// TestReadinessDegradesWhenDockerServiceIsAbsent covers the nil-service branch.
// A nil *DockerService stored in an interface is a non-nil interface value, so
// this is also the regression guard against pinging through it and panicking.
func TestReadinessDegradesWhenDockerServiceIsAbsent(t *testing.T) {
	w := requestFrom(healthRouter(NewHealthHandler(nil, "")), "/health/ready", "127.0.0.1")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	body := decodeHealthBody(t, w)
	names, ok := body["degraded"].([]any)
	if !ok || len(names) != 1 || names[0] != "docker" {
		t.Fatalf("degraded = %v, want [docker]", body["degraded"])
	}
}

// TestReadinessTimesOutOnAHungDaemon: without a bound on the probe, repeated
// 30-second checks against a hung daemon pile up goroutines indefinitely.
func TestReadinessTimesOutOnAHungDaemon(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	docker := &fakePinger{block: blocked}
	h := NewHealthHandler(docker, "")
	h.probeTimeout = 50 * time.Millisecond

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- requestFrom(healthRouter(h), "/health/ready", "127.0.0.1") }()

	select {
	case w := <-done:
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
		body := decodeHealthBody(t, w)
		if body["status"] != "degraded" {
			t.Errorf("status = %v, want %q", body["status"], "degraded")
		}
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("readiness never returned against a hung daemon: the probe has no timeout")
	}

	// The guard above only proves the request RETURNED. It deliberately does
	// NOT carry the size assertion any more: at wsHangGuardCeiling it would
	// also be satisfied by a handler that ignored h.probeTimeout and used a
	// large constant, which is exactly the defect this test's own failure
	// message names. That assertion moves here (agent-os-jar5).
	//
	// This reads the budget Ready put on the probe's context, sampled inside
	// Ping at entry — a property of the code under test, not of the box. The
	// comparison is ONE-SIDED on purpose: scheduling delay between
	// context.WithTimeout (health.go:114) and Ping's first line can only make
	// the observed budget SMALLER, so a loaded runner cannot push it past the
	// bound. A handler using a larger constant reports a larger budget and
	// fails, however slow the machine is.
	if !docker.sawDeadline.Load() {
		t.Fatal("Ready gave the probe no deadline at all: h.probeTimeout is not reaching the context")
	}
	if budget := time.Duration(docker.probeBudget.Load()); budget > h.probeTimeout {
		t.Errorf("probe budget = %v, want <= %v (h.probeTimeout): Ready is not honouring its configured probe timeout",
			budget, h.probeTimeout)
	}
}

// ── Reachability ────────────────────────────────────────────────────────────

// TestLoopbackReachesBothWithNoConfiguration — the container's own HEALTHCHECK
// must work out of the box.
func TestLoopbackReachesBothWithNoConfiguration(t *testing.T) {
	r := healthRouter(NewHealthHandler(&fakePinger{}, ""))

	for _, tc := range []struct{ path, ip string }{
		{"/health", "127.0.0.1"},
		{"/health/ready", "127.0.0.1"},
		{"/health", "::1"},
		{"/health/ready", "::1"},
		// The replaced handler used net.IP.IsLoopback, so the whole 127.0.0.0/8
		// range reached it. Keep that rather than narrowing to the literal
		// 127.0.0.1 that middleware.IsTrustedIP matches.
		{"/health", "127.0.0.2"},
		{"/health/ready", "127.0.0.2"},
	} {
		if w := requestFrom(r, tc.path, tc.ip); w.Code != http.StatusOK {
			t.Errorf("GET %s from %s: status = %d, want %d", tc.path, tc.ip, w.Code, http.StatusOK)
		}
	}
}

// TestNonLoopbackDeniedByDefault pins the no-silent-exposure-change requirement:
// an upgrade with no new configuration must keep the pre-split restriction.
func TestNonLoopbackDeniedByDefault(t *testing.T) {
	r := healthRouter(NewHealthHandler(&fakePinger{}, ""))

	for _, path := range []string{"/health", "/health/ready"} {
		w := requestFrom(r, path, "10.1.2.3")
		if w.Code != http.StatusForbidden {
			t.Errorf("GET %s from 10.1.2.3: status = %d, want %d — the default must not widen exposure",
				path, w.Code, http.StatusForbidden)
		}
	}
}

// TestNonLoopbackAllowedOnceItsNetworkIsListed is the other half: the endpoint
// is reachable by an uptime monitor once its network is configured.
func TestNonLoopbackAllowedOnceItsNetworkIsListed(t *testing.T) {
	r := healthRouter(NewHealthHandler(&fakePinger{}, "10.1.0.0/16"))

	for _, path := range []string{"/health", "/health/ready"} {
		if w := requestFrom(r, path, "10.1.2.3"); w.Code != http.StatusOK {
			t.Errorf("GET %s from 10.1.2.3 with 10.1.0.0/16 allowed: status = %d, want %d",
				path, w.Code, http.StatusOK)
		}
	}

	// A host outside the listed range is still denied.
	if w := requestFrom(r, "/health", "10.9.2.3"); w.Code != http.StatusForbidden {
		t.Errorf("GET /health from 10.9.2.3: status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestDeniedRequestSkipsTheDockerProbe — a rejected caller must not be able to
// drive daemon round-trips.
func TestDeniedRequestSkipsTheDockerProbe(t *testing.T) {
	docker := &fakePinger{}
	requestFrom(healthRouter(NewHealthHandler(docker, "")), "/health/ready", "10.1.2.3")

	if got := docker.calls.Load(); got != 0 {
		t.Errorf("denied request pinged Docker %d time(s), want 0", got)
	}
}

// TestHealthPathsArePublic keeps middleware.PublicPaths truthful: both routes
// are mounted outside the protected group, and that list is read by the CSRF
// middleware as well as auth.
func TestHealthPathsArePublic(t *testing.T) {
	for _, path := range []string{"/health", "/health/ready"} {
		if !middleware.IsPublicPath(path) {
			t.Errorf("%s is served outside the protected group but is not in middleware.PublicPaths", path)
		}
	}
}
