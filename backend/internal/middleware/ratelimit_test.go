package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// newAuthTestRouter returns a router with the auth limiter in front of a handler
// that fully consumes the request body, so any test that passes also proves the
// body survived the limiter's peek.
func newAuthTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	InitRateLimiters(config.DefaultAPIRateLimitPerMin)

	r := gin.New()
	r.Use(RateLimitAuth())
	r.POST("/api/v1/auth/login", func(c *gin.Context) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "BAD_BODY"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"username": body.Username, "password": body.Password})
	})
	return r
}

func loginAttempt(r *gin.Engine, ip, username string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"username":%q,"password":"hunter2"}`, username)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":54321"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// One IP, two accounts: exhausting one account's budget must not spend the
// other's. This is the shared-bucket regression — behind a reverse proxy every
// user presents the same client IP.
func TestAuthLimit_AccountsFromOneIPAreIndependent(t *testing.T) {
	r := newAuthTestRouter(t)
	const ip = "203.0.113.10"

	for i := 0; i < authAccountMaxReqs; i++ {
		if w := loginAttempt(r, ip, "alice"); w.Code != http.StatusOK {
			t.Fatalf("alice attempt %d: expected 200, got %d (%s)", i+1, w.Code, w.Body.String())
		}
	}

	if w := loginAttempt(r, ip, "alice"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("alice over budget: expected 429, got %d (%s)", w.Code, w.Body.String())
	}

	if w := loginAttempt(r, ip, "bob"); w.Code != http.StatusOK {
		t.Fatalf("bob shares alice's bucket: expected 200, got %d (%s)", w.Code, w.Body.String())
	}
}

// The per-IP ceiling still applies, so rotating usernames from one address does
// not buy unlimited attempts.
func TestAuthLimit_UsernameRotationHitsPerIPCeiling(t *testing.T) {
	r := newAuthTestRouter(t)
	const ip = "203.0.113.11"

	for i := 0; i < authIPMaxReqs; i++ {
		if w := loginAttempt(r, ip, fmt.Sprintf("user%03d", i)); w.Code != http.StatusOK {
			t.Fatalf("rotation attempt %d: expected 200, got %d (%s)", i+1, w.Code, w.Body.String())
		}
	}

	w := loginAttempt(r, ip, "user999")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 once the per-IP ceiling is reached, got %d (%s)", w.Code, w.Body.String())
	}
}

// One account attacked from many distinct addresses still meets a ceiling: the
// account-wide layer is the only one a distributed attacker does not evade.
func TestAuthLimit_OneAccountAcrossManyIPsIsStillLimited(t *testing.T) {
	r := newAuthTestRouter(t)

	// Each source IP gets its own per-IP and per-(IP, account) budget, so only
	// the account-wide layer can deny here.
	for i := 0; i < authAccountAnyIPMaxReqs; i++ {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		if w := loginAttempt(r, ip, "alice"); w.Code != http.StatusOK {
			t.Fatalf("attempt %d from %s: expected 200, got %d (%s)", i+1, ip, w.Code, w.Body.String())
		}
	}

	w := loginAttempt(r, "198.51.100.200", "alice")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 once the account-wide ceiling is reached, got %d (%s)", w.Code, w.Body.String())
	}

	// A different account from a fresh address is unaffected.
	if w := loginAttempt(r, "198.51.100.201", "bob"); w.Code != http.StatusOK {
		t.Fatalf("bob caught by alice's account-wide bucket: expected 200, got %d (%s)", w.Code, w.Body.String())
	}
}

// The reported availability bug, end to end: behind a proxy that is not in
// TRUSTED_NETWORKS every user presents the same client IP. A team must still be
// able to log in, including after one member fumbles their password.
//
// This is why the per-IP ceiling had to rise from 5: the broad layer is checked
// first, so at 5/min it denies the sixth request from the whole deployment and
// the per-account layer never gets to help. The composite key fixes the keying;
// this constant is what makes the fix reachable.
func TestAuthLimit_SharedProxyAddressDoesNotLockOutOtherUsers(t *testing.T) {
	r := newAuthTestRouter(t)
	const sharedProxyIP = "203.0.113.20"

	// One user burns their whole per-account budget getting the password wrong.
	for i := 0; i < authAccountMaxReqs; i++ {
		if w := loginAttempt(r, sharedProxyIP, "fumbler"); w.Code != http.StatusOK {
			t.Fatalf("fumbler attempt %d: expected 200, got %d", i+1, w.Code)
		}
	}
	if w := loginAttempt(r, sharedProxyIP, "fumbler"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("fumbler should be limited after %d attempts, got %d", authAccountMaxReqs, w.Code)
	}

	// Everyone else behind the same proxy address still gets in.
	for i := 0; i < 10; i++ {
		user := fmt.Sprintf("colleague%02d", i)
		if w := loginAttempt(r, sharedProxyIP, user); w.Code != http.StatusOK {
			t.Fatalf("%s locked out by a colleague's failed logins: got %d (%s)", user, w.Code, w.Body.String())
		}
	}
}

// Case variants must not multiply the budget: the key folds case even though
// login itself compares usernames case-sensitively.
func TestAuthLimit_CaseVariantsShareABucket(t *testing.T) {
	r := newAuthTestRouter(t)
	const ip = "203.0.113.12"

	for i := 0; i < authAccountMaxReqs; i++ {
		if w := loginAttempt(r, ip, "alice"); w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i+1, w.Code)
		}
	}

	if w := loginAttempt(r, ip, "ALICE"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected case variant to share alice's bucket, got %d (%s)", w.Code, w.Body.String())
	}
}

// The limiter reads the body to key on the username; the handler must still see
// it intact.
func TestAuthLimit_RequestBodyReachesHandlerIntact(t *testing.T) {
	r := newAuthTestRouter(t)

	w := loginAttempt(r, "203.0.113.13", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, `"username":"alice"`) || !strings.Contains(got, `"password":"hunter2"`) {
		t.Fatalf("handler did not receive the full body, got: %s", got)
	}
}

// A body past the peek limit is passed through untouched rather than buffered,
// and the handler still binds it.
func TestAuthLimit_OversizedBodyIsPassedThroughUnread(t *testing.T) {
	r := newAuthTestRouter(t)

	padding := strings.Repeat("x", loginBodyPeekLimit)
	body := fmt.Sprintf(`{"username":"alice","password":"hunter2","note":%q}`, padding)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.14:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for an oversized-but-valid body, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"password":"hunter2"`) {
		t.Fatalf("oversized body was truncated before the handler: %s", w.Body.String())
	}
}

