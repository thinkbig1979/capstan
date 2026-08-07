package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Proxy trust decides which address Capstan treats as the client, and getting it
// wrong is silent in both directions: a proxy that is not trusted collapses every
// user onto one address (shared rate limit bucket), and a proxy that forwards a
// client-supplied X-Forwarded-For instead of overwriting it lets a caller choose
// their own apparent address. This file makes both states visible in the log.
// AUTH_DISABLED is unaffected by any of this — it checks the real socket peer,
// never a forwarded header (agent-os-0s4, see middleware/auth.go).

// untrustedWarnPeerLimit caps how many distinct peers get their own named
// warning inside ONE window. Beyond it the peer map stops growing on
// attacker-chosen input and further occurrences are counted instead of logged
// — this is a misconfiguration signal, not an audit trail, so the first few
// addresses are the informative ones and the rest only need to be countable.
//
// Lowered from 64 to 8 by agent-os-coi. The old value was not a budget so much
// as a fuse: it never reset, decayed, or re-warned, so 64 requests from 64
// attacker-chosen source addresses silenced the warning for the remaining life
// of the process. OBSERVED 2026-08-07 against cb2431c: after that burn, 500
// requests from a genuinely-misconfigured proxy at a new address produced 0
// warn lines.
//
// The value is small on purpose, because raising it does almost nothing.
// MEASURED 2026-08-07 — windows in which the genuine proxy gets named, under a
// sustained flood of 1000 fresh peers per window over 100 windows:
//
//	N=8 -> 3/100    N=32 -> 5/100    N=64 -> 9/100    N=256 -> 31/100
//	positive control, no flood -> 100/100
//
// A 32x increase in N buys 3% -> 31% while costing 32x the log volume, so N is
// a weak lever and there is no trade here worth paying for. What actually
// rescues the genuine proxy is the carry-over set in untrustedWarnBudget below
// (3/100 -> 46/100 under the same heavy flood, and 46/100 -> 100/100 under a
// light one), because it discriminates on persistence rather than on order of
// arrival. N only has to be large enough to be useful when nothing is
// flooding.
const untrustedWarnPeerLimit = 8

// untrustedWarnWindow is how long a budget lasts before the named-peer slots
// refill and anything suppressed in the meantime is rolled up into one line.
//
// Deliberately NOT shortened to make tests fast. OBSERVED 2026-08-07 (adversary
// pass on this bead's plan): one identical 20000-request flood produced 63 / 31
// / 12 rollup lines at window = 1ms / 2ms / 5ms, because at those scales the
// number of rollovers is a function of wall-clock scheduling rather than of the
// input. A test that shortens the window is flaky by construction; tests rewind
// windowStart in-package instead (see rewindUntrustedWarnWindow in
// proxytrust_test.go).
const untrustedWarnWindow = time.Hour

