package handlers

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// parseEnvFile used to answer EVERY read failure with an empty list and a nil
// error (agent-os-2ucq), so a permission or I/O fault on global.env read as
// "no variables defined" with no log line, and GetGlobalEnv's error branch
// could never run. Absence is the only condition that legitimately means
// "empty": a fresh install has no global.env yet. Everything else propagates
// and surfaces as a 5xx carrying its cause.
//
// The unreadable arm plants a DIRECTORY at the file's path: os.ReadFile then
// fails with EISDIR, which is not fs.ErrNotExist, and the technique does not
// depend on the uid the tests run as (chmod 000 is a no-op for root).

func TestParseGlobalEnvFile_AbsentIsEmptyButOtherReadErrorsPropagate(t *testing.T) {
	dir := t.TempDir()

	vars, err := parseEnvFile(filepath.Join(dir, "global.env"))
	require.NoError(t, err, "absence is the empty-result contract")
	assert.Equal(t, []map[string]string{}, vars)

	require.NoError(t, os.Mkdir(filepath.Join(dir, "dir.env"), 0o755))
	vars, err = parseEnvFile(filepath.Join(dir, "dir.env"))
	require.Error(t, err, "a read failure that is not absence must propagate, not read as an empty file")
	assert.False(t, errors.Is(err, fs.ErrNotExist))
	assert.Nil(t, vars)
}

func TestSettingsHandler_GetGlobalEnv_UnreadableFileIs500WithCause(t *testing.T) {
	handler, router := newTestSettingsHandler(t)
	require.NoError(t, os.Mkdir(handler.cfg.DataDir+"/global.env", 0o755))
	logs := captureHandlerLogs(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/global-env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code,
		"an unreadable global.env must not read as an empty env; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
	assert.NotContains(t, w.Body.String(), "is a directory", "the cause stays out of the wire body")
	assert.Contains(t, logs.String(), "level=ERROR")
	assert.Contains(t, logs.String(), "is a directory", "the ERROR line must name the cause; got %q", logs.String())
}

// newGlobalEnvRouterWithRequestID mounts the one route this file's control
// exercises, behind the RequestID middleware, so logServerFault (respond.go)
// stamps the caller's X-Request-ID sentinel on any line it writes and the
// control's absence assertion can be about THIS request rather than about the
// process-global slog sink (agent-os-nho7).
//
// A local three-line router rather than one line added to
// setupSettingsFullRouter (settings_ext_test.go): that helper serves ~20 tests
// that never asked for the middleware, and its file is outside this bead's
// FILES. The route is registered exactly as it is there, envUnlockedMiddleware
// included, so the handler is reached the same way.
func newGlobalEnvRouterWithRequestID(h *SettingsHandler) *gin.Engine {
	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/settings/global-env", envUnlockedMiddleware(), h.GetGlobalEnv)
	return r
}

// Control on the same instrument: a fresh install with no global.env is still
// an empty success, and nothing is logged at ERROR for it — discriminated on
// this request's own id, see requireNoOwnErrorLines (ua4y_7lg1_cause_test.go).
func TestSettingsHandler_GetGlobalEnv_AbsentFileIsEmptySuccessWithoutErrorLog(t *testing.T) {
	handler, _ := newTestSettingsHandler(t)
	router := newGlobalEnvRouterWithRequestID(handler)
	_, statErr := os.Stat(handler.cfg.DataDir + "/global.env")
	require.True(t, errors.Is(statErr, fs.ErrNotExist), "precondition: the file is absent")
	logs := captureHandlerLogs(t)

	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()

	req := httptest.NewRequest(http.MethodGet, "/settings/global-env", nil)
	req.Header.Set(middleware.RequestIDHeader, reqID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	<-plantDone

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"vars":[]`)
	requireNoOwnErrorLines(t, logs.String(), sentinel, "an absent global.env (absence is not a fault)")
}
