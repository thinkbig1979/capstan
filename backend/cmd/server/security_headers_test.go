package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// TestSecurityHeadersCSP guards the Content-Security-Policy emitted by
// SecurityHeaders(). The dashboard's charting bundle (recharts) uses eval, so
// the script-src directive must include 'unsafe-eval' alongside the per-request
// nonce and 'strict-dynamic' — otherwise the browser blocks chart rendering
// with "blocked a JavaScript eval (script-src)".
func TestSecurityHeadersCSP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	SecurityHeaders(&config.Config{})(c)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("expected Content-Security-Policy header to be set")
	}

	for _, want := range []string{
		"script-src",
		"'strict-dynamic'",
		"'unsafe-eval'",
		"frame-ancestors 'none'",
		"connect-src 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q\n  got: %s", want, csp)
		}
	}
}

// TestSecurityHeadersCSP_ConnectSrcFromConfig verifies the connect-src directive
// includes configured CORS origins (and their ws/wss variants) so a cross-origin
// reverse-proxy deployment is not blocked by a localhost-only policy (M6).
func TestSecurityHeadersCSP_ConnectSrcFromConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	cfg := &config.Config{CORSOrigins: "https://capstan.example.com"}
	SecurityHeaders(cfg)(c)

	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"https://capstan.example.com",
		"wss://capstan.example.com",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("connect-src missing %q\n  got: %s", want, csp)
		}
	}
	if strings.Contains(csp, "localhost") {
		t.Errorf("production CSP must not include localhost\n  got: %s", csp)
	}

	// A unique nonce must be present in script-src for strict-dynamic to work.
	if nonce, ok := c.Get("csp_nonce"); !ok || nonce == "" {
		t.Error("expected a csp_nonce to be set on the context")
	} else if !strings.Contains(csp, "'nonce-"+nonce.(string)+"'") {
		t.Errorf("CSP script-src missing the request nonce %q\n  got: %s", nonce, csp)
	}
}
