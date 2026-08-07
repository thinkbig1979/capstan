package middleware

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Log message prefixes asserted on throughout this file. Kept as constants so
// a test counting "the named warning" cannot accidentally also count the
// rollup line, which is a different message about a different thing.
const (
	msgXFPNamed    = "X-Forwarded-Proto received from an untrusted peer"
	msgXFPRollup   = "X-Forwarded-Proto is still arriving from untrusted peers"
	msgProxyNamed  = "Forwarding header received from an untrusted peer"
	msgProxyRollup = "Forwarding headers are still arriving from untrusted peers"
)

// captureSlog redirects the process-wide slog default to a buffer for the
// duration of the test and restores the previous default on cleanup. Mirrors
// internal/config/config_test.go's and internal/services/git_credentials_test.go's
// helper of the same name.
//
// The TEXT handler is pinned on purpose, not incidentally. The rollup
// assertions below match on the slog TEXT wire format — "elapsed=2h0m0s",
// "suppressed_requests=508", "named_peers=8" — so they depend on BOTH the key
// names and "key=value" rendering. Swap this for a JSON handler and every one
// of them fails against `"elapsed":"2h0m0s"` while the code under test is
// perfectly correct. It already bit once: renaming the log key from "since" to
// "elapsed" broke exactly these lines.
//
// This matches the production DEFAULT, which is text: internal/logging's
// ConfigureFromEnv falls back to FormatText when LOG_FORMAT is unset, and only
// builds a JSON handler when an operator sets LOG_FORMAT=json. So these
// assertions are checking the format most deployments actually emit — but a
// runbook that tells an operator to grep for "elapsed=" is wrong for the JSON
// deployments, where the same field is "elapsed":"...".
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// resetUntrustedWarnBudget returns a budget to its zero state between tests so
// one test's peers don't silence another's assertions regardless of run order.
//
// It clears EVERY field, not just peers. Clearing only the map was enough
// before agent-os-coi added the window and rollup fields, and leaving them
// behind is not a cosmetic leak: OBSERVED 2026-08-07, after a peers-only
// "reset" one clean request emitted a rollup carrying the PREVIOUS test's
// suppressed_requests, which makes any exact-count assertion on rollup content
// depend on run order. prev matters just as much — a stale carry-over set
// would name a peer that this test never warned about.
func resetUntrustedWarnBudget(b *untrustedWarnBudget) {
	b.mu.Lock()
	b.peers = nil
	b.prev = nil
	b.windowStart = time.Time{}
	b.suppressed = 0
	b.mu.Unlock()
}

func resetUntrustedForwardedProtoWarned() {
	resetUntrustedWarnBudget(&untrustedForwardedProtoWarned)
}

// resetUntrustedProxyWarned is the sibling for the forwarding-header budget,
// which had no reset helper at all before agent-os-coi — its only test
// (TestTrustedProxyWarning_DoesNotAlterRequests in ratelimit_test.go) asserts
// on the response rather than on log lines, so nothing needed one until now.
func resetUntrustedProxyWarned() {
	resetUntrustedWarnBudget(&untrustedProxyWarned)
}

// rewindUntrustedWarnWindow moves a budget's window start into the past so the
// next occurrence rolls the window over. This is how the rollover tests below
// stay deterministic: shortening untrustedWarnWindow instead makes the number
// of rollovers a function of wall-clock scheduling (OBSERVED: 63 / 31 / 12
// rollup lines from one identical flood at window = 1ms / 2ms / 5ms).
// A budget that has never seen an occurrence is left alone. Its windowStart is
// the zero Time, and rewinding that produces a far-past but NON-zero time, so
// the next admit would take the rollover branch instead of the IsZero branch —
// a test that rewound before its first admit would silently exercise a
// different path from the one it appears to.
func rewindUntrustedWarnWindow(b *untrustedWarnBudget, by time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.windowStart.IsZero() {
		return
	}
	b.windowStart = b.windowStart.Add(-by)
}

// budgetState reads a budget's internals for assertions that log lines cannot
// make: "zero suppressed increments", which is invisible from the outside
// until a window rolls over, and the sizes of the two maps, which is how the
// carry-over's memory bound is asserted directly rather than inferred from
// line counts.
func budgetState(b *untrustedWarnBudget) (peers, prev int, suppressed uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.peers), len(b.prev), b.suppressed
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

