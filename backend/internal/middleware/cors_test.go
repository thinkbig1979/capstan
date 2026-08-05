package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// newCORSRouter wires CORSMiddleware ahead of a plain 200 handler so tests
// can inspect exactly the headers the middleware sets.
func newCORSRouter(allowedOrigins string) *gin.Engine {
	r := gin.New()
	r.Use(CORSMiddleware(allowedOrigins))
	r.GET("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.OPTIONS("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

// TestCORSMiddleware_AllowedOriginGetsHeaders covers acceptance #4 (allowed
// half) and #5 (Vary: Origin, DEFECT 3): an allowlisted origin gets ACAO
// echoed back plus the caching-safety Vary header.
func TestCORSMiddleware_AllowedOriginGetsHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Origin", "https://a.example")

	w := httptest.NewRecorder()
	newCORSRouter("https://a.example,https://b.example").ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://a.example", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "Origin", w.Header().Get("Vary"),
		"without Vary: Origin a shared cache could serve origin A's ACAO-bearing response to origin B")
}

// TestCORSMiddleware_DisallowedOriginGetsNoACAO covers acceptance #4
// (disallowed half): an origin outside the allowlist gets no ACAO, and gets
// no Vary either, matching production behaviour (Vary is only meaningful
// where the response actually varies by Origin).
func TestCORSMiddleware_DisallowedOriginGetsNoACAO(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Origin", "https://evil.example")

	w := httptest.NewRecorder()
	newCORSRouter("https://a.example,https://b.example").ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Empty(t, w.Header().Get("Vary"))
}

// TestCORSMiddleware_EmptyConfigIsDefaultDeny confirms the allowedOrigins ==
// "" early return (cors.go:13-16) is intended behaviour, NOT something to
// "fix": cors.go is the only ACAO emitter in the repo, the frontend proxy is
// same-origin, and CORS_ORIGINS defaults empty, so empty config -> no
// headers -> browser default-deny is correct.
func TestCORSMiddleware_EmptyConfigIsDefaultDeny(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Origin", "https://a.example")

	w := httptest.NewRecorder()
	newCORSRouter("").ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Vary"))
}

// TestCORSMiddleware_OptionsPreflightShortCircuits confirms the existing
// preflight handling (204, no further handler) survives the Vary addition
// unchanged for both allowed and disallowed origins.
func TestCORSMiddleware_OptionsPreflightShortCircuits(t *testing.T) {
	t.Run("allowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/probe", nil)
		req.Header.Set("Origin", "https://a.example")

		w := httptest.NewRecorder()
		newCORSRouter("https://a.example").ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, "https://a.example", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "Origin", w.Header().Get("Vary"))
	})

	t.Run("disallowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/probe", nil)
		req.Header.Set("Origin", "https://evil.example")

		w := httptest.NewRecorder()
		newCORSRouter("https://a.example").ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})
}
