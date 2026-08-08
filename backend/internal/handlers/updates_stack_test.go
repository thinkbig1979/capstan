package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// updateStack (updates.go:451) and enqueueJobWithBroadcasts (updates.go:396)
// both measured 0.0% (agent-os-wo9x): the two functions that actually mutate
// live stacks, while their read-only siblings sat at 57-78%.
//
// The `run` closure updateStack builds needs a real Docker daemon, so these
// tests cover the handler's own decisions — the guards, the outdated-service
// selection, the enqueue and the response — and cover
// enqueueJobWithBroadcasts directly with a stub run function.

func seedStack(t *testing.T, db *database.DB, id, project string) {
	t.Helper()
	// stacks.directory is a foreign key into directories, so the directory row
	// has to exist first.
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path:      "/srv/stacks/" + project,
		Name:      project,
		RootDir:   "/srv/stacks",
		ScannedAt: time.Now(),
	}))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          id,
		ProjectName: project,
		Directory:   "/srv/stacks/" + project,
		ComposeFile: "compose.yml",
		Status:      "running",
	}))
}

func cachedUpdate(stackID, service string) models.CachedUpdate {
	return models.CachedUpdate{
		ID:            "cu-" + service,
		ContainerID:   "c-" + service,
		ContainerName: service,
		Image:         "nginx:latest",
		ImageRef:      "docker.io/library/nginx:latest",
		State:         "running",
		StackID:       stackID,
		ServiceName:   service,
		IsCompose:     true,
		LocalDigest:   "sha256:old",
		RemoteDigest:  "sha256:new",
		ScannedAt:     time.Now().Format(time.RFC3339),
	}
}

func postUpdateStack(t *testing.T, h *ResourcesHandler, stackID string) *httptest.ResponseRecorder {
	t.Helper()
	router := setupResourcesRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/api/resources/stacks/"+stackID+"/update", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestUpdateStack_ServiceUnavailableWithoutAJobManager(t *testing.T) {
	// newTestResourcesHandler builds the handler with no job manager, which is
	// how the server runs when the manager failed to start.
	h := newTestResourcesHandler(t)
	seedStack(t, h.db, "s1", "web")

	w := postUpdateStack(t, h, "s1")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUpdateStack_NotFoundForAnUnknownStack(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)

	w := postUpdateStack(t, h, "does-not-exist")

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateStack_ReportsNoUpdatesWhenNothingIsOutdated(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	seedStack(t, h.db, "s1", "web")

	w := postUpdateStack(t, h, "s1")

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, true, body["noUpdates"])
	assert.Equal(t, "", body["jobId"], "no job may be enqueued when there is nothing to do")
}

func TestUpdateStack_IgnoresCachedUpdatesBelongingToOtherStacks(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	seedStack(t, h.db, "s1", "web")
	seedStack(t, h.db, "s2", "api")
	require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{cachedUpdate("s2", "other")}))

	w := postUpdateStack(t, h, "s1")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, decodeBody(t, w)["noUpdates"],
		"another stack's outdated service must not trigger an update of this one")
}

func TestUpdateStack_IgnoresNonComposeAndUnnamedServices(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	seedStack(t, h.db, "s1", "web")

	standalone := cachedUpdate("s1", "loose")
	standalone.IsCompose = false
	unnamed := cachedUpdate("s1", "")
	unnamed.ID = "cu-unnamed"
	unnamed.ContainerID = "c-unnamed"
	unnamed.ContainerName = "unnamed"
	require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{standalone, unnamed}))

	w := postUpdateStack(t, h, "s1")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, decodeBody(t, w)["noUpdates"],
		"a stack update only covers compose services that have a service name")
}

func TestUpdateStack_EnqueuesAJobForOutdatedServices(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	seedStack(t, h.db, "s1", "web")
	require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{
		cachedUpdate("s1", "web"),
		cachedUpdate("s1", "worker"),
	}))

	w := postUpdateStack(t, h, "s1")

	// Enqueuing is asynchronous, so this path answers 202, not 200.
	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)
	jobID, ok := body["jobId"].(string)
	require.True(t, ok, "response must carry a jobId")
	assert.NotEmpty(t, jobID)
	assert.NotEqual(t, true, body["noUpdates"])

	job := h.jobManager.Get(jobID)
	require.NotNil(t, job, "the job must be retrievable by the id we handed the client")
	assert.Equal(t, "stack", job.TargetType)
	assert.Equal(t, "s1", job.TargetID)
	assert.Equal(t, "web", job.Name)
}

// ── enqueueJobWithBroadcasts ────────────────────────────────────────────────

