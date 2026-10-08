package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/execx"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestLogsHandler_GetLogs_LogsCauseOn500 is the seen-failing-first test for
// agent-os-7lg1's db.GetStack collapse at logs.go's GetLogs: before the
// split, `if err != nil || stack == nil` mapped ANY error — including a
// faulted database, not just a missing row — to the same silent 404, so a
// database outage never logged and never even reported the correct status
// code. faultyDB (faulty_db_test.go) forces GetStack to fail with "sql:
// database is closed", never errdefs.ErrNotFound.
//
// Two-sided per the brief: TestLogsHandler_GetLogs_NotFoundStaysSilent below
// is the control proving the ordinary 404 path is unchanged and still silent.
func TestLogsHandler_GetLogs_LogsCauseOn500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db := faultyDB(t)
	handler := NewLogsHandler(nil, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(10))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	req := httptest.NewRequest(http.MethodGet, "/api/stacks/any-id/logs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var appErr models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &appErr))
	require.Equal(t, "INTERNAL_ERROR", appErr.Code)

	if !strings.Contains(buf.String(), "database is closed") {
		t.Fatalf("500 emitted with no log of the underlying cause. captured = %q", buf.String())
	}
}

// TestLogsHandler_GetLogs_NotFoundStaysSilent is the control for the test
// above: a genuinely missing stack must still be a silent 404 (client fault,
// not logged at ERROR), so the split doesn't turn ordinary not-found traffic
// into log noise.
//
// The silence half is discriminated on this request's own id (agent-os-nho7):
// RequestID() puts the sentinel on the context and logServerFault stamps it on
// any line it writes. See requireNoOwnErrorLines (ua4y_7lg1_cause_test.go).
func TestLogsHandler_GetLogs_NotFoundStaysSilent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewLogsHandler(nil, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(10))
	router := gin.New()
	router.Use(middleware.RequestID())
	handler.RegisterRoutes(router.Group("/api"))

	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()

	req := httptest.NewRequest(http.MethodGet, "/api/stacks/no-such-stack/logs", nil)
	req.Header.Set(middleware.RequestIDHeader, reqID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	<-plantDone

	require.Equal(t, http.StatusNotFound, w.Code)
	// Not assert.Empty: db construction itself logs an INFO "database schema
	// version" line (process-wide slog default). The claim is narrower — no
	// ERROR line for an ordinary client-fault 404, and specifically not one
	// for THIS request — so check for that.
	requireNoOwnErrorLines(t, buf.String(), sentinel, "an ordinary not-found")
}

// TestLogsHandler_StreamLogs_LogsCauseOn500 is StreamLogs' counterpart to
// TestLogsHandler_GetLogs_LogsCauseOn500 above — same db.GetStack collapse,
// reached before serveWS (plain HTTP JSON, no upgrade attempted), so a plain
// httptest GET without upgrade headers observes the response directly.
func TestLogsHandler_StreamLogs_LogsCauseOn500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))
	db := faultyDB(t)
	handler := NewLogsHandler(docker, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(10))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	req := httptest.NewRequest(http.MethodGet, "/api/ws/logs/any-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var appErr models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &appErr))
	require.Equal(t, "INTERNAL_ERROR", appErr.Code)

	if !strings.Contains(buf.String(), "database is closed") {
		t.Fatalf("500 emitted with no log of the underlying cause. captured = %q", buf.String())
	}
}

// TestLogsHandler_StreamLogs_DockerUnavailableLogsCause is the seen-failing-
// first test for logs.go:104: before routing through handleError, a 503
// DOCKER_UNAVAILABLE response was written via writeJSONError's plain c.JSON,
// bypassing handleError's logServerFault entirely — a 5xx that never logged
// (same class as agent-os-7z8c/agent-os-7lg1: a server fault silently
// discarded, just via a bypassed logger instead of a discarded err).
func TestLogsHandler_StreamLogs_DockerUnavailableLogsCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewLogsHandler(nil, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(10))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	req := httptest.NewRequest(http.MethodGet, "/api/ws/logs/any-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var appErr models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &appErr))
	require.Equal(t, "DOCKER_UNAVAILABLE", appErr.Code)

	if !strings.Contains(buf.String(), "Docker daemon unreachable") {
		t.Fatalf("503 emitted with no log line. captured = %q", buf.String())
	}
}

