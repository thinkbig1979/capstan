package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The session guard owns the SESSION_EXPIRED signal (agent-os-318). Every 401
// AuthMiddleware mints means "this session cannot be used", so it must carry
// SESSION_EXPIRED — the frontend interceptor logs out on that code and only
// that code, leaving UNAUTHORIZED to mean "the credential you just typed is
// wrong". Before this fix the missing-token and invalid-token paths sent
// UNAUTHORIZED, making the two cases indistinguishable on the wire.
// See backend/internal/models/errors.go for the contract.

func newSessionGuardRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	r := gin.New()
	r.Use(AuthMiddleware(db, "test-secret-key-32-chars", false, ""))
	r.GET("/api/v1/dashboard/stats", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func decodeErrorBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("response body is not JSON: %v (%s)", err, string(body))
	}
	return payload
}

func TestAuthMiddleware_MissingTokenIsSessionExpired(t *testing.T) {
	r := newSessionGuardRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no token, got %d (%s)", w.Code, w.Body.String())
	}
	payload := decodeErrorBody(t, w.Body.Bytes())
	if payload["code"] != "SESSION_EXPIRED" {
		t.Fatalf("expected code SESSION_EXPIRED for a missing token, got %v (%s)", payload["code"], w.Body.String())
	}
}

func TestAuthMiddleware_InvalidTokenIsSessionExpired(t *testing.T) {
	r := newSessionGuardRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	req.Header.Set("Authorization", "Bearer not.a.jwt")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with a garbage token, got %d (%s)", w.Code, w.Body.String())
	}
	payload := decodeErrorBody(t, w.Body.Bytes())
	if payload["code"] != "SESSION_EXPIRED" {
		t.Fatalf("expected code SESSION_EXPIRED for an invalid token, got %v (%s)", payload["code"], w.Body.String())
	}
}

// TestAuthMiddleware_MissingSubIsSessionExpired guards agent-os-bm6: a token
// that is validly signed, carries the right "iss", and points at a live
// session row (so it clears every other check in AuthMiddleware) but has no
// "sub" claim must not fall through to c.Next() with userID unset. Before
// this fix, claims["sub"].(string) failing its type assertion had no else
// branch, so the request passed the guard and every handler downstream saw
// "not authenticated" (c.Get("userID") miss) instead of the guard itself
// rejecting the token. That silently turns "your session cannot be used"
// into "you're not logged in" for every route those handlers touch.
//
// The session (jti) is real and unexpired here specifically to isolate the
// missing-sub defect from the missing-token/invalid-token/dead-session paths
// already covered above — this test must fail only because of the sub gap.
func TestAuthMiddleware_MissingSubIsSessionExpired(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now()
	user := models.User{
		ID:        "test-user-id",
		Username:  "subless-token-user",
		Password:  "irrelevant-hash",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.CreateUser(user); err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	session := models.Session{
		ID:        "test-session-id",
		UserID:    user.ID,
		ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
	}
	if err := db.CreateSession(session); err != nil {
		t.Fatalf("failed to seed session: %v", err)
	}

	secret := "test-secret-key-32-chars"
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		// Deliberately no "sub" claim — this is the defect under test.
		"username": user.Username,
		"jti":      session.ID,
		"iat":      now.Unix(),
		"exp":      now.Add(24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}

	r := gin.New()
	r.Use(AuthMiddleware(db, secret, false, ""))
	r.GET("/api/v1/dashboard/stats", func(c *gin.Context) {
		// If the guard let a sub-less token through, userID is unset here —
		// prove that rather than trusting the middleware's own bookkeeping.
		userID, _ := c.Get("userID")
		t.Errorf("handler reached with sub-less token; userID=%v", userID)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token with no sub claim, got %d (%s)", w.Code, w.Body.String())
	}
	payload := decodeErrorBody(t, w.Body.Bytes())
	if payload["code"] != "SESSION_EXPIRED" {
		t.Fatalf("expected code SESSION_EXPIRED for a token with no sub claim, got %v (%s)", payload["code"], w.Body.String())
	}
}
