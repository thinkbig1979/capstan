package handlers

import (
	"encoding/json"
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

// noEnvStack is one stack with a compose file and NO env file recorded, the
// state "Extract to .env" meets on a stack that never had one (agent-os-4p86).
// The env unlock gate is satisfied so the request reaches the write.
type noEnvStack struct {
	router   *gin.Engine
	db       *database.DB
	id       string
	dir      string
	original string
}

const noEnvOriginalCompose = "services: {}\n"

func newNoEnvStack(t *testing.T, composeFile string) noEnvStack {
	t.Helper()
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	dir := filepath.Join(root, "stack1")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(noEnvOriginalCompose), 0644))
	createTestDirectory(t, db, dir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack1", Directory: dir, ComposeFile: composeFile, EnvFile: "", ProjectName: "stack1",
	}))

	compose := NewComposeHandler(services.NewLinterService(), db, &config.Config{StacksDir: root})
	compose.SetOperationLock(services.NewOperationLock())
	router := gin.New()
	g := router.Group("/api")
	g.Use(func(c *gin.Context) { c.Set(middleware.CtxEnvUnlocked, true); c.Next() })
	compose.RegisterRoutes(g)
	return noEnvStack{router: router, db: db, id: "stack1", dir: dir, original: noEnvOriginalCompose}
}

const extractedCompose = "services:\n  web:\n    image: nginx\n    environment:\n      TOKEN: ${TOKEN}\n"

func (s noEnvStack) put(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/"+s.id+"/compose-env", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func (s noEnvStack) recordedEnvFile(t *testing.T) string {
	t.Helper()
	stack, err := s.db.GetStack(s.id)
	require.NoError(t, err)
	return stack.EnvFile
}

func extractBody(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"composeContent": extractedCompose, "envRaw": "TOKEN=abc123\n"})
	require.NoError(t, err)
	return string(b)
}

// Extract to .env on a stack with no env file used to save the compose change
// (now referencing ${TOKEN}) and drop the value, answering success. The value
// must land in a new .env, the stack must record it so compose is handed it,
// and the response must name the file.
func TestComposeEnvPut_NoEnvFile_CreatesDotEnv(t *testing.T) {
	s := newNoEnvStack(t, "compose.yaml")

	w := s.put(t, extractBody(t))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	//nolint:gosec // path is under the test's own TempDir
	env, err := os.ReadFile(filepath.Join(s.dir, ".env"))
	require.NoError(t, err, "the extracted value was dropped: no .env was created")
	assert.Equal(t, "TOKEN=abc123\n", string(env))
	info, err := os.Stat(filepath.Join(s.dir, ".env"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	//nolint:gosec // path is under the test's own TempDir
	compose, err := os.ReadFile(filepath.Join(s.dir, "compose.yaml"))
	require.NoError(t, err)
	assert.Equal(t, extractedCompose, string(compose))

	assert.Equal(t, ".env", s.recordedEnvFile(t), "an unrecorded .env is not passed to compose as --env-file")

	var resp struct {
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, ".env", resp.Details["env"])
}

// The other side of the guard: a compose-only save on the same stack creates
// nothing and records nothing.
func TestComposeEnvPut_NoEnvFile_ComposeOnlyCreatesNothing(t *testing.T) {
	s := newNoEnvStack(t, "compose.yaml")

	w := s.put(t, `{"composeContent":"services:\n  web:\n    image: nginx\n"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	_, err := os.Lstat(filepath.Join(s.dir, ".env"))
	assert.True(t, os.IsNotExist(err), "a save with no env content must not create .env")
	assert.Equal(t, "", s.recordedEnvFile(t))
}

// The .env created for the request is removed when the compose write then
// fails. The compose file is named inside a folder that does not exist, so its
// temp file cannot be created, while the .env beside it is written first. (A
// chmod would not do: a root runner ignores it.)
func TestComposeEnvPut_NoEnvFile_ComposeWriteFails_RemovesNewEnv(t *testing.T) {
	s := newNoEnvStack(t, filepath.Join("missing-dir", "compose.yaml"))

	w := s.put(t, extractBody(t))
	require.Equal(t, http.StatusInternalServerError, w.Code, "the compose write must fail for this test to mean anything; body: %s", w.Body.String())

	_, err := os.Lstat(filepath.Join(s.dir, ".env"))
	assert.True(t, os.IsNotExist(err), "a half-applied change: the new .env was left behind after the compose write failed")
	assert.Equal(t, "", s.recordedEnvFile(t))
}

// With no env file recorded but an entry at .env on disk (a scan older than
// the file), the frontend's envRaw is only the new VAR=value line
// (useExtractToEnv.ts), so writing it would replace the real file's other
// values. Refuse before any write, whatever the entry is.
func TestComposeEnvPut_UnrecordedExistingDotEnv_Refused(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, envPath string)
		bytes string
	}{
		{"file", func(t *testing.T, envPath string) {
			require.NoError(t, os.WriteFile(envPath, []byte("KEEP=1\nOTHER=2\n"), 0600))
		}, "KEEP=1\nOTHER=2\n"},
		{"dangling symlink", func(t *testing.T, envPath string) {
			require.NoError(t, os.Symlink("nowhere", envPath))
		}, ""},
		{"directory", func(t *testing.T, envPath string) {
			require.NoError(t, os.Mkdir(envPath, 0755))
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newNoEnvStack(t, "compose.yaml")
			envPath := filepath.Join(s.dir, ".env")
			tc.setup(t, envPath)

			w := s.put(t, extractBody(t))
			assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "not linked to this stack")

			info, err := os.Lstat(envPath)
			require.NoError(t, err, ".env must still be there")
			switch tc.name {
			case "file":
				//nolint:gosec // path is under the test's own TempDir
				got, rerr := os.ReadFile(envPath)
				require.NoError(t, rerr)
				assert.Equal(t, tc.bytes, string(got), "the existing .env was overwritten")
			case "dangling symlink":
				assert.NotZero(t, info.Mode()&os.ModeSymlink, "the symlink was replaced")
			case "directory":
				assert.True(t, info.IsDir(), "the directory was replaced")
			}

			//nolint:gosec // path is under the test's own TempDir
			compose, err := os.ReadFile(filepath.Join(s.dir, "compose.yaml"))
			require.NoError(t, err)
			assert.Equal(t, s.original, string(compose), "compose must not change when the env part is refused")
			assert.Equal(t, "", s.recordedEnvFile(t))
		})
	}
}
