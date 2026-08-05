package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/handlers"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// fakeStackStore satisfies handlers' unexported stackStore interface
// structurally (Go interface satisfaction needs no shared import of the
// interface type), so StacksHandler can be constructed here without a real
// *database.DB. Every method beyond ListStacks/GetStack is exercised by
// routes this test never calls; they only need to exist.
type fakeStackStore struct{}

func (fakeStackStore) ListStacks() ([]models.Stack, error)       { return nil, nil }
func (fakeStackStore) GetStack(id string) (*models.Stack, error) { return nil, nil }
func (fakeStackStore) GetStackByProjectName(name string) (*models.Stack, error) {
	return nil, nil
}
func (fakeStackStore) ListStacksByDirectory(path string) ([]models.Stack, error) {
	return nil, nil
}
func (fakeStackStore) UpsertStack(stack models.Stack) error      { return nil }
func (fakeStackStore) UpdateStackStatus(id, status string) error { return nil }
func (fakeStackStore) DeleteStack(id string) error               { return nil }
func (fakeStackStore) DeleteDirectoryIfOrphaned(path string) (bool, error) {
	return false, nil
}

// deadlineProbeDocker satisfies stackDocker. GetStackStatuses is the call
// StacksHandler.List makes with c.Request.Context() (internal/handlers/stacks.go),
// so recording whether that context carries a deadline is a direct read of the
// one fact agent-os-qru.1 is about: does stacksGroup's timeout middleware
// actually reach requests routed through it.
type deadlineProbeDocker struct {
	called      bool
	sawDeadline bool
}

func (d *deadlineProbeDocker) GetStackStatuses(ctx context.Context, db services.DashboardDB) (map[string]services.LiveStatus, error) {
	d.called = true
	_, d.sawDeadline = ctx.Deadline()
	return map[string]services.LiveStatus{}, nil
}

func (d *deadlineProbeDocker) StartVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (d *deadlineProbeDocker) StopVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (d *deadlineProbeDocker) RestartVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (d *deadlineProbeDocker) PullVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (d *deadlineProbeDocker) DeleteVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}

// TestStacksGroupTimeoutAppliesToItsRoutes pins agent-os-qru.1: stacksGroup's
// 120s timeout middleware was being registered with .Use() AFTER
// stacksHandler/envHandler/composeHandler.RegisterRoutes had already run.
// gin@v1.12.0's RouterGroup.combineHandlers (routergroup.go:88) snapshots the
// handler chain at ROUTE REGISTRATION time, so that Use() never applied to
// any /stacks route.
//
// This calls wireStacksGroup — the exact function main() calls — rather than
// replicating the registration order by hand, so a regression that separates
// the Use() from the RegisterRoutes() calls inside main.go's real wiring is
// what this test is sensitive to, not just Gin's general behaviour.
func TestStacksGroupTimeoutAppliesToItsRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	protected := r.Group("/api/v1")

	docker := &deadlineProbeDocker{}
	cfg := &config.Config{}
	stacksHandler := handlers.NewStacksHandler(docker, nil, nil, fakeStackStore{}, cfg, nil, nil)
	envHandler := handlers.NewEnvHandler(nil, cfg)
	composeHandler := handlers.NewComposeHandler(nil, nil, cfg)

	wireStacksGroup(protected, stacksHandler, envHandler, composeHandler, 50*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !docker.called {
		t.Fatalf("GET /api/v1/stacks never reached StacksHandler.List via the real wireStacksGroup wiring")
	}
	if !docker.sawDeadline {
		t.Fatalf("stacksGroup route ran with NO request deadline — the timeout middleware registered on " +
			"stacksGroup did not apply to its own routes (agent-os-qru.1: Use() after RegisterRoutes() is a no-op " +
			"for routes already registered, per gin@v1.12.0 RouterGroup.combineHandlers)")
	}
}
