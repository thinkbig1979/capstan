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

// resetInvalidTrustedNetworkWarned clears the warn-once malformed-entry set
// (auth.go) between tests, mirroring resetUntrustedForwardedProtoWarned
// above for the same reason: package-level state must not leak between
// tests regardless of run order.
func resetInvalidTrustedNetworkWarned() {
	invalidTrustedNetworkWarned.mu.Lock()
	invalidTrustedNetworkWarned.entries = make(map[string]struct{})
	invalidTrustedNetworkWarned.mu.Unlock()
}

// TestIsSecureRequest_BareIPsInTrustedListDoNotWarn guards agent-os-ab9's own
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
// This asserts exactly 0 invalid-trusted-network lines, not "<= 1": the
// fixture list here contains no malformed entry (both "127.0.0.1" and
// "::1" are valid IPs, just not CIDRs), so the only correct count is zero.
// A "<= 1" bound here would pass on zero OR on a broken fix that warns once
// total regardless of request count - it cannot tell "no warning is due"
// from "warned once, coincidentally within budget". The companion case
// where a warning genuinely IS due is
// TestIsSecureRequest_MalformedTrustedNetworkEntryWarnsOnceNotPerRequest
// below, which is what a reviewer flagged this original combined test as
// unable to prove: this file used to assert "<= 1" against a fixture with
// no malformed entry, which also passes if the warning is deleted entirely.
//
// Both counts (XFP and invalid-trusted-network) are still asserted together:
// asserting only the CIDR count could pass a fix that broke XFP warning
// entirely, and asserting only the XFP count would not catch this defect at
// all.
func TestIsSecureRequest_BareIPsInTrustedListDoNotWarn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"}) // the DEFAULT list, no malformed entry
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()
	resetInvalidTrustedNetworkWarned()
	defer resetInvalidTrustedNetworkWarned()

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
	if cidrWarnLines != 0 {
		t.Errorf("invalid-trusted-network warn lines = %d, want exactly 0 - bare IPs in the trusted list are valid configuration and must never warn\nlog:\n%s", cidrWarnLines, logOutput)
	}
}

// TestIsSecureRequest_MalformedTrustedNetworkEntryWarnsOnceNotPerRequest is
// the companion to the bare-IP case above: a TRUSTED_NETWORKS entry that is
// genuinely malformed (neither a valid IP nor a valid CIDR) still deserves a
// warning - operators need to know their config has a typo - but at most
// once per distinct entry, not once per request. The fixture list here
// deliberately DOES contain a malformed entry ("not-a-cidr"), so this is the
// case that catches deleting warnInvalidTrustedNetworkOnce's call entirely
// (which the sibling "== 0" test above cannot: it has nothing malformed to
// warn about in the first place). Asserts an exact count across many
// requests from the same peer, the same shape as
// TestWarnUntrustedForwardedProto_CapsAtLimit below and
// TestIsSecureRequest_UntrustedPeerDoesNotFloodLogs's original intent.
func TestIsSecureRequest_MalformedTrustedNetworkEntryWarnsOnceNotPerRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "not-a-cidr"})
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()
	resetInvalidTrustedNetworkWarned()
	defer resetInvalidTrustedNetworkWarned()

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
	cidrWarnLines := strings.Count(logOutput, "Invalid trusted network")
	if cidrWarnLines != 1 {
		t.Errorf("invalid-trusted-network warn lines from a genuinely malformed entry over 50 requests = %d, want exactly 1\nlog:\n%s", cidrWarnLines, logOutput)
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

// TestWarnInvalidTrustedNetworkOnce_CapsAtLimit is the sibling of
// TestWarnUntrustedForwardedProto_CapsAtLimit above, for
// invalidTrustedNetworkWarnLimit (auth.go) rather than
// untrustedForwardedProtoWarnLimit (proxytrust.go). Flagged in this bead's
// own report as a gap alongside the sibling cap, which had the same gap
// before this bead added a test for it. Unlike the peer cap, this budget is
// keyed by distinct malformed CONFIG entries, all evaluated within a single
// IsTrustedIP call, so one request already walks the whole list - the
// assertion is on the count produced by that one call, not on repetition.
func TestWarnInvalidTrustedNetworkOnce_CapsAtLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const malformedEntries = invalidTrustedNetworkWarnLimit + 6
	entries := make([]string, malformedEntries)
	for i := range entries {
		entries[i] = fmt.Sprintf("not-a-cidr-%d", i)
	}
	InitTrustedProxyNetworks(entries)
	defer InitTrustedProxyNetworks(nil)
	resetInvalidTrustedNetworkWarned()
	defer resetInvalidTrustedNetworkWarned()

	buf := captureSlog(t)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	IsSecureRequest(c)

	cidrWarnLines := strings.Count(buf.String(), "Invalid trusted network")
	if cidrWarnLines != invalidTrustedNetworkWarnLimit {
		t.Errorf("invalid-trusted-network warn lines from %d malformed entries = %d, want exactly %d (invalidTrustedNetworkWarnLimit)",
			malformedEntries, cidrWarnLines, invalidTrustedNetworkWarnLimit)
	}
}
