package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRecoveryRouter wires RecoveryMiddleware ahead of a handler selected by
// the "mode" query param, so each test can drive a specific panic scenario
// through the real gin dispatch path rather than calling the closure directly.
func newRecoveryRouter() *gin.Engine {
	r := gin.New()
	r.Use(RecoveryMiddleware())
	r.GET("/probe", func(c *gin.Context) {
		switch c.Query("mode") {
		case "panic-after-write":
			c.JSON(http.StatusOK, gin.H{"ok": true})
			panic("boom after write")
		case "panic-before-write":
			panic("boom before write")
		case "panic-abort-handler":
			panic(http.ErrAbortHandler)
		case "panic-wrapped-abort-handler":
			panic(fmt.Errorf("wrapped: %w", http.ErrAbortHandler))
		default:
			c.Status(http.StatusOK)
		}
	})
	return r
}

// TestRecoveryMiddleware_PanicAfterWrite is DEFECT 1: the deferred recover
// used to call c.JSON(500, ...) unconditionally, appending a second JSON
// object onto whatever the handler had already flushed. gin's
// responseWriter.WriteHeader (response_writer.go:65-72) refuses to change a
// status once committed, so the status was already stuck at 200 before this
// fix — only the appended body bytes were the bug. This test must fail
// against unfixed recovery.go, showing the doubled body, and pass once the
// middleware checks c.Writer.Written() before writing anything.
func TestRecoveryMiddleware_PanicAfterWrite(t *testing.T) {
	w := httptest.NewRecorder()
	newRecoveryRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe?mode=panic-after-write", nil))

	assert.Equal(t, http.StatusOK, w.Code, "status must be whatever the handler already committed; gin cannot change it post-write")
	assert.Equal(t, `{"ok":true}`, w.Body.String(), "body must be exactly what the handler wrote, with nothing appended by the recovery middleware")
}

// TestRecoveryMiddleware_PanicBeforeWrite is a regression guard, not evidence
// the defect was fixed: this case passes identically with or without the
// Written() guard, since nothing has been written yet when the panic fires.
func TestRecoveryMiddleware_PanicBeforeWrite(t *testing.T) {
	w := httptest.NewRecorder()
	newRecoveryRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe?mode=panic-before-write", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.JSONEq(t, `{"code":"INTERNAL_ERROR","message":"Internal server error"}`, w.Body.String())
}

// TestRecoveryMiddleware_ErrAbortHandlerIsRePanicked is DEFECT 2. net/http's
// own recovery (GOROOT src/net/http/server.go:1940) tests the panic value
// with pointer/value identity, "err != ErrAbortHandler", not errors.Is. So
// the middleware must re-panic the sentinel itself; re-panicking a wrapped
// error would fail that identity check upstream and dump a 64KB stack trace
// per occurrence. ServeHTTP re-panicking means it must escape this call, so
// this test asserts with require.Panics / PanicsWithValue rather than
// inspecting a returned status.
func TestRecoveryMiddleware_ErrAbortHandlerIsRePanicked(t *testing.T) {
	r := newRecoveryRouter()
	req := httptest.NewRequest(http.MethodGet, "/probe?mode=panic-abort-handler", nil)

	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		r.ServeHTTP(httptest.NewRecorder(), req)
	})
}

// TestRecoveryMiddleware_WrappedErrAbortHandlerStillRepanicksSentinel drives
// the same scenario through a %w-wrapped error to prove the middleware
// unwraps with errors.Is for detection but re-panics http.ErrAbortHandler
// itself (not the wrapped value), which is what net/http's identity check at
// server.go:1940 requires.
func TestRecoveryMiddleware_WrappedErrAbortHandlerStillRepanicksSentinel(t *testing.T) {
	r := newRecoveryRouter()
	req := httptest.NewRequest(http.MethodGet, "/probe?mode=panic-wrapped-abort-handler", nil)

	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		r.ServeHTTP(httptest.NewRecorder(), req)
	})
}

// TestRecoveryMiddleware_NoPanicPassesThrough is a sanity check that the
// middleware is transparent on the non-panic path.
func TestRecoveryMiddleware_NoPanicPassesThrough(t *testing.T) {
	w := httptest.NewRecorder()
	newRecoveryRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}
