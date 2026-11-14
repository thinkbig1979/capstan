package handlers

import (
	"context"
	"net/http"
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
		// Seeded "stopped" below, so a run that writes nothing cannot pass.
		{"restart succeeds", "restart", map[string]services.StreamLine{"down": doneStoppedHnys, "up": doneRunningHnys}, "running"},
		{"start fails", "start", map[string]services.StreamLine{"up": doneFailedHnys}, "error"},
		// The early return when the stop phase fails is its own exit path.
		{"restart fails at stop", "restart", map[string]services.StreamLine{"down": doneFailedHnys, "up": doneRunningHnys}, "error"},
		// Review finding A: down ran, then the up phase's own lookup refused.
		// The stack is stopped, so this is a failure to store, as REST's
		// RestartVerified stores "error"; Refused only means "nothing ran".
		{"restart refused at up after down ran", "restart", map[string]services.StreamLine{"down": doneStoppedHnys, "up": {Type: "done", Success: false, Outcome: truth.OutcomeFailed, Reason: "refused", Refused: true}}, "error"},
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
			if tc.action == "restart" && tc.want == "running" {
				require.NoError(t, db.UpdateStackStatus("stack-a", "stopped"))
			}

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

// Review R1: a run RunStreaming refuses before compose starts (here: a compose
// project name two stacks share) stores nothing, as REST's
// refuseSharedProjectName stores nothing. Real DockerService, as main.go wires it.
func TestOperationsRefusedRunStoresNothing_Hnys(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(dockerAPI.Close)
			svc := newTestDockerServiceAgainst(t, dockerAPI)
			db, err := database.NewWithMigrations(":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { db.Close() })
			seedSharedProjectStacks(t, db) // both seeded "running"
			svc.SetStackLookup(db)
			router := gin.New()
			NewOperationsHandler(svc, db, services.NewOperationLock(), NewConnectionManager(5)).
				RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)
			srv := httptest.NewServer(router)
			t.Cleanup(srv.Close)

			frames := readOperationFrames(t, srv, "s-alpha", action)

			// Control: the run really was refused, with the reason on the wire.
			done := framesOfType(frames, "done")
			require.Len(t, done, 1, "frames: %v", frames)
			require.Equal(t, "failed", done[0]["outcome"])
			require.Contains(t, done[0]["reason"], "/srv/stacks/beta")
			require.NotContains(t, done[0], "refused", "server-only field leaked: %v", done[0])

			stack, err := db.GetStack("s-alpha")
			require.NoError(t, err)
			require.Equal(t, "running", stack.Status, "a refused run must not store a status")
		})
	}
}