// untrustedWarnBudget is the shared warn-once-plus-rollup budget behind both
// warnUntrustedProxy and warnUntrustedForwardedProto (agent-os-coi). Both
// warnings are driven entirely by attacker-reachable input — any peer can
// send a forwarding header from any source address — so neither can afford an
// unbounded map, and neither can afford a bound that never refills.
//
// Design notes, each of which was considered and is deliberate:
//
//   - The window-scoped clear of peers is the fix. A cap alone is a fuse that
//     an attacker burns once; a cap that refills every window bounds log
//     volume without ever making the condition permanently invisible.
//
//   - suppressed counts REQUESTS, not distinct peers, and it is exact. An
//     exact distinct-peer count needs a set that grows one entry per source
//     address: a single /16 sweep would put 65536 entries in the very
//     structure whose cap exists to stop remotely-driven map growth, turning
//     a diagnostic bug into an availability one. A CAPPED distinct set is
//     worse than useless — it saturates and lies, reporting 32 when the truth
//     is 50000. A uint64 of requests is O(1) and honest.
//
//   - The carry-over set (prev) is what makes the genuine proxy visible. On
//     rollover the previous window's named peers are RETAINED, and a peer
//     found there is named again without competing for a fresh slot. The
//     discriminator is persistence: a scanner's addresses are fresh every
//     window and never appear in prev, while a misconfigured proxy is one
//     fixed address and always does. It is sticky by construction — named
//     once puts a peer in prev, which names it next window, which puts it in
//     prev again — so visibility is permanent from the first window the peer
//     wins a slot, not a per-window coin flip. MEASURED: 3/100 windows ->
//     46/100 under a heavy flood, 46/100 -> 100/100 under a light one.
//
//   - There is deliberately NO sample-peer field. An earlier design had one,
//     write-once per window. MEASURED across every scenario and both
//     variants, it held the genuine misconfigured proxy's address in ZERO
//     cases: with no flood nothing is suppressed so it is never written at
//     all, and under a flood the attacker owns it either way, because under
//     write-once the FIRST over-budget peer is an attacker just as reliably
//     as the last one. It was the cheapest field in the line for an attacker
//     to control and it never once carried the address an operator needed.
//     named_peers replaces it and answers a question that is actually
//     answerable: were the per-peer lines above the whole set, or a truncated
//     sample of it?
//
//   - Plain sync.Mutex, not RWMutex. Every call mutates something (a map or
//     the counter), so there is no read-only path for readers to share and
//     RWMutex is strictly slower for a write-only workload. An atomic fast
//     path would be premature: the only traffic that reaches here is traffic
//     already being ignored.
//
//   - No background goroutine to flush a stale window. Emission is lazy, on
//     the next occurrence, which means a burst followed by total silence
//     leaves the final rollup unemitted until something else arrives — an
//     accepted trade. A timer goroutine would call slog.Warn from a
//     background thread while tests swap slog.Default() in and out via
//     captureSlog (proxytrust_test.go), which is a data race on the default
//     logger. (INFERRED from that helper, not executed under -race.)
//     ratelimit.go's `go rl.cleanup()` is a precedent in this package for the
//     opposite choice, and a poor one to copy: it has no done channel.
type untrustedWarnBudget struct {
	mu sync.Mutex
	// peers holds the addresses that have had a named warning line in the
	// CURRENT window. Bounded by untrustedWarnNamedCeiling, not by
	// untrustedWarnPeerLimit, because carry-over peers bypass the latter.
	peers map[string]struct{}
	// prev holds the PREVIOUS window's named peers. A peer found here is
	// named again without competing for a fresh slot. Replaced wholesale on
	// each rollover, never merged, so it cannot accumulate across windows.
	prev map[string]struct{}
	// windowStart is when the current window began; zero until the first
	// occurrence. Tests rewind it rather than shortening untrustedWarnWindow.
	windowStart time.Time
	// suppressed counts occurrences in the current window that would have
	// warned but were over the named-peer budget.
	suppressed uint64
}

// untrustedWarnNamedCeiling is the hard bound on named lines in one window,
// and therefore on the size of peers and of prev.
//
// It is 2N rather than N because the two admission routes are independent:
// carry-over peers bypass the cap, so up to N of them can be named on top of
// N fresh names. Reaching 2N requires a specific arrival order — the fresh
// peers must fill the cap FIRST, after which a carry-over peer still gets in.
// If the persistent peers arrive first they consume the same slots the fresh
// ones would have, and the window lands at N. Both orders are ordinary
// traffic, so the bound has to be stated at 2N even though the measured common
// case sits exactly at N: a flood reusing 1000, 16 or 8 addresses every window
// produces 8.0 named lines/window in all three cases (MEASURED 2026-08-07
// against this code).
//
// It is ENFORCED, not merely documented, and that is load-bearing. Without an
// explicit ceiling the structure grows without bound across windows: prev is
// last window's peers, so |peers| <= |prev| + N gives |peers| growing by N per
// window as long as the previous window's names keep recurring. An attacker
// who retains addresses and sends fresh ones first, recurring ones second,
// drives exactly that — VERIFIED by
// TestUntrustedWarnBudget_CarryOverStaysBoundedUnderAdversarialOrdering, which
// grows past 2N within a handful of windows if the ceiling is removed. That
// would be remotely-driven unbounded map growth in the very structure whose
// cap exists to prevent it, which is the failure this whole design is built to
// avoid. Found 2026-08-07 while implementing agent-os-coi; the carry-over
// design as specified asserted "prev holds at most N entries", which does not
// hold once peers itself can reach 2N.
const untrustedWarnNamedCeiling = 2 * untrustedWarnPeerLimit

