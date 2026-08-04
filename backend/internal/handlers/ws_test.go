package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func TestConnectionManager_Add(t *testing.T) {
	cm := NewConnectionManager(2)
	conn := &Connection{
		ID:     uuid.New().String(),
		UserID: "user1",
	}

	err := cm.Add(conn.ID, conn)
	assert.NoError(t, err)
	assert.Equal(t, 1, cm.Count())
	assert.Equal(t, 1, cm.CountByUser("user1"))

	err = cm.Add(uuid.New().String(), &Connection{
		ID:     uuid.New().String(),
		UserID: "user1",
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, cm.Count())

	err = cm.Add(uuid.New().String(), &Connection{
		ID:     uuid.New().String(),
		UserID: "user1",
	})
	assert.Error(t, err)
	assert.Equal(t, 2, cm.Count())
}

func TestConnectionManager_Remove(t *testing.T) {
	cm := NewConnectionManager(10)
	connID := uuid.New().String()
	conn := &Connection{
		ID:     connID,
		UserID: "user1",
	}

	err := cm.Add(connID, conn)
	require.NoError(t, err)
	assert.Equal(t, 1, cm.Count())

	cm.Remove(connID)
	assert.Equal(t, 0, cm.Count())
	assert.Equal(t, 0, cm.CountByUser("user1"))

	cm.Remove(connID)
	assert.Equal(t, 0, cm.Count())
}

func TestConnectionManager_Get(t *testing.T) {
	cm := NewConnectionManager(10)
	connID := uuid.New().String()
	conn := &Connection{
		ID:     connID,
		UserID: "user1",
	}

	err := cm.Add(connID, conn)
	require.NoError(t, err)

	retrieved, exists := cm.Get(connID)
	assert.True(t, exists)
	assert.Equal(t, conn, retrieved)

	_, exists = cm.Get("nonexistent")
	assert.False(t, exists)
}

func TestConnectionManager_CloseAll(t *testing.T) {
	cm := NewConnectionManager(10)

	for i := 0; i < 3; i++ {
		conn := &Connection{
			ID:     uuid.New().String(),
			UserID: "user1",
		}
		require.NoError(t, cm.Add(conn.ID, conn))
	}

	assert.Equal(t, 3, cm.Count())
	assert.Equal(t, 3, cm.CountByUser("user1"))
	cm.CloseAll()
	assert.Equal(t, 0, cm.Count())
	assert.Equal(t, 0, cm.CountByUser("user1"))
}

func TestValidateJWT(t *testing.T) {
	secret := "test-secret-key-32-chars-long!!"

	claims := map[string]interface{}{
		"sub":      "user123",
		"username": "testuser",
		"jti":      "session123",
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token, err := generateJWTForTest(claims, secret)
	require.NoError(t, err)

	parsed, err := middleware.ValidateJWT(token, secret)
	assert.NoError(t, err)
	assert.Equal(t, "user123", parsed["sub"])
	assert.Equal(t, "testuser", parsed["username"])

	_, err = middleware.ValidateJWT("invalid-token", secret)
	assert.Error(t, err)

	_, err = middleware.ValidateJWT(token, "wrong-secret")
	assert.Error(t, err)
}

// TestValidateJWT_ExpiredTokenErrorFormat pins what golang-jwt/jwt/v5 actually
// returns for an expired token (measured against v5.3.1, go.mod:14):
// Error() == "token has invalid claims: token is expired", and
// errors.Is(err, jwt.ErrTokenExpired) == true. ws.go used to string-match
// err.Error() == "token is expired by" — not even a substring of the real
// message — which left its SESSION_EXPIRED branch permanently dead
// (agent-os-2zq). If this test starts failing on a future jwt upgrade, the
// fix at ws.go (errors.Is against jwt.ErrTokenExpired) needs to be
// re-verified against whatever the library returns now, rather than assumed
// to still work.
func TestValidateJWT_ExpiredTokenErrorFormat(t *testing.T) {
	secret := "test-secret-key-32-chars-long!!"
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		"sub": "user123",
		"jti": "session123",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)

	_, valErr := middleware.ValidateJWT(token, secret)
	require.Error(t, valErr)

	assert.True(t, errors.Is(valErr, jwt.ErrTokenExpired),
		"expected errors.Is(err, jwt.ErrTokenExpired) to hold for an expired token, got: %v", valErr)
	assert.NotEqual(t, "token is expired by", valErr.Error(),
		"the dead string literal from ws.go must not equal the real message")
}

// TestAuthenticateToken_ExpiredTokenReturnsSessionExpired is the direct
// regression for the dead branch: with the string-literal check at ws.go:177
// ("token is expired by", which the library never returns — see
// TestValidateJWT_ExpiredTokenErrorFormat), an expired token fell through to
// the generic UNAUTHORIZED branch instead of SESSION_EXPIRED. AuthMiddleware
// (middleware/auth.go:134-142) sends SESSION_EXPIRED for the same condition;
// models/errors.go documents that ws.go must match (agent-os-2zq).
func TestAuthenticateToken_ExpiredTokenReturnsSessionExpired(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	secret := "test-secret-key-32-chars-long!!"
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		"sub": "user123",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)

	_, authErr := authenticateToken(token, db, secret)
	require.Error(t, authErr)

	appErr, ok := authErr.(*models.AppError)
	require.True(t, ok, "expected *models.AppError, got %T", authErr)
	assert.Equal(t, models.ErrSessionExpired, appErr.Code,
		"an expired WS token must carry SESSION_EXPIRED per models/errors.go, matching AuthMiddleware")
}

