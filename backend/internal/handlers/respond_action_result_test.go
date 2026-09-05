package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// These tests pin agent-os-7lic: an ActionResult whose outcome renders as a
// 5xx (OutcomeFailed -> HTTPStatus() 500, or renderDockerResult's 503) must
// leave one ERROR line carrying the Reason and the underlying Err, joined to
// the access log by request_id. Before the fix, renderResult was a bare
// c.JSON(r.HTTPStatus(), r) and ActionResult.Err is json:"-", so the cause
// reached neither the client nor the operator.
//
// The log line is the ONLY change. Every assertion on w.Code and w.Body
// below is a byte-for-byte guard that the wire format did not move.

// renderedBody is what c.JSON(status, r) wrote before this bead, computed
// independently of the renderer so a change to the body would be caught.
func renderedBody(t *testing.T, r truth.ActionResult) string {
	t.Helper()
	b, err := json.Marshal(r)
	require.NoError(t, err)
	return string(b)
}

// logLineContaining returns the single captured line holding needle, so a
// test can assert on attrs of THAT line rather than anywhere in the buffer.
func logLineContaining(t *testing.T, buf *syncLogBuffer, needle string) string {
	t.Helper()
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no log line contains %q. captured = %q", needle, buf.String())
	return ""
}

// TestRenderResult_FailedLogsCauseAndReason is the primary failing-first arm:
// a truth.Failed result with a non-nil Err. The sentinel lives ONLY in the
// Err, never in the Reason, so a log line that prints the sanitised Reason
// but drops the error chain still fails here.
func TestRenderResult_FailedLogsCauseAndReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := errors.New("cause-sentinel-7lic-a1")
	r := truth.Failed("failed to write env file", cause, truth.KV("id", "stack-1"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	renderResult(c, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (wire format must not move)", w.Code)
	}
	if got, want := strings.TrimSpace(w.Body.String()), renderedBody(t, r); got != want {
		t.Fatalf("body changed:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(w.Body.String(), "cause-sentinel-7lic-a1") {
		t.Fatalf("the cause leaked into the response body: %s", w.Body.String())
	}

	line := logLineContaining(t, buf, "cause-sentinel-7lic-a1")
	for _, want := range []string{"level=ERROR", "status=500", "code=ACTION_FAILED", "failed to write env file"} {
		if !strings.Contains(line, want) {
			t.Errorf("ERROR line missing %q: %s", want, line)
		}
	}
}

// TestRenderResult_NonFailedOutcomesStaySilent is the other side of the same
// instrument (trap 2 included): Success and NoChange are 200 and Partial is
// 207, and logServerFault's own < 500 guard keeps every one of them silent.
// No Partial log line is added on purpose; a 207 already names its failed
// subset in Details for the client.
func TestRenderResult_NonFailedOutcomesStaySilent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name   string
		r      truth.ActionResult
		status int
	}{
		{"success", truth.Success("quiet-sentinel-7lic-ok"), http.StatusOK},
		{"no_change", truth.NoChange("quiet-sentinel-7lic-nochange"), http.StatusOK},
		{"partial", truth.Partial("quiet-sentinel-7lic-partial", truth.KV("rollbackError", "x")), http.StatusMultiStatus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureHandlerLogs(t)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			renderResult(c, tc.r)

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if got, want := strings.TrimSpace(w.Body.String()), renderedBody(t, tc.r); got != want {
				t.Fatalf("body changed:\n got %s\nwant %s", got, want)
			}
			if strings.Contains(buf.String(), tc.r.Reason) {
				t.Fatalf("a %d emitted a log line; only >= 500 should. captured = %q", tc.status, buf.String())
			}
		})
	}
}

