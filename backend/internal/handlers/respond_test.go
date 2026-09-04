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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
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

// captureHandlerLogs swaps the process-wide slog default for a buffer and
// restores it.
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
func captureHandlerLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
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
