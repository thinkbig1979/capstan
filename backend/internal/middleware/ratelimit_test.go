package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newAuthTestRouter returns a router with the auth limiter in front of a handler
// that fully consumes the request body, so any test that passes also proves the
// body survived the limiter's peek.
func newAuthTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	InitRateLimiters()

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

// Bodies that are not JSON must not break the limiter or the handler's own
// error reporting.
func TestAuthLimit_NonJSONBodyFallsBackToSentinelBucket(t *testing.T) {
	r := newAuthTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("not json at all"))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.15:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected the handler to reject the body with 400, got %d (%s)", w.Code, w.Body.String())
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
