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

// createStackRequest drives POST /stacks with the recording docker double and
// returns the recorder, the double and the DB, for the agent-os-f3ah arms.
func createStackForProjectName(t *testing.T, name string, deploy bool) (*httptest.ResponseRecorder, *recordingStackDocker, *database.DB) {
	t.Helper()
	return createStackForProjectNameWithContent(t, name, "services:\n  web:\n    image: nginx:1.21\n", deploy)
}

// createStackForProjectNameWithContent is createStackForProjectName with the
// compose body under the test's control, for the agent-os-89z2 arms where the
// top-level `name:` in the request body is the thing being exercised.
func createStackForProjectNameWithContent(t *testing.T, name, composeContent string, deploy bool) (*httptest.ResponseRecorder, *recordingStackDocker, *database.DB) {
	t.Helper()
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: tempDir}
	docker := &recordingStackDocker{}
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(docker, scanner, services.NewLinterService(), db, cfg,
		services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	require.NoError(t, db.CreateUser(models.User{ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime}))
	createTestDirectory(t, db, filepath.Join(tempDir, name))

	reqBytes, err := json.Marshal(map[string]interface{}{
		"name":           name,
		"composeContent": composeContent,
		"deploy":         deploy,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w, docker, db
}

// TestStacksHandler_Create_PersistsTheNameComposeDerives is agent-os-f3ah on
// the Capstan-created path. Before it, "MyStack" was persisted verbatim and
// the deploy ran `-p MyStack`, which compose rejects — a loud failure, which
// this must not turn into a silent one: the deploy and the row must both
// carry the normalised name compose will actually use.
func TestStacksHandler_Create_PersistsTheNameComposeDerives(t *testing.T) {
	w, docker, db := createStackForProjectName(t, "MyStack", true)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	require.Len(t, docker.started, 1)
	assert.Equal(t, "mystack", docker.started[0].ProjectName, "the deploy must target the name compose derives, or compose rejects -p")

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	assert.Equal(t, "mystack", stacks[0].ProjectName)

	// The 201 carries the stack struct, so the wire value follows the row.
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	respStack := resp["details"].(map[string]interface{})["stack"].(map[string]interface{})
	assert.Equal(t, "mystack", respStack["projectName"])
}

// TestStacksHandler_Create_RefusesANameThatNormalisesToNothing: "---" passes
// the charset check but compose derives an EMPTY project name from it, which
// compose refuses on every -p. Refuse it here, naming the rule, rather than
// persisting a row that can never start.
func TestStacksHandler_Create_RefusesANameThatNormalisesToNothing(t *testing.T) {
	w, docker, db := createStackForProjectName(t, "---", false)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "letter or digit", "the refusal must name the rule")
	assert.Empty(t, docker.started)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	assert.Empty(t, stacks, "a refused create must not leave a row behind")
}

// TestStacksHandler_Create_AcceptsAnAlreadyNormalName is the control for the
// two arms above: a name compose would not change is stored byte-for-byte.
func TestStacksHandler_Create_AcceptsAnAlreadyNormalName(t *testing.T) {
	w, docker, db := createStackForProjectName(t, "ok-normal", true)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Len(t, docker.started, 1)
	assert.Equal(t, "ok-normal", docker.started[0].ProjectName)
	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	assert.Equal(t, "ok-normal", stacks[0].ProjectName)
}

// TestStacksHandler_Create_AdoptsTheTopLevelNameFromTheComposeBody is
// agent-os-89z2 on the Capstan-created path.
//
// Create writes the compose file to disk (stack_crud.go:193-194) BEFORE it
// derives the project name, so it can and must ask compose the same question
// the scanner asks about the same file. If it derived the directory name here
// instead, the create would deploy under "-p namedstack" while the scan that
// immediately follows it rewrote the row to "custom": the containers the create
// had just started would be orphaned from the row every later operation reads —
// the agent-os-07x failure, re-entered through a different door.
//
// The deploy arm is the half that matters: buildComposeArgs passes
// stack.ProjectName as -p, so this asserts what compose is actually told.
func TestStacksHandler_Create_AdoptsTheTopLevelNameFromTheComposeBody(t *testing.T) {
	w, docker, db := createStackForProjectNameWithContent(t, "namedstack",
		"name: custom\nservices:\n  web:\n    image: nginx:1.21\n", true)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	require.Len(t, docker.started, 1)
	assert.Equal(t, "custom", docker.started[0].ProjectName,
		"the deploy must run under the project compose itself would use for this file")

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	assert.Equal(t, "custom", stacks[0].ProjectName,
		"the row must carry the same name the deploy used, or the scan that follows orphans it")

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	respStack := resp["details"].(map[string]interface{})["stack"].(map[string]interface{})
	assert.Equal(t, "custom", respStack["projectName"])
}

// TestStacksHandler_Create_RefusesANameThatNormalisesToNothing_EvenWithATopLevelName
// pins that the :64 guard is unchanged by agent-os-89z2. It validates the
// REQUEST NAME, which becomes the directory on disk, and it runs before any
// file is written — so there is no compose file to ask, and a `name:` in the
// body cannot buy a directory compose can derive nothing from.
func TestStacksHandler_Create_RefusesANameThatNormalisesToNothing_EvenWithATopLevelName(t *testing.T) {
	w, docker, db := createStackForProjectNameWithContent(t, "---",
		"name: custom\nservices:\n  web:\n    image: nginx:1.21\n", false)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "letter or digit", "the refusal must name the rule")
	assert.Empty(t, docker.started)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	assert.Empty(t, stacks, "a refused create must not leave a row behind")
}