// TestBuildLogsCmd_DoesNotLeakCapstanSecrets covers StreamLogs' `docker
// compose logs` child (buildLogsCmd, extracted from StreamLogs so this test
// can inspect the command it builds without a real docker binary). Before
// the fix, cmd.Env was left nil, so the child inherited Capstan's full
// os.Environ() — including JWT_SECRET — which a user-authored compose.yaml
// (handlers/compose.go, handlers/stack_crud.go) can interpolate via ${VAR}
// (agent-os-3ux, the sibling of agent-os-iey's fix in package services).
//
// cmd.Env is nil until Start()/Run() populates it from os.Environ() at that
// time (Go's os/exec defers the lookup), so asserting on the field directly
// without running anything would not distinguish "nil Env" from "Env
// deliberately left empty" — it would only catch that Env was never
// assigned, not that the assignment scrubs secrets. Redirecting the
// constructed *exec.Cmd at `sh -c env` and running it observes the actual
// environment the real docker child would receive.
func TestBuildLogsCmd_DoesNotLeakCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-logs-handler")
	t.Setenv("STORAGE_KEY", "sentinel-value-storage-handler")
	t.Setenv("GIT_HTTPS_TOKEN", "sentinel-value-git-handler")

	h := &LogsHandler{}
	stack := models.Stack{Directory: t.TempDir(), ComposeFile: "compose.yaml", ProjectName: "p"}

	cmd := h.buildLogsCmd(context.Background(), stack)

	shPath, err := exec.LookPath("sh")
	require.NoError(t, err)
	cmd.Path = shPath
	cmd.Args = []string{"sh", "-c", "env"}

	out, err := cmd.Output()
	require.NoError(t, err)

	output := string(out)
	assert.NotContains(t, output, "sentinel-value-logs-handler")
	assert.NotContains(t, output, "sentinel-value-storage-handler")
	assert.NotContains(t, output, "sentinel-value-git-handler")
	assert.Contains(t, output, "PATH=")
}

// TestBuildLogsCmd_IsBuiltByExecx: StreamLogs' child comes from execx.Command
// like every other non-interactive child, so it carries execx.WaitDelay
// (agent-os-qags.29). Before the move it was a raw exec.CommandContext with
// WaitDelay 0.
func TestBuildLogsCmd_IsBuiltByExecx(t *testing.T) {
	h := &LogsHandler{}
	stack := models.Stack{Directory: t.TempDir(), ComposeFile: "compose.yaml", ProjectName: "p"}

	cmd := h.buildLogsCmd(context.Background(), stack)

	assert.Equal(t, execx.WaitDelay, cmd.WaitDelay)
}

