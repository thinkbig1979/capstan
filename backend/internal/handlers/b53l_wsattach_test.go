package handlers

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newBackupWSFixtureWithLogger is newBackupWSFixture (backup_ws_cap_test.go)
// with an injectable *slog.Logger, so a test can capture exactly this
// handler's own h.logger.Info/Debug/Error calls — sendDoneFrame's completion
// log, specifically — without depending on the global slog default. h.logger
// is bound to the *slog.Logger object passed at construction; a later
// slog.SetDefault (what captureHandlerLogs does) never reaches it, since the
// existing fixture hardcodes slog.Default() at construction time, before any
// swap. AUTH_DISABLED (true) throughout, same as newBackupWSFixture's
// authDisabled=true callers.
//
// CLEANUP ORDERING, load-bearing: register `t.Cleanup(func(){ close(release)
// })` AFTER calling this, never before — h.Stop's cleanup (registered here)
// blocks with no bound until every in-flight exec goroutine finishes
// (BackupHandler.Stop's own doc comment), and a still-blocked run only
// finishes once release is closed. t.Cleanup runs LIFO, so a release-close
// registered before this call would run AFTER h.Stop and hang the whole test
// binary until the runtime's own package timeout. This is exactly the
// ordering backup_ws_cap_test.go's TestBackupWSAttach_RevokesConnectionEvenAtCap
// already documents for newBackupWSFixture; the same rule applies here.
func newBackupWSFixtureWithLogger(t *testing.T, cm *ConnectionManager, release chan struct{}, logger *slog.Logger) *httptest.Server {
	t.Helper()

	db := newBackupHandlerDB(t)
	svc := buildBlockingBackupSvc(t, db, release)
	h := NewBackupHandler(svc, db, logger)
	t.Cleanup(h.Stop)
	h.SetConnectionManager(cm)

	router := newBackupRouter(h)
	wsGroup := router.Group("/api")
	h.RegisterWSRoutes(wsGroup, "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// waitForCMCount blocks until cm.Count() == want. Same hang-guard discipline
// as waitForServerSideClose (monitoring_metrics_close_test.go): a bound that
// never fires in a correct run, not a latency budget.
func waitForCMCount(t *testing.T, cm *ConnectionManager, want int, what string) {
	t.Helper()
	guard := hangGuardDeadline(t)
	for {
		if cm.Count() == want {
			return
		}
		if !time.Now().Before(guard) {
			t.Fatalf("%s: cm.Count() = %d, never reached %d", what, cm.Count(), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestBackupWSAttach_ClientDisconnectNeverSendsPrematureDoneFrame pins
// agent-os-b53l: forwardLive's `defer close(live)` (services/backup_runner.go)
// fires on BOTH of its exits — the run finishing AND the client going away —
// so wsAttach's select over `<-wsCtx.Done()` and `line, ok := <-attached.Live`
// used to see both channels ready at the same instant on a client disconnect
// and pick between them uniformly at random. Landing on the Live branch
// treated a still-running backup as complete and emitted a "done" frame with
// an empty outcome, roughly half the time (agent-os-b53l's own measurement).
//
// The random select means one run proves nothing either way (COMMON BLOCK
// clause 5's "check that could only have come out one way" trap). A
// SEQUENTIAL repeat (dial, wait for registration, disconnect, wait for
// deregistration, repeat) turned out not to be enough: measured at 40
// sequential iterations against BOTH pre-fix and post-fix backup.go, the
// ambiguous ordering never manifested even once on this box — the read pump
// closing wsCtx.Done() and forwardLive's goroutine closing Live never
// actually raced when there was only ever one disconnect in flight at a
// time, most likely because the runtime resumes the already-parked wsAttach
// goroutine (blocked on select since before the disconnect) before
// forwardLive's goroutine gets scheduled to react to the same closed
// channel. So this drives CONCURRENCY concurrent attach/disconnect cycles
// against the SAME still-running run simultaneously (goroutines, not a
// sequential loop) — deliberately creating enough simultaneous scheduling
// pressure across this box's 8 cores that at least one of them lands in the
// ambiguous window, rather than relying on one disconnect's timing alone.
//
// The observable is a LOG LINE rather than a WS frame because the client's
// connection is gone by the time the server reacts — there is nothing left to
// read a frame from; h.logger is injected directly (see
// newBackupWSFixtureWithLogger) so this does not depend on package-level slog
// state at all, and slog's built-in handlers already serialise concurrent
// Handle calls internally (log/slog/handler.go's commonHandler holds its own
// mutex around every write), so concurrent goroutines logging through the
// same *slog.Logger need no extra locking here. Reading buf.String() only
// after waitForCMCount(0) is what makes the final read race-free: release()
// is the last statement wsAttach's own defer stack runs, so every one of its
// synchronous log calls has already happened before its connection leaves cm.
//
// Seen failing first, verified via `go test -overlay` against the
// pre-b53l-fix backup.go (the `!ok` branch with no wsCtx.Err() guard): FAILED
// on 2 of 3 runs, one of them logging the smoking gun this bead describes
// verbatim — `msg="Backup WS operation completed" run_id=<id> outcome=""` —
// twice among the 150 concurrent disconnects. Against the fix, 5/5 runs and a
// -race run all passed with zero occurrences.
func TestBackupWSAttach_ClientDisconnectNeverSendsPrematureDoneFrame(t *testing.T) {
	const concurrency = 150

	cm := NewConnectionManager(concurrency + 10)
	release := make(chan struct{})
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	srv := newBackupWSFixtureWithLogger(t, cm, release, logger)
	// Registered AFTER the fixture so LIFO unblocks h.Stop's cleanup first —
	// see newBackupWSFixtureWithLogger's doc comment.
	t.Cleanup(func() { close(release) })

	// ONE run, still executing for the whole test (release is not closed
	// until t.Cleanup). Every goroutine below attaches its own fresh WS
	// client to this same run — Attach starts an independent forwardLive
	// fan-out per caller, so this exercises the race concurrently without
	// needing more runs (and without fighting BackupService's single global
	// busy flag, which would refuse a second concurrent LaunchBackup call).
	runID := kickOffBackupRun(t, srv)

	errCh := make(chan error, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn := dialBackupWS(t, srv, "/api/ws/backups/run/"+runID, "")
			require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
			var startFrame map[string]interface{}
			if err := conn.ReadJSON(&startFrame); err != nil {
				errCh <- fmt.Errorf("goroutine %d: reading start frame: %w", i, err)
				return
			}
			if startFrame["type"] != "start" {
				errCh <- fmt.Errorf("goroutine %d: first frame type = %v, want \"start\"", i, startFrame["type"])
				return
			}
			// Simulate the client vanishing: close the raw TCP connection
			// with no WebSocket close handshake — the same technique
			// TestMonitoringMetricsWS_ClientDisconnectClosesConnection
			// (monitoring_metrics_close_test.go) uses for the identical
			// purpose. Not calling t.Fatal/require from here on purpose:
			// testing.T does not support FailNow from a non-test goroutine,
			// so failures are reported to errCh and asserted after wg.Wait().
			conn.Close()
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	waitForCMCount(t, cm, 0, fmt.Sprintf("all %d concurrent disconnects", concurrency))

	// sendDoneFrame's own log line: `msg="Backup WS operation completed"
	// run_id=<runID> outcome=...`. Checking for the combined literal (msg
	// immediately followed by this run's run_id, matching the exact attr
	// order in backup.go's h.logger.Info call) rather than a bare
	// "run_id=<runID>" substring: several OTHER log lines on the correct
	// disconnect path also carry run_id for the same run (e.g. "WS client
	// disconnected; op continues"), so a bare run_id check would
	// false-positive on the very state this test wants to see.
	//
	// The marker ALSO pins outcome="" (the run has no outcome yet, so the
	// premature branch reports an empty one): since agent-os-nt0m, Attach
	// admits at most services.MaxAttachersPerRun live attachers per run and
	// refuses the surplus BY RESULT, a Done frame with outcome "failed" and a
	// reason naming the limit. That refusal is a legitimate terminal frame
	// for a surplus viewer and logs `outcome=failed`, so with 150 concurrent
	// attaches most of them are refused and a bare run_id marker turned this
	// test red on main the moment #297 (b53l) and #298 (nt0m) were both
	// merged, each green alone. The empty outcome is what distinguishes the
	// defect this test pins from that refusal. The count stays at 150 on
	// purpose: the race is only reachable under contention (the sequential
	// form never reproduced it; 24 concurrent attaches did not either under
	// a reverted guard, 5/5 green), so do not slim it.
	marker := fmt.Sprintf(`msg="Backup WS operation completed" run_id=%s outcome=""`, runID)
	if strings.Contains(buf.String(), marker) {
		t.Fatalf("a done frame was logged for run %q while it was still executing "+
			"(its restic call stays blocked for the whole test), across %d concurrent attaches — captured: %q",
			runID, concurrency, buf.String())
	}
}

// TestBackupWSAttach_GenuineCompletionStillDeliversRealOutcome is the
// required two-sided control on the SAME instrument (the Live-branch inside
// wsAttach's streaming select): a client that stays connected through a run's
// ACTUAL completion must still receive the real terminal frame. This is what
// stops "just never send a done frame from that branch" from looking like a
// valid fix — it specifically targets wsCtx.Err() == nil at the moment Live
// closes (client never disconnected), which is exactly the case the b53l fix
// must NOT suppress.
//
// The client attaches while the run is still blocked (proving Live has not
// closed yet), then the block is released so the run finishes for real with
// the client attached and reading throughout — unlike the premature-frame
// test above, this never closes the client connection.
func TestBackupWSAttach_GenuineCompletionStillDeliversRealOutcome(t *testing.T) {
	cm := NewConnectionManager(10)
	release := make(chan struct{})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	srv := newBackupWSFixtureWithLogger(t, cm, release, logger)

	runID := kickOffBackupRun(t, srv)
	conn := dialBackupWS(t, srv, "/api/ws/backups/run/"+runID, "")
	defer conn.Close()

	requireConnRegistered(t, cm, func(c *Connection) bool { return true }, "backup WS attach")
	requireBackupStartFrame(t, conn)

	// Let the run finish for real, client still attached and about to read.
	// Explicit close here (not via t.Cleanup) — the run must actually reach
	// dr.done during the test body itself, not just at teardown.
	close(release)

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	var doneFrame map[string]interface{}
	for {
		var frame map[string]interface{}
		require.NoError(t, conn.ReadJSON(&frame), "reading frames until the terminal done frame")
		if frame["type"] == "done" {
			doneFrame = frame
			break
		}
	}

	outcome, _ := doneFrame["outcome"].(string)
	require.NotEmpty(t, outcome,
		"a client attached throughout a run's genuine completion must receive its real outcome, "+
			"not the empty outcome agent-os-b53l describes for a premature frame; frame=%v", doneFrame)

	waitForCMCount(t, cm, 0, "post-completion cleanup")
	marker := fmt.Sprintf(`msg="Backup WS operation completed" run_id=%s outcome=%s`, runID, outcome)
	require.Contains(t, buf.String(), marker,
		"the completion log must match the frame actually sent")
}
