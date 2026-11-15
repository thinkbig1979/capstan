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

// agent-os-n9kr: the scanner keeps a stack row whose compose file is gone while
// its containers still run (agent-os-epc5), and marks it "error". The live
// branch of applyLiveStatus used to replace that with the containers' own
// status, so the list and the stack page read "running" for a stack Capstan
// can no longer start, stop or edit. Fail closed: the containers stay listed,
// the status is "error".
func TestApplyLiveStatus_GoneComposeWithContainersIsError_N9kr(t *testing.T) {
	live := map[string]services.LiveStatus{
		"shop": {Status: "running", Containers: []models.Container{{ID: "c1", Name: "shop-web-1", State: "running"}}},
	}

	gone := &models.Stack{ProjectName: "shop", Directory: t.TempDir(), ComposeFile: "compose.yaml"}
	applyLiveStatus(gone, live)
	assert.Equal(t, "error", gone.Status, "a stack whose compose file is gone must not read as its containers' status")
	assert.Len(t, gone.Containers, 1, "its running containers stay listed")

	// Control: the same stack with its compose file present takes the live status.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o600))
	present := &models.Stack{ProjectName: "shop", Directory: dir, ComposeFile: "compose.yaml"}
	applyLiveStatus(present, live)
	assert.Equal(t, "running", present.Status)
	assert.Len(t, present.Containers, 1)
}
