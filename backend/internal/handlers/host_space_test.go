package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type hostSpaceFake struct {
	summary    *services.HostSummary
	candidates *services.HostCandidates
	err        error

	freeRes     *services.HostFreeSpaceResult
	freeErr     error
	freeCalls   []services.HostFreeSpaceRequest
	freeCtx     context.Context
	freeCtxErr  error
	invalidated int
}

func (f *hostSpaceFake) FreeSpace(ctx context.Context, req services.HostFreeSpaceRequest) (*services.HostFreeSpaceResult, error) {
	f.freeCalls = append(f.freeCalls, req)
	f.freeCtx = ctx
	f.freeCtxErr = ctx.Err()
	return f.freeRes, f.freeErr
}

func (f *hostSpaceFake) Invalidate() { f.invalidated++ }

func (f *hostSpaceFake) Summary(context.Context) (*services.HostSummary, error) {
	return f.summary, f.err
}

func (f *hostSpaceFake) Candidates(context.Context) (*services.HostCandidates, error) {
	return f.candidates, f.err
}

func hostSpaceRouter(t *testing.T, svc hostSpaceService) *gin.Engine {
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

// freeSpaceRouter wires the handler as main.go does: with the shared
// operation lock and an action log on a real database.
func freeSpaceRouter(t *testing.T, svc *hostSpaceFake) (*gin.Engine, *services.OperationLock, *database.DB) {
	t.Helper()
	db := fn7x3MemoryDB(t)
	h := NewResourcesHandler(nil, db, nil)
	lock := services.NewOperationLock()
	h.SetOperationLock(lock)
	h.SetHostSpaceService(svc)
	r := gin.New()
	h.RegisterRoutes(r.Group("/api"))
	return r, lock, db
}

func postFreeSpace(r *gin.Engine, body string) (*httptest.ResponseRecorder, map[string]any) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/resources/host/free-space", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		return w, nil
	}
	return w, out
}

func category(removed []string, failed, skipped []services.HostFreeSpaceItemIssue) services.HostFreeSpaceCategoryResult {
	if removed == nil {
		removed = []string{}
	}
	if failed == nil {
		failed = []services.HostFreeSpaceItemIssue{}
	}
	if skipped == nil {
		skipped = []services.HostFreeSpaceItemIssue{}
	}
	return services.HostFreeSpaceCategoryResult{Removed: removed, Failed: failed, Skipped: skipped}
}

func freeResult(images services.HostFreeSpaceCategoryResult) *services.HostFreeSpaceResult {
	empty := category(nil, nil, nil)
	freed := int64(300)
	return &services.HostFreeSpaceResult{Images: images, Containers: empty, Volumes: empty, Networks: empty, SpaceReclaimed: &freed}
}

// AC5: while any stack holds its lock, free-space answers 409 and Docker is
// never asked to remove anything.
func TestHostFreeSpace_StackLockHeldAnswers409(t *testing.T) {
	fake := &hostSpaceFake{freeRes: freeResult(category([]string{"sha256:a"}, nil, nil))}
	r, lock, _ := freeSpaceRouter(t, fake)

	token, err := lock.Acquire("s1", services.OpKindUpdate)
	require.NoError(t, err)
	w, body := postFreeSpace(r, `{"images":["sha256:a"]}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body = %s", w.Body.String())
	assert.Equal(t, "OPERATION_IN_PROGRESS", body["code"])
	assert.Empty(t, fake.freeCalls, "FreeSpace ran while a stack operation held its lock")

	// Same request once the stack is released: it runs, and while it runs the
	// stack could not have been locked (the exclusive turn).
	lock.Release("s1", token)
	w, _ = postFreeSpace(r, `{"images":["sha256:a"]}`)
	assert.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
	assert.Len(t, fake.freeCalls, 1)
}

// AC6 validation: refused with 400 before the lock or Docker is touched.
func TestHostFreeSpace_ValidationRefuses(t *testing.T) {
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", fmt.Sprintf("id%d", i))
	}
	cases := map[string]string{
		"empty body":         `{}`,
		"empty lists":        `{"images":[],"volumes":[],"buildCache":false}`,
		"not JSON":           `images=a`,
		"1001 IDs":           `{"images":[` + strings.Join(ids, ",") + `]}`,
		"ID with a slash":    `{"volumes":["a/b"]}`,
		"ID with a space":    `{"containers":["a b"]}`,
		"empty ID":           `{"networks":[""]}`,
		"ID starting with -": `{"images":["-f"]}`,
		"ID over 128 chars":  `{"images":["` + strings.Repeat("a", 129) + `"]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &hostSpaceFake{}
			r, _, _ := freeSpaceRouter(t, fake)
			w, out := postFreeSpace(r, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body = %s", w.Body.String())
			assert.Equal(t, "VALIDATION_ERROR", out["code"])
			assert.Empty(t, fake.freeCalls)
		})
	}

	// The accepted side on the same instrument: 1000 IDs, a sha256 ID, a
	// volume name with dots and dashes, and build cache alone.
	ok := make([]string, 1000)
	for i := range ok {
		ok[i] = fmt.Sprintf("%q", fmt.Sprintf("id%d", i))
	}
	for name, body := range map[string]string{
		"1000 IDs":         `{"images":[` + strings.Join(ok, ",") + `]}`,
		"sha256 ID":        `{"images":["sha256:` + strings.Repeat("ab", 32) + `"]}`,
		"volume name":      `{"volumes":["my_app.data-1"]}`,
		"build cache only": `{"buildCache":true}`,
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			fake := &hostSpaceFake{freeRes: freeResult(category(nil, nil, nil))}
			r, _, _ := freeSpaceRouter(t, fake)
			w, _ := postFreeSpace(r, body)
			assert.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
			assert.Len(t, fake.freeCalls, 1)
		})
	}
}