// Bodies the limiter cannot parse must fall through to the handler unchanged.
// The limiter must never become a new way to reject an otherwise valid request:
// the 400 here is the handler's own, not the limiter's.
func TestAuthLimit_UnparseableBodyFallsThroughToHandler(t *testing.T) {
	cases := map[string]string{
		"not json":            "not json at all",
		"empty body":          "",
		"username not string": `{"username":123,"password":"x"}`,
		"username absent":     `{"password":"x"}`,
		"json array":          `["alice"]`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r := newAuthTestRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "203.0.113.15:1234"
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			got := w.Body.String()
			if strings.Contains(got, "INVALID_KEY") || strings.Contains(got, "RATE_LIMITED") {
				t.Fatalf("the limiter rejected the request instead of passing it to the handler: %d %s", w.Code, got)
			}
			if !strings.Contains(got, "BAD_BODY") && w.Code != http.StatusOK {
				t.Fatalf("expected the handler's own verdict, got %d %s", w.Code, got)
			}
		})
	}
}

// The peek must leave the request byte-identical, including Content-Length,
// which is what gin's binding and any downstream middleware rely on.
func TestPeekLoginUsername_PreservesBodyAndContentLength(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const body = `{"username":"Alice","password":"hunter2"}`

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	lengthBefore := c.Request.ContentLength
	headerBefore := c.Request.Header.Get("Content-Length")

	if got := peekLoginUsername(c); got != "Alice" {
		t.Fatalf("peekLoginUsername = %q, want \"Alice\" (raw, before normalisation)", got)
	}

	if c.Request.ContentLength != lengthBefore {
		t.Errorf("ContentLength changed: %d -> %d", lengthBefore, c.Request.ContentLength)
	}
	if got := c.Request.Header.Get("Content-Length"); got != headerBefore {
		t.Errorf("Content-Length header changed: %q -> %q", headerBefore, got)
	}

	rest, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("body unreadable after peek: %v", err)
	}
	if string(rest) != body {
		t.Fatalf("body after peek = %q, want %q", rest, body)
	}

	// The replacement must still be a ReadCloser, and closing it must be safe.
	if err := c.Request.Body.Close(); err != nil {
		t.Fatalf("closing the restored body failed: %v", err)
	}
}