// untrustedWarnRollup summarises one elapsed window. Returned by admit so the
// caller can log it OUTSIDE the mutex, as the named warning already is.
type untrustedWarnRollup struct {
	suppressed uint64
	// namedPeers is len(peers) at rollover: how many addresses got their own
	// line in the window being closed. It tells the operator whether those
	// lines were the whole set or a truncated sample of a much larger one.
	namedPeers int
	// elapsed is the ACTUAL span of the window being closed, not
	// untrustedWarnWindow. Under lazy emission the two differ without bound:
	// OBSERVED 2026-08-07, a sketch that logged the nominal constant printed
	// "window=1h0m0s" over a span of ten hours, which an operator reads as a
	// rate that is wrong by 10x. Logged under the key "elapsed" rather than
	// "since": the value is a duration, and "since=2h0m0s" reads as a
	// timestamp someone formatted wrong.
	elapsed time.Duration
}

// admit records one occurrence from remoteIP and reports what the caller
// should log. named is true when this peer has earned its own warning line in
// the current window. rollup is non-nil when a window just elapsed with
// suppressed occurrences in it; the caller must log it whether or not named
// is also true, because a single call can legitimately produce both — the
// rollup for the window that just closed, and a named line for the same peer
// under the refilled budget.
//
// Rollover is evaluated at the TOP of every call, ahead of the already-seen
// check, rather than only when an occurrence is suppressed. That ordering is
// load-bearing: a peer already in the map short-circuits at the seen check,
// so a rollover placed after it would never fire while traffic comes from
// already-warned peers — which is the realistic deployment shape, since the
// genuinely-misconfigured proxy is one fixed address hitting every request.
// The window would never roll, the budget would never refill, and the pending
// rollup would never emit: a smaller copy of the defect this function exists
// to fix.
func (b *untrustedWarnBudget) admit(remoteIP string) (named bool, rollup *untrustedWarnRollup) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Read the clock INSIDE the lock. Read outside it, two callers can enter
	// with out-of-order timestamps, and the one that locks second computes a
	// negative elapsed and skips a rollover it was entitled to. The next call
	// fires it, so nothing is lost permanently — but elapsed is the field an
	// operator divides by to get a rate, and one time.Now() under an
	// uncontended mutex costs nothing.
	now := time.Now()

	if b.windowStart.IsZero() {
		b.windowStart = now
	} else if elapsed := now.Sub(b.windowStart); elapsed >= untrustedWarnWindow {
		if b.suppressed > 0 {
			rollup = &untrustedWarnRollup{
				suppressed: b.suppressed,
				namedPeers: len(b.peers),
				elapsed:    elapsed.Round(time.Second),
			}
		}
		// Retain, do not discard: this window's named peers become next
		// window's carry-over set. Assignment rather than merge, so prev
		// never spans more than one rollover.
		b.prev = b.peers
		b.peers = nil
		b.suppressed = 0
		b.windowStart = now
	}

	if b.peers == nil {
		b.peers = make(map[string]struct{})
	}
	if _, seen := b.peers[remoteIP]; seen {
		return false, rollup
	}
	// A peer that was named last window is named again, bypassing the
	// per-window cap. This is the carry-over, and the bypass IS the
	// mechanism: it is the only route by which a persistent peer stays
	// visible once a flood of fresh addresses owns every fresh slot.
	// Still subject to untrustedWarnNamedCeiling — see that constant for the
	// unbounded growth an unenforced ceiling allows.
	if _, recurring := b.prev[remoteIP]; recurring && len(b.peers) < untrustedWarnNamedCeiling {
		b.peers[remoteIP] = struct{}{}
		return true, rollup
	}
	if len(b.peers) >= untrustedWarnPeerLimit {
		b.suppressed++
		return false, rollup
	}
	b.peers[remoteIP] = struct{}{}
	return true, rollup
}

