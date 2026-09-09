package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// routine404Fixture builds the real chain a request takes — LoggingMiddleware
// in front, a real EnvHandler over a real DB behind — and returns it with the
// ids of two stacks that differ only in whether an env file exists.
//
// The middleware has to be the real one and it has to be in front, because the
// whole claim under test is about the level LoggingMiddleware picks. Driving
// the handler alone would prove only that MarkRoutineOutcome was called, not
// that anything downstream reads it.
func routine404Fixture(t *testing.T) (*gin.Engine, string, string) {
	t.Helper()

	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	noEnvDir := filepath.Join(tempDir, "noenv")
	missingEnvDir := filepath.Join(tempDir, "missingenv")
	require.NoError(t, os.MkdirAll(noEnvDir, 0755))
	require.NoError(t, os.MkdirAll(missingEnvDir, 0755))
	createTestDirectory(t, db, noEnvDir)
	createTestDirectory(t, db, missingEnvDir)

	// EnvFile "" is the in-class state: the stack simply has no env file, which
	// is ordinary configuration.
	noEnvID := filepath.Base(tempDir) + "~noenv:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          noEnvID,
		Directory:   noEnvDir,
		ComposeFile: "compose.yaml",
		EnvFile:     "",
		ProjectName: "noenv-default",
	}))

	// EnvFile is set but nothing is written to disk: the DB and the filesystem
	// disagree, which is the out-of-class inconsistency that must keep warning.
	missingEnvID := filepath.Base(tempDir) + "~missingenv:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          missingEnvID,
		Directory:   missingEnvDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "missingenv-default",
	}))

	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})

	r := gin.New()
	r.Use(middleware.LoggingMiddleware())
	group := r.Group("/api/v1/stacks")
	group.Use(envUnlockedMiddleware())
	group.GET("/:id/env", handler.Get)
	group.PUT("/:id/env", handler.Put)

	return r, noEnvID, missingEnvID
}

// TestEnvHandler_RoutineNoEnvFile404LogLevel is the pin for agent-os-hjmf: a
// stack with no env file is an ordinary state, so asking for its env must not
// write a WARN line.
//
// The routine rows are only half the test, and the smaller half. The three
// must-still-WARN rows are what distinguishes the fix from the two ways of
// breaking 4xx logging that would also satisfy the routine rows — marking the
// whole handler routine, or adding models.ErrNotFound to routineErrorCodes.
// The second of those is separately trapped by
// TestHandleError_MarksOnlyListedCodes in prfj_routine_404_log_test.go; the
// first is trapped only here, because "Env file not found on disk" is minted
// by the same handler, with the same status, and even with the same
// models.ErrNotFound code as the routine answer. Nothing but a per-site marker
// separates those two rows.
func TestEnvHandler_RoutineNoEnvFile404LogLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r, noEnvID, missingEnvID := routine404Fixture(t)

	cases := []struct {
		name   string
		method string
		id     string
		body   string
		want   string
	}{
		{
			name:   "GET no env file is routine: Info",
			method: http.MethodGet,
			id:     noEnvID,
			want:   "INFO",
		},
		{
			// The same in-class response is minted a second time on the write
			// path, so the marker has to be applied at both sites. A fix that
			// touched only Get leaves this row red.
			name:   "PUT no env file is routine: Info",
			method: http.MethodPut,
			id:     noEnvID,
			body:   `{"raw":"A=1\n"}`,
			want:   "INFO",
		},
		{
			// Same handler, same 404, same NOT_FOUND code as the row above,
			// and it must stay a warning: the DB believes there is an env file
			// and the disk disagrees, which is a real inconsistency.
			name:   "GET env file missing on disk is an inconsistency: still Warn",
			method: http.MethodGet,
			id:     missingEnvID,
			want:   "WARN",
		},
		// There is deliberately no PUT row for the missing-on-disk case: the
		// write path does not answer 404 there, it CREATES the file and
		// answers 200. OBSERVED while writing this test — the row existed,
		// asserted WARN, and failed on `status: want 404, got 200`. The
		// out-of-class control for the write path is the unknown-stack row
		// below instead.
		{
			name:   "GET unknown stack is a client error: still Warn",
			method: http.MethodGet,
			id:     "no-such-stack",
			want:   "WARN",
		},
		{
			// The must-still-WARN arm for the write path specifically. Without
			// it, marking the whole of Put routine would pass every other row.
			name:   "PUT unknown stack is a client error: still Warn",
			method: http.MethodPut,
			id:     "no-such-stack",
			body:   `{"raw":"A=1\n"}`,
			want:   "WARN",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureHandlerLogs(t)

			var body *strings.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(tc.method, "/api/v1/stacks/"+tc.id+"/env", body)
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status: want 404, got %d. body = %q", w.Code, w.Body.String())
			}

			if got := httpRequestLineLevel(t, buf.String()); got != tc.want {
				t.Fatalf("want level %s, got %q. captured = %q", tc.want, got, buf.String())
			}
		})
	}
}
