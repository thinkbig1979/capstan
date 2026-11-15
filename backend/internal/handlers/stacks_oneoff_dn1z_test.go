package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-dn1z part 3: a stack's one-off containers reach the stack payload
// apart from its service containers.
func TestApplyLiveStatus_CarriesOneOffs_Dn1z(t *testing.T) {
	// A real compose file: an unreadable one reads "error" (agent-os-n9kr).
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o600))
	stack := &models.Stack{ProjectName: "web", Directory: dir, ComposeFile: "compose.yaml"}
	applyLiveStatus(stack, map[string]services.LiveStatus{
		"web": {
			Status:     "running",
			Containers: []models.Container{{ID: "w1", Name: "web-app-1"}},
			OneOffs:    []models.Container{{ID: "w2", Name: "web-app-run-1"}},
		},
	})
	assert.Equal(t, "running", stack.Status)
	assert.Equal(t, []models.Container{{ID: "w1", Name: "web-app-1"}}, stack.Containers)
	assert.Equal(t, []models.Container{{ID: "w2", Name: "web-app-run-1"}}, stack.OneOffContainers)
}
