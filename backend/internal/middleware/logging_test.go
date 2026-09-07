package middleware

import (
	"bytes"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// captureSlogAt is captureSlog (proxytrust_test.go) with the threshold as a
// parameter, and the difference is the whole point of these tests.
//
// captureSlog pins the handler at LevelWarn, so anything below Warn is simply
// absent from its buffer — which makes "buffer empty" mean EITHER "logged at
// Info" OR "not logged at all". That is a one-sided instrument: it would show
// a fix that correctly downgrades the routine 404 to Info and a fix that
// deleted the log call outright as the same result. Capturing at LevelDebug
// lets these tests assert level=INFO is POSITIVELY PRESENT, so a broken
// logging path fails instead of passing.
//
// The stdlib log package must be restored explicitly, and its writer and flags
// read BEFORE the swap: slog.SetDefault also does log.SetOutput(handlerWriter{})
// and log.SetFlags(0), and slog.SetDefault(prev) undoes neither, so restoring
// slog alone leaks the redirect and every later stdlib-log write in this test
// binary lands in a dead buffer (agent-os-ac0o). Same reasoning as captureSlog;
// see TestCaptureSlog_RestoresStdlibLog in proxytrust_test.go.
func captureSlogAt(t *testing.T, level slog.Leveler) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevSlog := slog.Default()
	prevWriter, prevFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return &buf
}

// levelOfRequestLine returns the slog level stamped on the single "HTTP
// request" line in out, or "" when there is no such line.
//
// It insists on EXACTLY ONE such line. A count is the half that makes the
// level assertion meaningful: without it, a change that emitted two lines (say
// the old Warn plus a new Info) would still satisfy a strings.Contains check
// for the level the test wanted, and the noise this exists to remove would
// still be in the log.
func levelOfRequestLine(t *testing.T, out string) string {
	t.Helper()

	var found []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, `msg="HTTP request"`) {
			continue
		}
		found = append(found, line)
	}
	if len(found) == 0 {
		return ""
	}
	if len(found) > 1 {
		t.Fatalf("expected exactly one \"HTTP request\" line, got %d:\n%s", len(found), strings.Join(found, "\n"))
	}

	for _, field := range strings.Fields(found[0]) {
		if strings.HasPrefix(field, "level=") {
			return strings.TrimPrefix(field, "level=")
		}
	}
	t.Fatalf("the \"HTTP request\" line carries no level= field: %q", found[0])
	return ""
}

// TestLoggingMiddleware_CaptureControl is the positive control for every test
// below. It proves the instrument FIRES — that a request through
// LoggingMiddleware reaches the captured buffer at all — so that a specific
// level found missing later is evidence about the level rather than evidence
// the harness never ran.
func TestLoggingMiddleware_CaptureControl(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureSlogAt(t, slog.LevelDebug)

	r := gin.New()
	r.Use(LoggingMiddleware())
	r.GET("/api/v1/stacks", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil))

	if got := buf.String(); !strings.Contains(got, `msg="HTTP request"`) {
		t.Fatalf("capture instrument did not fire: no \"HTTP request\" line. captured = %q", got)
	}
}

// TestLoggingMiddleware_RoutineOutcomeLevel pins BOTH directions of
// agent-os-prfj on ONE instrument.
//
// The bug: LoggingMiddleware chose its level from the status alone, so a 404
// that is the routine, expected answer ("this directory is not a git
// repository", asked of every stack on every page visit) logged at Warn just
// like a genuine client error. Warning level stops being a signal when the
// normal case fills it.
//
// The fix must therefore be discriminating, not merely quiet. Every row below
// runs the same middleware through the same recorder; the marked 404 dropping
// to Info WHILE the unmarked 404 — same status, same path — stays at Warn is
// what separates a working fix from one that broke 4xx logging altogether.
// The two 500 rows are the guard on the other side: the marker must not be
// able to downgrade a server fault, whatever sets it.
func TestLoggingMiddleware_RoutineOutcomeLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		status  int
		routine bool
		want    string
	}{
		{"routine 404 is the fix: Info, not Warn", http.StatusNotFound, true, "INFO"},
		{"unmarked 404 must stay Warn (same status as the row above)", http.StatusNotFound, false, "WARN"},
		{"unmarked 401 must stay Warn", http.StatusUnauthorized, false, "WARN"},
		{"unmarked 403 must stay Warn", http.StatusForbidden, false, "WARN"},
		{"unmarked 422 must stay Warn", http.StatusUnprocessableEntity, false, "WARN"},
		{"unmarked 500 must stay Error", http.StatusInternalServerError, false, "ERROR"},
		{"marked 500 must stay Error: the marker cannot mute a server fault", http.StatusInternalServerError, true, "ERROR"},
		{"unmarked 200 must stay Info", http.StatusOK, false, "INFO"},
		{"marked 200 must stay Info", http.StatusOK, true, "INFO"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureSlogAt(t, slog.LevelDebug)

			r := gin.New()
			r.Use(LoggingMiddleware())
			r.GET("/api/v1/git", func(c *gin.Context) {
				if tc.routine {
					MarkRoutineOutcome(c)
				}
				c.JSON(tc.status, gin.H{})
			})

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/git", nil))

			got := levelOfRequestLine(t, buf.String())
			if got != tc.want {
				t.Fatalf("status=%d routine=%v: want level %s, got %q. captured = %q",
					tc.status, tc.routine, tc.want, got, buf.String())
			}
		})
	}
}

// TestIsRoutineOutcome_DefaultsFalse pins the safe default: every request that
// never calls MarkRoutineOutcome keeps the level it has always had. Without
// this, a mistake that made IsRoutineOutcome true by default would silence
// every 4xx in the server, which is the failure the explicit code list in
// handlers/respond.go exists to prevent.
func TestIsRoutineOutcome_DefaultsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if IsRoutineOutcome(c) {
		t.Fatal("IsRoutineOutcome must be false on a context nothing marked")
	}

	MarkRoutineOutcome(c)
	if !IsRoutineOutcome(c) {
		t.Fatal("IsRoutineOutcome must be true after MarkRoutineOutcome")
	}
}

// TestRoutineOutcomeHelpers_NilContext pins the nil guards, matching
// RequestIDFrom's contract (requestid.go). Handlers are not the only callers —
// agent-os-hjmf will call MarkRoutineOutcome from bare c.JSON sites — so a nil
// context must be inert rather than a panic.
func TestRoutineOutcomeHelpers_NilContext(t *testing.T) {
	if IsRoutineOutcome(nil) {
		t.Fatal("IsRoutineOutcome(nil) must be false")
	}
	MarkRoutineOutcome(nil) // must not panic
}
