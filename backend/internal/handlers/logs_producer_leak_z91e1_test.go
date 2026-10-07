package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// parkedLogProducers returns the stacks of goroutines parked in a channel send
// inside one of StreamLogs' closures. The only closure there that sends on a
// channel is the scanner goroutine feeding logChan, so this keys on the
// producer itself rather than on any goroutine StreamLogs started.
func parkedLogProducers() []string {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2) //nolint:errcheck // Writing into a bytes.Buffer cannot fail.
	var parked []string
	for _, g := range strings.Split(buf.String(), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		if strings.Contains(header, "[chan send") && strings.Contains(g, "(*LogsHandler).StreamLogs.func") {
			parked = append(parked, g)
		}
	}
	return parked
}

// TestStreamLogs_ClosedClientDoesNotStrandProducer is agent-os-z91e.1. The
// fake `docker` writes far more than logChan's 100-line buffer and then stays
// up, so when the client leaves the scanner goroutine is mid-stream with a
// full channel. Before the fix its bare send parked forever once the read loop
// stopped draining; it must exit with the handler.
func TestStreamLogs_ClosedClientDoesNotStrandProducer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bin := t.TempDir()
	script := "#!/bin/sh\nyes 'web-1  | 2026-01-01T00:00:00Z hello' | head -n 50000\nexec sleep 60\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)) //nolint:gosec // test fixture must be executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	stackDir := t.TempDir()
	createTestDirectory(t, db, stackDir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: stackDir, ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))

	// Closed when StreamLogs returns, so the check below runs against a
	// handler that has finished its cleanup, not one still tearing down.
	handlerDone := make(chan struct{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		close(handlerDone)
	})
	NewLogsHandler(docker, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(5)).
		RegisterRoutes(router.Group("/api"))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/logs/stack-a"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	resp.Body.Close()

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err, "no log line arrived")
	// A closed tab: no close frame, the socket just goes.
	require.NoError(t, conn.UnderlyingConn().Close())

	select {
	case <-handlerDone:
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("StreamLogs never returned after the client left")
	}

	// The fixed producer exits as soon as cleanup cancels its context; the
	// broken one never does, so this waits for zero rather than sleeping.
	deadline := time.Now().Add(5 * time.Second) // wall-clock ok: a slow runner can only delay the exit it waits for, and 5s is orders above it
	parked := parkedLogProducers()
	for len(parked) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		parked = parkedLogProducers()
	}
	if len(parked) > 0 {
		t.Fatalf("the log producer is still parked on a send to logChan after StreamLogs returned:\n%s", parked[0])
	}
}
