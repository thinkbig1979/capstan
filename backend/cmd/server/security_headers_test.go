package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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

	SecurityHeaders()(c)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("expected Content-Security-Policy header to be set")
	}

	for _, want := range []string{
		"script-src",
		"'strict-dynamic'",
		"'unsafe-eval'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q\n  got: %s", want, csp)
		}
	}

	// A unique nonce must be present in script-src for strict-dynamic to work.
	if nonce, ok := c.Get("csp_nonce"); !ok || nonce == "" {
		t.Error("expected a csp_nonce to be set on the context")
	} else if !strings.Contains(csp, "'nonce-"+nonce.(string)+"'") {
		t.Errorf("CSP script-src missing the request nonce %q\n  got: %s", nonce, csp)
	}
}
