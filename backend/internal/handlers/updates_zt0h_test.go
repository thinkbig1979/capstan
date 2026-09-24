package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-zt0h. Both cache-reading branches of checkUpdates rebuild
// ContainerUpdateInfo from cached_updates. The Updates tab tells "not managed
// by Capstan" from "the stack lookup failed" only through stackLookupFailed,
// and says where the project lives only through the two compose labels, so
// both branches must send all three. The old-row case is the default a
// pre-migration-18 row reads back as.

type zt0hWireRow struct {
	ContainerID        string  `json:"containerId"`
	StackID            string  `json:"stackId"`
	StackLookupFailed  *bool   `json:"stackLookupFailed"`
	ComposeWorkingDir  *string `json:"composeWorkingDir"`
	ComposeConfigFiles *string `json:"composeConfigFiles"`
}

func zt0hCached(id string, failed bool, dir, files string) models.CachedUpdate {
	return models.CachedUpdate{
		ID: "cu-" + id, ContainerID: id, ContainerName: id, Image: "app:1", ImageRef: "app:1",
		State: "running", ProjectName: "proj-" + id, ServiceName: "web", IsCompose: true,
		StackLookupFailed: failed, ComposeWorkingDir: dir, ComposeConfigFiles: files,
		LocalDigest: "sha256:a", RemoteDigest: "sha256:b", ScannedAt: time.Now().Format(time.RFC3339),
	}
}

func TestCheckUpdates_CacheBranchesCarryStackContext(t *testing.T) {
	branches := []struct {
		name string
		url  string
		want int
		h    func(*ResourcesHandler) *ResourcesHandler
	}{
		{"plain GET", "/api/resources/updates", http.StatusOK, func(h *ResourcesHandler) *ResourcesHandler { return h }},
		{"refresh=true", "/api/resources/updates?refresh=true", http.StatusAccepted, func(h *ResourcesHandler) *ResourcesHandler {
			return &ResourcesHandler{db: h.db, scheduler: benignScanner{}, actionLog: services.NewActionLogger(h.db)}
		}},
	}
	for _, b := range branches {
		t.Run(b.name, func(t *testing.T) {
			base := newTestResourcesHandler(t)
			require.NoError(t, base.db.SetCachedUpdates([]models.CachedUpdate{
				zt0hCached("unmanaged", false, "/home/op/proj", "/home/op/proj/a.yml,/home/op/proj/b.yml"),
				zt0hCached("failed", true, "/srv/failed", "/srv/failed/compose.yml"),
				zt0hCached("old", false, "", ""),
			}))

			w := httptest.NewRecorder()
			setupResourcesRouter(b.h(base)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, b.url, nil))
			require.Equal(t, b.want, w.Code, "body: %s", w.Body.String())

			var body struct {
				Updates []zt0hWireRow `json:"updates"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			rows := map[string]zt0hWireRow{}
			for _, r := range body.Updates {
				rows[r.ContainerID] = r
			}
			require.Len(t, rows, 3, "body: %s", w.Body.String())

			for id, want := range map[string]struct {
				failed     bool
				dir, files string
			}{
				"unmanaged": {false, "/home/op/proj", "/home/op/proj/a.yml,/home/op/proj/b.yml"},
				"failed":    {true, "/srv/failed", "/srv/failed/compose.yml"},
			} {
				r := rows[id]
				require.NotNil(t, r.StackLookupFailed, "%s: stackLookupFailed must be sent, not omitted", id)
				assert.Equal(t, want.failed, *r.StackLookupFailed, id)
				require.NotNil(t, r.ComposeWorkingDir, "%s: composeWorkingDir dropped", id)
				assert.Equal(t, want.dir, *r.ComposeWorkingDir, id)
				require.NotNil(t, r.ComposeConfigFiles, "%s: composeConfigFiles dropped", id)
				assert.Equal(t, want.files, *r.ComposeConfigFiles, id)
			}

			old := rows["old"]
			require.NotNil(t, old.StackLookupFailed, "the flag is sent on every row, defaults included")
			assert.False(t, *old.StackLookupFailed)
			assert.Nil(t, old.ComposeWorkingDir, "an unrecorded path is omitted, never sent as a made-up value")
			assert.Nil(t, old.ComposeConfigFiles)
		})
	}
}