func TestPeekLoginUsername_NilRequestAndNilBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = nil
	if got := peekLoginUsername(c); got != "" {
		t.Fatalf("peekLoginUsername with a nil request = %q, want \"\"", got)
	}

	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c.Request.Body = nil
	if got := peekLoginUsername(c); got != "" {
		t.Fatalf("peekLoginUsername with a nil body = %q, want \"\"", got)
	}
}

// check() is the backstop for a caller that skips validation. It must deny, not
// wave the request through. Inverting this back to fail-open should fail here.
func TestCheckFailsClosedOnInvalidKey(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 100)

	invalid := []string{
		"",
		"not-an-ip",
		"999.999.999.999",
		"login:203.0.113.1|",   // empty account
		"login:203.0.113.1|ab", // account below the minimum length
		"login:203.0.113.1|alice; DROP TABLE users", // wrong charset
		"login:not-an-ip|alice",                     // bad scope
		"login:203.0.113.1",                         // missing separator
		strings.Repeat("a", 257),
	}

	for _, key := range invalid {
		if rl.check(key) {
			t.Fatalf("check(%q) allowed an unvalidatable key; it must fail closed", key)
		}
	}
}

func TestValidateRateLimitKey_AcceptedShapes(t *testing.T) {
	valid := []string{
		"127.0.0.1",
		"::1",
		"2001:db8::1",
		"550e8400-e29b-41d4-a716-446655440000",
		"login:203.0.113.1|alice",
		"login:203.0.113.1|-",
		"login:*|alice",
		"login:::1|alice",
	}
	for _, key := range valid {
		if !validateRateLimitKey(key) {
			t.Errorf("validateRateLimitKey(%q) = false, want true", key)
		}
	}

	// The composite branch is an additional shape, not a wildcard: a key that
	// merely starts with the prefix is still rejected.
	invalid := []string{
		"login:",
		"login:|alice",
		"login:203.0.113.1|Alice",
		"login:203.0.113.1|alice|bob",
		"login:*|*",
	}
	for _, key := range invalid {
		if validateRateLimitKey(key) {
			t.Errorf("validateRateLimitKey(%q) = true, want false", key)
		}
	}
}

// Fail-closed means any drift between the key RateLimitAuth generates and what
// validateRateLimitKey accepts denies every login rather than failing quietly
// open. Drive the real generator — a hand-written literal would keep passing
// while production drifted.
func TestGeneratedLoginKeysRoundTripThroughValidation(t *testing.T) {
	usernames := []string{
		"alice",                   // ordinary
		"abc",                     // minimum length
		strings.Repeat("u", 50),   // maximum length
		"a-b_c",                   // both permitted separators
		"ALICE",                   // folded to lower case by the generator
		"  alice  ",               // trimmed by the generator
		"bad user!",               // sentinel fallback
		"ab",                      // too short: sentinel fallback
		strings.Repeat("u", 51),   // too long: sentinel fallback
		"",                        // absent username: sentinel fallback
		"alice; DROP TABLE users", // injection-shaped: sentinel fallback
	}
	clientIPs := []string{
		"203.0.113.7",
		"127.0.0.1",
		"::1",
		"2001:db8::1",
		"0000:0000:0000:0000:0000:0000:0000:0001", // long form, as a proxy may forward it
	}

	for _, ip := range clientIPs {
		for _, username := range usernames {
			// Exactly the two calls RateLimitAuth makes.
			account := normalizeAccount(username)
			perIP := loginRateLimitKey(ip, account)
			if !validateRateLimitKey(perIP) {
				t.Errorf("generated per-IP key %q was rejected by its own validator", perIP)
			}
			anyIP := loginRateLimitKey(loginKeyAnyIP, account)
			if !validateRateLimitKey(anyIP) {
				t.Errorf("generated account-wide key %q was rejected by its own validator", anyIP)
			}
		}

		// The IP itself is the key for the broad layer.
		if !validateRateLimitKey(ip) {
			t.Errorf("client IP %q was rejected as a rate limit key", ip)
		}
	}

	// The sentinel must validate in its own right: if it did not, a malformed
	// username would become a hard denial for that client rather than a shared
	// bucket.
	sentinelKey := loginRateLimitKey("203.0.113.7", loginKeyUnknownAccount)
	if !validateRateLimitKey(sentinelKey) {
		t.Fatalf("sentinel key %q was rejected; a bad username would deny the client outright", sentinelKey)
	}
}

