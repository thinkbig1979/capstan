package middleware

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// captureSlog redirects the process-wide slog default to a buffer for the
// duration of the test and restores the previous default on cleanup. Mirrors
// internal/config/config_test.go's and internal/services/git_credentials_test.go's
// helper of the same name.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// resetUntrustedForwardedProtoWarned clears the warn-once peer set between
// tests so one test's peers don't silence another's assertions regardless of
// run order.
func resetUntrustedForwardedProtoWarned() {
	untrustedForwardedProtoWarned.mu.Lock()
	untrustedForwardedProtoWarned.peers = make(map[string]struct{})
	untrustedForwardedProtoWarned.mu.Unlock()
}

// TestIsSecureRequest_UntrustedPeerDoesNotFloodLogs guards agent-os-ab9's own
// log-flood defect, found during review of the peer-trust gate itself.
// Gating X-Forwarded-Proto put IsTrustedIP (via isTrustedProxyPeer in
// proxytrust.go) on a per-request hot path for the first time - previously
// it only ran on the AUTH_DISABLED bypass and the health-check path, both
// far colder. Before the fix in IsTrustedIP (auth.go), every bare-IP entry
// in the DEFAULT trusted list ("127.0.0.1", "::1" - neither of which is a
// CIDR) produced an "Invalid trusted network CIDR" warning on EVERY call
// that reached it, not once: OBSERVED, 50 requests from one untrusted peer
// against the default list produced 100 such lines (2 bare-IP entries x 50
// calls) before the fix, alongside exactly 1 XFP warning -
// warnUntrustedForwardedProto's own warn-once budget already worked
// correctly; the flood was entirely in the function it calls underneath.
// This matters specifically in the deployment this bead exists to fix:
// default TRUSTED_NETWORKS behind a reverse proxy not itself in that list,
// where every request through the proxy would have hit this path.
//
// Both counts are asserted together: asserting only "CIDR lines <= 1" could
// pass a fix that broke XFP warning entirely, and asserting only "XFP lines
// == 1" would not have caught this defect at all.
func TestIsSecureRequest_UntrustedPeerDoesNotFloodLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"}) // the DEFAULT list
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()

	buf := captureSlog(t)

	for i := 0; i < 50; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		req.Header.Set("X-Forwarded-Proto", "https")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		IsSecureRequest(c)
	}

	logOutput := buf.String()
	xfpWarnLines := strings.Count(logOutput, "X-Forwarded-Proto received from an untrusted peer")
	cidrWarnLines := strings.Count(logOutput, "Invalid trusted network")

	if xfpWarnLines != 1 {
		t.Errorf("XFP warn-once lines = %d, want exactly 1 over 50 requests from the same peer\nlog:\n%s", xfpWarnLines, logOutput)
	}
	if cidrWarnLines > 1 {
		t.Errorf("invalid-trusted-network warn lines = %d, want <= 1 - bare IPs in the default list must not warn at all, and even a genuinely malformed entry must warn once, not once per request\nlog:\n%s", cidrWarnLines, logOutput)
	}
}

// TestWarnUntrustedForwardedProto_CapsAtLimit guards the
// untrustedForwardedProtoWarnLimit budget added alongside the peer-trust
// gate in proxytrust.go: warnings must stop growing on attacker-chosen
// input (one distinct peer per forged request) beyond the cap, rather than
// the map growing unbounded. Mirrors the pre-existing warnUntrustedProxy
// pattern, which this bead's own report flagged as having no such test
// either.
func TestWarnUntrustedForwardedProto_CapsAtLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"})
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()

	buf := captureSlog(t)

	const distinctPeers = untrustedForwardedProtoWarnLimit + 6
	for i := 0; i < distinctPeers; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.1.%d:1234", i)
		req.Header.Set("X-Forwarded-Proto", "https")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		IsSecureRequest(c)
	}

	xfpWarnLines := strings.Count(buf.String(), "X-Forwarded-Proto received from an untrusted peer")
	if xfpWarnLines != untrustedForwardedProtoWarnLimit {
		t.Errorf("XFP warn lines from %d distinct peers = %d, want exactly %d (untrustedForwardedProtoWarnLimit)",
			distinctPeers, xfpWarnLines, untrustedForwardedProtoWarnLimit)
	}
}
