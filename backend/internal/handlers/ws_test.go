package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
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

// TestConnectionManager_CloseForSession is two-sided on the same instrument
// (agent-os-teop acceptance #2): the revoked session's connection must close
// AND a connection for a different, live session must stay open. A
// close-everything fix passes a one-sided test and is wrong.
func TestConnectionManager_CloseForSession(t *testing.T) {
	cm := NewConnectionManager(10)
	revoked := &Connection{ID: uuid.New().String(), UserID: "user1", SessionID: "sess-revoked"}
	live := &Connection{ID: uuid.New().String(), UserID: "user2", SessionID: "sess-live"}
	require.NoError(t, cm.Add(revoked.ID, revoked))
	require.NoError(t, cm.Add(live.ID, live))

	cm.CloseForSession("sess-revoked")

	_, revokedPresent := cm.Get(revoked.ID)
	assert.False(t, revokedPresent, "the connection for the revoked session must be closed and removed")

	_, livePresent := cm.Get(live.ID)
	assert.True(t, livePresent, "a connection for a different, non-revoked session must stay open")
	assert.Equal(t, 1, cm.Count())
	assert.Equal(t, 1, cm.CountByUser("user2"))
}

// TestConnectionManager_CloseForSession_EmptyIsNoop guards the AUTH_DISABLED
// case named in the brief: upgradeConnection never sets SessionID when auth
// is disabled, so every anonymous connection carries SessionID "". If
// CloseForSession("") matched "" == "" it would tear down every dev-mode
// connection on the host from a single anonymous logout.
func TestConnectionManager_CloseForSession_EmptyIsNoop(t *testing.T) {
	cm := NewConnectionManager(10)
	anon := &Connection{ID: uuid.New().String(), UserID: "anonymous", SessionID: ""}
	require.NoError(t, cm.Add(anon.ID, anon))

	cm.CloseForSession("")

	_, present := cm.Get(anon.ID)
	assert.True(t, present, "an empty sessionID must close nothing, not match every empty-SessionID connection")
}

// TestConnectionManager_CloseForUser_ExcludesCaller mirrors
// database.DeleteSessionsByUserExcluding's semantics: a password change
// revokes every OTHER live session for the user, never the request's own.
// Two-sided plus a third control: the caller's own connection stays open,
// another session for the SAME user closes, and a connection for a
// DIFFERENT user is untouched.
func TestConnectionManager_CloseForUser_ExcludesCaller(t *testing.T) {
	cm := NewConnectionManager(10)
	caller := &Connection{ID: uuid.New().String(), UserID: "user1", SessionID: "sess-caller"}
	otherSession := &Connection{ID: uuid.New().String(), UserID: "user1", SessionID: "sess-other"}
	otherUser := &Connection{ID: uuid.New().String(), UserID: "user2", SessionID: "sess-elsewhere"}
	require.NoError(t, cm.Add(caller.ID, caller))
	require.NoError(t, cm.Add(otherSession.ID, otherSession))
	require.NoError(t, cm.Add(otherUser.ID, otherUser))

	cm.CloseForUser("user1", "sess-caller")

	_, callerPresent := cm.Get(caller.ID)
	assert.True(t, callerPresent, "the caller's own session must not be closed by its own password change")

	_, otherSessionPresent := cm.Get(otherSession.ID)
	assert.False(t, otherSessionPresent, "another live session for the same user must be closed")

	_, otherUserPresent := cm.Get(otherUser.ID)
	assert.True(t, otherUserPresent, "a connection for a different user must be untouched")
}

// TestConnectionManager_CloseForUser_AnonymousIsNoop is CloseForUser's half of
// the AUTH_DISABLED guard: upgradeConnection assigns every connection userID
// "anonymous" in that mode, so CloseForUser("anonymous", ...) must not be
// triggerable into closing every dev-mode connection on the host.
func TestConnectionManager_CloseForUser_AnonymousIsNoop(t *testing.T) {
	cm := NewConnectionManager(10)
	anon := &Connection{ID: uuid.New().String(), UserID: "anonymous", SessionID: ""}
	require.NoError(t, cm.Add(anon.ID, anon))

	cm.CloseForUser("anonymous", "")

	_, present := cm.Get(anon.ID)
	assert.True(t, present, `userID "anonymous" (what AUTH_DISABLED assigns) must close nothing`)
}