// gin's ClientIP returns "" when RemoteAddr does not parse (context.go: it runs
// net.ParseIP over RemoteIP and bails on nil), which is what a zone-scoped IPv6
// peer produces. That must be rejected at the IP layer with a 400, never folded
// into a composite key like "login:|alice".
func TestAuthLimit_UnparseableClientAddressIsRejectedNotKeyed(t *testing.T) {
	r := newAuthTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"alice","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "[fe80::1%eth0]:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unparseable client address, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INVALID_KEY") {
		t.Fatalf("expected INVALID_KEY, got: %s", w.Body.String())
	}
	if validateRateLimitKey(loginRateLimitKey("", "alice")) {
		t.Fatal("a composite key with an empty scope must not validate")
	}
}

// accessOrder and accessIndex must not outlive the entries they order.
// cleanup() deletes expired keys from requests; if it does not also drop them
// from accessOrder/accessIndex via removeKey, the list grows for the process
// lifetime and re-created keys are appended twice.
func TestAccessOrderMirrorsRequestsAfterExpiry(t *testing.T) {
	rl := NewRateLimiter(40*time.Millisecond, 100)

	for i := 0; i < 50; i++ {
		key := loginRateLimitKey("203.0.113.9", fmt.Sprintf("user%03d", i))
		if !rl.check(key) {
			t.Fatalf("check(%q) denied below the limit", key)
		}
	}

	rl.mu.RLock()
	before := rl.accessOrder.Len()
	rl.mu.RUnlock()
	if before != 50 {
		t.Fatalf("expected 50 tracked keys, got %d", before)
	}

	// Wait for cleanup() to tick past the window and expire everything.
	deadline := time.Now().Add(3 * time.Second)
	for {
		rl.mu.RLock()
		reqs, order, index := len(rl.requests), rl.accessOrder.Len(), len(rl.accessIndex)
		rl.mu.RUnlock()
		if reqs == 0 && order == 0 && index == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after expiry: len(requests)=%d, accessOrder.Len()=%d, len(accessIndex)=%d, want 0, 0, 0", reqs, order, index)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Re-creating the same keys must not append duplicates.
	for i := 0; i < 50; i++ {
		rl.check(loginRateLimitKey("203.0.113.9", fmt.Sprintf("user%03d", i)))
	}
	rl.mu.RLock()
	reqs, order, index := len(rl.requests), rl.accessOrder.Len(), len(rl.accessIndex)
	rl.mu.RUnlock()
	if order != reqs || index != reqs {
		t.Fatalf("accessOrder/accessIndex drifted from requests: accessOrder.Len()=%d, len(accessIndex)=%d, len(requests)=%d", order, index, reqs)
	}
}

// This replaces TestPruneAccessOrderKeepsSurvivorOrder: pruneAccessOrder no
// longer exists because removeKey deletes from requests, accessOrder, and
// accessIndex together at the one call site that ever removes a key, so the
// drift that test policed (a key surviving in requests but not accessOrder,
// or vice versa) is now structurally impossible rather than repaired
// after the fact. What still needs pinning is the behaviour the list gives
// removeKey/evictLRU for free: eviction must take the least-recently-used
// key, and a refreshed key must not be it.
func TestEvictLRUPicksLeastRecentlyUsed(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 100)
	rl.maxEntries = 3

	for _, k := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		if !rl.check(k) {
			t.Fatalf("check(%q) denied below the limit", k)
		}
	}

	// Refresh 1.1.1.1 so 2.2.2.2 becomes the least recently used.
	if !rl.check("1.1.1.1") {
		t.Fatal("refresh of 1.1.1.1 denied")
	}

	// A fourth key pushes the entry count over maxEntries, but evictLRU runs at
	// the top of check() before the new key is inserted, so the map is only
	// over budget *after* this call returns; eviction is one call behind. A
	// fifth call is what actually triggers it.
	if !rl.check("4.4.4.4") {
		t.Fatal("check(4.4.4.4) denied")
	}
	if !rl.check("5.5.5.5") {
		t.Fatal("check(5.5.5.5) denied")
	}

	rl.mu.RLock()
	_, has1 := rl.requests["1.1.1.1"]
	_, has2 := rl.requests["2.2.2.2"]
	_, has3 := rl.requests["3.3.3.3"]
	_, has4 := rl.requests["4.4.4.4"]
	_, has5 := rl.requests["5.5.5.5"]
	reqs, order, index := len(rl.requests), rl.accessOrder.Len(), len(rl.accessIndex)
	rl.mu.RUnlock()

	if has2 {
		t.Fatal("2.2.2.2 should have been evicted as the least recently used key")
	}
	if !has1 || !has3 || !has4 || !has5 {
		t.Fatalf("unexpected eviction: has1=%v has3=%v has4=%v has5=%v (want all true)", has1, has3, has4, has5)
	}
	// evictLRU trims down to maxEntries before the new key is inserted, so
	// steady state after inserting a new key is maxEntries+1, not maxEntries.
	if reqs != 4 || order != 4 || index != 4 {
		t.Fatalf("accessOrder/accessIndex should mirror requests (4 entries), got requests=%d accessOrder.Len()=%d len(accessIndex)=%d", reqs, order, index)
	}
}