// TestWarnUntrustedForwardedProto_CapsAtLimit guards the named-peer budget
// added alongside the peer-trust gate in proxytrust.go: warnings must stop
// growing on attacker-chosen input (one distinct peer per forged request)
// beyond the cap, rather than the map growing unbounded. Renamed constant
// only by agent-os-coi (untrustedForwardedProtoWarnLimit ->
// untrustedWarnPeerLimit, now shared with warnUntrustedProxy); the assertion
// is unchanged.
//
// NOTE for anyone treating this as the regression test for agent-os-coi: it
// is not, and cannot be. It is written symbolically against the constant, so
// it passes at ANY cap value, and it says nothing about whether a burned
// budget ever recovers. The tests that gate agent-os-coi are
// TestWarnUntrustedForwardedProto_BurnedBudgetRefillsAndRollsUp and its
// warnUntrustedProxy sibling below.
func TestWarnUntrustedForwardedProto_CapsAtLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"})
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()

	buf := captureSlog(t)

	const distinctPeers = untrustedWarnPeerLimit + 6
	for i := 0; i < distinctPeers; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.1.%d:1234", i)
		req.Header.Set("X-Forwarded-Proto", "https")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		IsSecureRequest(c)
	}

	xfpWarnLines := strings.Count(buf.String(), msgXFPNamed)
	if xfpWarnLines != untrustedWarnPeerLimit {
		t.Errorf("XFP warn lines from %d distinct peers = %d, want exactly %d (untrustedWarnPeerLimit)",
			distinctPeers, xfpWarnLines, untrustedWarnPeerLimit)
	}
}

// TestWarnInvalidTrustedNetworkOnce_CapsAtLimit is the sibling of
// TestWarnUntrustedForwardedProto_CapsAtLimit above, for
// invalidTrustedNetworkWarnLimit (auth.go) rather than
// untrustedWarnPeerLimit (proxytrust.go). Flagged in this bead's
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

// wireRequest builds a request by parsing an actual HTTP wire representation
// rather than by calling Header.Set. That distinction matters for the
// no-protocol-claim cases below: a bare "X-Forwarded-Proto:" line is what a
// client actually sends, and net/http parses it into []string{""} — length 1,
// which is why it used to pass a len(values)==0 guard and reach the warn path
// (OBSERVED 2026-08-07 against cb2431c: one such header burned one budget
// slot). Header.Set("X-Forwarded-Proto", "") produces the same slice, but
// going through the parser removes any doubt that the shape is reachable.
func wireRequest(t *testing.T, remoteIP string, headerLines ...string) *http.Request {
	t.Helper()
	raw := "GET / HTTP/1.1\r\nHost: x\r\n" + strings.Join(headerLines, "\r\n") + "\r\n\r\n"
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("wire parse %q: %v", raw, err)
	}
	req.RemoteAddr = remoteIP + ":1234"
	return req
}

// sendForwardedProto runs one request carrying a genuine "https" claim from
// remoteIP through IsSecureRequest, which is the path that reaches
// warnUntrustedForwardedProto.
func sendForwardedProto(t *testing.T, remoteIP string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remoteIP + ":1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	IsSecureRequest(c)
}

