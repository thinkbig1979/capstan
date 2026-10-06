package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// detachStreamer stands in for a compose process that outlives the browser
// tab. Each RunStreaming call emits one line, then parks until the test
// releases it; if the context it was handed ends first, that is the process
// being killed, and it is recorded.
type detachStreamer struct {
	release chan struct{}

	mu       sync.Mutex
	calls    []string
	killed   []string
	finished []string
	started  chan string
}

func newDetachStreamer() *detachStreamer {
	return &detachStreamer{release: make(chan struct{}), started: make(chan string, 4)}
}

func (f *detachStreamer) RunStreaming(ctx context.Context, _ models.Stack, subcommand string, _ []string) <-chan services.StreamLine {
	f.mu.Lock()
	f.calls = append(f.calls, subcommand)
	f.mu.Unlock()
	// Unbuffered on purpose: every send waits for the handler to read, so a
	// handler that stops draining after a disconnect would park this
	// "process" forever instead of letting it finish.
	out := make(chan services.StreamLine)
	go func() {
		defer close(out)
		out <- services.StreamLine{Type: "data", Line: subcommand + " running"}
		f.started <- subcommand
		select {
		case <-f.release:
		case <-ctx.Done():
			f.mu.Lock()
			f.killed = append(f.killed, subcommand)
			f.mu.Unlock()
			return
		}
		out <- services.StreamLine{Type: "data", Line: subcommand + " more output"}
		out <- services.StreamLine{Type: "done", Success: true}
		f.mu.Lock()
		f.finished = append(f.finished, subcommand)
		f.mu.Unlock()
	}()
	return out
}

func (f *detachStreamer) snapshot() (calls, killed, finished []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]string(nil), f.killed...), append([]string(nil), f.finished...)
}

// TestOperations_ClientDisconnectDoesNotKillTheProcess is agent-os-a1ye.3's
// acceptance (b). The client goes away mid-run (the WS reader errors). The
// process must keep running, the lock must stay held while it does (another
// operation on the stack would race it), and must be free once it finishes.
// For restart, the up phase must still run after a disconnect during down,
// or the stack is left stopped.
func TestOperations_ClientDisconnectDoesNotKillTheProcess(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   []string // RunStreaming subcommands that must run to completion
	}{
		{"start", []string{"up"}},
		{"restart", []string{"down", "up"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			streamer := newDetachStreamer()
			lock := services.NewOperationLock()

			db, err := database.NewWithMigrations(":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { db.Close() })
			createTestDirectory(t, db, "/test/dir")
			require.NoError(t, db.UpsertStack(models.Stack{
				ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
				ProjectName: "proj-a", Status: "running",
			}))
			router := gin.New()
			NewOperationsHandler(streamer, db, lock, NewConnectionManager(5)).
				RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)
			srv := httptest.NewServer(router)
			t.Cleanup(srv.Close)

			url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/operations/stack-a/" + tc.action
			conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
			require.NoError(t, err)
			resp.Body.Close()

			// Wait until the first phase is running, then drop the client the
			// way a closed tab does: no close frame, the socket just goes.
			select {
			case <-streamer.started:
			case <-time.After(time.Until(hangGuardDeadline(t))):
				t.Fatal("the operation never started")
			}
			require.NoError(t, conn.UnderlyingConn().Close())

			// Give the handler's reader time to see the dead socket. On the
			// pre-fix handler this is when the process context is cancelled,
			// so wait until either that happens or the window passes. Nothing
			// on the fixed handler signals "the reader has seen it", so this
			// is a window, and it can only err towards a false GREEN on a
			// loaded runner (the kill not yet observed), never a false red.
			window := time.Now().Add(2 * time.Second) // wall-clock ok: the window is how long a kill is given to show, and a slow runner can only shorten what it catches, never fail correct code
			for time.Now().Before(window) {
				if _, killed, _ := streamer.snapshot(); len(killed) > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if _, killed, _ := streamer.snapshot(); len(killed) > 0 {
				t.Fatalf("closing the browser tab killed the process: %v", killed)
			}

			// Still running, so the stack is still locked.
			if _, err := lock.Acquire("stack-a"); err == nil {
				lock.Release("stack-a")
				t.Fatal("the stack lock was released while the operation was still running")
			}

			close(streamer.release)

			guard := hangGuardDeadline(t)
			for {
				if _, err := lock.Acquire("stack-a"); err == nil {
					lock.Release("stack-a")
					break
				}
				if time.Now().After(guard) {
					t.Fatal("the stack lock was never released after the operation finished")
				}
				time.Sleep(20 * time.Millisecond)
			}

			calls, killed, finished := streamer.snapshot()
			require.Empty(t, killed, "process killed after the disconnect")
			require.Equal(t, tc.want, calls, "phases started")
			require.Equal(t, tc.want, finished, "phases run to completion")
		})
	}
}
