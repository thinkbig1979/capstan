package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-z91e.38 (owner decision D28): a stack lifecycle action runs compose
// with `-p <project name>`, so on a stack whose name another stack carries it
// would act on the other stack's containers too, under only this stack's lock.
// The handlers refuse it with 409 AMBIGUOUS_STACK before the lock, the action
// log or the status write, the same shape as z91e.19's container actions.

// z91e38StackDocker records which verbs ran. Each succeeds unless failWith is
// set, in which case it fails with that cause (the compose path's own refusal).
type z91e38StackDocker struct {
	mu       sync.Mutex
	calls    []string
	failWith error
}

func (f *z91e38StackDocker) run(verb string) (truth.ActionResult, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, verb)
	if f.failWith != nil {
		return truth.Failed(f.failWith.Error(), f.failWith), ""
	}
	return truth.Success(verb+" ok", truth.KV("status", "running")), ""
}

func (f *z91e38StackDocker) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (*z91e38StackDocker) GetStackStatuses(context.Context, services.DashboardDB) (map[string]services.LiveStatus, error) {
	return map[string]services.LiveStatus{}, nil
}
func (f *z91e38StackDocker) StartVerified(models.Stack) (truth.ActionResult, string) {
	return f.run("start")
}
func (f *z91e38StackDocker) StopVerified(models.Stack) (truth.ActionResult, string) {
	return f.run("stop")
}
func (f *z91e38StackDocker) RestartVerified(models.Stack) (truth.ActionResult, string) {
	return f.run("restart")
}
func (f *z91e38StackDocker) PullVerified(models.Stack) (truth.ActionResult, string) {
	return f.run("pull")
}
func (f *z91e38StackDocker) DeleteVerified(models.Stack) (truth.ActionResult, string) {
	return f.run("delete")
}

func newZ91e38StacksFixture(t *testing.T) (*gin.Engine, *database.DB, *services.OperationLock, *z91e38StackDocker) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	seedSharedProjectStacks(t, db)
	seedStackInDir(t, db, "s-web", "web", "/srv/stacks/web")
	fake := &z91e38StackDocker{}
	lock := services.NewOperationLock()
	h := NewStacksHandler(fake, nil, nil, db, &config.Config{StacksDir: "/srv/stacks"}, services.NewActionLogger(db), lock)
	r := gin.New()
	h.RegisterRoutes(r.Group("/stacks"))
	return r, db, lock, fake
}

var z91e38LifecycleRoutes = []struct{ name, method, suffix string }{
	{"start", http.MethodPost, "/start"},
	{"stop", http.MethodPost, "/stop"},
	{"restart", http.MethodPost, "/restart"},
	{"pull", http.MethodPost, "/pull"},
	{"delete", http.MethodDelete, "?confirm=true"},
}

func TestStackLifecycle_SharedProjectNameRefuses(t *testing.T) {
	for _, rt := range z91e38LifecycleRoutes {
		t.Run(rt.name, func(t *testing.T) {
			r, db, lock, fake := newZ91e38StacksFixture(t)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(rt.method, "/stacks/s-alpha"+rt.suffix, nil))

			require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrAmbiguousStack, body["code"])
			assert.Contains(t, body["message"], "/srv/stacks/alpha")
			assert.Contains(t, body["message"], "/srv/stacks/beta")
			assert.Empty(t, fake.recorded(), "a refused action must run no compose verb")

			stored, err := db.GetStack("s-alpha")
			require.NoError(t, err)
			assert.Equal(t, "running", stored.Status, "a refused action must not rewrite the stored status")
			logged, err := db.GetActionsByStack("s-alpha", 10)
			require.NoError(t, err)
			assert.Empty(t, logged, "a refused action must leave no action log row")
			tok, err := lock.Acquire("s-alpha", services.OpKindStart)
			require.NoError(t, err, "the refusal left s-alpha locked")
			lock.Release("s-alpha", tok)

			// Other side, same fixture and route: the uniquely named stack gets
			// past the check. Delete then stops at its directory inspection
			// (the fixture's /srv/stacks/web does not exist), which runs after
			// the check, so for it the evidence is a different refusal.
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(rt.method, "/stacks/s-web"+rt.suffix, nil))
			assert.NotEqual(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			if rt.name == "delete" {
				assert.Contains(t, w.Body.String(), "failed to inspect stack directory")
				return
			}
			assert.Contains(t, fake.recorded(), rt.name)
		})
	}
}

// The compose path's own refusal (DockerService.mutatingComposeArgs) reaches a
// handler when the name became shared after the pre-check. renderDockerResult
// answers it with the same 409, not a generic failure.
func TestStackLifecycle_ComposeRefusalRendersAs409(t *testing.T) {
	r, _, _, fake := newZ91e38StacksFixture(t)
	fake.failWith = &database.AmbiguousProjectNameError{ProjectName: "web", Stacks: []models.Stack{
		{ID: "s-web", Directory: "/srv/stacks/web"}, {ID: "s-late", Directory: "/srv/stacks/late"},
	}}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/stacks/s-web/start", nil))

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrAmbiguousStack, body["code"])
	assert.Contains(t, body["message"], "/srv/stacks/late")
}

// recordingStreamer forwards to a real DockerService and records each
// subcommand the handler asked it to run. The refusal itself, and that a
// refused RunStreaming starts no compose process, is pinned in services
// (TestRunStreaming_SharedProjectNameIsRefused, exec count 0); this records
// which phases the handler went on to request.
type recordingStreamer struct {
	svc  *services.DockerService
	mu   sync.Mutex
	subs []string
}

