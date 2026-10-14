package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type hostSpaceFake struct {
	summary    *services.HostSummary
	candidates *services.HostCandidates
	err        error
}

func (f *hostSpaceFake) Summary(context.Context) (*services.HostSummary, error) {
	return f.summary, f.err
}

func (f *hostSpaceFake) Candidates(context.Context) (*services.HostCandidates, error) {
	return f.candidates, f.err
}

func hostSpaceRouter(t *testing.T, svc hostSpaceReader) *gin.Engine {
	t.Helper()
	h := NewResourcesHandler(nil, fn7x3MemoryDB(t), nil)
	if svc != nil {
		h.SetHostSpaceService(svc)
	}
	r := gin.New()
	h.RegisterRoutes(r.Group("/api"))
	return r
}

func hostSpaceGet(r *gin.Engine, path string) (*httptest.ResponseRecorder, map[string]any) {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	// A 404 from an unregistered route is plain text: no body to inspect.
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		return w, nil
	}
	return w, body
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestHostSummaryRouteServesTheSummaryShape(t *testing.T) {
	summary := &services.HostSummary{
		Disk:       services.HostDiskSummary{Total: 1000, InUse: 700, Reclaimable: 300, ReclaimableAll: 400, VolumesSizeKnown: true},
		Containers: services.HostContainerSummary{Count: 3, Running: 1, Stopped: 2, ReclaimableCount: 1, ReclaimableBytes: 5},
		Images:     services.HostImageSummary{Count: 4, InUse: 2, Bytes: 600, ReclaimableCount: 2, ReclaimableBytes: 250},
		Volumes:    services.HostVolumeSummary{Count: 2, Attached: 1, Bytes: 300, SizeKnown: true, ReclaimableCount: 1, ReclaimableBytes: 100},
		Networks:   services.HostNetworkSummary{Count: 5, BuiltIn: 3, StackOwned: 1, ReclaimableCount: 1},
		BuildCache: services.HostBuildCacheSummary{Count: 2, Bytes: 95, ReclaimableBytes: 45},
		MeasuredAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
	}
	w, body := hostSpaceGet(hostSpaceRouter(t, &hostSpaceFake{summary: summary}), "/api/resources/host/summary")

	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
	assert.Equal(t, []string{"buildCache", "containers", "disk", "images", "measuredAt", "networks", "volumes"}, keysOf(body))
	assert.JSONEq(t, `{
		"disk": {"total":1000,"inUse":700,"reclaimable":300,"reclaimableAll":400,"volumesSizeKnown":true},
		"containers": {"count":3,"running":1,"stopped":2,"reclaimableCount":1,"reclaimableBytes":5},
		"images": {"count":4,"inUse":2,"bytes":600,"reclaimableCount":2,"reclaimableBytes":250},
		"volumes": {"count":2,"attached":1,"bytes":300,"sizeKnown":true,"reclaimableCount":1,"reclaimableBytes":100},
		"networks": {"count":5,"builtIn":3,"stackOwned":1,"reclaimableCount":1},
		"buildCache": {"count":2,"bytes":95,"reclaimableBytes":45},
		"measuredAt": "2026-10-14T12:00:00Z"
	}`, w.Body.String())
}

// Docker unavailable answers 503 DOCKER_UNAVAILABLE; any other failure 500.
// Neither sends a summary body.
func TestHostSummaryRouteErrorsAreNotAZeroSummary(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"docker unavailable", fmt.Errorf("reading Docker disk usage: %w", services.ErrDockerUnavailable), http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE"},
		{"other failure", errors.New("listing stacks: disk I/O error"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		for _, path := range []string{"/api/resources/host/summary", "/api/resources/host/candidates"} {
			t.Run(tc.name+" "+path, func(t *testing.T) {
				w, body := hostSpaceGet(hostSpaceRouter(t, &hostSpaceFake{err: tc.err}), path)
				assert.Equal(t, tc.status, w.Code, "body = %s", w.Body.String())
				assert.Equal(t, tc.code, body["code"])
				assert.NotContains(t, body, "disk")
				assert.NotContains(t, body, "images")
			})
		}
	}
}

func TestHostSummaryRoutesRefuseWithoutTheService(t *testing.T) {
	r := hostSpaceRouter(t, nil)
	for _, path := range []string{"/api/resources/host/summary", "/api/resources/host/candidates"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		require.NotPanics(t, func() { r.ServeHTTP(w, req) })
		assertUnavailable(t, w)
	}
}

func TestHostSummaryCandidatesRouteServesItems(t *testing.T) {
	size := int64(200)
	candidates := &services.HostCandidates{
		Images:     []services.HostCandidateItem{{ID: "sha256:a", Label: "app:0", Size: &size}, {ID: "sha256:b", Label: "0123456789abcdef012"}},
		BuildCache: services.HostBuildCacheCandidates{Count: 1, Bytes: 10},
		Containers: []services.HostCandidateItem{},
		Volumes:    []services.HostCandidateItem{{ID: "loose", Label: "loose", Size: new(int64)}},
		Networks:   []services.HostCandidateItem{},
	}
	w, _ := hostSpaceGet(hostSpaceRouter(t, &hostSpaceFake{candidates: candidates}), "/api/resources/host/candidates")

	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
	// An unknown size is omitted; a known 0 is sent.
	assert.JSONEq(t, `{
		"images": [{"id":"sha256:a","label":"app:0","size":200},{"id":"sha256:b","label":"0123456789abcdef012"}],
		"buildCache": {"count":1,"bytes":10},
		"containers": [],
		"volumes": [{"id":"loose","label":"loose","size":0}],
		"networks": []
	}`, w.Body.String())
}
