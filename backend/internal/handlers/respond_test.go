package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestHandleError_WrappedAppErrorKeepsStatus pins N9 (agent-os-4pa.3): handleError
// must surface an *AppError's status even when it is wrapped with %w, instead of
// collapsing to a generic 500. Seen failing first against the type-assertion form
// (err.(*models.AppError)), which does not traverse a wrap.
func TestHandleError_WrappedAppErrorKeepsStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	appErr := models.NewAppError(http.StatusConflict, "CONFLICT", "already exists")
	wrapped := fmt.Errorf("create user: %w", appErr)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, wrapped)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d — a wrapped AppError must keep its status", w.Code, http.StatusConflict)
	}
}

// TestHandleError_DirectAppErrorKeepsStatus is the unwrapped control: the behaviour
// that already worked must keep working.
func TestHandleError_DirectAppErrorKeepsStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	appErr := models.NewAppError(http.StatusBadRequest, "BAD_REQUEST", "nope")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, appErr)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestHandleError_PlainErrorFallsBackTo500 pins the fallback for a non-AppError.
func TestHandleError_PlainErrorFallsBackTo500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, errors.New("boom"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// syncLogBuffer is the sink captureHandlerLogs hands to slog. A plain
// bytes.Buffer is NOT enough here: slog serialises its own Write calls
// (log/slog/handler.go's commonHandler holds a mutex around every write),
// but a reader calling String() sits outside that lock, and in this package
// the reader is often a different goroutine from the writer — the WS tests
// read the buffer while other connections' handler goroutines are still
// logging on their own tick loops. That was a real `WARNING: DATA RACE`
// under -race with the package under parallel load (agent-os-2h1r), and it
// is what TestLogCapture_ConcurrentReadIsRaceFree pins deterministically.
// Guarding both sides in the type makes every reader safe by construction,
// whatever goroutine it runs on.
type syncLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncLogBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncLogBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureHandlerLogs swaps the process-wide slog default for a mutex-guarded
// buffer (see syncLogBuffer) and restores it.
//
// The stdlib log package must be restored explicitly. slog.SetDefault ALSO
// does log.SetOutput(handlerWriter{...}) and log.SetFlags(0), and restoring
// with slog.SetDefault(prev) does not undo either — so without the two extra
// saves below, every later stdlib-log write in this test binary lands in a
// dead buffer instead of stderr. That is not theoretical here: this package
// stands up 12 httptest.NewServer instances across 8 files, and an
// http.Server with a nil ErrorLog reports through the stdlib log package.
// Leaking the redirect would silently swallow "superfluous WriteHeader"
// warnings, hijack complaints and panic traces from serving goroutines —
// in exactly the WebSocket tests most likely to need them.
//
// Assertions on the buffer use strings.Contains, never equality or a line
// count. Not because of t.Parallel: all 55 parallel tests in this package are
// in backup_test.go, and Go releases the parallel barrier only after every
// serial test has returned, so they cannot overlap these. The reason is
// narrower — an unrelated goroutine writing one line must not break an
// assertion about a sentinel.
func captureHandlerLogs(t *testing.T) *syncLogBuffer {
	t.Helper()
	var buf syncLogBuffer
	prevSlog := slog.Default()
	prevWriter, prevFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return &buf
}

// TestHandleError_LogCaptureControl is the positive control for the three tests
// below. It proves the capture instrument fires, so that a buffer found empty
// there is evidence of absence rather than evidence the harness never ran.
func TestHandleError_LogCaptureControl(t *testing.T) {
	buf := captureHandlerLogs(t)
	slog.Error("control", "error", errors.New("control-sentinel-4b1c"))
	if !strings.Contains(buf.String(), "control-sentinel-4b1c") {
		t.Fatalf("capture instrument did not fire; every assertion below is meaningless. got %q", buf.String())
	}
}

// TestHandleError_Plain500LogsTheCause pins agent-os-7z8c: a non-AppError
// reaching handleError becomes a generic 500 whose body deliberately withholds
// the cause, so the cause must reach the operator's log instead. Before the fix
// the fallback branch discarded err entirely and prod had no record of WHY.
//
// The sentinel lives ONLY in the wrapped cause, never in the status, code or
// message. That is what makes this a ratchet: a future "sanitised" log line
// that emits status and code but drops the error chain still fails here.
func TestHandleError_Plain500LogsTheCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := errors.New("cause-sentinel-9f2a")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, fmt.Errorf("failed to count commits: %w", cause))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(buf.String(), "cause-sentinel-9f2a") {
		t.Fatalf("500 emitted with no log of the underlying cause. captured = %q", buf.String())
	}
	if strings.Contains(w.Body.String(), "cause-sentinel-9f2a") {
		t.Fatalf("the cause leaked into the response body: %s", w.Body.String())
	}
}

