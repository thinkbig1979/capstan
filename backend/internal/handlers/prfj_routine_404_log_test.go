package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// httpRequestLineLevel returns the level stamped on the single "HTTP request"
// line (middleware.LoggingMiddleware's fixed msg) in out, or "" when there is
// none.
//
// It matches on that msg specifically rather than scanning the whole buffer,
// because the 500 row below ALSO produces logServerFault's "request failed"
// ERROR line. A test that looked for "ERROR" anywhere in the buffer would find
// that one and pass regardless of what the middleware chose — the exact
// mistake that would let a broken level rule through.
func httpRequestLineLevel(t *testing.T, out string) string {
	t.Helper()

	var found []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, `msg="HTTP request"`) {
			found = append(found, line)
		}
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

// TestHandleError_RoutineOutcomeLogLevel is the end-to-end pin for
// agent-os-prfj: a real gin engine, LoggingMiddleware in front, handleError
// behind, and one AppError per row — the actual chain a request takes, not the
// middleware driven with a synthetic status.
//
// The first two rows are the whole test. They are the SAME PATH and the SAME
// STATUS and differ only in the AppError's CODE:
//
//   - GIT_NOT_REPO is the routine answer. GET /api/v1/git is asked of every
//     stack the frontend renders, and on a host with no git-backed stack every
//     one of those answers 404. It must not be a warning.
//   - NOT_FOUND is git.go resolvePathFromStack's answer for an unknown
//     stackId. Same endpoint, same 404, and a real client error that must
//     still warn.
//
// So this is not a test that the routine 404 went quiet — that alone is also
// what breaking 4xx logging entirely looks like. It is a test that the two
// separated, which only a code-keyed rule can do. A future "simplification" of
// routineErrorCodes to a path check or a status check fails on row two.
func TestHandleError_RoutineOutcomeLogLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		err  *models.AppError
		want string
	}{
		{
			name: "GIT_NOT_REPO 404 is routine: Info",
			err:  models.NewAppError(http.StatusNotFound, models.ErrGitNotRepo, "Not a git repository"),
			want: "INFO",
		},
		{
			name: "NOT_FOUND 404 on the SAME path is a client error: still Warn",
			err:  models.NewAppError(http.StatusNotFound, models.ErrNotFound, "Stack not found"),
			want: "WARN",
		},
		{
			// The must-still-log arm for agent-os-n2df. STACK_DIR_MISSING is a
			// 404 on this same endpoint, minted by the same gitFailure that
			// mints the routine GIT_NOT_REPO — and it must stay a warning. An
			// unmounted stacks volume produces it for every stack at once, so
			// silencing it would trade the noise this bead removed for a real
			// incident going quiet.
			name: "STACK_DIR_MISSING 404 is a server-side incident: still Warn",
			err:  models.NewAppError(http.StatusNotFound, models.ErrStackDirMissing, "Stack directory does not exist on disk"),
			want: "WARN",
		},
		{
			// GIT_NO_COMMITS is routine like GIT_NOT_REPO: a repo that exists
			// but has no commits is a normal state, asked about on every visit.
			name: "GIT_NO_COMMITS 404 is routine: Info",
			err:  models.NewAppError(http.StatusNotFound, models.ErrGitNoCommits, "Repository has no commits yet"),
			want: "INFO",
		},
		{
			name: "UNAUTHORIZED 401 still Warn",
			err:  models.NewAppError(http.StatusUnauthorized, models.ErrUnauthorized, "Unauthorized"),
			want: "WARN",
		},
		{
			name: "FORBIDDEN 403 still Warn",
			err:  models.NewAppError(http.StatusForbidden, models.ErrForbidden, "Forbidden"),
			want: "WARN",
		},
		{
			name: "VALIDATION_ERROR 422 still Warn",
			err:  models.NewAppError(http.StatusUnprocessableEntity, models.ErrValidation, "Invalid"),
			want: "WARN",
		},
		{
			name: "INTERNAL_ERROR 500 still Error",
			err:  models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "boom"),
			want: "ERROR",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureHandlerLogs(t)

			r := gin.New()
			r.Use(middleware.LoggingMiddleware())
			r.GET("/api/v1/git", func(c *gin.Context) { handleError(c, tc.err) })

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/git", nil))

			if w.Code != tc.err.Status {
				t.Fatalf("status: want %d, got %d", tc.err.Status, w.Code)
			}

			got := httpRequestLineLevel(t, buf.String())
			if got != tc.want {
				t.Fatalf("code %s (%d): want level %s, got %q. captured = %q",
					tc.err.Code, tc.err.Status, tc.want, got, buf.String())
			}
		})
	}
}

// TestHandleError_RoutineMarkerDoesNotDisturbLogServerFault pins that
// logServerFault's existing behaviour is untouched by the marker: still silent
// below 500, still emitting its "request failed" line at 500.
//
// The routine branch runs BEFORE logServerFault in handleError, so a mistake
// there — an early return, a swallowed 5xx — would show up here and nowhere in
// the level table above, where a 500 already logs at Error from the middleware
// for independent reasons.
func TestHandleError_RoutineMarkerDoesNotDisturbLogServerFault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("routine 404 stays silent in logServerFault", func(t *testing.T) {
		buf := captureHandlerLogs(t)

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		handleError(c, models.NewAppError(http.StatusNotFound, models.ErrGitNotRepo, "Not a git repository"))

		if got := buf.String(); strings.Contains(got, "request failed") {
			t.Fatalf("logServerFault must stay silent below 500. captured = %q", got)
		}
	})

	t.Run("500 still emits request failed", func(t *testing.T) {
		buf := captureHandlerLogs(t)

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		handleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "boom"))

		if got := buf.String(); !strings.Contains(got, "request failed") {
			t.Fatalf("logServerFault must still log a 5xx. captured = %q", got)
		}
	})
}

// TestHandleError_MarksOnlyListedCodes pins the marker itself, independent of
// the middleware, so a failure says WHICH half broke: the code list here or
// the level rule there.
//
// models.ErrNotFound is the row that matters. It is deliberately NOT in
// routineErrorCodes even though two in-class sites in handlers/env.go answer
// with it (agent-os-hjmf, "No env file associated with this stack"), because
// the SAME code is also the genuine "Stack not found" client error. Adding it
// to the list would silence both. Those sites must call
// middleware.MarkRoutineOutcome directly instead, and this assertion is what
// stops the shortcut being taken here.
func TestHandleError_MarksOnlyListedCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		code string
		want bool
	}{
		{models.ErrGitNotRepo, true},
		// GIT_NO_COMMITS earned its own code in agent-os-n2df precisely so it
		// could be listed here without dragging ErrNotFound's 20 genuine
		// "Stack not found" sites along with it. The row below is the other
		// half of that argument and must stay false.
		{models.ErrGitNoCommits, true},
		{models.ErrNotFound, false},
		// STACK_DIR_MISSING is minted by the same function as GIT_NOT_REPO and
		// is deliberately NOT routine: an unmounted stacks volume must stay a
		// warning (agent-os-n2df).
		{models.ErrStackDirMissing, false},
		{models.ErrStackNotFound, false},
		{models.ErrUnauthorized, false},
		{models.ErrForbidden, false},
		{models.ErrValidation, false},
		{models.ErrGitDirty, false},
		{models.ErrGitConflict, false},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			handleError(c, models.NewAppError(http.StatusNotFound, tc.code, "x"))

			if got := middleware.IsRoutineOutcome(c); got != tc.want {
				t.Fatalf("code %s: IsRoutineOutcome = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}
