package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-d1ue: the stack env save and the global env save refused a locked
// session with the older 403 FORBIDDEN shape. They answer the same
// ENV_LOCKED refusal as every other locked write now, and still write
// nothing: a locked session holds blanked values, and saving them would
// overwrite every secret.

func TestEnvSaves_LockedAnswerEnvLocked_d1ue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dir := filepath.Join(root, "app")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	const original = "DB_PASSWORD=owner-secret\n"
	//nolint:forbidigo // test fixture outside any stack lock
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(original), 0o600))
	//nolint:forbidigo // test fixture outside any stack lock
	require.NoError(t, os.WriteFile(filepath.Join(root, "global.env"), []byte(original), 0o600))
	createTestDirectory(t, db, dir)
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s1", Directory: dir, ComposeFile: "compose.yml", EnvFile: ".env", ProjectName: "s1"}))

	cfg := &config.Config{StacksDir: root, DataDir: root}
	env := NewEnvHandler(db, cfg)
	settings := NewSettingsHandler(db, root, "test-secret-key-32-chars-long!!!", false, nil, cfg)
	router := gin.New() // no unlock mark: locked
	env.RegisterRoutes(router.Group("/api/stacks"))
	router.PUT("/api/settings/global-env", settings.UpdateGlobalEnv)

	for _, tc := range []struct{ name, path, body, file, message string }{
		{"stack env", "/api/stacks/s1/env", `{"raw":"DB_PASSWORD=\n"}`, filepath.Join(dir, ".env"), "Unlock env values to save environment variables: "},
		{"global env", "/api/settings/global-env", `{"vars":[{"key":"DB_PASSWORD","value":""}]}`, filepath.Join(root, "global.env"), "Unlock env values to save global environment variables: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body.String())
			assert.Equal(t, models.ErrEnvLocked, envLockedCodeH71i(t, w))
			assert.Contains(t, w.Body.String(), tc.message+lockedBlankedReason)
			//nolint:gosec // test reads its own fixture
			got, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			assert.Equal(t, original, string(got), "a refused save writes nothing")
		})
	}
}