// TestSafeWriteMessage_SurvivesConcurrentRevocationClose is the regression for
// the crash hazard measured on agent-os-teop: closing a live connection via
// CloseForSession/CloseForUser while another goroutine is mid-write to the
// SAME gorilla connection (exactly terminal.go's PTY writer racing a
// revocation from an HTTP request goroutine) is a genuine concurrent write.
// gorilla panics on a concurrent WriteMessage — best-effort, unsynchronized
// c.isWriting bool (gorilla/websocket@v1.5.3 conn.go:610-624) — on whichever
// goroutine loses the race, and nothing recovers a panic in a bare `go`
// goroutine like writeToWebSocket's.
//
// What prevents it: safeWriteCloseMessage sends the close frame via gorilla's
// WriteControl rather than WriteMessage. WriteControl is documented safe to
// call concurrently with an in-flight data write (doc.go:133-134: "The Close
// and WriteControl methods can be called concurrently with all other
// methods") — it takes gorilla's own internal control-frame lock, not
// WriteMutex, so this test exercises that contract directly rather than
// safeWriteMessage's WriteMutex (which still serializes terminal.go/logs.go's
// OWN data writers against each other, but is not what makes THIS close safe).
// A test that only closes an IDLE connection would pass trivially and prove
// nothing about this — this one keeps a real writer goroutine hammering the
// connection throughout the close.
func TestSafeWriteMessage_SurvivesConcurrentRevocationClose(t *testing.T) {
	// A channel handoff, not a polled variable: Upgrade() itself writes the
	// handshake response to the raw net.Conn, and reading a plain variable
	// from the test goroutine before that write has a synchronizes-with edge
	// with it is its own data race, independent of anything under test here.
	serverConnCh := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConnCh <- c
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer clientConn.Close()
	defer resp.Body.Close()

	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(time.Second):
		t.Fatal("server-side upgrade did not complete in time")
	}

	conn := &Connection{
		ID:        uuid.New().String(),
		UserID:    "user1",
		SessionID: "sess-1",
		Conn:      serverConn,
		CreatedAt: time.Now(),
	}
	cm := NewConnectionManager(10)
	require.NoError(t, cm.Add(conn.ID, conn))

	// Drain client-side reads so the server's writes never block on a full
	// socket buffer while the writer loop below spins.
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	stop := make(chan struct{})
	writerDone := make(chan struct{})
	var writerPanic any
	go func() {
		defer close(writerDone)
		// This goroutine's own recover: gorilla's panic fires in whichever
		// goroutine loses the race (its detector is an unsynchronized bool,
		// not a mutex), so it can land here instead of in CloseForSession's
		// goroutine. Recovering locally turns a would-be process crash into a
		// normal, reportable test failure.
		defer func() { writerPanic = recover() }()
		for i := 0; i < 5000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := safeWriteMessage(conn, websocket.BinaryMessage, []byte("pty-output")); err != nil {
				return
			}
		}
	}()

	// Give the writer a head start so the close below lands mid-stream —
	// the shape of a logout arriving while a terminal is actively streaming.
	time.Sleep(2 * time.Millisecond)

	// Its own recover too: gorilla's detector is an unsynchronized bool, not a
	// mutex, so the panic can land in EITHER goroutine depending on timing —
	// confirmed by observation, not assumed: reproducing this hazard against
	// an intentionally-broken build hit both call sites across repeated runs.
	// Without this recover a regression here would hard-crash the whole test
	// binary instead of failing this one test.
	var closePanic any
	func() {
		defer func() { closePanic = recover() }()
		cm.CloseForSession("sess-1")
	}()
	close(stop)
	<-writerDone

	assert.Nil(t, writerPanic,
		"a revoked-session close must not race the connection's own writer and crash the process: %v", writerPanic)
	assert.Nil(t, closePanic,
		"closing a revoked session must not itself panic on a concurrent write: %v", closePanic)

	_, stillPresent := cm.Get(conn.ID)
	assert.False(t, stillPresent, "the revoked connection must be removed from the manager")
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
//
// A real, unexpired session is seeded and its ID set as "jti" (agent-os-gm5)
// so the token clears the jti guard and is rejected for its own expiry, not
// for lacking jti. Without this, a jti-less-but-otherwise-valid token would
// be rejected with the same SESSION_EXPIRED code regardless of whether it
// was actually expired, and this test could no longer tell the two apart —
// it would keep passing even if real expiry detection broke.
func TestAuthenticateToken_ExpiredTokenReturnsSessionExpired(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	now := time.Now()
	userID := uuid.New().String()
	user := models.User{
		ID:        userID,
		Username:  "expired-token-user",
		Password:  "irrelevant-hash",
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, db.CreateUser(user))

	sessionID := uuid.New().String()
	session := models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
	}
	require.NoError(t, db.CreateSession(session))

	secret := "test-secret-key-32-chars-long!!"
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		"sub": userID,
		"jti": sessionID,
		"iat": now.Add(-2 * time.Hour).Unix(),
		"exp": now.Add(-1 * time.Hour).Unix(),
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
// UNAUTHORIZED instead of SESSION_EXPIRED. A real, unexpired session is
// seeded and its ID set as "jti" so the token clears the jti guard
// (agent-os-gm5) and reaches the sub check on its own merits — without a
// live session behind "jti", the jti guard would reject the token first and
// this test would no longer isolate the missing-sub defect it exists to
// catch (mirrors middleware/auth_test.go's TestAuthMiddleware_MissingSubIsSessionExpired).
func TestAuthenticateToken_MissingSubReturnsSessionExpired(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	now := time.Now()
	userID := uuid.New().String()
	user := models.User{
		ID:        userID,
		Username:  "subless-token-user",
		Password:  "irrelevant-hash",
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, db.CreateUser(user))

	sessionID := uuid.New().String()
	session := models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
	}
	require.NoError(t, db.CreateSession(session))

	secret := "test-secret-key-32-chars-long!!"
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		// Deliberately no "sub" claim — this is the defect under test.
		"jti": sessionID,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
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
