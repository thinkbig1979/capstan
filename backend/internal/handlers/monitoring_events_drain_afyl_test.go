package handlers

import (
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The identity upgradeConnection gives an unauthenticated loopback dial under
// AUTH_DISABLED (agent-os-8uuw), so the slots these tests open count here.
const eventsDrainUser = "anon:127.0.0.1"

// TestMonitoringEventsWS_ClientGoneFreesSlotWithoutEvents is the regression
// test for agent-os-afyl. handleEventsWebSocket had no reader, so it noticed a
// departed client only when a Docker event made a write fail. On a quiet host
// no event comes, and every closed tab kept its per-user slot until restart:
// ten page loads filled the cap of 10 and the next socket (the log view) was
// refused with 4429. No event is broadcast here on purpose; the slot must be
// freed by noticing the client is gone, not by a failed write.
func TestMonitoringEventsWS_ClientGoneFreesSlotWithoutEvents(t *testing.T) {
	const n = 3
	cases := []struct {
		name  string
		leave func(t *testing.T, c *websocket.Conn)
	}{
		// The TCP connection ends with no WebSocket close frame: a killed
		// tab or a closed browser context.
		{"abrupt", func(_ *testing.T, c *websocket.Conn) { _ = c.NetConn().Close() }},
		// A page navigating away: a close frame, then the TCP close.
		{"clean", func(t *testing.T, c *websocket.Conn) {
			require.NoError(t, c.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "")))
			_ = c.Close()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := NewConnectionManager(10)
			eb := NewEventBus()
			srv := newEventsTestFixture(t, cm, eb)

			var conns []*websocket.Conn
			for i := 0; i < n; i++ {
				c, resp := dialEvents(t, srv)
				resp.Body.Close()
				conns = append(conns, c)
			}
			require.Eventually(t, func() bool { return cm.CountByUser(eventsDrainUser) == n },
				time.Until(hangGuardDeadline(t)), 10*time.Millisecond,
				"the %d sockets never registered", n)

			for _, c := range conns {
				tc.leave(t, c)
			}

			// Hang guard, not a latency budget: a fixed handler frees the
			// slots as soon as its reader sees the close, in milliseconds.
			// The base never frees them at all.
			require.Eventually(t, func() bool { return cm.CountByUser(eventsDrainUser) == 0 },
				time.Until(hangGuardDeadline(t)), 10*time.Millisecond,
				"slots never freed after the clients left: %d still counted", cm.CountByUser(eventsDrainUser))
			require.Equal(t, 0, eb.SubscriberCount(), "handler left its event bus subscription behind")
		})
	}
}

// TestMonitoringEventsWS_LiveClientKeepsSlotAndEvents is the control: a reader
// that ended the handler too eagerly (say on the first frame it reads) would
// pass the test above. A client that sends a data frame and then stays
// connected past a quiet spell must stay counted and still receive an event.
// Pongs are not exercised: the server's first ping is DefaultPingInterval
// (30s) away.
func TestMonitoringEventsWS_LiveClientKeepsSlotAndEvents(t *testing.T) {
	cm := NewConnectionManager(10)
	eb := NewEventBus()
	srv := newEventsTestFixture(t, cm, eb)

	clientConn, resp := dialEvents(t, srv)
	defer clientConn.Close()
	defer resp.Body.Close()

	require.Eventually(t, func() bool { return eb.SubscriberCount() == 1 },
		time.Until(hangGuardDeadline(t)), 10*time.Millisecond,
		"handler never subscribed to the event bus")

	// One data frame, then a quiet spell with the client alive; the slot
	// must still be held after both. The real client sends no data frames;
	// this pins that watchPeer ends the handler on a read ERROR only.
	require.NoError(t, clientConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello"}`)))
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 1, cm.CountByUser(eventsDrainUser), "a live client lost its slot after sending a frame")

	eb.Broadcast(models.StackEvent{Type: "test", Timestamp: time.Now()})
	require.NoError(t, clientConn.SetReadDeadline(hangGuardDeadline(t)))
	_, _, err := clientConn.ReadMessage()
	require.NoError(t, err, "expected the broadcast event while still connected")
	require.Equal(t, 1, cm.CountByUser(eventsDrainUser), "a live client lost its slot after an event")
}

// TestMonitoringEventsWS_RevokeRacingClientLeaveDropsSubscription pins the
// revocation path through the new reader (adopted from the agent-os-afyl
// review). closeMatching (logout, password change, the session sweep)
// removes the slot itself, but before watchPeer nothing told the handler: a
// revoked events socket kept its event bus subscription and goroutines until
// an event write failed. Now closeMatching's Conn.Close fails watchPeer's
// read, which ends the handler. Half the clients leave at the same moment, so
// both exits race; every subscription and slot must end either way, and a
// client still connected must see the 4401 close frame.
func TestMonitoringEventsWS_RevokeRacingClientLeaveDropsSubscription(t *testing.T) {
	const n = 5
	cm := NewConnectionManager(10)
	eb := NewEventBus()
	srv := newEventsTestFixture(t, cm, eb)

	var conns []*websocket.Conn
	for i := 0; i < n; i++ {
		c, resp := dialEvents(t, srv)
		resp.Body.Close()
		conns = append(conns, c)
	}
	require.Eventually(t, func() bool { return eb.SubscriberCount() == n },
		time.Until(hangGuardDeadline(t)), 5*time.Millisecond,
		"the %d handlers never subscribed", n)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cm.closeMatching(func(*Connection) bool { return true }, CloseCodeAuthFailure, "Session revoked", 0)
	}()
	go func() {
		defer wg.Done()
		for i, c := range conns {
			if i%2 == 0 {
				_ = c.NetConn().Close()
			}
		}
	}()
	wg.Wait()

	require.Eventually(t, func() bool { return eb.SubscriberCount() == 0 && cm.Count() == 0 },
		time.Until(hangGuardDeadline(t)), 5*time.Millisecond,
		"revoked handlers kept running: subscriptions=%d registered=%d", eb.SubscriberCount(), cm.Count())

	// conns[1] never closed its side, so it must read the revocation frame.
	require.NoError(t, conns[1].SetReadDeadline(hangGuardDeadline(t)))
	_, _, err := conns[1].ReadMessage()
	var ce *websocket.CloseError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, CloseCodeAuthFailure, ce.Code)
	for i, c := range conns {
		if i%2 == 1 {
			_ = c.Close()
		}
	}
}