// TestAuthenticateToken_MalformedTokenReturnsSessionExpired covers the other
// half of the same branch: a JWT validation failure that is NOT expiry (bad
// signature, malformed token) fell into ws.go's generic UNAUTHORIZED branch
// too. AuthMiddleware sends SESSION_EXPIRED unconditionally for any JWT
// validation failure (middleware/auth.go:134-142); ws.go must match.
func TestAuthenticateToken_MalformedTokenReturnsSessionExpired(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	_, authErr := authenticateToken("not-a-valid-token", db, "test-secret-key-32-chars-long!!")
	require.Error(t, authErr)

	appErr, ok := authErr.(*models.AppError)
	require.True(t, ok, "expected *models.AppError, got %T", authErr)
	assert.Equal(t, models.ErrSessionExpired, appErr.Code,
		"a malformed WS token must carry SESSION_EXPIRED, not UNAUTHORIZED")
}

// TestAuthenticateToken_MissingSubReturnsSessionExpired covers the
// missing-"sub"-claim branch (ws.go:210-217 before the fix): a structurally
// valid but unusable token ("no usable token" per models/errors.go) minted
// UNAUTHORIZED instead of SESSION_EXPIRED. No "jti" claim is set so the
// session lookup is skipped and the sub check is reached directly.
func TestAuthenticateToken_MissingSubReturnsSessionExpired(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	secret := "test-secret-key-32-chars-long!!"
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)

	_, authErr := authenticateToken(token, db, secret)
	require.Error(t, authErr)

	appErr, ok := authErr.(*models.AppError)
	require.True(t, ok, "expected *models.AppError, got %T", authErr)
	assert.Equal(t, models.ErrSessionExpired, appErr.Code,
		"a token missing its sub claim must carry SESSION_EXPIRED, not UNAUTHORIZED")
}

// TestAuthenticateToken_MissingJtiReturnsSessionExpired guards agent-os-gm5:
// a structurally valid token with a real "sub" but no "jti" claim must not
// skip the session/revocation lookup. Before this fix, claims["jti"].(string)
// failing its type assertion had no else branch, so the token fell straight
// through to the sub check and authenticated with no session row ever
// checked — meaning it could never be revoked by logout.
func TestAuthenticateToken_MissingJtiReturnsSessionExpired(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	secret := "test-secret-key-32-chars-long!!"
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		"sub": "user123",
		// Deliberately no "jti" claim — this is the defect under test.
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)

	_, authErr := authenticateToken(token, db, secret)
	require.Error(t, authErr)

	appErr, ok := authErr.(*models.AppError)
	require.True(t, ok, "expected *models.AppError, got %T", authErr)
	assert.Equal(t, models.ErrSessionExpired, appErr.Code,
		"a token missing its jti claim must carry SESSION_EXPIRED, not skip the session lookup")
}