// TestHandleError_AppError5xxLogsTheCause is the typed arm. models.AppError has
// no wrapped-cause field and its Error() returns only Message, so a 5xx built
// from one is exactly the case where the log is the only place the truth can go.
func TestHandleError_AppError5xxLogsTheCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	appErr := models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, fmt.Errorf("apperr-sentinel-77de: %w", appErr))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(buf.String(), "apperr-sentinel-77de") {
		t.Fatalf("5xx AppError emitted with no log of the wrapping cause. captured = %q", buf.String())
	}
}

// TestHandleError_4xxStaysSilent is the other side of the same instrument. A 4xx
// is the client's fault, not a server fault, and LoggingMiddleware already
// records it at WARN — logging all 36 call sites at ERROR would bury the
// real 5xx lines this change exists to surface.
//
// What makes this test non-vacuous is a mutation, not its pairing with the
// control above. The control proves the buffer works; it says nothing about
// whether the code has a removable guard. Deleting logServerFault's
// "if status < http.StatusInternalServerError { return }" is what fails this
// test, verified:
//
//	--- FAIL: TestHandleError_4xxStaysSilent
//	    a 4xx emitted an ERROR line; only 5xx should.
//	    captured = "...status=404 code=NOT_FOUND error=quiet-sentinel-1a3b"
func TestHandleError_4xxStaysSilent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, models.NewAppError(http.StatusNotFound, models.ErrNotFound, "quiet-sentinel-1a3b"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if strings.Contains(buf.String(), "quiet-sentinel-1a3b") {
		t.Fatalf("a 4xx emitted an ERROR line; only 5xx should. captured = %q", buf.String())
	}
}

// TestHandleError_AppErrorCauseIsLogged pins agent-os-2mhb: AppError.Cause,
// set by NewAppErrorWithCause, was previously invisible everywhere — Error()
// returns only Message, and for an AppError passed to handleError unwrapped
// (as respondDockerErr/respondIfEncryptionUnavailable do), "error", err in
// logServerFault logs exactly that sanitised Message, never the real cause.
//
// Distinguishes from TestHandleError_AppError5xxLogsTheCause above, which
// wraps the AppError with fmt.Errorf("%w", ...) — a different mechanism
// (the wrapping error's own Error() string already contains the sentinel).
// This test's AppError is unwrapped; only a change that reads .Cause off it
// can pass this one.
func TestHandleError_AppErrorCauseIsLogged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := errors.New("cause-sentinel-2mhb")
	appErr := models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", cause)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, appErr)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(buf.String(), "cause-sentinel-2mhb") {
		t.Fatalf("AppError.Cause not logged; a fresh 500 built by NewAppErrorWithCause left no record of the real failure. captured = %q", buf.String())
	}
	if strings.Contains(w.Body.String(), "cause-sentinel-2mhb") {
		t.Fatalf("the cause leaked into the response body: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"cause"`) {
		t.Fatalf("response body must never carry a %q key: %s", "cause", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"message":"Internal server error"`) {
		t.Fatalf("Message must stay unchanged in the body: %s", w.Body.String())
	}
}

// TestRespondDockerErr_FallbackCarriesCauseToLog pins agent-os-2mhb: the
// fallback branch of respondDockerErr minted a fresh AppError from
// status/code/message alone and discarded err entirely, so a 500 reached
// through this helper left no record of the real docker failure anywhere.
func TestRespondDockerErr_FallbackCarriesCauseToLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := errors.New("fallback-cause-2mhb")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondDockerErr(c, cause, http.StatusInternalServerError, "SOME_CODE", "generic failure")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(buf.String(), "fallback-cause-2mhb") {
		t.Fatalf("respondDockerErr's fallback branch discarded err; log has no cause. captured = %q", buf.String())
	}
	if strings.Contains(w.Body.String(), "fallback-cause-2mhb") {
		t.Fatalf("the cause leaked into the response body: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"message":"generic failure"`) {
		t.Fatalf("Message must stay unchanged in the body: %s", w.Body.String())
	}
}

// TestRespondDockerErr_DockerUnavailableCarriesCauseToLog is the sibling for
// respondDockerErr's other branch: the ErrDockerUnavailable sentinel case
// also minted a fresh AppError without the original err.
func TestRespondDockerErr_DockerUnavailableCarriesCauseToLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := fmt.Errorf("stack start: %w", services.ErrDockerUnavailable)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondDockerErr(c, cause, http.StatusInternalServerError, "IGNORED", "ignored")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(buf.String(), "stack start") {
		t.Fatalf("respondDockerErr's docker-unavailable branch discarded err; log has no cause. captured = %q", buf.String())
	}
	if strings.Contains(w.Body.String(), "stack start") {
		t.Fatalf("the cause leaked into the response body: %s", w.Body.String())
	}
}

