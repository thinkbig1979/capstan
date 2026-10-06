package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// These tests guard agent-os-n4ca.6: the credential the app issues (the
// capstan_token cookie) wins over an Authorization header. The browser never
// sends the header (App.tsx registers `() => null` as getToken), so a header
// arriving next to a cookie came from somewhere else and must not override the
// session the cookie names. The header is honoured only when no cookie is sent
// and it carries the "Bearer " scheme.

const tokenSourceSecret = "test-secret-key-32-chars"

// seedTokenSession creates a user with a live session and returns a JWT for it.
func seedTokenSession(t *testing.T, db *database.DB, userID, sessionID string) string {
	t.Helper()
	now := time.Now()
	if err := db.CreateUser(models.User{
		ID:        userID,
		Username:  userID + "-name",
		Password:  "irrelevant-hash",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	if err := db.CreateSession(models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("failed to seed session: %v", err)
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":      jwtIssuer,
		"sub":      userID,
		"username": userID + "-name",
		"jti":      sessionID,
		"iat":      now.Unix(),
		"exp":      now.Add(24 * time.Hour).Unix(),
	}).SignedString([]byte(tokenSourceSecret))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}
	return signed
}

// newTokenSourceRouter returns a router whose handler echoes the userID
// AuthMiddleware authenticated, plus tokens for two distinct live sessions.
func newTokenSourceRouter(t *testing.T) (r *gin.Engine, cookieUserToken, headerUserToken string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cookieUserToken = seedTokenSession(t, db, "cookie-user", "cookie-session")
	headerUserToken = seedTokenSession(t, db, "header-user", "header-session")

	r = gin.New()
	r.Use(AuthMiddleware(db, tokenSourceSecret, false, ""))
	r.GET("/api/v1/dashboard/stats", func(c *gin.Context) {
		c.String(http.StatusOK, c.GetString("userID"))
	})
	return r, cookieUserToken, headerUserToken
}

func serveWith(r *gin.Engine, cookie, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "capstan_token", Value: cookie})
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddleware_CookieWinsOverAuthorizationHeader(t *testing.T) {
	r, cookieTok, headerTok := newTokenSourceRouter(t)

	cases := map[string]string{
		"invalid header":                "Bearer not.a.jwt",
		"header naming another session": "Bearer " + headerTok,
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			w := serveWith(r, cookieTok, header)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 authenticated by the cookie, got %d (%s)", w.Code, w.Body.String())
			}
			if got := w.Body.String(); got != "cookie-user" {
				t.Fatalf("expected the cookie's session (cookie-user) to authenticate, got %q", got)
			}
		})
	}
}

// A cookie that is present but invalid is a dead session: it must not fall
// back to whatever the header says.
func TestAuthMiddleware_InvalidCookieDoesNotFallBackToHeader(t *testing.T) {
	r, _, headerTok := newTokenSourceRouter(t)

	w := serveWith(r, "not.a.jwt", "Bearer "+headerTok)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an invalid cookie even with a valid header, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestAuthMiddleware_BearerHeaderAloneAuthenticates(t *testing.T) {
	r, _, headerTok := newTokenSourceRouter(t)

	w := serveWith(r, "", "Bearer "+headerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a Bearer header with no cookie, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "header-user" {
		t.Fatalf("expected the header's session (header-user) to authenticate, got %q", got)
	}
}

func TestAuthMiddleware_HeaderWithoutBearerSchemeIsIgnored(t *testing.T) {
	r, _, headerTok := newTokenSourceRouter(t)

	for name, header := range map[string]string{
		"bare token":   headerTok,
		"other scheme": "Basic " + headerTok,
	} {
		t.Run(name, func(t *testing.T) {
			w := serveWith(r, "", header)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 for an Authorization header without the Bearer scheme, got %d (%s)", w.Code, w.Body.String())
			}
		})
	}
}

// An empty capstan_token cookie is still a sent cookie: it wins and yields no
// token, rather than letting the header decide.
func TestAuthMiddleware_EmptyCookieDoesNotFallBackToHeader(t *testing.T) {
	r, _, headerTok := newTokenSourceRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	req.Header.Set("Cookie", "capstan_token=")
	req.Header.Set("Authorization", "Bearer "+headerTok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an empty cookie even with a valid header, got %d (%s)", w.Code, w.Body.String())
	}
}