// TestRenderResult_NilErrLogsReason pins trap 1: stack_crud.go's
// path-outside-root refusal and compose.go's rollback verification pass a
// nil Err with a Reason. The log must never be gated on Err != nil; the
// Reason is the message and the cause attr is simply absent.
func TestRenderResult_NilErrLogsReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	r := truth.Failed("nil-err-sentinel-7lic: refusing to delete", nil, truth.KV("id", "stack-1"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	renderResult(c, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	line := logLineContaining(t, buf, "nil-err-sentinel-7lic")
	if !strings.Contains(line, "level=ERROR") || !strings.Contains(line, "status=500") {
		t.Fatalf("Reason logged but not as a 500 ERROR line: %s", line)
	}
	if strings.Contains(line, "cause=") {
		t.Fatalf("a nil Err must not produce a cause attr (would print <nil> and mislead): %s", line)
	}
}

// TestRenderDockerResult_FailedLiteralLogsCause pins the subset trap: 7 of
// the failed-capable responses in handlers are truth.ActionResult{...}
// literals built from a service result (stack_lifecycle.go, stack_crud.go),
// never constructed by truth.Failed. Logging keyed on the constructor would
// miss them; keyed on the rendered status it catches them.
func TestRenderDockerResult_FailedLiteralLogsCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := errors.New("literal-sentinel-7lic-b2")
	r := truth.ActionResult{
		Outcome: truth.OutcomeFailed,
		Reason:  "compose up did not verify as running",
		Details: map[string]any{"status": "error"},
		Err:     cause,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	renderDockerResult(c, cause, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if got, want := strings.TrimSpace(w.Body.String()), renderedBody(t, r); got != want {
		t.Fatalf("body changed:\n got %s\nwant %s", got, want)
	}
	line := logLineContaining(t, buf, "literal-sentinel-7lic-b2")
	if !strings.Contains(line, "status=500") || !strings.Contains(line, "code=ACTION_FAILED") {
		t.Fatalf("literal Failed result logged without status/code: %s", line)
	}
}

// TestRenderDockerResult_DockerUnavailableLogs503 covers renderDockerResult's
// own truth.Failed site (the 30th): the 503 substitution. The body must stay
// exactly the DockerUnavailableMessage result, and the log must carry the
// original error chain that the body withholds.
func TestRenderDockerResult_DockerUnavailableLogs503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := fmt.Errorf("stack start: %w", services.ErrDockerUnavailable)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	renderDockerResult(c, cause, truth.Failed("ignored-when-docker-is-down", cause))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got, want := strings.TrimSpace(w.Body.String()), renderedBody(t, truth.Failed(DockerUnavailableMessage, cause)); got != want {
		t.Fatalf("503 body changed:\n got %s\nwant %s", got, want)
	}
	line := logLineContaining(t, buf, "stack start")
	if !strings.Contains(line, "status=503") {
		t.Fatalf("docker-unavailable ActionResult logged without status=503: %s", line)
	}
}

// actionLogFixture drives the real Delete handler end to end (real DB,
// scanner, linter, opLock) with only the Docker service faked, so a test can
// reach a truth.Failed site or an ActionResult literal on the actual handler
// path, under the real RequestID middleware.
type actionLogFixture struct {
	tempDir string
	db      *database.DB
	router  *gin.Engine
}

func newActionLogFixture(t *testing.T, docker *fakeStackDocker) *actionLogFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	cfg := &config.Config{StacksDir: tempDir}
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(docker, scanner, services.NewLinterService(), db, cfg,
		services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.Use(middleware.RequestID())
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)

	return &actionLogFixture{tempDir: tempDir, db: db, router: router}
}

// createStack posts a real Create and returns the registered stack row.
func (f *actionLogFixture) createStack(t *testing.T, name string) models.Stack {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":           name,
		"composeContent": deleteSiblingCompose,
		"deploy":         false,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "setup: Create %q must succeed, body=%s", name, w.Body.String())

	stacks, err := f.db.ListStacksByDirectory(filepath.Join(f.tempDir, name))
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	return stacks[0]
}

