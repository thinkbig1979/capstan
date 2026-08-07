package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// recordingStackDocker captures the stacks handed to StartVerified so a test can
// assert which compose project a deploy actually targeted.
//
// It is separate from fakeStackDocker (stacks_test.go) on purpose: that double's
// StartVerified returns a zero ActionResult, and Create's create-with-deploy path
// branches on the outcome. Recording here keeps this file's expectations from
// depending on a fixture the delete tests own.
type recordingStackDocker struct {
	started []models.Stack
}

func (f *recordingStackDocker) GetStackStatuses(context.Context, services.DashboardDB) (map[string]services.LiveStatus, error) {
	return map[string]services.LiveStatus{}, nil
}
func (f *recordingStackDocker) StartVerified(s models.Stack) (truth.ActionResult, string) {
	f.started = append(f.started, s)
	return truth.Success("stack started"), "started"
}
func (f *recordingStackDocker) StopVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *recordingStackDocker) RestartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *recordingStackDocker) PullVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *recordingStackDocker) DeleteVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}

// TestStacksHandler_CreateWithDeploy_ProjectNameMatchesPersistedRow guards
// agent-os-07x: create-with-deploy deployed under "<name>-default" while the row
// the scanner then wrote — and every later operation reads — said "<name>".
// docker.go's buildComposeArgs passes stack.ProjectName as -p, so the two
// derivations disagreeing means status lookups miss the running container and a
// later Start creates a second, colliding project.
//
// The assertion is deliberately a comparison between the two derivations rather
// than a literal: it fails on any future divergence, not just this one.
func TestStacksHandler_CreateWithDeploy_ProjectNameMatchesPersistedRow(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	docker := &recordingStackDocker{}
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(docker, scanner, services.NewLinterService(), db, cfg,
		services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)

	require.NoError(t, db.CreateUser(models.User{
		ID:        "test-user-id",
		Username:  "testuser",
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}))

	const stackName = "myapp"
	createTestDirectory(t, db, filepath.Join(tempDir, stackName))

	reqBytes, err := json.Marshal(map[string]interface{}{
		"name":           stackName,
		"composeContent": "services:\n  web:\n    image: nginx:1.21\n",
		"deploy":         true,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	require.Len(t, docker.started, 1, "create-with-deploy should have deployed exactly once")
	deployedProject := docker.started[0].ProjectName

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	persistedProject := stacks[0].ProjectName

	assert.Equal(t, persistedProject, deployedProject,
		"deploy targeted compose project %q but the stored row says %q — every later "+
			"operation reads the stored value, so the running containers are orphaned",
		deployedProject, persistedProject)

	// The response is what the frontend caches, so it has to agree too.
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	respStack := resp["details"].(map[string]interface{})["stack"].(map[string]interface{})
	assert.Equal(t, persistedProject, respStack["projectName"],
		"create response reported a different project name than the row it stored")
}

// TestScannerAndCreateAgreeOnProjectName pins the shared derivation itself, so a
// change to either caller's rule is caught at the unit level and not only
// through the handler.
func TestScannerAndCreateAgreeOnProjectName(t *testing.T) {
	tests := []struct {
		dirName string
		name    string
		want    string
	}{
		{"myapp", "default", "myapp"},
		{"myapp", "api", "myapp-api"},
		{"myapp", "web:2", "myapp-web-2"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, services.ComposeProjectName(tt.dirName, tt.name),
			"ComposeProjectName(%q, %q)", tt.dirName, tt.name)
	}
}
