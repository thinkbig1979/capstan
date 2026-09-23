package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-p9e1. getDashboardStats checked the errors from
// GetAllContainersWithDetails and GetDiskUsage, logged them and carried on with
// zero values, so one 200 reported "0 containers, 0 running, 0 B on disk, every
// stack stopped" when Docker could not be asked. The keys each read backs are
// now OMITTED on its fault (the agent-os-xppj convention: absent, never wrong),
// while the rest of the response is still served.

// A running container in the compose project "web", which the seeded stack
// below also uses, so a healthy read counts that stack as running.
const p9e1OneRunningContainer = `[{"Id":"c1","Names":["/web-1"],"Image":"nginx","State":"running",` +
	`"Status":"Up","Labels":{"com.docker.compose.project":"web"}}]`

const p9e1NonZeroDiskUsage = `{"Images":[{"Id":"i1","Size":1000}],"Containers":[],"Volumes":[],"BuildCache":[]}`

const p9e1EmptyDiskUsage = `{"Images":[],"Containers":[],"Volumes":[],"BuildCache":[]}`

// newFakeDashboardEngine serves the two Docker Engine endpoints getDashboardStats
// reads, each with its own status and body, so one can fault while the other
// answers.
func newFakeDashboardEngine(t *testing.T, listStatus int, listBody string, dfStatus int, dfBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.WriteHeader(listStatus)
			_, _ = w.Write([]byte(listBody)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
		case strings.HasSuffix(r.URL.Path, "/containers/c1/json"):
			// The per-container inspect GetAllContainersWithDetails enriches a
			// running row with; its contents are not what these tests measure.
			_, _ = w.Write([]byte(`{"Id":"c1","Name":"/web-1","State":{"Status":"running","Running":true}}`)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
		case strings.HasSuffix(r.URL.Path, "/system/df"):
			w.WriteHeader(dfStatus)
			_, _ = w.Write([]byte(dfBody)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
		default:
			t.Errorf("unexpected Docker Engine request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func getDashboardStatsBody(t *testing.T, engine *httptest.Server) map[string]json.RawMessage {
	t.Helper()
	docker := newTestDockerServiceAgainst(t, engine)

	db, err := database.NewWithMigrations(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	seedStack(t, db, "s1", "web")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewDashboardHandler(nil, docker, db, NewConnectionManager(10)).
		RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body.String())
	return body
}

var p9e1ContainerKeys = []string{"totalContainers", "runningContainers", "containers", "runningStacks", "stoppedStacks"}

var p9e1DiskKeys = []string{"diskUsage", "imageDiskUsage"}

// FAULT ARM, containers. The disk read and the stack list still answer, so the
// response must still be served with them; only the container-derived keys are
// unknown.
func TestDashboardStats_UnreadableContainersAreOmittedNotZeroed(t *testing.T) {
	body := getDashboardStatsBody(t, newFakeDashboardEngine(t,
		http.StatusInternalServerError, `{"message":"boom"}`, http.StatusOK, p9e1NonZeroDiskUsage))

	require.JSONEq(t, `1`, string(body["totalStacks"]), "precondition: the stack count must be intact")
	require.Contains(t, body, "diskUsage", "precondition: the disk read answered, so it must still be served")

	for _, key := range p9e1ContainerKeys {
		raw, present := body[key]
		require.False(t, present,
			"a GetAllContainersWithDetails fault is being emitted as a factual %s = %s — "+
				"indistinguishable from a host that genuinely has none, which "+
				"TestDashboardStats_EmptyHostStillReportsZero proves this server also sends",
			key, string(raw))
	}
}

// FAULT ARM, disk usage. The container read still answers.
func TestDashboardStats_UnreadableDiskUsageIsOmittedNotZeroed(t *testing.T) {
	body := getDashboardStatsBody(t, newFakeDashboardEngine(t,
		http.StatusOK, p9e1OneRunningContainer, http.StatusInternalServerError, `{"message":"boom"}`))

	require.JSONEq(t, `1`, string(body["totalContainers"]), "precondition: the container read answered")
	require.JSONEq(t, `1`, string(body["runningStacks"]), "precondition: the container read answered")

	for _, key := range p9e1DiskKeys {
		raw, present := body[key]
		require.False(t, present,
			"a GetDiskUsage fault is being emitted as a factual %s = %s — "+
				"indistinguishable from a host using no disk, which "+
				"TestDashboardStats_EmptyHostStillReportsZero proves this server also sends",
			key, string(raw))
	}
}

// EMPTY-HOST ARM, the positive side of the same instrument. A healthy Docker
// with no containers and no disk usage genuinely reports zero and must still
// SAY zero, or the fault arms above are satisfied by a handler that never sends
// these keys at all.
func TestDashboardStats_EmptyHostStillReportsZero(t *testing.T) {
	body := getDashboardStatsBody(t, newFakeDashboardEngine(t,
		http.StatusOK, `[]`, http.StatusOK, p9e1EmptyDiskUsage))

	require.JSONEq(t, `0`, string(body["totalContainers"]))
	require.JSONEq(t, `0`, string(body["runningContainers"]))
	require.JSONEq(t, `[]`, string(body["containers"]))
	require.JSONEq(t, `0`, string(body["runningStacks"]))
	require.JSONEq(t, `1`, string(body["stoppedStacks"]))
	require.JSONEq(t, `0`, string(body["imageDiskUsage"]))
	require.JSONEq(t, `{"images":0,"containers":0,"volumes":0,"buildCache":0,"total":0}`, string(body["diskUsage"]))
}
