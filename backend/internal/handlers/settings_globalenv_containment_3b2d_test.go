package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-3b2d: global.env is written only inside DataDir. A global.env that
// is a symlink out of DataDir is refused (409); reading it is unchanged.

func TestGlobalEnvPut_LinkOutsideDataDirRefused_3b2d(t *testing.T) {
	dataDir := t.TempDir()
	_, outside := outsideLink3b2d(t, dataDir, "global.env")
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	h := NewSettingsHandler(db, "", "secret-3b2d", false, nil, &config.Config{DataDir: dataDir})
	router := gin.New()
	router.PUT("/settings/global-env", unlocked4hxa, h.UpdateGlobalEnv)

	w := doJSONSiou(t, router, http.MethodPut, "/settings/global-env", map[string]any{
		"vars": []map[string]string{{"key": "A", "value": "1"}},
	})

	assertUntouched3b2d(t, outside)
	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "global.env links to a file outside "+dataDir)
}
