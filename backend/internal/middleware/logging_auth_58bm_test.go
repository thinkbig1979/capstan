package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// agent-os-58bm: the request log hid the Authorization header only when it
// started with "Bearer ", so a Basic credential, a lowercase "bearer" or any
// other value was logged verbatim. Now every non-empty value logs as its
// scheme word (when it is a known one) and "***"; an unknown first word may
// be the credential itself, so it is not kept.
func TestLoggingMiddleware_58bm_AuthorizationNeverLogged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct{ header, want, secret string }{
		{"Bearer SEKb1", `authorization="Bearer ***"`, "SEKb1"},
		{"Basic U0VLYjI6cHc=", `authorization="Basic ***"`, "U0VLYjI6cHc="},
		{"bearer SEKb3", `authorization="bearer ***"`, "SEKb3"},
		{"SEKb4", `authorization=***`, "SEKb4"},
		{`Digest username="u", response="SEKb5"`, `authorization="Digest ***"`, "SEKb5"},
		{"SEKb6 rest", `authorization=***`, "SEKb6"},
		{"", `authorization=""`, ""},
	}
	for _, c := range cases {
		buf := captureSlogAt(t, slog.LevelDebug)
		r := gin.New()
		r.Use(LoggingMiddleware())
		r.GET("/api/v1/stacks", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		r.ServeHTTP(httptest.NewRecorder(), req)
		out := buf.String()
		if !strings.Contains(out, `msg="HTTP request"`) {
			t.Fatalf("no request line for %q: %q", c.header, out)
		}
		if c.secret != "" && strings.Contains(out, c.secret) {
			t.Errorf("header %q: the log shows %q: %s", c.header, c.secret, out)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("header %q: want %s in %s", c.header, c.want, out)
		}
	}
}