// TestWarnUntrustedForwardedProto_BurnedBudgetRefillsAndRollsUp is the
// regression test for agent-os-coi, and it is the only thing gating that
// change: TestWarnUntrustedForwardedProto_CapsAtLimit above is written
// symbolically against the constant and stays green at any cap, so the
// existing suite passes both before and after.
//
// The defect: the warn-once peer map was keyed on the attacker-chosen source
// address, capped, and never reset. OBSERVED 2026-08-07 against cb2431c —
// after 64 requests from 64 distinct peers, 500 requests from a genuinely-
// misconfigured proxy at a new address produced 0 warn lines, for the
// remaining life of the process. That warning is the only runtime signal that
// a deployment has silently dropped Secure from its cookies and suppressed
// HSTS on a genuinely-HTTPS site.
//
// This asserts BOTH halves on one instrument, because either alone is
// satisfiable by a broken fix:
//
//   - Signal survives: after the window rolls over, the genuine proxy is
//     named again, and the requests suppressed in the meantime are reported
//     as an exact count. A fix that only lowered the cap fails here.
//   - Volume stays bounded: no more than untrustedWarnNamedCeiling+1 lines in
//     any one window. The bound is 2N rather than N because carry-over peers
//     bypass the cap and can be named on top of N fresh names; see that
//     constant. A "fix" that simply removed the cap fails here.
//
// The flood vector is "X-Forwarded-Proto: https", NOT the empty header the
// bead originally named: since agent-os-coi the empty header is treated as
// absent and never reaches this budget at all, so a flood test written to it
// would pass vacuously. That shape is owned by
// TestIsSecureRequest_NoProtocolClaimIsTreatedAsAbsent below.
func TestWarnUntrustedForwardedProto_BurnedBudgetRefillsAndRollsUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"})
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()

	const (
		overflowPeers   = 8
		burnPeers       = untrustedWarnPeerLimit + overflowPeers
		genuineRequests = 500
		genuinePeer     = "192.168.50.7"
	)
	// --- Window 1: burn the named-peer budget from distinct addresses. ---
	burn := captureSlog(t)
	for i := 0; i < burnPeers; i++ {
		sendForwardedProto(t, fmt.Sprintf("10.0.1.%d", i))
	}
	if got := strings.Count(burn.String(), msgXFPNamed); got != untrustedWarnPeerLimit {
		t.Errorf("named warn lines from %d distinct peers = %d, want exactly %d (untrustedWarnPeerLimit)",
			burnPeers, got, untrustedWarnPeerLimit)
	}
	if got := strings.Count(burn.String(), msgXFPRollup); got != 0 {
		t.Errorf("rollup lines inside an un-elapsed window = %d, want 0", got)
	}

	// --- Window 1: the real misconfiguration, at a new address, over and
	// over. Silence here is CORRECT and is not the defect: the budget for
	// this window is legitimately spent. The defect was that this silence
	// was permanent. ---
	during := captureSlog(t)
	for i := 0; i < genuineRequests; i++ {
		sendForwardedProto(t, genuinePeer)
	}
	if got := strings.Count(during.String(), msgXFPNamed) + strings.Count(during.String(), msgXFPRollup); got != 0 {
		t.Errorf("lines emitted after the window's budget was spent = %d, want 0\nlog:\n%s", got, during.String())
	}

	const wantSuppressed = overflowPeers + genuineRequests
	if _, _, suppressed := budgetState(&untrustedForwardedProtoWarned); suppressed != wantSuppressed {
		t.Errorf("suppressed after window 1 = %d, want %d", suppressed, wantSuppressed)
	}

	// --- Window 2: rewind rather than sleep, then send ONE more request from
	// the same genuine proxy. ---
	rewindUntrustedWarnWindow(&untrustedForwardedProtoWarned, 2*untrustedWarnWindow)

	after := captureSlog(t)
	sendForwardedProto(t, genuinePeer)
	out := after.String()

	if got := strings.Count(out, msgXFPNamed); got != 1 {
		t.Errorf("named warn lines for the genuine proxy in the window AFTER the burn = %d, want exactly 1 - a burned budget must refill, this is the whole defect\nlog:\n%s", got, out)
	}
	if got := strings.Count(out, msgXFPRollup); got != 1 {
		t.Errorf("rollup lines on the first occurrence after the window elapsed = %d, want exactly 1\nlog:\n%s", got, out)
	}
	// The bound is 2N+1, not N+1, and the reason is worth stating here rather
	// than only at untrustedWarnNamedCeiling, because the observed numbers
	// never approach it and that invites a later reader to "simplify" it to N.
	// The two admission routes are independent: carry-over peers bypass the
	// cap, so up to N of them can be named on top of N fresh names. Which one
	// you get is ORDER-DEPENDENT. If the persistent peers arrive first they
	// are added to peers and so consume the very slots the fresh ones would
	// have taken, and the window lands at N — which is why a flood reusing
	// 1000, 16 or 8 addresses every window all measure 8.0 lines/window rather
	// than anywhere near 16. Reaching 2N requires the fresh names to fill the cap
	// FIRST, after which a carry-over peer still gets in past it. Both orders
	// are ordinary traffic, so the assertion has to be written at 2N even
	// though the common case sits at N.
	if total := strings.Count(out, msgXFPNamed) + strings.Count(out, msgXFPRollup); total > untrustedWarnNamedCeiling+1 {
		t.Errorf("total lines in window 2 = %d, want <= %d", total, untrustedWarnNamedCeiling+1)
	}
	// Exact suppressed REQUESTS, not a distinct-peer count: an exact distinct
	// count would need a set that grows one entry per source address, and a
	// capped one would print 8 when the truth is 508.
	if want := fmt.Sprintf("suppressed_requests=%d", wantSuppressed); !strings.Contains(out, want) {
		t.Errorf("rollup does not carry %q\nlog:\n%s", want, out)
	}
	// named_peers is what replaced the old sample field: it says whether the
	// per-peer lines above were the whole set or a truncated view of it.
	if want := fmt.Sprintf("named_peers=%d", untrustedWarnPeerLimit); !strings.Contains(out, want) {
		t.Errorf("rollup does not carry %q\nlog:\n%s", want, out)
	}
	if strings.Contains(out, "sample=") {
		t.Errorf("rollup carries a sample peer - that field was removed, it never held the genuine proxy's address\nlog:\n%s", out)
	}
	// The ACTUAL elapsed span, not the nominal constant. A sketch that logged
	// the constant printed window=1h0m0s over a ten-hour span, which reads as
	// a rate wrong by 10x.
	if !strings.Contains(out, "elapsed=2h0m0s") {
		t.Errorf("rollup does not report the actual 2h span\nlog:\n%s", out)
	}
	if strings.Contains(out, "elapsed="+untrustedWarnWindow.String()) {
		t.Errorf("rollup reports the nominal window constant instead of the elapsed span\nlog:\n%s", out)
	}
}