// A fixed, well-formed UUID: RequestID() honours an inbound X-Request-ID only
// when it parses, so the test can assert the exact value on the log line.
const actionLogRequestID = "3f2b9c1e-7a4d-4e8b-9c6a-1d2e3f4a5b6c"

func (f *actionLogFixture) deleteStack(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?confirm=true", nil)
	req.Header.Set(middleware.RequestIDHeader, actionLogRequestID)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// TestStacksHandler_Delete_PathOutsideRoot_LogsReasonWithRequestID drives
// stack_crud.go's path-traversal refusal (truth.Failed with a nil Err) through
// the real endpoint. The stack row's Directory is rewritten to a directory
// outside StacksDir, which is exactly the "malformed stack record" the guard
// exists for. Docker is never reached.
func TestStacksHandler_Delete_PathOutsideRoot_LogsReasonWithRequestID(t *testing.T) {
	f := newActionLogFixture(t, &fakeStackDocker{})
	stack := f.createStack(t, "outside-root")

	// A sibling of StacksDir, never inside it. stacks.directory is a foreign
	// key onto directories.path, so the outside directory is registered
	// first; that is also what a real malformed record would look like.
	outside := t.TempDir()
	require.NoError(t, f.db.UpsertDirectory(models.Directory{Path: outside, Name: "outside", ScannedAt: testTime}))
	stack.Directory = outside
	require.NoError(t, f.db.UpsertStack(stack))

	buf := captureHandlerLogs(t)
	w := f.deleteStack(t, stack.ID)

	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	require.Contains(t, w.Body.String(), "refusing to delete", "the Reason must still reach the client")

	line := logLineContaining(t, buf, "refusing to delete")
	for _, want := range []string{"level=ERROR", "request_id=" + actionLogRequestID, "status=500", "code=ACTION_FAILED"} {
		if !strings.Contains(line, want) {
			t.Errorf("ERROR line missing %q: %s", want, line)
		}
	}
}

// TestStacksHandler_Delete_DockerFailed_LogsCauseWithRequestID drives the
// stack_crud.go DeleteVerified literal (truth.ActionResult{Outcome:
// deleteAR.Outcome, Err: deleteAR.Err}) through renderDockerResult on the real
// endpoint. The sentinel is only in Err, which is json:"-", so before the fix
// it reached nothing at all.
func TestStacksHandler_Delete_DockerFailed_LogsCauseWithRequestID(t *testing.T) {
	cause := errors.New("endpoint-sentinel-7lic-c3")
	f := newActionLogFixture(t, &fakeStackDocker{
		deleteAR: truth.Failed("compose down failed", cause),
	})
	stack := f.createStack(t, "docker-fails")

	buf := captureHandlerLogs(t)
	w := f.deleteStack(t, stack.ID)

	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	require.Contains(t, w.Body.String(), "compose down did not verify as removed")
	require.NotContains(t, w.Body.String(), "endpoint-sentinel-7lic-c3", "Err is json:\"-\" and must stay out of the body")

	line := logLineContaining(t, buf, "endpoint-sentinel-7lic-c3")
	for _, want := range []string{"level=ERROR", "request_id=" + actionLogRequestID, "status=500", "code=ACTION_FAILED", "compose down failed"} {
		if !strings.Contains(line, want) {
			t.Errorf("ERROR line missing %q: %s", want, line)
		}
	}
}

// TestStacksHandler_Delete_Success_StaysSilent is the endpoint-level control
// on the same instrument: a Delete that succeeds renders 200 and must not
// emit an ERROR line.
func TestStacksHandler_Delete_Success_StaysSilent(t *testing.T) {
	f := newActionLogFixture(t, &fakeStackDocker{})
	stack := f.createStack(t, "happy")

	buf := captureHandlerLogs(t)
	w := f.deleteStack(t, stack.ID)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	if strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("a successful delete emitted an ERROR line: %q", buf.String())
	}
}
