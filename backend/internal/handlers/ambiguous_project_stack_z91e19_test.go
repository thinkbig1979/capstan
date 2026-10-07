package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// seedStackInDir is seedStack with the directory named, so two stacks can
// share a compose project name from different directories, the way the
// scanner stores alpha/ and beta/ whose compose files both say `name: shared`.
func seedStackInDir(t *testing.T, db *database.DB, id, project, dir string) {
	t.Helper()
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: dir, Name: dir, RootDir: "/srv/stacks", ScannedAt: time.Now()}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: id, ProjectName: project, Directory: dir, ComposeFile: "compose.yaml", Status: "running"}))
}

func seedSharedProjectStacks(t *testing.T, db *database.DB) {
	t.Helper()
	seedStackInDir(t, db, "s-alpha", "shared", "/srv/stacks/alpha")
	seedStackInDir(t, db, "s-beta", "shared", "/srv/stacks/beta")
}

// TestContainerActions_SharedProjectNameRefuses is agent-os-z91e.19 (D25) for
// the Resources container actions: the "shared" label belongs to two stacks,
// so the action cannot know whose lock to take. Before the fix the lookup
// silently answered s-alpha (or whichever row came first), locked it, and ran
// the action while s-beta stayed unlocked.
func TestContainerActions_SharedProjectNameRefuses(t *testing.T) {
	for _, a := range containerActions("amb") {
		t.Run(a.name, func(t *testing.T) {
			h, lock, fake := newContainerLockFixture(t)
			seedSharedProjectStacks(t, h.db)
			fake.labels["amb"] = map[string]string{"com.docker.compose.project": "shared"}

			w := doContainerAction(h, a)

			require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrAmbiguousStack, body["code"])
			assert.Contains(t, body["message"], "/srv/stacks/alpha")
			assert.Contains(t, body["message"], "/srv/stacks/beta")
			assert.Empty(t, fake.recorded(), "a refused action must leave the container untouched")

			// Neither stack was left locked by the refusal.
			for _, id := range []string{"s-alpha", "s-beta"} {
				tok, err := lock.Acquire(id, services.OpKindStart)
				require.NoError(t, err, "%s was left locked", id)
				lock.Release(id, tok)
			}

			// Other side, same fixture: the uniquely named stack still acts.
			w = doContainerAction(h, containerAction{a.name, a.method, strings.Replace(a.path, "/amb", "/managed", 1)})
			assert.Less(t, w.Code, 300, "managed container action, body: %s", w.Body.String())
		})
	}
}

// newUpdateContainerFixture serves the two Docker calls updateContainer makes
// before it decides (the constructor's ping and the container inspect), with
// container "amb" labelled "shared" and "solo" labelled "web".
func newUpdateContainerFixture(t *testing.T) *ResourcesHandler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/amb/json"), strings.HasSuffix(r.URL.Path, "/containers/solo/json"):
			project := "shared"
			if strings.Contains(r.URL.Path, "/solo/") {
				project = "web"
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"x","Name":"/app","Image":"sha256:old","Config":{"Image":"nginx:latest","Labels":{"com.docker.compose.project":"` + project + `","com.docker.compose.service":"app"}}}`)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	docker := newTestDockerServiceAgainst(t, srv)
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	jm := services.NewUpdateJobManager(15 * time.Minute)
	t.Cleanup(func() { jm.Stop() })
	h := NewResourcesHandlerWithJobManager(docker, db, nil, jm)
	h.SetOperationLock(services.NewOperationLock())
	seedSharedProjectStacks(t, db)
	seedStackInDir(t, db, "s-web", "web", "/srv/stacks/web")
	return h
}

// TestUpdateContainer_SharedProjectNameRefuses is the same decision for a
// manual container update: refused before any lock, history row or job.
func TestUpdateContainer_SharedProjectNameRefuses(t *testing.T) {
	h := newUpdateContainerFixture(t)
	router := setupResourcesRouter(h)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/containers/amb/update", nil))

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrAmbiguousStack, body["code"])
	assert.Contains(t, body["message"], "/srv/stacks/alpha")
	assert.Contains(t, body["message"], "/srv/stacks/beta")
	assert.Empty(t, h.jobManager.List(), "a refused update must not be queued")
	hist, _, err := h.db.GetUpdateHistory(models.UpdateHistoryFilters{})
	require.NoError(t, err)
	assert.Empty(t, hist, "a refused update must leave no history row")

	// Other side, same fixture: a uniquely named project is accepted.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/containers/solo/update", nil))
	assert.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
}

func getStackBody(t *testing.T, h *StacksHandler, id string) map[string]interface{} {
	t.Helper()
	router := gin.New()
	router.GET("/stacks/:id", h.Get)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stacks/"+id, nil))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	return decodeBody(t, w)
}

// TestStacksHandler_Get_NamesStacksSharingItsProjectName is the stack page's
// half of D25: BOTH stacks sharing a project name say so, each naming the
// other, and a stack with a unique name carries no field at all.
func TestStacksHandler_Get_NamesStacksSharingItsProjectName(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	seedSharedProjectStacks(t, db)
	seedStackInDir(t, db, "s-web", "web", "/srv/stacks/web")
	h := NewStacksHandler(nil, nil, nil, db, &config.Config{StacksDir: "/srv/stacks"}, services.NewActionLogger(db), services.NewOperationLock())

	for id, other := range map[string]map[string]interface{}{
		"s-alpha": {"id": "s-beta", "directory": "/srv/stacks/beta"},
		"s-beta":  {"id": "s-alpha", "directory": "/srv/stacks/alpha"},
	} {
		body := getStackBody(t, h, id)
		assert.Equal(t, []interface{}{other}, body["projectNameSharedWith"], "stack %s", id)
	}

	body := getStackBody(t, h, "s-web")
	_, present := body["projectNameSharedWith"]
	assert.False(t, present, "a uniquely named stack must not carry the field: %v", body)
}

// sharedNameFaultStore answers GetStack but fails the project-name lookup with
// a plain error, the way a database fault would.
type sharedNameFaultStore struct{ fakeStackStore }

func (sharedNameFaultStore) GetStackByProjectName(string) (*models.Stack, error) {
	return nil, errors.New("sql: database is closed")
}

// TestStacksHandler_Get_SharedNameCheckFaultStillServesTheStack: the warning
// is display only, so a fault in the extra lookup is logged and the field left
// out; the stack itself is still served.
func TestStacksHandler_Get_SharedNameCheckFaultStillServesTheStack(t *testing.T) {
	buf := captureHandlerLogs(t)
	store := &sharedNameFaultStore{fakeStackStore{stack: &models.Stack{ID: "s1", ProjectName: "web", Directory: "/nowhere", ComposeFile: "compose.yaml"}}}
	h := NewStacksHandler(nil, nil, nil, store, &config.Config{StacksDir: "/srv/stacks"}, nil, services.NewOperationLock())

	body := getStackBody(t, h, "s1")

	assert.Equal(t, "s1", body["id"])
	_, present := body["projectNameSharedWith"]
	assert.False(t, present, "a failed check must not invent a warning: %v", body)
	assert.Contains(t, buf.String(), "compose project name is shared")
	assert.Contains(t, buf.String(), "database is closed")
}