// TestWarnUntrustedProxy_BurnedBudgetRefillsAndRollsUp is the same guarantee
// for the pre-existing sibling. warnUntrustedProxy predates the X-Forwarded-
// Proto gate and had the identical never-resetting peer cap; agent-os-coi
// covers both because fixing one and leaving the other one function away is
// incoherent. Called directly rather than through TrustedProxyWarning: the
// middleware only reaches it when gin's ClientIP() equals RemoteIP(), which
// is orthogonal to the budget being tested here.
func TestWarnUntrustedProxy_BurnedBudgetRefillsAndRollsUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetUntrustedProxyWarned()
	defer resetUntrustedProxyWarned()

	const (
		overflowPeers   = 8
		burnPeers       = untrustedWarnPeerLimit + overflowPeers
		genuineRequests = 500
		genuinePeer     = "192.168.60.7"
		header          = "X-Forwarded-For"
	)
	burn := captureSlog(t)
	for i := 0; i < burnPeers; i++ {
		warnUntrustedProxy(fmt.Sprintf("10.0.2.%d", i), header)
	}
	for i := 0; i < genuineRequests; i++ {
		warnUntrustedProxy(genuinePeer, header)
	}
	if got := strings.Count(burn.String(), msgProxyNamed); got != untrustedWarnPeerLimit {
		t.Errorf("named warn lines in window 1 = %d, want exactly %d (untrustedWarnPeerLimit) over %d occurrences",
			got, untrustedWarnPeerLimit, burnPeers+genuineRequests)
	}
	if got := strings.Count(burn.String(), msgProxyRollup); got != 0 {
		t.Errorf("rollup lines inside an un-elapsed window = %d, want 0", got)
	}

	rewindUntrustedWarnWindow(&untrustedProxyWarned, 2*untrustedWarnWindow)

	after := captureSlog(t)
	warnUntrustedProxy(genuinePeer, header)
	out := after.String()

	if got := strings.Count(out, msgProxyNamed); got != 1 {
		t.Errorf("named warn lines for the genuine proxy in the window AFTER the burn = %d, want exactly 1\nlog:\n%s", got, out)
	}
	if got := strings.Count(out, msgProxyRollup); got != 1 {
		t.Errorf("rollup lines on the first occurrence after the window elapsed = %d, want exactly 1\nlog:\n%s", got, out)
	}
	const wantSuppressed = overflowPeers + genuineRequests
	if want := fmt.Sprintf("suppressed_requests=%d", wantSuppressed); !strings.Contains(out, want) {
		t.Errorf("rollup does not carry %q\nlog:\n%s", want, out)
	}
	if want := fmt.Sprintf("named_peers=%d", untrustedWarnPeerLimit); !strings.Contains(out, want) {
		t.Errorf("rollup does not carry %q\nlog:\n%s", want, out)
	}
	if strings.Contains(out, "sample=") {
		t.Errorf("rollup carries a sample peer - that field was removed\nlog:\n%s", out)
	}
	if !strings.Contains(out, "elapsed=2h0m0s") {
		t.Errorf("rollup does not report the actual 2h span\nlog:\n%s", out)
	}
}