// TestAuthenticateToken_RevokedSessionRejectsToken is the positive control
// for agent-os-gm5: a normal token WITH jti must still authenticate, and
// deleting its session row (what logout does) must revoke it — proving the
// jti guard isn't just rejecting everything.
func TestAuthenticateToken_RevokedSessionRejectsToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	userID := uuid.New().String()
	user := models.User{
		ID:        userID,
		Username:  "testuser",
		Password:  "hashedpassword",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, db.CreateUser(user))

	sessionID := uuid.New().String()
	session := models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.CreateSession(session))

	claims := map[string]interface{}{
		"sub":      userID,
		"username": "testuser",
		"jti":      sessionID,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}
	token, err := generateJWTForTest(claims, "test-secret-key-32-chars-long!!")
	require.NoError(t, err)

	resultUserID, err := authenticateToken(token, db, "test-secret-key-32-chars-long!!")
	require.NoError(t, err)
	assert.Equal(t, userID, resultUserID)

	// Logout: delete the session the jti points at.
	require.NoError(t, db.DeleteSession(sessionID))

	_, authErr := authenticateToken(token, db, "test-secret-key-32-chars-long!!")
	require.Error(t, authErr)
	appErr, ok := authErr.(*models.AppError)
	require.True(t, ok, "expected *models.AppError, got %T", authErr)
	assert.Equal(t, models.ErrSessionExpired, appErr.Code,
		"the same token must be rejected once its session is revoked")
}

func TestAuthenticateToken_EmptyToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	_, err = authenticateToken("", db, "test-secret")
	assert.Error(t, err)
}

func TestAuthenticateToken_ValidToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	userID := uuid.New().String()
	user := models.User{
		ID:        userID,
		Username:  "testuser",
		Password:  "hashedpassword",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err = db.CreateUser(user)
	require.NoError(t, err)

	sessionID := uuid.New().String()

	session := models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}
	err = db.CreateSession(session)
	require.NoError(t, err)

	claims := map[string]interface{}{
		"sub":      userID,
		"username": "testuser",
		"jti":      sessionID,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token, err := generateJWTForTest(claims, "test-secret-key-32-chars-long!!")
	require.NoError(t, err)

	resultUserID, err := authenticateToken(token, db, "test-secret-key-32-chars-long!!")
	assert.NoError(t, err)
	assert.Equal(t, userID, resultUserID)
}

func TestAuthenticateToken_InvalidToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	_, err = authenticateToken("not-a-valid-token", db, "test-secret-key-32-chars-long!!")
	assert.Error(t, err)
}

func generateJWTForTest(claims map[string]interface{}, secret string) (string, error) {
	return generateJWT(
		claims["sub"].(string),
		claims["username"].(string),
		claims["jti"].(string),
		secret,
	)
}

func originReq(origin, host string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws/events", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestUpgraderCheckOrigin(t *testing.T) {
	cases := []struct {
		name         string
		corsOrigins  string
		authDisabled bool
		origin       string
		host         string
		want         bool
	}{
		{"no origin header is allowed", "", false, "", "localhost:5001", true},
		{"same-origin allowed when no allowlist", "", false, "http://localhost:5001", "localhost:5001", true},
		{"cross-origin denied when auth on and no allowlist", "", false, "http://localhost:3001", "localhost:5001", false},
		{"dev proxy origin allowed when auth disabled", "", true, "http://localhost:3001", "localhost:5001", true},
		{"127.0.0.1 dev origin allowed when auth disabled", "", true, "http://127.0.0.1:3001", "localhost:5001", true},
		{"non-loopback denied even when auth disabled", "", true, "http://evil.example.com", "localhost:5001", false},
		{"allowlisted origin permitted", "https://capstan.ctsvps.work", false, "https://capstan.ctsvps.work", "capstan.ctsvps.work", true},
		{"non-allowlisted origin denied", "https://capstan.ctsvps.work", false, "https://evil.example.com", "capstan.ctsvps.work", false},
		{"allowlist plus auth disabled still allows loopback", "https://capstan.ctsvps.work", true, "http://localhost:3001", "localhost:5001", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			InitUpgrader(tc.corsOrigins, tc.authDisabled)
			got := upgrader.CheckOrigin(originReq(tc.origin, tc.host))
			assert.Equal(t, tc.want, got)
		})
	}
}