// TestStreamLogsTeardown_GrandchildHoldingThePipeDoesNotHoldTheFollow is a PIN,
// green both sides by construction; it guards against a future change to the
// teardown shape. It replays StreamLogs' teardown (StdoutPipe read by a
// scanner goroutine, then cancel, Kill, Wait) on buildLogsCmd's child, whose
// grandchild keeps stdout open the way the compose plugin can. With StdoutPipe
// there is no exec copy goroutine, so Wait returns at the direct child's exit
// and closes the read end, which ends the scanner. WaitDelay plays no part:
// probed with WaitDelay 0 and 5s, both returned in 0s (agent-os-qags.29). A
// change to io.Pipe or cmd.Stdout = <writer> would make Wait wait on the
// grandchild, and this test then needs WaitDelay to stay under its bound.
func TestStreamLogsTeardown_GrandchildHoldingThePipeDoesNotHoldTheFollow(t *testing.T) {
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	h := &LogsHandler{}
	stack := models.Stack{Directory: t.TempDir(), ComposeFile: "compose.yaml", ProjectName: "p"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := h.buildLogsCmd(ctx, stack)
	cmd.Path = sh
	cmd.Args = []string{"sh", "-c", "echo line; sleep 20"}

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cancel()
		_ = cmd.Process.Kill() //nolint:errcheck // as in StreamLogs: the process may already be gone
		_ = cmd.Wait()         //nolint:errcheck // as in StreamLogs: the kill's non-zero exit
		<-scanned
	}()
	const bound = 2 * time.Second
	select {
	case <-done:
	case <-time.After(bound): // wall-clock ok: the teardown returns at once; the sleep grandchild runs 20s
		t.Fatalf("StreamLogs' teardown still waiting %s after cancel: the grandchild holding stdout holds the follow", bound)
	}
}

func TestParseLogLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *LogLine
		wantNil bool
	}{
		{
			name:    "empty line",
			input:   "",
			wantNil: true,
		},
		{
			name:    "line without pipe",
			input:   "just a message",
			wantNil: true,
		},
		{
			name:  "simple log line",
			input: "web-1  | GET / 200",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "",
				Message:   "GET / 200",
			},
		},
		{
			name:  "log line with timestamp",
			input: "db-1  | 2026-02-13T10:00:00Z LOG: checkpoint complete",
			want: &LogLine{
				Container: "db-1",
				Timestamp: "2026-02-13T10:00:00Z",
				Message:   "LOG: checkpoint complete",
			},
		},
		{
			name:  "log line with RFC3339 timestamp",
			input: "web-1  | 2026-02-13T10:00:00.123Z [INFO] Starting server",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "2026-02-13T10:00:00.123Z",
				Message:   "[INFO] Starting server",
			},
		},
		{
			name:  "log line with datetime timestamp",
			input: "web-1  | 2026-02-13 10:00:00 Hello world",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "2026-02-13 10:00:00",
				Message:   "Hello world",
			},
		},
		{
			name:  "log line with just container and pipe",
			input: "web-1  |",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "",
				Message:   "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLogLine(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("parseLogLine() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Errorf("parseLogLine() = nil, want %v", tt.want)
				return
			}
			if got.Container != tt.want.Container {
				t.Errorf("parseLogLine().Container = %v, want %v", got.Container, tt.want.Container)
			}
			if got.Timestamp != tt.want.Timestamp {
				t.Errorf("parseLogLine().Timestamp = %v, want %v", got.Timestamp, tt.want.Timestamp)
			}
			if got.Message != tt.want.Message {
				t.Errorf("parseLogLine().Message = %v, want %v", got.Message, tt.want.Message)
			}
		})
	}
}

func TestParseLogLines(t *testing.T) {
	input := `web-1  | 2026-02-13T10:00:00Z [INFO] Starting
db-1  | 2026-02-13T10:00:01Z LOG: ready
web-1  | GET / 200
`

	tests := []struct {
		name            string
		input           string
		containerFilter string
		wantCount       int
	}{
		{
			name:            "no filter",
			input:           input,
			containerFilter: "",
			wantCount:       3,
		},
		{
			name:            "filter by container",
			input:           input,
			containerFilter: "web-1",
			wantCount:       2,
		},
		{
			name:            "filter by different container",
			input:           input,
			containerFilter: "db-1",
			wantCount:       1,
		},
		{
			name:            "filter by non-existent container",
			input:           input,
			containerFilter: "redis-1",
			wantCount:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLogLines(tt.input, tt.containerFilter)
			if len(got) != tt.wantCount {
				t.Errorf("parseLogLines() returned %d lines, want %d", len(got), tt.wantCount)
			}
		})
	}
}
