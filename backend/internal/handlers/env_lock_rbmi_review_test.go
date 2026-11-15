package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-rbmi review F2: the server must select "capstan" whatever order
// the client offers its protocols in. With "capstan" offered first, a server
// that echoes the client's FIRST offer passed every test; offering the token
// first is what tells the two apart.
func requireEchoOnlyCapstan(t *testing.T, resp *http.Response, conn *websocket.Conn, token string) {
	t.Helper()
	require.Equal(t, "capstan", resp.Header.Get("Sec-WebSocket-Protocol"))
	require.Equal(t, "capstan", conn.Subprotocol())
	for name, values := range resp.Header {
		for _, v := range values {
			require.NotContains(t, v, token, "the unlock token came back in response header %s", name)
		}
	}
}

func TestStreamLogs_TokenOfferedFirstStillEchoesOnlyCapstan(t *testing.T) {
	store := services.NewEnvUnlockStore()
	srv := rbmiLogsServer(t, store)
	token := rbmiMint(t, store)

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"capstan-unlock." + token, "capstan"}
	conn, resp, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws/logs/stack-a", nil)
	require.NoError(t, err)
	defer conn.Close()
	resp.Body.Close()
	requireEchoOnlyCapstan(t, resp, conn, token)

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	var line LogLine
	require.NoError(t, conn.ReadJSON(&line))
	requireRaw(t, line.Message)
}

func TestTerminalWS_TokenOfferedFirstStillEchoesOnlyCapstan(t *testing.T) {
	f := rbmiTerminalServer(t)
	token := rbmiMint(t, f.store)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"capstan-unlock." + token, "capstan"}
	conn, resp, err := dialer.Dial(f.url, nil)
	require.NoError(t, err)
	defer conn.Close()
	resp.Body.Close()
	requireEchoOnlyCapstan(t, resp, conn, token)
}

// agent-os-rbmi review F3: a dead token sent in-band while the first token is
// live must change nothing. The terminal stays open past several re-checks
// and the shell still answers. If renew swapped in a token it never checked,
// the next re-check would read locked and close the session.
func TestTerminalWS_DeadInBandTokenLeavesALiveSessionWorking(t *testing.T) {
	shortRecheck(t)
	unlock := newStubUnlock("t1")
	lt := dialLiveTerminal(t, unlock.middleware("t1"))
	unlock.waitChecked(t, "t1", 1)

	require.NoError(t, lt.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"unlock","token":"forged"}`)))
	unlock.waitChecked(t, "forged", 1)
	unlock.waitChecked(t, "t1", 3) // re-checks still run on t1
	lt.requireOpen(t)

	// The pty echoes what is typed, so input coming back proves the session
	// is live end to end, not just an open socket.
	require.NoError(t, lt.conn.WriteMessage(websocket.BinaryMessage, []byte("rbmiping\n")))
	lt.requireOutputContains(t, "rbmiping")
}
