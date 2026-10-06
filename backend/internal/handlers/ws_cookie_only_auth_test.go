package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

const cookieOnlyTestSecret = "test-secret-key-32-chars-long!!!"

// newCookieOnlyProbeServer serves one WS route through serveWS with auth
// enabled. An authenticated connection is told its UserID in a text frame,
// so a test can tell "accepted" apart from "refused" by what it reads. The
// done signal lets each case wait for the handler, so serveWS's refusal log
// line lands inside this test and not in a later test's captured buffer.
func newCookieOnlyProbeServer(t *testing.T, db *database.DB) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	router, handled := withHandlerDoneSignal(gin.New())
	cm := NewConnectionManager(10)
	router.GET("/ws/probe", func(c *gin.Context) {
		conn, cleanup, err := serveWS(c, db, cookieOnlyTestSecret, false, cm, wsRegistration{
			refuseCode:   CloseCodeRateLimit,
			refuseReason: "Too many connections",
		})
		if err != nil {
			return
		}
		defer cleanup()
		if err := conn.Conn.WriteMessage(websocket.TextMessage, []byte("authenticated:"+conn.UserID)); err != nil {
			t.Errorf("writing the accepted marker: %v", err)
		}
		_ = conn.Conn.Close()
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, handled
}

// readFirstOutcome returns the first text frame the server sends, or the
// close code and reason when it closes first.
func readFirstOutcome(t *testing.T, conn *websocket.Conn) (text string, closeCode int, closeText string) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	_, data, err := conn.ReadMessage()
	if err == nil {
		return string(data), 0, ""
	}
	if ce, ok := err.(*websocket.CloseError); ok {
		return "", ce.Code, ce.Text
	}
	t.Fatalf("reading the first outcome: %v", err)
	return "", 0, ""
}

// TestWSAuth_CookieIsTheOnlyCredential pins agent-os-n4ca.2: the WebSocket
// gate accepts the capstan_token cookie and nothing else. The in-frame
// {type:"auth", token} handshake was removed with the login body token, so a
// client with no cookie is refused even when it sends a valid token in the
// first frame. Both arms use the same valid token, so the refusal can only
// come from where the token travelled.
func TestWSAuth_CookieIsTheOnlyCredential(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	userID, token := createAuthedTestUser(t, db, cookieOnlyTestSecret)
	srv, handled := newCookieOnlyProbeServer(t, db)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/probe"

	t.Run("cookie is accepted", func(t *testing.T) {
		conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": {"capstan_token=" + token}})
		require.NoError(t, err)
		defer conn.Close()
		defer resp.Body.Close()

		text, code, reason := readFirstOutcome(t, conn)
		waitHandled(t, handled)
		require.Equal(t, "authenticated:"+userID, text, "closed with %d %q instead", code, reason)
	})

	t.Run("token in the first frame without a cookie is refused", func(t *testing.T) {
		conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
		require.NoError(t, err)
		defer conn.Close()
		defer resp.Body.Close()

		require.NoError(t, conn.WriteJSON(map[string]string{"type": "auth", "token": token}))

		text, code, reason := readFirstOutcome(t, conn)
		waitHandled(t, handled)
		require.Empty(t, text, "a frame token must not authenticate the connection")
		require.Equal(t, CloseCodeAuthFailure, code)
		require.Equal(t, "Authentication required", reason)
	})
}
