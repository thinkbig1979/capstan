package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// newTestDockerServiceAgainst points a real *services.DockerService (via its
// only constructor, NewDockerService) at the given fake Docker Engine HTTP
// server, using DOCKER_HOST rather than a doubled type — DockerService has no
// test-injection seam of its own (its client field is unexported), same as
// the constraint documented on newFakeMonitorService in
// monitoring_metrics_close_test.go. t.Setenv scopes the override to this
// test and is why this test does not run in parallel with others.
func newTestDockerServiceAgainst(t *testing.T, srv *httptest.Server) *services.DockerService {
	t.Helper()
	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	ds, err := services.NewDockerService(&config.Config{})
	require.NoError(t, err)
	return ds
}

// TestDashboardMetricsWS_SetupErrorClosesConnection is dashboard.go's half of
// the agent-os-14gr regression: GetRunningContainerIDs failing sends the
// client a close frame (safeWriteCloseMessage) but, before the fix, never
// closed the server-side socket. The fuller two-sided instrument (error exit,
// write-error exit, and the still-streaming control) lives against
// monitoring.go's handleMetricsWebSocket in
// monitoring_metrics_close_test.go — DashboardHandler needs a full
// *services.DockerService (not just *services.MonitorService) to reach even
// this one path, via DOCKER_HOST above.
func TestDashboardMetricsWS_SetupErrorClosesConnection(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusInternalServerError, `{"message":"boom"}`, nil)
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

	serverConn := firstConnection(t, cm)

	if !waitForServerSideClose(t, serverConn, cm) {
		t.Error("server-side connection was never closed after the GetRunningContainerIDs error path returned")
	}
}