var untrustedProxyWarned untrustedWarnBudget

// trustedProxyNetworks holds the effective trusted-proxy network list used to
// decide whether X-Forwarded-Proto is honoured (agent-os-ab9). It is set once
// at startup by InitTrustedProxyNetworks from the SAME effective list handed
// to gin's SetTrustedProxies in main.go — gin's trusted-proxy machinery only
// governs X-Forwarded-For/X-Real-IP (RemoteIPHeaders), it never touches
// X-Forwarded-Proto, so without this the header was honoured from any peer
// whatsoever. Feeding both from one computed list is meant to keep the two
// "which peers do we trust" answers in agreement, but it does NOT guarantee
// that on a malformed TRUSTED_NETWORKS entry: gin's own trusted-proxy parser
// (prepareTrustedCIDRs) returns on the FIRST invalid entry and drops
// everything after it, while IsTrustedIP (auth.go) skips a bad entry with
// `continue` and keeps evaluating the rest of the list. A config like
// "garbage,10.0.0.0/24" therefore leaves gin trusting NOBODY for
// X-Forwarded-For while this gate still trusts the whole 10.0.0.0/24 range
// for X-Forwarded-Proto — VERIFIED 2026-08-05 by the orchestrator (adversary
// pass on this bead). main.go only warns on SetTrustedProxies' error rather
// than refusing to start, so a single typo in TRUSTED_NETWORKS can ship this
// divergence silently. Not fixed here — see the comment at the
// InitTrustedProxyNetworks call site in main.go.
var trustedProxyNetworks struct {
	mu       sync.RWMutex
	networks string
}

// InitTrustedProxyNetworks records the effective trusted-proxy network list.
// Call once at startup with the same []string passed to gin's
// SetTrustedProxies (see middleware.LogTrustedProxies, an existing precedent
// for startup-set middleware state). A nil or empty slice clears it, which
// leaves only the unconditional loopback trust that IsTrustedIP already
// grants.
func InitTrustedProxyNetworks(networks []string) {
	trustedProxyNetworks.mu.Lock()
	trustedProxyNetworks.networks = strings.Join(networks, ",")
	trustedProxyNetworks.mu.Unlock()
}

// isTrustedProxyPeer reports whether remoteIP is allowed to influence
// IsSecureRequest via X-Forwarded-Proto. Delegates to IsTrustedIP
// (internal/middleware/auth.go) rather than a second trust evaluator — two
// implementations of "is this peer trusted" that can disagree is its own
// defect.
func isTrustedProxyPeer(remoteIP string) bool {
	trustedProxyNetworks.mu.RLock()
	networks := trustedProxyNetworks.networks
	trustedProxyNetworks.mu.RUnlock()
	return IsTrustedIP(remoteIP, networks)
}

var untrustedForwardedProtoWarned untrustedWarnBudget

// warnUntrustedForwardedProto logs at most once per peer per window when
// X-Forwarded-Proto arrives from a peer outside the trusted-proxy list, plus a
// rollup when peers beyond the window's budget were suppressed. This is a
// SEPARATE budget from untrustedProxyWarned above rather than a shared one:
// TrustedProxyWarning fires only when ClientIP()==RemoteIP() (gin found no
// trusted proxy to resolve XFF through), a check that doesn't apply here —
// X-Forwarded-Proto isn't part of gin's RemoteIPHeaders at all, so this path
// must evaluate peer trust independently of gin's resolution, and the two
// warnings describe different consequences (rate-limit bucket collapse vs.
// cookie/HSTS security) that are each worth their own message and own budget.
//
// Note that an X-Forwarded-Proto carrying no protocol claim at all — an empty
// value, or one that is only commas and spaces — never reaches here: since
// agent-os-coi, IsSecureRequest treats it as absent. That was the cheapest way
// to burn this budget, at a cost of one header line to the attacker.
func warnUntrustedForwardedProto(remoteIP string) {
	named, rollup := untrustedForwardedProtoWarned.admit(remoteIP)

	if rollup != nil {
		slog.Warn("X-Forwarded-Proto is still arriving from untrusted peers - per-peer warnings went over budget, summarised here",
			"suppressed_requests", rollup.suppressed,
			"elapsed", rollup.elapsed,
			"named_peers", rollup.namedPeers,
			"effect", "Secure cookie flag and HSTS are decided from the real connection instead (TLS only)",
			"fix", "add the proxy's address to TRUSTED_NETWORKS, and make sure it overwrites the header rather than forwarding a client-supplied value")
	}
	if !named {
		return
	}

	slog.Warn("X-Forwarded-Proto received from an untrusted peer - it is being ignored",
		"peer", remoteIP,
		"effect", "Secure cookie flag and HSTS are decided from the real connection instead (TLS only)",
		"fix", "add this address to TRUSTED_NETWORKS, and make sure the proxy overwrites the header rather than forwarding a client-supplied value")
}