// TestWarnUntrustedForwardedProto_QuietWindowEmitsNoRollup covers the other
// side of the rollover: a window that elapsed with nothing suppressed must
// refill the named-peer budget (so the same peer is warned about again, which
// is how an operator learns the condition PERSISTS) without emitting a rollup
// about zero requests.
func TestWarnUntrustedForwardedProto_QuietWindowEmitsNoRollup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"})
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()

	buf := captureSlog(t)
	sendForwardedProto(t, "192.168.50.8")
	sendForwardedProto(t, "192.168.50.8") // same peer, same window: warn-once
	if got := strings.Count(buf.String(), msgXFPNamed); got != 1 {
		t.Fatalf("named warn lines for one peer in one window = %d, want 1", got)
	}

	rewindUntrustedWarnWindow(&untrustedForwardedProtoWarned, 2*untrustedWarnWindow)

	after := captureSlog(t)
	sendForwardedProto(t, "192.168.50.8")
	if got := strings.Count(after.String(), msgXFPNamed); got != 1 {
		t.Errorf("named warn lines for the same peer in the NEXT window = %d, want 1 - the signal must recur\nlog:\n%s", got, after.String())
	}
	if got := strings.Count(after.String(), msgXFPRollup); got != 0 {
		t.Errorf("rollup lines after a window with nothing suppressed = %d, want 0\nlog:\n%s", got, after.String())
	}
}

