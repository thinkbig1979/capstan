package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-dn1z part 3: a stack's one-off containers reach the stack payload
// apart from its service containers.
func TestApplyLiveStatus_CarriesOneOffs_Dn1z(t *testing.T) {
	stack := &models.Stack{ProjectName: "web"}
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