// check() guards every access to requests, accessOrder, and accessIndex with
// rl.mu — container/list.List and a plain map are not otherwise safe for
// concurrent use. The single mutex covering all three was true of the slice
// design too, but this rewrite is the first time a shared-state structure
// (list.Element pointers) crosses between requests and accessIndex, so it is
// worth a direct hammering test under -race rather than relying only on the
// sequential, single-goroutine coverage the HTTP-level tests give the same
// code path.
func TestCheckConcurrentAccessDoesNotRace(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 1000)

	const goroutines = 50
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Deliberately overlapping keyspace (5 keys shared across all 50
				// goroutines) so refresh, insert, and evict all interleave on the
				// same map entries and list nodes, not just disjoint ones.
				key := fmt.Sprintf("10.0.%d.%d", g%5, i%5)
				rl.check(key)
			}
		}(g)
	}
	wg.Wait()
}

func TestNormalizeAccount(t *testing.T) {
	cases := map[string]string{
		"alice":                 "alice",
		"ALICE":                 "alice",
		"  Alice  ":             "alice",
		"":                      loginKeyUnknownAccount,
		"ab":                    loginKeyUnknownAccount,
		"has space":             loginKeyUnknownAccount,
		"alice@example.com":     loginKeyUnknownAccount,
		strings.Repeat("a", 51): loginKeyUnknownAccount,
		strings.Repeat("a", 50): strings.Repeat("a", 50),
	}
	for in, want := range cases {
		if got := normalizeAccount(in); got != want {
			t.Errorf("normalizeAccount(%q) = %q, want %q", in, got, want)
		}
	}
}

// A forwarding header from a peer that is not a trusted proxy is the signature
// of the misconfiguration, and must not disturb the request itself.
func TestTrustedProxyWarning_DoesNotAlterRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies([]string{"127.0.0.1"}); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	r.Use(TrustedProxyWarning())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.RemoteAddr = "203.0.113.99:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "203.0.113.99" {
		t.Fatalf("expected the untrusted peer address, got %q", got)
	}
}

func TestForwardingHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if _, ok := forwardingHeader(req); ok {
		t.Fatal("expected no forwarding header on a bare request")
	}

	req.Header.Set("X-Real-IP", "1.2.3.4")
	name, ok := forwardingHeader(req)
	if !ok || name != "X-Real-IP" {
		t.Fatalf("forwardingHeader = (%q, %v), want (X-Real-IP, true)", name, ok)
	}

	if _, ok := forwardingHeader(nil); ok {
		t.Fatal("expected forwardingHeader(nil) to report no header")
	}
}

func TestPeekLoginUsername_NilBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c.Request.Body = http.NoBody

	if got := peekLoginUsername(c); got != "" {
		t.Fatalf("peekLoginUsername on an empty body = %q, want \"\"", got)
	}

	// The body must still be readable (and empty) afterwards.
	rest, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("body unreadable after peek: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("expected an empty body after peek, got %q", rest)
	}
}

