package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// statusRecordingStore records every stored stack status.
type statusRecordingStore struct {
	fakeStackStore
	stored []string
}

func (s *statusRecordingStore) UpdateStackStatus(_ string, status string) error {
	s.stored = append(s.stored, status)
	return nil
}

// agent-os-vush: a delete that keeps the stack's row stores the verified state
// compose down left behind, so the Docker-down fallback does not keep showing
// the pre-delete status.
func TestStacksHandler_Delete_KeptRowStoresVerifiedStatus_Vush(t *testing.T) {
	cases := []struct {
		name      string
		deleteAR  truth.ActionResult
		deleteErr error
		want      string
	}{
		{
			name: "partial delete keeps the row",
			deleteAR: truth.Failed("stack not fully removed: 1 container(s) still present (running:1)", nil,
				truth.KV("status", "partial")),
			want: "partial",
		},
		{
			name:      "verified down, then the row delete fails",
			deleteAR:  truth.Success("stack and volumes removed", truth.KV("status", "stopped")),
			deleteErr: errors.New("database is locked"),
			want:      "stopped",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tempRoot := t.TempDir()
			stackDir := filepath.Join(tempRoot, "mystack")
			require.NoError(t, os.MkdirAll(stackDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {}\n"), 0o600))

			id := "root~mystack:default"
			store := &statusRecordingStore{fakeStackStore: fakeStackStore{
				stack:     &models.Stack{ID: id, Directory: stackDir, ComposeFile: "compose.yaml", ProjectName: "mystack-default", Status: "running"},
				deleteErr: tc.deleteErr,
			}}
			docker := &fakeStackDocker{deleteAR: tc.deleteAR}
			logDB, err := database.NewWithMigrations(":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { _ = logDB.Close() })
			handler := NewStacksHandler(docker, nil, nil, store, &config.Config{StacksDir: tempRoot},
				services.NewActionLogger(logDB), services.NewOperationLock())

			router := gin.New()
			router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?confirm=true", nil))

			require.NotEqual(t, http.StatusOK, w.Code, "fixture: the delete did not complete: %s", w.Body.String())
			assert.Equal(t, []string{tc.want}, store.stored)
		})
	}
}
