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
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// fileLockFixture is one stack on disk with a compose file and (unless
// withEnv is false) an .env, behind the compose and env routes, both wired to
// lock. The env unlock gate is satisfied so the writes reach the lock.
func fileLockFixture(t *testing.T, withEnv bool) (router *gin.Engine, lock *services.OperationLock, stackID, stackDir string) {
	t.Helper()
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	stackDir = filepath.Join(root, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {}\n"), 0644))
	envFile := ""
	if withEnv {
		envFile = ".env"
		require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte("A=1\n"), 0600))
	}
	createTestDirectory(t, db, stackDir)
	stackID = "stack1"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: stackID, Directory: stackDir, ComposeFile: "compose.yaml", EnvFile: envFile, ProjectName: "stack1",
	}))

	cfg := &config.Config{StacksDir: root}
	lock = services.NewOperationLock()
	compose := NewComposeHandler(services.NewLinterService(), db, cfg)
	compose.SetOperationLock(lock)
	env := NewEnvHandler(db, cfg)
	env.SetOperationLock(lock)

	router = gin.New()
	g := router.Group("/api")
	g.Use(func(c *gin.Context) { c.Set(middleware.CtxEnvUnlocked, true); c.Next() })
	compose.RegisterRoutes(g)
	env.RegisterRoutes(g)
	return router, lock, stackID, stackDir
}

// TestComposeEnvWrites_LockedStack is agent-os-a1ye.4: compose and env file
// writes used to land while a backup or lifecycle op held the stack. Each
// write route is called twice on the same fixture: with the stack held it must
// answer 409 and leave the files untouched; with the stack free the same
// request must succeed and leave the lock free again.
func TestComposeEnvWrites_LockedStack(t *testing.T) {
	const newCompose = "services:\n  web:\n    image: nginx\n"
	for _, tc := range []struct {
		name, method, path, body string
		withEnv                  bool
		file, want               string // file the free request writes, and its expected content
		okStatus                 int
	}{
		{"PUT compose", http.MethodPut, "/compose", `{"content":"services:\n  web:\n    image: nginx\n"}`, true, "compose.yaml", newCompose, http.StatusOK},
		{"PUT compose-env", http.MethodPut, "/compose-env", `{"composeContent":"services:\n  web:\n    image: nginx\n","envRaw":"B=2\n"}`, true, ".env", "B=2\n", http.StatusOK},
		{"PUT env", http.MethodPut, "/env", `{"raw":"B=2\n"}`, true, ".env", "B=2\n", http.StatusOK},
		{"POST env", http.MethodPost, "/env", `{"content":"B=2\n"}`, false, ".env", "B=2\n", http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, lock, stackID, stackDir := fileLockFixture(t, tc.withEnv)
			read := func(name string) string {
				//nolint:gosec // test helper: name is a literal from this file, stackDir is the test's own TempDir
				b, err := os.ReadFile(filepath.Join(stackDir, name))
				if os.IsNotExist(err) {
					return "<absent>"
				}
				require.NoError(t, err)
				return string(b)
			}
			call := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(tc.method, "/api/"+stackID+tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w
			}
			composeBefore, envBefore := read("compose.yaml"), read(".env")

			backupToken, err := lock.Acquire(stackID, services.OpKindBackup)
			require.NoError(t, err)
			w := call()
			require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrOperationInProgress, body["code"])
			assert.Contains(t, body["message"], "backup in progress since")
			assert.Equal(t, composeBefore, read("compose.yaml"), "compose file written while the stack was locked")
			assert.Equal(t, envBefore, read(".env"), "env file written while the stack was locked")
			lock.Release(stackID, backupToken)

			w = call()
			require.Equal(t, tc.okStatus, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, tc.want, read(tc.file))
			token, err := lock.Acquire(stackID, services.OpKindStart)
			require.NoError(t, err, "the write left the stack locked")
			lock.Release(stackID, token)
		})
	}
}