// LogTrustedProxies records the effective trusted-proxy configuration at
// startup. Whether the list came from TRUSTED_NETWORKS or from the localhost
// default is the single most useful fact when diagnosing "logins are randomly
// refused" or "auth was skipped for a request I don't recognise".
func LogTrustedProxies(proxies []string, fromConfig bool) {
	source := "default"
	if fromConfig {
		source = "TRUSTED_NETWORKS"
	}
	slog.Info("Trusted proxy configuration",
		"source", source,
		"proxies", strings.Join(proxies, ","),
		// Updated agent-os-ab9: this list now also gates X-Forwarded-Proto,
		// which decides the Secure cookie flag and HSTS, not just
		// X-Forwarded-For/client-IP attribution — say so, since this is the
		// line an operator reads at boot.
		"note", "X-Forwarded-For and X-Forwarded-Proto are only honored from these addresses")
}

// TrustedProxyWarning warns when a request arrives with a forwarding header from
// a peer that is not a trusted proxy. That combination means the header is being
// ignored and every user behind that proxy shares one apparent address, which
// otherwise fails silently.
func TrustedProxyWarning() gin.HandlerFunc {
	return func(c *gin.Context) {
		if header, ok := forwardingHeader(c.Request); ok {
			// ClientIP() returns the peer address unchanged when the peer is not a
			// trusted proxy, which is exactly the misconfiguration signature.
			remote := c.RemoteIP()
			if remote != "" && c.ClientIP() == remote {
				warnUntrustedProxy(remote, header)
			}
		}
		c.Next()
	}
}

func forwardingHeader(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if r.Header.Get(h) != "" {
			return h, true
		}
	}
	return "", false
}

// warnUntrustedProxy logs at most once per peer address per window, plus a
// rollup for peers suppressed beyond the window's budget. A per-request
// warning on the auth path would be an easy way to flood the log; a budget
// that never refilled was an equally easy way to silence it, which is what
// agent-os-coi fixed here and in warnUntrustedForwardedProto together — the
// two functions had the identical flaw and fixing one alone is incoherent.
func warnUntrustedProxy(remoteIP, header string) {
	named, rollup := untrustedProxyWarned.admit(remoteIP)

	if rollup != nil {
		slog.Warn("Forwarding headers are still arriving from untrusted peers - per-peer warnings went over budget, summarised here",
			"suppressed_requests", rollup.suppressed,
			"elapsed", rollup.elapsed,
			"named_peers", rollup.namedPeers,
			"effect", "all clients behind this proxy share one apparent IP for rate limiting",
			"fix", "add the proxy's address to TRUSTED_NETWORKS, and make sure it overwrites the header rather than forwarding a client-supplied value")
	}
	if !named {
		return
	}

	slog.Warn("Forwarding header received from an untrusted peer - it is being ignored",
		"peer", remoteIP,
		"header", header,
		"effect", "all clients behind this proxy share one apparent IP for rate limiting",
		"fix", "add this address to TRUSTED_NETWORKS, and make sure the proxy overwrites the header rather than forwarding a client-supplied value")
}