// captureBroadcasts collects the events the handler emits during a job.
// EventBus.Broadcast drops on a full channel, so the buffer is generous.
func captureBroadcasts(t *testing.T) (func() []models.StackEvent, func()) {
	t.Helper()

	ch := make(chan models.StackEvent, 256)
	DefaultEventBus().Subscribe(ch)

	var mu sync.Mutex
	var got []models.StackEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range ch {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		}
	}()

	stop := func() {
		DefaultEventBus().Unsubscribe(ch)
		<-done
	}

	return func() []models.StackEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]models.StackEvent, len(got))
		copy(out, got)
		return out
	}, stop
}

// settle lets the collector goroutine drain what Broadcast already queued.
func settle() { time.Sleep(50 * time.Millisecond) }

func waitForJob(t *testing.T, h *ResourcesHandler, jobID string, want services.Status) *services.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j := h.jobManager.Get(jobID); j != nil && j.Status == want {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s never reached status %s", jobID, want)
	return nil
}

func TestEnqueueJobWithBroadcasts_SuccessPath(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	events, stop := captureBroadcasts(t)
	defer stop()

	spec := services.JobSpec{TargetType: "stack", TargetID: "s1", Name: "web", StackID: "s1"}
	var sawJobID string
	job := h.enqueueJobWithBroadcasts(spec, func(_ context.Context, jobID string, emit func(services.LogLine), setStatus func(services.Status)) error {
		sawJobID = jobID
		setStatus(services.StatusPulling)
		emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStatus, Text: "working"})
		return nil
	})
	require.NotNil(t, job)

	waitForJob(t, h, job.ID, services.StatusSuccess)
	assert.Equal(t, job.ID, sawJobID, "the manager injects the job id into the run signature")

	settle()
	var progress, complete int
	for _, e := range events() {
		switch e.Type {
		case "update_job_progress":
			progress++
			assert.Equal(t, job.ID, e.JobID)
			assert.Equal(t, "stack", e.TargetType)
		case "update_job_complete":
			complete++
			assert.Equal(t, string(services.StatusSuccess), e.Status)
			assert.Empty(t, e.JobError)
		}
	}
	assert.GreaterOrEqual(t, progress, 1, "every setStatus must broadcast progress")
	assert.Equal(t, 1, complete, "exactly one terminal broadcast")
}

func TestEnqueueJobWithBroadcasts_FailurePathCarriesTheError(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	events, stop := captureBroadcasts(t)
	defer stop()

	spec := services.JobSpec{TargetType: "stack", TargetID: "s1", Name: "web", StackID: "s1"}
	job := h.enqueueJobWithBroadcasts(spec, func(_ context.Context, _ string, _ func(services.LogLine), setStatus func(services.Status)) error {
		setStatus(services.StatusPulling)
		return errors.New("compose pull failed")
	})
	require.NotNil(t, job)

	waitForJob(t, h, job.ID, services.StatusError)

	settle()
	var complete int
	for _, e := range events() {
		if e.Type == "update_job_complete" {
			complete++
			assert.Equal(t, string(services.StatusError), e.Status)
			assert.Equal(t, "compose pull failed", e.JobError,
				"the client needs the reason, not just the status")
		}
	}
	assert.Equal(t, 1, complete)
}

func TestEnqueueJobWithBroadcasts_TerminalBroadcastCarriesTheOutcome(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	events, stop := captureBroadcasts(t)
	defer stop()

	spec := services.JobSpec{TargetType: "container", TargetID: "c1", Name: "web-1"}
	job := h.enqueueJobWithBroadcasts(spec, func(_ context.Context, jobID string, _ func(services.LogLine), _ func(services.Status)) error {
		// This is how the real run closures report a typed outcome; the
		// terminal broadcast must read it back off the job.
		h.jobManager.SetOutcome(jobID, "no_change", "already up to date")
		return nil
	})
	require.NotNil(t, job)

	waitForJob(t, h, job.ID, services.StatusSuccess)

	settle()
	var seen bool
	for _, e := range events() {
		if e.Type == "update_job_complete" {
			seen = true
			assert.Equal(t, "no_change", e.Outcome)
			assert.Equal(t, "already up to date", e.Reason)
		}
	}
	assert.True(t, seen)
}

func TestEnqueueJobWithBroadcasts_ProgressCarriesEveryStatusTransition(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	events, stop := captureBroadcasts(t)
	defer stop()

	spec := services.JobSpec{TargetType: "stack", TargetID: "s1", Name: "web", StackID: "s1"}
	job := h.enqueueJobWithBroadcasts(spec, func(_ context.Context, _ string, _ func(services.LogLine), setStatus func(services.Status)) error {
		setStatus(services.StatusPulling)
		setStatus(services.StatusPulling)
		return nil
	})
	require.NotNil(t, job)

	waitForJob(t, h, job.ID, services.StatusSuccess)

	settle()
	var progress int
	for _, e := range events() {
		if e.Type == "update_job_progress" {
			progress++
		}
	}
	assert.GreaterOrEqual(t, progress, 2, "one broadcast per setStatus call")
}
