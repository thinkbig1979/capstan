package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AuthMiddleware used to tell "expired" apart from every other parse failure
// by searching the error text for the word "expired" (agent-os-ih0i). Both
// branches carry the same 401 and the same SESSION_EXPIRED code, so what a
// library that rewords its message would silently change is only the
// user-facing message, not the frontend's logout decision (which keys on the
// code, frontend/src/lib/error-handler.ts). The predicate is now
// errors.Is(err, jwt.ErrTokenExpired), which golang-jwt/v5 joins into every
// expiry failure regardless of wording.

// expiryRenamedError models a jwt release that kept ErrTokenExpired as the
// sentinel identity but reworded the message so it no longer contains the
// substring "expired". A plain fmt.Errorf("%w") cannot model this because
// ErrTokenExpired's own text is "token is expired".
type expiryRenamedError struct{}

func (expiryRenamedError) Error() string        { return "token has invalid claims: exp is in the past" }
func (expiryRenamedError) Is(target error) bool { return target == jwt.ErrTokenExpired }

func signGuardToken(t *testing.T, secret string, exp time.Time) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":      jwtIssuer,
		"sub":      "user-id",
		"username": "user",
		"jti":      "session-id",
		"iat":      now.Add(-2 * time.Hour).Unix(),
		"exp":      exp.Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}

func guardMessageFor(t *testing.T, r *gin.Engine, bearer string) (int, string, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	payload := decodeErrorBody(t, w.Body.Bytes())
	return w.Code, w.Body.String(), payload
}

// TestAuthMiddleware_ExpiredTokenMessageSurvivesRewordedLibraryError is the
// arm seen failing first: with the text-match predicate the reworded error
// falls into the "Invalid authorization token" branch.
func TestAuthMiddleware_ExpiredTokenMessageSurvivesRewordedLibraryError(t *testing.T) {
	r := newSessionGuardRouter(t)

	prev := validateJWT
	validateJWT = func(string, string) (jwt.MapClaims, error) { return nil, expiryRenamedError{} }
	t.Cleanup(func() { validateJWT = prev })

	code, body, payload := guardMessageFor(t, r, "any.token.here")
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (%s)", code, body)
	}
	if payload["code"] != "SESSION_EXPIRED" {
		t.Fatalf("expected code SESSION_EXPIRED, got %v (%s)", payload["code"], body)
	}
	if payload["message"] != "Session expired" {
		t.Fatalf("expiry classified by error text: expected message %q, got %v (%s)", "Session expired", payload["message"], body)
	}
}

// TestAuthMiddleware_ExpiredTokenThroughRealParserIsSessionExpired is the
// positive control on the real parser: errors.Is fires on the error
// golang-jwt/v5 v5.3.1 actually joins for a past "exp".
func TestAuthMiddleware_ExpiredTokenThroughRealParserIsSessionExpired(t *testing.T) {
	r := newSessionGuardRouter(t)
	signed := signGuardToken(t, "test-secret-key-32-chars", time.Now().Add(-time.Hour))

	code, body, payload := guardMessageFor(t, r, signed)
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (%s)", code, body)
	}
	if payload["code"] != "SESSION_EXPIRED" {
		t.Fatalf("expected code SESSION_EXPIRED, got %v (%s)", payload["code"], body)
	}
	if payload["message"] != "Session expired" {
		t.Fatalf("expected message %q for a genuinely expired token, got %v (%s)", "Session expired", payload["message"], body)
	}
}

// TestAuthMiddleware_InvalidSignatureIsNotReportedAsExpired is the control on
// the other branch: a token signed with the wrong secret (and not expired)
// must still read "Invalid authorization token", same status and code.
func TestAuthMiddleware_InvalidSignatureIsNotReportedAsExpired(t *testing.T) {
	r := newSessionGuardRouter(t)
	signed := signGuardToken(t, "attacker-secret-key-32-chars-xx", time.Now().Add(time.Hour))

	code, body, payload := guardMessageFor(t, r, signed)
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (%s)", code, body)
	}
	if payload["code"] != "SESSION_EXPIRED" {
		t.Fatalf("expected code SESSION_EXPIRED, got %v (%s)", payload["code"], body)
	}
	if payload["message"] != "Invalid authorization token" {
		t.Fatalf("expected message %q for a bad signature, got %v (%s)", "Invalid authorization token", payload["message"], body)
	}
}
