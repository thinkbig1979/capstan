package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-txqx: the two update jobs changed a stack's containers and never
// stored the stack's status, so the stack list's Docker-down fallback kept
// showing the status from before the update. After the job the stack is
// re-read and that status stored. The seeded status is "stopped" and the
// re-read answers "partial", which no update result maps to, so a stored
// "partial" can only have come from the re-read.

type txqxUpdater struct {
	mu     sync.Mutex
	ar     truth.ActionResult
	status string
	reads  int
}

func (f *txqxUpdater) InspectContainer(_ context.Context, id string) (container.InspectResponse, error) {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: id, Name: "/web-api-1", Image: "sha256:old"},
		Config: &container.Config{Image: "nginx:latest", Labels: map[string]string{
			"com.docker.compose.project": "web", "com.docker.compose.service": "api",
		}},
	}, nil
}

func (f *txqxUpdater) UpdateContainerStreaming(context.Context, string, services.DashboardDB,
	func(services.LogLine), func(services.Status)) (models.UpdateResult, truth.ActionResult) {
	return models.UpdateResult{}, f.ar
}

func (f *txqxUpdater) UpdateComposeServiceStreaming(context.Context, models.Stack, string,
	func(services.LogLine), func(services.Status)) (string, string, int64, truth.ActionResult) {
	return "", "", 0, f.ar
}

func (f *txqxUpdater) Status(models.Stack) (string, []models.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.status, nil, nil
}

func (f *txqxUpdater) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

var txqxResults = []struct {
	name    string
	ar      truth.ActionResult
	job     services.Status
	want    string
	reReads bool
}{
	{"success", truth.Success("Updated"), services.StatusSuccess, "partial", true},
	{"failed", truth.Failed("compose up failed", errors.New("exit status 1")), services.StatusError, "partial", true},
	{"no Docker", truth.Failed("docker unavailable", services.ErrDockerUnavailable), services.StatusError, "stopped", false},
}

func TestUpdateJobsStoreStackStatus_Txqx(t *testing.T) {
	routes := map[string]func(t *testing.T, h *ResourcesHandler) *httptest.ResponseRecorder{
		"updateContainer": func(t *testing.T, h *ResourcesHandler) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/api/resources/containers/c-api/update", nil)
			w := httptest.NewRecorder()
			setupResourcesRouter(h).ServeHTTP(w, req)
			return w
		},
		"updateStack": func(t *testing.T, h *ResourcesHandler) *httptest.ResponseRecorder {
			require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{cachedUpdate("s1", "api")}))
			return postUpdateStack(t, h, "s1")
		},
	}
	for route, post := range routes {
		for _, tc := range txqxResults {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				h := newTestResourcesHandlerWithJobManager(t)
				seedStack(t, h.db, "s1", "web")
				require.NoError(t, h.db.UpdateStackStatus("s1", "stopped"))
				fake := &txqxUpdater{ar: tc.ar, status: "partial"}
				h.updater = fake

				w := post(t, h)
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
				jobID, _ := decodeBody(t, w)["jobId"].(string)
				require.NotEmpty(t, jobID)
				waitForJob(t, h, jobID, tc.job)

				got, err := h.db.GetStack("s1")
				require.NoError(t, err)
				assert.Equal(t, tc.want, got.Status)
				assert.Equal(t, tc.reReads, fake.readCount() > 0, "re-read the stack")
			})
		}
	}
}