func (r *recordingStreamer) RunStreaming(ctx context.Context, stack models.Stack, subcommand string, extraArgs []string) <-chan services.StreamLine {
	r.mu.Lock()
	r.subs = append(r.subs, subcommand)
	r.mu.Unlock()
	return r.svc.RunStreaming(ctx, stack, subcommand, extraArgs)
}

func (r *recordingStreamer) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs = nil
}

func (r *recordingStreamer) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.subs...)
}

// newZ91e38OperationsFixture serves /ws/operations over a real DockerService
// with the stack lookup installed, as main.go wires it. The Docker API is a
// ping-only stub, and the seeded stack directories do not exist, so a run that
// gets past the refusal fails at chdir before any docker process starts.
func newZ91e38OperationsFixture(t *testing.T) (*httptest.Server, *recordingStreamer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(dockerAPI.Close)
	svc := newTestDockerServiceAgainst(t, dockerAPI)
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	seedSharedProjectStacks(t, db)
	seedStackInDir(t, db, "s-web", "web", "/srv/stacks/web")
	svc.SetStackLookup(db)
	rec := &recordingStreamer{svc: svc}
	r := gin.New()
	NewOperationsHandler(rec, db, services.NewOperationLock(), NewConnectionManager(5)).
		RegisterRoutes(r.Group("/api"), "test-secret-key-32-chars-long!!!", true)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, rec
}

// readOperationFrames dials the operations route and returns every JSON frame
// until the server closes the socket.
func readOperationFrames(t *testing.T, srv *httptest.Server, stackID, action string) []map[string]interface{} {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/operations/" + stackID + "/" + action
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer conn.Close()
	defer resp.Body.Close()
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	var frames []map[string]interface{}
	for {
		var f map[string]interface{}
		if err := conn.ReadJSON(&f); err != nil {
			break
		}
		frames = append(frames, f)
	}
	return frames
}

func framesOfType(frames []map[string]interface{}, typ string) []map[string]interface{} {
	var out []map[string]interface{}
	for _, f := range frames {
		if f["type"] == typ {
			out = append(out, f)
		}
	}
	return out
}

// The stack page's own Start/Stop/Restart/Pull buttons use this route
// (StackPage.tsx useStreamingOperation), so a shared name is refused inside the
// stream, where the client can read why: a failed done frame naming both
// stacks. A restart stops after its refused down phase and never starts.
func TestOperations_SharedProjectName_RestartStopsAtTheRefusedDownPhase(t *testing.T) {
	srv, rec := newZ91e38OperationsFixture(t)

	frames := readOperationFrames(t, srv, "s-alpha", "restart")

	done := framesOfType(frames, "done")
	require.Len(t, done, 1, "frames: %v", frames)
	assert.NotEqual(t, true, done[0]["success"], "frames: %v", frames)
	assert.Equal(t, "failed", done[0]["outcome"])
	assert.Contains(t, done[0]["error"], "/srv/stacks/alpha")
	assert.Contains(t, done[0]["error"], "/srv/stacks/beta")
	assert.Empty(t, framesOfType(frames, "phase"), "a refused down phase must not go on to start: %v", frames)
	assert.Equal(t, []string{"down"}, rec.requested())

	// Other side, same route: a uniquely named stack's down phase fails for
	// an unrelated reason (no directory) without a done frame, and the
	// handler does go on to start, so the check above is what stopped it.
	rec.reset()
	frames = readOperationFrames(t, srv, "s-web", "restart")
	assert.NotEmpty(t, framesOfType(frames, "phase"), "frames: %v", frames)
	assert.Equal(t, []string{"down", "up"}, rec.requested())
	assert.NotContains(t, fmt.Sprint(frames), "/srv/stacks/beta")
}

func TestOperations_SharedProjectName_StartTellsTheClientWhy(t *testing.T) {
	srv, rec := newZ91e38OperationsFixture(t)

	frames := readOperationFrames(t, srv, "s-alpha", "start")

	done := framesOfType(frames, "done")
	require.Len(t, done, 1, "frames: %v", frames)
	assert.NotEqual(t, true, done[0]["success"])
	assert.Equal(t, "failed", done[0]["outcome"])
	for _, key := range []string{"error", "reason"} {
		assert.Contains(t, done[0][key], "/srv/stacks/alpha", key)
		assert.Contains(t, done[0][key], "/srv/stacks/beta", key)
		assert.Contains(t, done[0][key], "give each stack its own compose project name", key)
	}
	assert.Equal(t, []string{"up"}, rec.requested())

	// Other side: the uniquely named stack is not refused for its name.
	frames = readOperationFrames(t, srv, "s-web", "start")
	assert.NotEmpty(t, frames)
	assert.NotContains(t, fmt.Sprint(frames), "/srv/stacks/beta")
}

// A stack update is refused before the lock and the job.
func TestUpdateStack_SharedProjectNameRefuses(t *testing.T) {
	h := newUpdateContainerFixture(t)
	router := setupResourcesRouter(h)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/stacks/s-alpha/update", nil))

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrAmbiguousStack, body["code"])
	assert.Contains(t, body["message"], "/srv/stacks/beta")
	assert.Empty(t, h.jobManager.List(), "a refused update must not be queued")

	// Other side: the uniquely named stack gets past the check (nothing is
	// cached as outdated, so it answers noUpdates).
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/stacks/s-web/update", nil))
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}
