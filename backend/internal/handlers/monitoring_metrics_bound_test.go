package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// agent-os-tk2q review F1: the metrics WebSocket listed the stack's containers
// on a context nothing cancels until the handler returns, and the Docker
// client has no HTTP timeout. A daemon that accepts the request and never
// answers parked the handler goroutine and its socket slot until it did.
// Producer: handleMetricsWebSocket's lookup context. Consumer: the
// GetContainersForStack calls at connect and on every empty-host tick.

func shortMetricsLookupBound(t *testing.T) {
	t.Helper()
	old := metricsLookupBound
	metricsLookupBound = 200 * time.Millisecond
	t.Cleanup(func() { metricsLookupBound = old })
}

func TestMonitoringMetricsWS_HungDaemonAtConnectClosesWithAnError(t *testing.T) {
	shortMetricsLookupBound(t)
	srv, _ := newHeldFakeDockerMetricsServer(t, http.StatusOK, `[]`) // never released: the daemon hangs
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, newFakeMonitorService(t, srv), cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	require.NoError(t, clientConn.SetReadDeadline(hangGuardDeadline(t)))
	_, _, err := clientConn.ReadMessage()
	var closeErr *websocket.CloseError
	require.True(t, errors.As(err, &closeErr), "the handler must give up on the hung lookup and close the socket, got %v", err)
	require.Equal(t, websocket.CloseInternalServerErr, closeErr.Code)
}

func TestMonitoringMetricsWS_HungDaemonOnAnEmptyHostTickStillSendsAFrame(t *testing.T) {
	shortMetricsLookupBound(t)
	var lists atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/json"):
			if lists.Add(1) > 1 {
				select { // every re-check hangs
				case <-r.Context().Done():
				case <-release:
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	// Registered after srv.Close, so it runs first: a handler still parked on
	// a hung re-check must not block srv.Close.
	t.Cleanup(func() { close(release) })
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, newFakeMonitorService(t, srv), cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	frame, _ := readMetricsFrame(t, clientConn, hangGuardDeadline(t),
		"a re-check the daemon never answers must time out and leave the tick to send its empty frame")
	require.Empty(t, frame.Containers)
	require.GreaterOrEqual(t, lists.Load(), int32(2), "control: the tick did re-check the host")
}