// TestRespondIfEncryptionUnavailable_CauseNotLoggedBelow500 is the 422 arm.
// respondIfEncryptionUnavailable now also carries err as Cause, but
// logServerFault is silent below 500 on purpose (see its doc comment), so
// this must NOT produce a log line — only the body-safety guarantee holds
// here. Do not read this as "the cause is unused"; it is carried for future
// callers (agent-os-2mhb brief).
func TestRespondIfEncryptionUnavailable_CauseNotLoggedBelow500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cause := fmt.Errorf("store token: %w", services.ErrEncryptionUnavailable)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handled := respondIfEncryptionUnavailable(c, cause)

	if !handled {
		t.Fatalf("respondIfEncryptionUnavailable did not recognize the sentinel")
	}
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	if strings.Contains(buf.String(), "store token") {
		t.Fatalf("a 422 must stay silent per logServerFault's <500 guard, but the cause was logged: %q", buf.String())
	}
	if strings.Contains(w.Body.String(), "store token") {
		t.Fatalf("the cause leaked into the response body: %s", w.Body.String())
	}
}

// TestCaptureHandlerLogs_RestoresStdlibLog guards the helper above, not the
// production code. slog.SetDefault silently re-points the stdlib log package,
// and slog.SetDefault(prev) does not undo it — so a cleanup that restores only
// slog leaks the redirect for the rest of the test binary.
//
// It is a ratchet, not a nicety: the leak is invisible while it does damage
// (http.Server error lines vanish into a dead buffer rather than failing
// anything), so the obvious simplification of the cleanup back to one line
// would go unnoticed forever. This is what notices.
func TestCaptureHandlerLogs_RestoresStdlibLog(t *testing.T) {
	writerBefore, flagsBefore := log.Writer(), log.Flags()

	t.Run("capture", func(t *testing.T) { _ = captureHandlerLogs(t) })

	if log.Writer() != writerBefore {
		t.Errorf("stdlib log writer not restored: the capture leaked its redirect, so later log output in this binary is swallowed")
	}
	if log.Flags() != flagsBefore {
		t.Errorf("stdlib log flags not restored: got %d, want %d", log.Flags(), flagsBefore)
	}
}

// TestLogCapture_ConcurrentReadIsRaceFree is the deterministic reproduction
// for agent-os-2h1r. captureHandlerLogs used to hand back a plain
// *bytes.Buffer: slog serialises its own writes, but a reader calling
// buf.String() sits outside that lock. The WS tests read the buffer while
// OTHER connections' handler goroutines are still logging (dashboard.go's
// empty-host branch logs on every tick since agent-os-ear5), which only
// fired under -race with the package under parallel load — latent, and the
// shape that turns a required check red with no finding.
//
// This test needs no load: there is no happens-before edge between the
// writer goroutine and the reads below until wg.Wait, so the race detector
// flags the pair regardless of wall-clock interleaving. Seen failing first on
// the *bytes.Buffer helper (build green, `WARNING: DATA RACE` with the read in
// bytes.(*Buffer).String and the write under log/slog's handler); clean on
// syncLogBuffer. The final assertion keeps it non-vacuous after the fix.
func TestLogCapture_ConcurrentReadIsRaceFree(t *testing.T) {
	buf := captureHandlerLogs(t)

	const iterations = 2000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			slog.Default().Info("race-probe-2h1r", "i", i)
		}
	}()

	for i := 0; i < iterations; i++ {
		_ = buf.String()
	}
	wg.Wait()

	if !strings.Contains(buf.String(), "race-probe-2h1r") {
		t.Fatalf("writer goroutine's lines never reached the buffer; got %q", buf.String())
	}
}