// TestIsSecureRequest_NoProtocolClaimIsTreatedAsAbsent owns the vector that
// agent-os-coi's flood test deliberately does not use: an X-Forwarded-Proto
// that is present on the wire but carries no protocol claim. Before the fix,
// the guard was len(values)==0, and net/http parses a bare
// "X-Forwarded-Proto:" into []string{""} — length 1 — so the header reached
// warnUntrustedForwardedProto and spent a budget slot at a cost to the
// attacker of one header line (OBSERVED 2026-08-07 against cb2431c).
//
// The guard is lastForwardedProto(values)=="" and NOT "every value is empty".
// The comma-only rows below are why: they have a non-empty value, so an
// all-values-empty test would let each of them through, and each costs the
// attacker one extra character.
//
// Both sides are asserted on one instrument. Without the two positive
// controls at the end, every assertion here would still pass if the warning
// were deleted outright, or if IsSecureRequest were hardcoded to false.
func TestIsSecureRequest_NoProtocolClaimIsTreatedAsAbsent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"})
	defer InitTrustedProxyNetworks(nil)
	defer resetUntrustedForwardedProtoWarned()

	noClaim := []struct {
		name        string
		headerLines []string
	}{
		{"bare header, no value", []string{"X-Forwarded-Proto:"}},
		{"single comma", []string{"X-Forwarded-Proto: ,"}},
		{"only commas", []string{"X-Forwarded-Proto: ,,,"}},
		{"comma with spaces", []string{"X-Forwarded-Proto:  ,  "}},
		{"two instances, neither claiming", []string{"X-Forwarded-Proto:", "X-Forwarded-Proto: ,"}},
	}

	for _, tc := range noClaim {
		t.Run(tc.name, func(t *testing.T) {
			resetUntrustedForwardedProtoWarned()
			buf := captureSlog(t)

			req := wireRequest(t, "10.0.4.1", tc.headerLines...)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			if IsSecureRequest(c) {
				t.Errorf("IsSecureRequest = true for %q, want false", req.Header.Values("X-Forwarded-Proto"))
			}

			out := buf.String()
			if got := strings.Count(out, msgXFPNamed) + strings.Count(out, msgXFPRollup); got != 0 {
				t.Errorf("warn lines for a header carrying no protocol claim (%q) = %d, want exactly 0\nlog:\n%s",
					req.Header.Values("X-Forwarded-Proto"), got, out)
			}
			// Zero warn lines alone would also hold if the occurrence were
			// silently counted as suppressed, which is still a budget spend
			// an attacker controls. Assert the budget was not touched at all.
			peers, _, suppressed := budgetState(&untrustedForwardedProtoWarned)
			if peers != 0 || suppressed != 0 {
				t.Errorf("budget after a no-claim header = (peers %d, suppressed %d), want (0, 0)", peers, suppressed)
			}

			// A trusted peer must reach the same answer. This is the half
			// that shows the change is logging-only: no input flips the
			// security decision, in either trust state.
			resetUntrustedForwardedProtoWarned()
			trusted := wireRequest(t, "127.0.0.1", tc.headerLines...)
			tc2, _ := gin.CreateTestContext(httptest.NewRecorder())
			tc2.Request = trusted
			if IsSecureRequest(tc2) {
				t.Errorf("IsSecureRequest = true from a TRUSTED peer for %q, want false - no protocol claim is not an HTTPS claim",
					trusted.Header.Values("X-Forwarded-Proto"))
			}
		})
	}

	t.Run("positive control: a real claim from an untrusted peer still warns", func(t *testing.T) {
		resetUntrustedForwardedProtoWarned()
		buf := captureSlog(t)

		req := wireRequest(t, "10.0.4.2", "X-Forwarded-Proto: https")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		if IsSecureRequest(c) {
			t.Error("IsSecureRequest = true from an untrusted peer, want false")
		}
		if got := strings.Count(buf.String(), msgXFPNamed); got != 1 {
			t.Fatalf("named warn lines for a genuine claim = %d, want 1 - the instrument does not fire, so the zero-line assertions above prove nothing", got)
		}
		if peers, _, _ := budgetState(&untrustedForwardedProtoWarned); peers != 1 {
			t.Errorf("budget peers after a genuine claim = %d, want 1", peers)
		}
	})

	// This looks redundant with the untrusted positive control above. It is
	// not: it is the only assertion in THIS test that requires a `true`
	// return, and without it the test proves only that IsSecureRequest can say
	// no. VERIFIED by mutation 2026-08-07: replacing the final
	// strings.EqualFold(proto, "https") with `return false` — which breaks the
	// honoured path while leaving the warn path intact — passes all five
	// no-claim subtests AND the untrusted control, and fails here alone.
	//
	// Scoped honestly: this is not the last line of defence for that mutation.
	// TestIsSecureRequest_TrustsLastForwardedProtoValue and
	// TestIsSecureRequest_GatesForwardedProtoOnPeerTrust catch it too, and a
	// cruder `return false` at the top of the function is additionally caught
	// by the untrusted control's log-line assertion. What this subtest buys is
	// that this test stands on its own.
	t.Run("positive control: a real claim from a trusted peer is still honoured", func(t *testing.T) {
		resetUntrustedForwardedProtoWarned()
		req := wireRequest(t, "127.0.0.1", "X-Forwarded-Proto: https")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		if !IsSecureRequest(c) {
			t.Error("IsSecureRequest = false for a trusted proxy claiming https, want true")
		}
	})
}

