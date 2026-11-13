package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
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
)

// agent-os-jsp3 (Edwin's 2026-10-20 add-on): when the scan that follows a
// create fails, the stack folder, its files and its row already exist. The
// handler answered 500 SCANNER_ERROR, telling the user the create failed for a
// stack that is on disk and listed. It now answers 207 partial, created and
// NOT deployed, carrying the stack; the scan error is logged, never sent.
//
// The scan is made to fail with a trigger rather than a fake (the handler
// holds a concrete *services.ScannerService): Create's RegisterDirectory only
// INSERTs the directories row, and the scan's UpsertDirectory is the first
// UPDATE of it, so only the scan hits the fault.
func TestStacksHandler_Create_ScanFailureIsCreatedNotDeployed_jsp3(t *testing.T) {
	const name = "jsp3-scanfail"
	const faultText = "jsp3-scan-fault-text"
	tempDir := t.TempDir()
	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	raw, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.Exec(`CREATE TRIGGER jsp3_fail_scan BEFORE UPDATE ON directories
		BEGIN SELECT RAISE(ABORT, '` + faultText + `'); END`)
	require.NoError(t, err)

	docker := &recordingStackDocker{}
	cfg := &config.Config{StacksDir: tempDir}
	handler := NewStacksHandler(docker, services.NewScannerService(cfg, db),
		services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	w := hgtbCreate(t, router, name, true)

	require.Equal(t, http.StatusMultiStatus, w.Code, "body: %s", w.Body.String())
	var resp struct {
		Outcome string         `json:"outcome"`
		Reason  string         `json:"reason"`
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "partial", resp.Outcome)
	assert.Contains(t, resp.Reason, "not deployed")
	assert.Equal(t, false, resp.Details["deployed"])
	stack, ok := resp.Details["stack"].(map[string]any)
	require.True(t, ok, "the created stack is in the body: %s", w.Body.String())
	assert.Equal(t, filepath.Join(tempDir, name), stack["directory"])
	assert.NotContains(t, w.Body.String(), faultText, "the scan error is logged, not sent")

	// Created: the folder, the file and the row exist. Not deployed.
	_, err = os.Stat(filepath.Join(tempDir, name, "compose.yaml"))
	require.NoError(t, err)
	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	assert.Empty(t, docker.started, "a stack whose scan failed is not deployed")
}