// AC6 outcome mapping, and the audit row.
func TestHostFreeSpace_OutcomeAndAudit(t *testing.T) {
	failed := []services.HostFreeSpaceItemIssue{{ID: "sha256:b", Reason: "conflict"}}
	skipped := []services.HostFreeSpaceItemIssue{{ID: "sha256:c", Reason: "no longer removable"}}
	cases := []struct {
		name    string
		res     *services.HostFreeSpaceResult
		status  int
		outcome string
	}{
		{"all removed", freeResult(category([]string{"sha256:a"}, nil, nil)), http.StatusOK, "success"},
		{"one failed", freeResult(category([]string{"sha256:a"}, failed, nil)), http.StatusMultiStatus, "partial"},
		{"one skipped", freeResult(category([]string{"sha256:a"}, nil, skipped)), http.StatusMultiStatus, "partial"},
		{"all skipped", freeResult(category(nil, nil, skipped)), http.StatusOK, "no_change"},
		{"every removal failed", freeResult(category(nil, failed, skipped)), http.StatusInternalServerError, "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &hostSpaceFake{freeRes: tc.res}
			r, _, db := freeSpaceRouter(t, fake)
			w, body := postFreeSpace(r, `{"images":["sha256:a","sha256:b","sha256:c"]}`)
			assert.Equal(t, tc.status, w.Code, "body = %s", w.Body.String())
			assert.Equal(t, tc.outcome, body["outcome"])

			details, _ := body["details"].(map[string]any)
			images, _ := details["images"].(map[string]any)
			require.NotNil(t, images, "details.images missing: %s", w.Body.String())
			if tc.outcome == "partial" && tc.name == "one failed" {
				assert.Equal(t, []any{map[string]any{"id": "sha256:b", "reason": "conflict"}}, images["failed"])
			}
			assert.Equal(t, float64(300), details["spaceReclaimed"])
			for _, key := range []string{"containers", "volumes", "networks", "buildCache"} {
				assert.Contains(t, details, key)
			}

			rows, err := db.GetRecentActions(10)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, services.ActionPrune, rows[0].Action)
			var detail map[string]any
			require.NoError(t, json.Unmarshal([]byte(rows[0].Detail), &detail))
			assert.Equal(t, "host_free_space", detail["resource"])
		})
	}
}

// An unknown spaceReclaimed is omitted, never sent as 0.
func TestHostFreeSpace_UnknownSpaceReclaimedIsOmitted(t *testing.T) {
	res := freeResult(category([]string{"sha256:a"}, nil, nil))
	res.SpaceReclaimed = nil
	r, _, _ := freeSpaceRouter(t, &hostSpaceFake{freeRes: res})
	w, body := postFreeSpace(r, `{"images":["sha256:a"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, body["details"], "spaceReclaimed")
}

// A failed candidate read removed nothing: an error response, no audit row.
func TestHostFreeSpace_ReadFailure(t *testing.T) {
	fake := &hostSpaceFake{freeErr: fmt.Errorf("reading Docker disk usage: %w", services.ErrDockerUnavailable)}
	r, _, db := freeSpaceRouter(t, fake)
	w, body := postFreeSpace(r, `{"images":["sha256:a"]}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body = %s", w.Body.String())
	assert.Equal(t, "DOCKER_UNAVAILABLE", body["code"])
	rows, err := db.GetRecentActions(10)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// The run's context outlives the request (safe-defaults rule 15) and is
// bounded by HostFreeSpaceTimeout.
func TestHostFreeSpace_RunContextIsOperationOwned(t *testing.T) {
	fake := &hostSpaceFake{freeRes: freeResult(category([]string{"sha256:a"}, nil, nil))}
	r, _, _ := freeSpaceRouter(t, fake)
	// The request is already cancelled (the tab closed) when it is served.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/resources/host/free-space", strings.NewReader(`{"images":["sha256:a"]}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	before := time.Now()
	r.ServeHTTP(w, req)

	require.Len(t, fake.freeCalls, 1, "body = %s", w.Body.String())
	assert.NoError(t, fake.freeCtxErr, "the run context ended with the request")
	deadline, ok := fake.freeCtx.Deadline()
	require.True(t, ok, "the run has no deadline")
	assert.WithinDuration(t, before.Add(services.HostFreeSpaceTimeout), deadline, time.Minute)
}

// A removal route invalidates the host summary even when Docker fails: the
// defer runs on every exit path. Paired with a read route that must not.
func TestHostFreeSpace_RemovalRoutesInvalidateTheSummary(t *testing.T) {
	fake := &hostSpaceFake{summary: &services.HostSummary{}}
	r := hostSpaceRouter(t, fake)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/build-cache/prune", nil))
	assert.Equal(t, 1, fake.invalidated, "build-cache prune did not invalidate (status %d)", w.Code)

	hostSpaceGet(r, "/api/resources/host/summary")
	assert.Equal(t, 1, fake.invalidated, "a read invalidated the summary")
}