// TestWarnUntrustedForwardedProto_RecurringPeerSurvivesASustainedFlood is the
// carry-over's own regression test, and it covers the case the burned-budget
// test above cannot: a genuine proxy that has to compete for attention in
// EVERY window against a flood large enough to own every fresh slot.
//
// Without carry-over, N is the only lever, and it is a weak one. MEASURED
// 2026-08-07 over 100 windows with 1000 fresh flood peers per window, counting
// windows in which the genuine proxy gets named at all: N=8 -> 3/100, N=32 ->
// 5/100, N=64 -> 9/100, N=256 -> 31/100, against a no-flood control of
// 100/100. With carry-over at N=8 the same scenario gives 46/100, and a light
// flood goes from 46/100 to 100/100. The 46 is not a coin flip: once the peer
// wins a slot it is in prev, so it is named next window, so it is in prev
// again. Visibility is permanent from the first window it lands.
//
// Both sides on one instrument, because "name the recurring peer" is trivially
// satisfiable by removing the cap: this asserts that the recurring peer IS
// named AND that the fresh flood peers beyond the cap are still suppressed in
// the same window.
func TestWarnUntrustedForwardedProto_RecurringPeerSurvivesASustainedFlood(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitTrustedProxyNetworks([]string{"127.0.0.1", "::1"})
	defer InitTrustedProxyNetworks(nil)
	resetUntrustedForwardedProtoWarned()
	defer resetUntrustedForwardedProtoWarned()

	const (
		genuinePeer = "192.168.70.7"
		floodPeers  = 200 // far past the cap, so every fresh slot is spoken for
	)

	// Window 1: the genuine proxy gets in early, then the flood arrives. This
	// is the "first lucky window" the measurement describes.
	w1 := captureSlog(t)
	sendForwardedProto(t, genuinePeer)
	for i := 0; i < floodPeers; i++ {
		sendForwardedProto(t, fmt.Sprintf("10.1.%d.%d", i/256, i%256))
	}
	if got := strings.Count(w1.String(), msgXFPNamed); got != untrustedWarnPeerLimit {
		t.Fatalf("window 1 named lines = %d, want %d - fixture is wrong, the flood must fill the budget",
			got, untrustedWarnPeerLimit)
	}

	// Window 2: the flood goes FIRST this time, taking every fresh slot before
	// the genuine proxy is heard from. Without carry-over this is exactly the
	// window in which the operator's proxy is invisible.
	rewindUntrustedWarnWindow(&untrustedForwardedProtoWarned, 2*untrustedWarnWindow)

	w2 := captureSlog(t)
	for i := 0; i < floodPeers; i++ {
		sendForwardedProto(t, fmt.Sprintf("10.2.%d.%d", i/256, i%256))
	}
	freshNamed := strings.Count(w2.String(), msgXFPNamed)
	if freshNamed != untrustedWarnPeerLimit {
		t.Fatalf("window 2 named lines from the fresh flood = %d, want %d", freshNamed, untrustedWarnPeerLimit)
	}
	_, _, suppressedAfterFlood := budgetState(&untrustedForwardedProtoWarned)
	if suppressedAfterFlood == 0 {
		t.Fatal("fresh flood peers beyond the cap were not suppressed - the cap is not doing its job")
	}

	sendForwardedProto(t, genuinePeer)

	if got := strings.Count(w2.String(), msgXFPNamed); got != freshNamed+1 {
		t.Errorf("the recurring peer was not named in window 2 (lines %d, want %d) - carry-over is the only route by which a persistent peer stays visible once a flood owns every fresh slot\nlog:\n%s",
			got, freshNamed+1, w2.String())
	}
	// The other side: naming the recurring peer must not have reopened the
	// gate for everyone else. Fresh peers are still suppressed after it.
	_, _, suppressedAfter := budgetState(&untrustedForwardedProtoWarned)
	sendForwardedProto(t, "10.9.9.9")
	if _, _, s := budgetState(&untrustedForwardedProtoWarned); s != suppressedAfter+1 {
		t.Errorf("a fresh peer after the carry-over was not suppressed (suppressed %d -> %d, want +1) - carry-over must not be a general cap bypass", suppressedAfter, s)
	}
}

// TestWarnUntrustedProxy_RecurringPeerSurvivesASustainedFlood is the same
// guarantee for the pre-existing sibling. The carry-over was measured only on
// the X-Forwarded-Proto side, so this verifies rather than assumes that the
// shared budget composes identically here.
func TestWarnUntrustedProxy_RecurringPeerSurvivesASustainedFlood(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetUntrustedProxyWarned()
	defer resetUntrustedProxyWarned()

	const (
		genuinePeer = "192.168.80.7"
		floodPeers  = 200
		header      = "X-Forwarded-For"
	)

	w1 := captureSlog(t)
	warnUntrustedProxy(genuinePeer, header)
	for i := 0; i < floodPeers; i++ {
		warnUntrustedProxy(fmt.Sprintf("10.3.%d.%d", i/256, i%256), header)
	}
	if got := strings.Count(w1.String(), msgProxyNamed); got != untrustedWarnPeerLimit {
		t.Fatalf("window 1 named lines = %d, want %d", got, untrustedWarnPeerLimit)
	}

	rewindUntrustedWarnWindow(&untrustedProxyWarned, 2*untrustedWarnWindow)

	w2 := captureSlog(t)
	for i := 0; i < floodPeers; i++ {
		warnUntrustedProxy(fmt.Sprintf("10.4.%d.%d", i/256, i%256), header)
	}
	freshNamed := strings.Count(w2.String(), msgProxyNamed)
	if freshNamed != untrustedWarnPeerLimit {
		t.Fatalf("window 2 named lines from the fresh flood = %d, want %d", freshNamed, untrustedWarnPeerLimit)
	}

	warnUntrustedProxy(genuinePeer, header)
	if got := strings.Count(w2.String(), msgProxyNamed); got != freshNamed+1 {
		t.Errorf("the recurring peer was not named in window 2 (lines %d, want %d)\nlog:\n%s",
			got, freshNamed+1, w2.String())
	}
}

