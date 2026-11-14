package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-hnys: a lifecycle action run over the operations WebSocket stores
// the stack's verified status, as the REST lifecycle routes do, on every exit
// path. The fixture seeds stack-a as "running".

var (
	doneStoppedHnys = services.StreamLine{Type: "done", Success: true, Outcome: truth.OutcomeSuccess, Status: "stopped"}
	doneRunningHnys = services.StreamLine{Type: "done", Success: true, Outcome: truth.OutcomeSuccess, Status: "running"}
	// A failure with no verified status, like "compose down failed".
	doneFailedHnys = services.StreamLine{Type: "done", Success: false, Outcome: truth.OutcomeFailed, Reason: "compose exited 1"}
)

func storedStatusHnys(t *testing.T, db *database.DB) string {
	t.Helper()
	stack, err := db.GetStack("stack-a")
	require.NoError(t, err)
	return stack.Status
}

func TestOperationsPersistStatus_Hnys(t *testing.T) {
	cases := []struct {
		name   string
		action string
		done   map[string]services.StreamLine
		want   string
	}{
		{"stop succeeds", "stop", map[string]services.StreamLine{"down": doneStoppedHnys}, "stopped"},
		{"start fails", "start", map[string]services.StreamLine{"up": doneFailedHnys}, "error"},
		// The early return when the stop phase fails is its own exit path.
		{"restart fails at stop", "restart", map[string]services.StreamLine{"down": doneFailedHnys, "up": doneRunningHnys}, "error"},
		// CONTROL (passes on base too): REST pull without restart writes no
		// status, so neither does the socket.
		{"pull writes nothing", "pull", map[string]services.StreamLine{"pull": {Type: "done", Success: true, Outcome: truth.OutcomeSuccess}}, "running"},
		// CONTROL (passes on base too): no done frame means the outcome is
		// unknown, so the stored status is left alone. The scripted streamer
		// sends a zero StreamLine for a missing key, which is not a done frame.
		{"no done frame writes nothing", "stop", map[string]services.StreamLine{}, "running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newOperationsFixtureWith(t, NewConnectionManager(5), &scriptedStreamer{done: tc.done})

			dialOperations(t, srv, "stack-a", tc.action)

			require.Equal(t, tc.want, storedStatusHnys(t, db))
		})
	}
}

// parkedStreamerHnys parks a run after its first line until released, then
// finishes with done. It is what lets the client leave mid-run.
type parkedStreamerHnys struct {
	started chan struct{}
	release chan struct{}
	done    services.StreamLine
}

func (p *parkedStreamerHnys) RunStreaming(_ context.Context, _ models.Stack, subcommand string, _ []string) <-chan services.StreamLine {
	out := make(chan services.StreamLine)
	go func() {
		defer close(out)
		out <- services.StreamLine{Type: "data", Line: subcommand + " running"}
		close(p.started)
		<-p.release
		out <- p.done
	}()
	return out
}

// Rule 15: the operation outlives the connection, so a stop whose client left
// mid-run still stores its result.
func TestOperationsPersistStatusAfterClientLeaves_Hnys(t *testing.T) {
	streamer := &parkedStreamerHnys{started: make(chan struct{}), release: make(chan struct{}), done: doneStoppedHnys}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))
	lock := services.NewOperationLock()
	router := gin.New()
	NewOperationsHandler(streamer, db, lock, NewConnectionManager(5)).
		RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/operations/stack-a/stop"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	resp.Body.Close()

	select {
	case <-streamer.started:
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("the operation never started")
	}
	// The tab closes: no close frame, the socket just goes.
	require.NoError(t, conn.UnderlyingConn().Close())
	close(streamer.release)

	// The handler stores the status before it releases the stack lock, so a
	// free lock means the run has fully finished.
	deadline := hangGuardDeadline(t)
	for {
		if token, err := lock.Acquire("stack-a", services.OpKindBackup); err == nil {
			lock.Release("stack-a", token)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the operation never released the stack lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	require.Equal(t, "stopped", storedStatusHnys(t, db))
}
