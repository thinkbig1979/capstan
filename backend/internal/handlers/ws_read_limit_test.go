package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// readLimitProbe stands up a route that upgrades through serveWS (the one
// upgrade site every WS handler shares) and reads one frame, reporting what the
// server-side read returned. It is the shape of the read pumps in
// operations.go, update_jobs_ws.go and backup.go.
func readLimitProbe(t *testing.T, reg wsRegistration) (dial func() *websocket.Conn, results <-chan error) {
	t.Helper()

	out := make(chan error, 1)
	router := gin.New()
	router.GET("/probe", func(c *gin.Context) {
		conn, release, err := serveWS(c, nil, "test-secret-key-32-chars-long!!!", true, nil, reg)
		if err != nil {
			out <- err
			return
		}
		defer release()
		_, _, readErr := conn.Conn.ReadMessage()
		out <- readErr
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return func() *websocket.Conn {
		url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/probe"
		conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close(); _ = resp.Body.Close() })
		return conn
	}, out
}

func awaitRead(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("server-side read never returned")
		return nil
	}
}

// TestWSReadLimit_FrameOverLimitIsRefused pins agent-os-a1ye.8: a frame of
// limit+1 bytes must fail the server's read with ErrReadLimit. Before the fix
// the read returned the whole frame (err == nil), because gorilla's default
// limit is unlimited.
func TestWSReadLimit_FrameOverLimitIsRefused(t *testing.T) {
	dial, results := readLimitProbe(t, wsRegistration{})
	conn := dial()

	require.NoError(t, conn.WriteMessage(websocket.TextMessage,
		[]byte(strings.Repeat("a", int(wsReadLimitDefault)+1))))

	err := awaitRead(t, results)
	require.ErrorIs(t, err, websocket.ErrReadLimit,
		"a %d-byte frame (limit+1) must fail the server read with ErrReadLimit; err == nil means it was read in full",
		wsReadLimitDefault+1)
}

// TestWSReadLimit_FrameAtLimitIsRead is the other side of the same instrument:
// exactly limit bytes still reads, so the test above cannot pass by a limit that
// rejects everything.
func TestWSReadLimit_FrameAtLimitIsRead(t *testing.T) {
	dial, results := readLimitProbe(t, wsRegistration{})
	conn := dial()

	require.NoError(t, conn.WriteMessage(websocket.TextMessage,
		[]byte(strings.Repeat("a", int(wsReadLimitDefault)))))

	require.NoError(t, awaitRead(t, results), "a frame of exactly the limit must be read")
}

// TestWSReadLimit_ClientSeesTooBigClose: the over-limit close is the existing
// gorilla policy close (1009), not a bare TCP drop.
func TestWSReadLimit_ClientSeesTooBigClose(t *testing.T) {
	dial, _ := readLimitProbe(t, wsRegistration{})
	conn := dial()
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))

	require.NoError(t, conn.WriteMessage(websocket.TextMessage,
		[]byte(strings.Repeat("a", int(wsReadLimitDefault)+1))))

	_, _, err := conn.ReadMessage()
	require.True(t, websocket.IsCloseError(err, websocket.CloseMessageTooBig),
		"client read after an over-limit frame = %v, want close 1009", err)
}

// TestWSReadLimit_RegistrationOverridesDefault proves wsRegistration.readLimit
// is plumbed through serveWS to the connection (terminal.go sets it to
// wsReadLimitTerminal). A frame well under the default but over a custom limit
// must be refused; a default-only implementation would read it.
func TestWSReadLimit_RegistrationOverridesDefault(t *testing.T) {
	const custom int64 = 1024
	dial, results := readLimitProbe(t, wsRegistration{readLimit: custom})
	conn := dial()

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, make([]byte, custom+1)))

	require.ErrorIs(t, awaitRead(t, results), websocket.ErrReadLimit,
		"a %d-byte frame is far under wsReadLimitDefault but over the registration's %d limit", custom+1, custom)
}

// TestWSReadLimit_LogsOnceWithoutPayload: one WARN naming the limit, carrying
// none of the frame's bytes.
func TestWSReadLimit_LogsOnceWithoutPayload(t *testing.T) {
	buf := captureHandlerLogs(t)
	done := make(chan struct{})
	router := gin.New()
	router.GET("/probe", func(c *gin.Context) {
		conn, release, err := serveWS(c, nil, "test-secret-key-32-chars-long!!!", true, nil, wsRegistration{})
		if err != nil {
			close(done)
			return
		}
		defer release()
		_, _, readErr := conn.Conn.ReadMessage()
		conn.logReadErr(readErr)
		close(done)
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/probe"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()
	defer resp.Body.Close()
	require.NoError(t, conn.WriteMessage(websocket.TextMessage,
		[]byte(strings.Repeat("SECRETPAYLOAD", int(wsReadLimitDefault)/10))))
	<-done

	got := buf.String()
	require.Equal(t, 1, strings.Count(got, "exceeded read limit"), "captured = %q", got)
	require.Contains(t, got, "level=WARN")
	require.Contains(t, got, "limit_bytes=65536")
	require.NotContains(t, got, "SECRETPAYLOAD")
}
