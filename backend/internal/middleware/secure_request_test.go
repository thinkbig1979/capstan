package middleware

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestIsSecureRequest_TrustsLastForwardedProtoValue guards against
// agent-os-qru.9: a reverse proxy that APPENDS to X-Forwarded-Proto rather
// than overwriting it (either as a comma-joined value on one header line, or
// as a second separate header line) must not let a client-forged value at
// the front of the list override the proxy's own genuine value at the end.
// IsSecureRequest must derive the protocol from the last NON-EMPTY value
// across all X-Forwarded-Proto header instances (and the last non-empty
// comma-separated element within an instance), not the first, in either
// wire shape, and a trailing empty element/instance must not be treated as
// an authoritative override of a real value earlier in the list. Both
// directions matter: a forged "http" ahead of a genuine "https" must not
// strip Secure/HSTS from a genuinely-HTTPS deployment, and (in the
// separate-header-lines wire shape - the comma-joined shape already
// returned false on the pre-agent-os-qru.9 code) a forged "https" ahead of a
// genuine "http" must not fabricate Secure/HSTS on a plaintext connection -
// see main.go's SecurityHeaders handler, which would then emit
// Strict-Transport-Security on plaintext (OBSERVED 2026-08-05).
func TestIsSecureRequest_TrustsLastForwardedProtoValue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		tls      bool
		xfp      []string // one entry per header instance (Header.Add call)
		expected bool
	}{
		{"genuine TLS, no XFP", true, nil, true},
		{"genuine plaintext, no XFP", false, nil, false},
		{"single XFP https", false, []string{"https"}, true},
		{"single XFP http", false, []string{"http"}, false},
		{"APPEND comma-joined: forged http ahead of real https", false, []string{"http, https"}, true},
		{"APPEND separate lines: forged http ahead of real https", false, []string{"http", "https"}, true},
		{"APPEND comma-joined: forged https ahead of real http", false, []string{"https, http"}, false},
		{"APPEND separate lines: forged https ahead of real http", false, []string{"https", "http"}, false},
		{"trailing empty header instance after real https", false, []string{"https", ""}, true},
		{"trailing empty comma element: \"https,\"", false, []string{"https,"}, true},
		{"trailing empty comma elements: \"https,,\"", false, []string{"https,,"}, true},
		{"forged http, real https, then trailing empty instance", false, []string{"http", "https", ""}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, v := range tc.xfp {
				req.Header.Add("X-Forwarded-Proto", v)
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			got := IsSecureRequest(c)
			if got != tc.expected {
				t.Errorf("IsSecureRequest() = %v, want %v (tls=%v xfp=%v)", got, tc.expected, tc.tls, tc.xfp)
			}
		})
	}
}

// TestIsSecureRequest_NilRequest guards the c.Request == nil branch added
// under agent-os-qru.9, mirroring the established nil-request precedent at
// TestPeekLoginUsername_NilRequestAndNilBody in ratelimit_test.go. A nil
// Request must not panic and must be treated as not secure.
func TestIsSecureRequest_NilRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = nil
	if got := IsSecureRequest(c); got != false {
		t.Errorf("IsSecureRequest() with a nil request = %v, want false", got)
	}
}
