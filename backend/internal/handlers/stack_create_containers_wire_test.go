package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestStacksHandler_Create_RereadFailureSendsEmptyContainers drives Create's
// non-fatal re-read branch: the scan succeeds, h.db.GetStack fails, and the
// handler answers with its own pre-scan literal. That literal is the one Stack
// producer outside database/stacks.go that reaches the wire, so it must open
// Containers as [] like emptyStack() does, or this path sends
// "containers":null (agent-os-e5pr).
//
// GetStack is made to fail with a trigger rather than a fake: the handler
// holds a concrete *database.DB. The trigger fires only on the scanner's
// upsert (status "unknown"; the Create literal writes "stopped") and stores
// text in the INTEGER git_ahead column, so the handler's own insert and the
// scan both succeed and only the re-read's Scan fails.
func TestStacksHandler_Create_RereadFailureSendsEmptyContainers(t *testing.T) {
	const name = "e5pr-reread"
	tempDir := t.TempDir()
	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// A second handle on the same file, used only to install the fault.
	raw, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.Exec(`CREATE TRIGGER e5pr_poison_reread AFTER INSERT ON stacks
		WHEN NEW.status = 'unknown'
		BEGIN UPDATE stacks SET git_ahead = 'not-an-int' WHERE id = NEW.id; END`)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	handler := NewStacksHandler(&recordingStackDocker{}, services.NewScannerService(cfg, db),
		services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))
	createTestDirectory(t, db, filepath.Join(tempDir, name))

	w := hgtbCreate(t, router, name, false)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Details struct {
			Stack map[string]json.RawMessage `json:"stack"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Premise: the re-read really failed, so the response is the literal. The
	// literal says "stopped"; a successful re-read would say "unknown".
	require.JSONEq(t, `"stopped"`, string(resp.Details.Stack["status"]),
		"re-read did not fail, so this test is not on the fallback branch: %s", w.Body.String())

	require.JSONEq(t, `[]`, string(resp.Details.Stack["containers"]),
		"Create's fallback literal sent containers as %s", resp.Details.Stack["containers"])
}
