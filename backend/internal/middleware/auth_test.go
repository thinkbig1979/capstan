package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
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
