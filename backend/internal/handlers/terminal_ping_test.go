package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// An idle terminal must receive server pings. Browsers send no keepalive of
// their own, so without pings the server's 60s read deadline closes a
// terminal nobody typed in for a minute (agent-os-0vjx).
//
// A fake `docker` on PATH keeps the session alive with no daemon: the
// `docker exec -it` that CreateSession starts just sleeps, and any other
// call (CloseSession's reapContainerShell) exits at once.
func TestTerminalPingsIdleConnection(t *testing.T) {
	fakeBin := t.TempDir()
	script := "#!/bin/sh\n[ \"$2\" = \"-it\" ] && exec sleep 30\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := terminalPingInterval
	terminalPingInterval = 50 * time.Millisecond
	t.Cleanup(func() { terminalPingInterval = old })

	lister := &fakeContainerLister{containersByProject: map[string][]string{
		"proj-a": {"proj-a-web-1"},
	}}
	f := newTerminalFixture(t, NewConnectionManager(5), lister)

	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/api/ws/terminal/stack-a/proj-a-web-1"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	defer conn.Close()

	pinged := make(chan struct{})
	conn.SetPingHandler(func(string) error {
		select {
		case <-pinged:
		default:
			close(pinged)
		}
		return nil
	})

	// Control frames are only processed while the client reads. A read error
	// before any ping means the session never came up, which is a fixture
	// failure, not the defect under test.
	readErr := make(chan error, 1)
	go func() {
		_ = conn.SetReadDeadline(hangGuardDeadline(t))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				readErr <- err
				return
			}
		}
	}()

	// The bound is the assertion: 40 ping intervals.
	select {
	case <-pinged:
	case err := <-readErr:
		t.Fatalf("connection ended before any ping: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("no ping within 2s on an idle terminal; the server's 60s read deadline will close it")
	}
}
