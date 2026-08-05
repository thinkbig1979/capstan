package middleware

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestIsSecureRequest_GatesForwardedProtoOnPeerTrust is the regression test
// for agent-os-ab9: X-Forwarded-Proto must be honoured ONLY when it arrives
// from a peer in the trusted-proxy list (the same effective list fed to
// gin's SetTrustedProxies via InitTrustedProxyNetworks in main.go), and
// IGNORED — falling back to the real connection — from any other peer.
//
// Before this gate, IsSecureRequest read the header unconditionally from any
// peer: a plaintext connection from an untrusted address could set
// "X-Forwarded-Proto: https" and receive Secure cookies and a plaintext HSTS
// header (OBSERVED 2026-08-05 against a6e0a29 — GET /api/v1/version from a
// non-trusted peer returned "Strict-Transport-Security:
// max-age=31536000; includeSubDomains" over an unencrypted connection).
//
// Both "ignored" and "honoured" are reachable by a broken implementation
// (e.g. one that gates nothing, or one that gates everything), so every case
// below is asserted in the same table rather than split across files, and
// the table includes both the forgery path (must now read false) and the
// genuine-HTTPS-behind-a-trusted-proxy path (must still read true) so a
// fix that closes the forgery hole by breaking real deployments fails this
// test too.
func TestIsSecureRequest_GatesForwardedProtoOnPeerTrust(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		trusted    []string // fed to InitTrustedProxyNetworks before the request
		remoteAddr string
		tls        bool
		xfp        string // empty means "no X-Forwarded-Proto header at all"
		expected   bool
	}{
		{
			name:       "trusted peer (configured CIDR, not loopback) + XFP https -> secure",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.35:54321",
			xfp:        "https",
			expected:   true,
		},
		{
			name:       "untrusted peer + XFP https -> NOT secure (the fix)",
			trusted:    []string{"127.0.0.1", "::1"},
			remoteAddr: "10.0.0.35:54321",
			xfp:        "https",
			expected:   false,
		},
		{
			name:       "untrusted peer + real TLS -> still secure (the gate must not touch a genuine TLS connection)",
			trusted:    []string{"127.0.0.1", "::1"},
			remoteAddr: "10.0.0.35:54321",
			tls:        true,
			xfp:        "https",
			expected:   true,
		},
		{
			name:       "trusted peer + XFP http -> not secure",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.35:54321",
			xfp:        "http",
			expected:   false,
		},
		{
			name:       "untrusted peer + no XFP header at all -> not secure (control: the check can read negative)",
			trusted:    []string{"127.0.0.1", "::1"},
			remoteAddr: "10.0.0.35:54321",
			xfp:        "",
			expected:   false,
		},
		{
			name:       "default trusted list (unconfigured TRUSTED_NETWORKS, loopback only) + XFP https from a real deployment peer -> NOT secure",
			trusted:    []string{"127.0.0.1", "::1"},
			remoteAddr: "172.18.0.5:54321",
			xfp:        "https",
			expected:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			InitTrustedProxyNetworks(tc.trusted)
			defer InitTrustedProxyNetworks(nil)

			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			got := IsSecureRequest(c)
			if got != tc.expected {
				t.Errorf("IsSecureRequest() = %v, want %v (trusted=%v remoteAddr=%v tls=%v xfp=%q)",
					got, tc.expected, tc.trusted, tc.remoteAddr, tc.tls, tc.xfp)
			}
		})
	}
}

// TestIsTrustedProxyPeer_DefaultsToLoopbackOnly guards the zero-value state
// of trustedProxyNetworks before InitTrustedProxyNetworks has ever been
// called (e.g. package init order, or a test that never sets it): only
// loopback should be trusted, matching IsTrustedIP's own unconditional
// loopback rule, never "trust everyone".
func TestIsTrustedProxyPeer_DefaultsToLoopbackOnly(t *testing.T) {
	InitTrustedProxyNetworks(nil)
	defer InitTrustedProxyNetworks(nil)

	if !isTrustedProxyPeer("127.0.0.1") {
		t.Error("isTrustedProxyPeer(127.0.0.1) = false, want true (loopback is always trusted)")
	}
	if isTrustedProxyPeer("10.0.0.35") {
		t.Error("isTrustedProxyPeer(10.0.0.35) = true, want false (nothing configured, not loopback)")
	}
}
