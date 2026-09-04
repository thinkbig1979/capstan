package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// TestDashboardMetricsWS_EmptyHostFrame_ContainersNotNull is the regression
// test for agent-os-5scv: on a host with zero running containers,
// handleDashboardMetricsWebSocket's len(containerIDs)==0 ticker branch
// (dashboard.go, inside handleDashboardMetricsWebSocket) must emit
// "containers":[] on the wire, not "containers":null. Go marshals a nil
// slice as JSON null; the frontend's MetricsMessage.containers is typed as a
// non-nullable array and dereferenced unguarded, so null crashes the app.
//
// This asserts on the RAW MESSAGE BYTES deliberately: decoding into a Go
// []models.ContainerMetrics (or a JS array) collapses the null/[] distinction
// — both unmarshal to an empty/nil slice — so a decoded-value assertion
// cannot fail and would not be evidence of anything.
func TestDashboardMetricsWS_EmptyHostFrame_ContainersNotNull(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[]`, nil)
	docker := newTestDockerServiceAgainst(t, srv)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cm := NewConnectionManager(10)
	handler := NewDashboardHandler(nil, docker, db, cm)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	wsSrv := httptest.NewServer(router)
	t.Cleanup(wsSrv.Close)

	url := "ws" + strings.TrimPrefix(wsSrv.URL, "http") + "/api/ws/dashboard/metrics"
	clientConn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer clientConn.Close()
	defer resp.Body.Close()

	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, raw, err := clientConn.ReadMessage()
	require.NoError(t, err, "expected a metrics frame on the empty-host ticker branch")

	t.Logf("raw frame bytes: %s", raw)

	if bytes.Contains(raw, []byte(`"containers":null`)) {
		t.Fatalf("empty-host frame carries \"containers\":null on the wire (nil slice marshaled), raw=%s", raw)
	}
	if !bytes.Contains(raw, []byte(`"containers":[]`)) {
		t.Fatalf("empty-host frame does not carry \"containers\":[] on the wire, raw=%s", raw)
	}
}

// TestDashboardMetricsWS_PopulatedHostFrame_ContainersPopulated is the
// must-pass control for the test above: a host WITH running containers must
// keep streaming real per-container entries, on the raw wire bytes, not an
// empty/null placeholder. Without this control, a fix that always emits
// "containers":[] regardless of actual container state would pass the
// empty-host arm for the wrong reason.
func TestDashboardMetricsWS_PopulatedHostFrame_ContainersPopulated(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[{"Id":"c1","State":"running"}]`, streamingStatsHandler())
	docker := newTestDockerServiceAgainst(t, srv)
	monitor := newFakeMonitorService(t, srv)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cm := NewConnectionManager(10)
	handler := NewDashboardHandler(monitor, docker, db, cm)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	wsSrv := httptest.NewServer(router)
	t.Cleanup(wsSrv.Close)

	url := "ws" + strings.TrimPrefix(wsSrv.URL, "http") + "/api/ws/dashboard/metrics"
	clientConn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer clientConn.Close()
	defer resp.Body.Close()

	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, raw, err := clientConn.ReadMessage()
	require.NoError(t, err, "expected a metrics frame on the populated-host streaming branch")

	t.Logf("raw frame bytes: %s", raw)

	if bytes.Contains(raw, []byte(`"containers":null`)) {
		t.Fatalf("populated-host frame carries \"containers\":null on the wire, raw=%s", raw)
	}
	if bytes.Contains(raw, []byte(`"containers":[]`)) {
		t.Fatalf("populated-host frame carries an empty \"containers\":[] on the wire despite a running container, raw=%s", raw)
	}
	if !bytes.Contains(raw, []byte(`"containerId":"c1"`)) {
		t.Fatalf("populated-host frame does not carry the running container's id on the wire, raw=%s", raw)
	}
}