// TestUntrustedWarnBudget_CarryOverStaysBoundedUnderAdversarialOrdering guards
// the memory bound that the carry-over design does not get for free.
//
// prev is the previous window's named set, and carry-over peers bypass the
// cap, so |peers| <= |prev| + N. Since prev is last window's peers, that
// recurrence grows the structure by N per window for as long as the previous
// window's names keep recurring — unbounded, and remotely driven, in the very
// map whose cap exists to stop remotely-driven growth.
//
// It needs an attacker who does two things, both cheap: RETAIN addresses
// across windows rather than using fresh ones, and send the fresh addresses
// FIRST so they take the N cap slots, leaving every retained address to enter
// through the bypass. This test replays exactly that, and asserts the hard
// ceiling holds. Remove the len(peers) < untrustedWarnNamedCeiling guard in
// admit and it fails within a few windows with a real, growing count.
//
// This is asserted on the budget's internals rather than on log lines because
// the growth IS the defect: a version that logged 2N lines per window while
// retaining thousands of map entries would pass a line-count assertion.
func TestUntrustedWarnBudget_CarryOverStaysBoundedUnderAdversarialOrdering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var b untrustedWarnBudget

	const windows = 25
	retained := make([]string, 0, windows*untrustedWarnPeerLimit)

	for w := 0; w < windows; w++ {
		// Fresh addresses first: they consume the N cap slots.
		for i := 0; i < untrustedWarnPeerLimit; i++ {
			addr := fmt.Sprintf("172.16.%d.%d", w, i)
			b.admit(addr)
			retained = append(retained, addr)
		}
		// Then every address ever used, each of which is in prev if it was
		// named last window, and so bypasses the cap.
		for _, addr := range retained {
			b.admit(addr)
		}

		peers, prev, _ := budgetState(&b)
		if peers > untrustedWarnNamedCeiling || prev > untrustedWarnNamedCeiling {
			t.Fatalf("window %d: budget grew past the ceiling (peers %d, prev %d, ceiling %d) - carry-over must not accumulate across windows",
				w, peers, prev, untrustedWarnNamedCeiling)
		}

		rewindUntrustedWarnWindow(&b, 2*untrustedWarnWindow)
	}

	// Positive control on the same instrument: the ceiling must be reachable,
	// otherwise the assertion above passes for the wrong reason (e.g. carry-
	// over silently not working at all, leaving peers at N forever).
	var reach untrustedWarnBudget
	for i := 0; i < untrustedWarnPeerLimit; i++ {
		reach.admit(fmt.Sprintf("172.20.0.%d", i))
	}
	rewindUntrustedWarnWindow(&reach, 2*untrustedWarnWindow)
	for i := 0; i < untrustedWarnPeerLimit; i++ { // fresh names fill the cap first
		reach.admit(fmt.Sprintf("172.20.1.%d", i))
	}
	for i := 0; i < untrustedWarnPeerLimit; i++ { // then the carry-overs bypass it
		reach.admit(fmt.Sprintf("172.20.0.%d", i))
	}
	if peers, _, _ := budgetState(&reach); peers != untrustedWarnNamedCeiling {
		t.Errorf("peers after fresh-then-carry-over = %d, want %d - the 2N bound must be REACHABLE, or the ceiling assertion above proves nothing",
			peers, untrustedWarnNamedCeiling)
	}
}