// newAPITestRouter returns a router with the general API limiter in front of a
// trivial handler, initialised at the given budget.
func newAPITestRouter(t *testing.T, apiMaxReqs int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	InitRateLimiters(apiMaxReqs)

	r := gin.New()
	r.Use(RateLimitByUser())
	r.GET("/api/v1/stacks", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func apiRequest(r *gin.Engine, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil)
	req.RemoteAddr = ip + ":54321"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The default budget must stay exactly 300/min. This is the production half of
// the RATE_LIMIT_API_PER_MIN pair: a deployment that never sets the variable
// gets the same ceiling it got before the variable existed. Asserting the
// constant alone would not catch a wiring mistake, so this drives real requests
// through the middleware and checks the boundary from both sides — the 300th is
// allowed, the 301st is not.
func TestAPILimit_DefaultBudgetIsUnchangedAt300(t *testing.T) {
	if config.DefaultAPIRateLimitPerMin != 300 {
		t.Fatalf("default API budget changed: expected 300, got %d — this is a production rate limit, not a tunable", config.DefaultAPIRateLimitPerMin)
	}

	r := newAPITestRouter(t, config.DefaultAPIRateLimitPerMin)
	const ip = "203.0.113.40"

	for i := 0; i < config.DefaultAPIRateLimitPerMin; i++ {
		if w := apiRequest(r, ip); w.Code != http.StatusOK {
			t.Fatalf("request %d of %d: expected 200, got %d (%s)", i+1, config.DefaultAPIRateLimitPerMin, w.Code, w.Body.String())
		}
	}

	if w := apiRequest(r, ip); w.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d: expected 429, got %d (%s)", config.DefaultAPIRateLimitPerMin+1, w.Code, w.Body.String())
	}
}

// The E2E half: raising the budget must actually raise it. Same instrument,
// same 301st request, opposite outcome — without this the test above would pass
// against a build that ignores the parameter entirely and always uses 300.
func TestAPILimit_RaisedBudgetAdmitsThe301stRequest(t *testing.T) {
	const raised = 2000
	r := newAPITestRouter(t, raised)
	const ip = "203.0.113.41"

	for i := 0; i < config.DefaultAPIRateLimitPerMin; i++ {
		if w := apiRequest(r, ip); w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d (%s)", i+1, w.Code, w.Body.String())
		}
	}

	if w := apiRequest(r, ip); w.Code != http.StatusOK {
		t.Fatalf("request %d under a raised budget of %d: expected 200, got %d (%s)", config.DefaultAPIRateLimitPerMin+1, raised, w.Code, w.Body.String())
	}

	// The raised budget is still a ceiling, not an off switch. A fix that
	// disabled limiting for the E2E backend would pass every assertion above.
	for i := config.DefaultAPIRateLimitPerMin + 1; i < raised; i++ {
		if w := apiRequest(r, ip); w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d (%s)", i+1, w.Code, w.Body.String())
		}
	}

	if w := apiRequest(r, ip); w.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d: expected 429 at the raised ceiling, got %d (%s)", raised+1, w.Code, w.Body.String())
	}
}

// A non-positive budget must be refused loudly at init. It is not a lax limit
// but a total API outage: check() rejects when len(valid) >= rl.maxReqs, so at 0
// the first request of every bucket compares 0 >= 0 and is refused, visible only
// as ordinary "Rate limit exceeded" warnings.
//
// 0 is Go's zero value, so it is the value a future caller is most likely to
// supply by accident — a risk that did not exist while the budget was a literal.
func TestInitRateLimiters_RejectsNonPositiveBudget(t *testing.T) {
	for _, budget := range []int{0, -1, -300} {
		t.Run(fmt.Sprintf("budget_%d", budget), func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatalf("InitRateLimiters(%d) returned normally; expected a panic, because this budget refuses every request", budget)
				}
				if msg := fmt.Sprint(recovered); !strings.Contains(msg, "RATE_LIMIT_API_PER_MIN") {
					t.Errorf("panic should name the variable an operator would have to fix, got: %s", msg)
				}
			}()
			InitRateLimiters(budget)
		})
	}
}

// The other side, on the same instrument: a positive budget is passed through
// untouched, not clamped, rounded, or otherwise adjusted by the guard. Without
// this, a guard that panicked on everything would satisfy the test above.
func TestInitRateLimiters_PassesPositiveBudgetThrough(t *testing.T) {
	for _, budget := range []int{1, config.DefaultAPIRateLimitPerMin, 2000} {
		t.Run(fmt.Sprintf("budget_%d", budget), func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("InitRateLimiters(%d) panicked on a valid budget: %v", budget, recovered)
				}
			}()
			InitRateLimiters(budget)

			if apiRateLimiter.maxReqs != budget {
				t.Errorf("expected the API limiter to carry budget %d, got %d", budget, apiRateLimiter.maxReqs)
			}
		})
	}

	// A budget of 1 is the tightest value the guard admits, so it is the one
	// that proves the boundary is at 0 and not somewhere above it: the first
	// request must be allowed and the second refused.
	r := newAPITestRouter(t, 1)
	const ip = "203.0.113.42"

	if w := apiRequest(r, ip); w.Code != http.StatusOK {
		t.Fatalf("first request at budget 1: expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if w := apiRequest(r, ip); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request at budget 1: expected 429, got %d (%s)", w.Code, w.Body.String())
	}
}
