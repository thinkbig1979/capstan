package handlers

import (
	"embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-4hxa: resolveWriteTarget refuses a dangling or looping link and a
// target that is not a regular file with text that tells the user what to do.
// Every caller wrapped it as the hidden cause of a generic 500, so the text
// only reached the log. A refusal now answers 409 with that text; every other
// write fault stays 500. Routers here are UNLOCKED (orch ruling): the w6wx
// locked-save gate is not what these tests are about.

// stackWithComposePath4hxa makes stack1 in a stacks root and lets shape put
// whatever it wants at stack1/compose.yaml. It returns an unlocked router with
// the compose and env routes, and the stack directory.
func stackWithComposePath4hxa(t *testing.T, envFile string, shape func(dir string)) (*gin.Engine, string) {
	t.Helper()
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	dir := filepath.Join(root, "stack1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	shape(dir)
	createTestDirectory(t, db, dir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack1", Directory: dir, ComposeFile: "compose.yaml", EnvFile: envFile, ProjectName: "stack1",
	}))

	cfg := &config.Config{StacksDir: root}
	compose := NewComposeHandler(services.NewLinterService(), db, cfg)
	compose.SetOperationLock(services.NewOperationLock())
	env := NewEnvHandler(db, cfg)
	env.SetOperationLock(services.NewOperationLock())
	router := gin.New()
	group := router.Group("/api", unlocked4hxa)
	compose.RegisterRoutes(group)
	env.RegisterRoutes(group)
	return router, dir
}

func unlocked4hxa(c *gin.Context) {
	c.Set("envUnlocked", true)
	c.Next()
}

const compose4hxa = "services:\n  web:\n    image: nginx:1.27\n"

// must4hxa fails a fixture step loudly: the shape closures have no t.
func must4hxa(err error) {
	if err != nil {
		panic(err)
	}
}

func danglingCompose4hxa(dir string) {
	if err := os.Symlink("missing.yaml", filepath.Join(dir, "compose.yaml")); err != nil {
		panic(err)
	}
}

func TestComposePut_DanglingLinkAnswers409WithRefusal_4hxa(t *testing.T) {
	router, dir := stackWithComposePath4hxa(t, "", danglingCompose4hxa)

	w := doJSONSiou(t, router, http.MethodPut, "/api/stack1/compose", map[string]string{"content": compose4hxa})

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	var body models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body.Message, "fix or remove the link")
	assert.Contains(t, body.Message, "missing.yaml")
	_, err := os.Lstat(filepath.Join(dir, "missing.yaml"))
	assert.True(t, os.IsNotExist(err), "the link's target is not created")
}

func TestComposeEnvPut_DanglingComposeLinkAnswers409_4hxa(t *testing.T) {
	router, _ := stackWithComposePath4hxa(t, "", danglingCompose4hxa)

	w := doJSONSiou(t, router, http.MethodPut, "/api/stack1/compose-env", map[string]string{"composeContent": compose4hxa})

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	var body struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "failed", body.Outcome)
	assert.Contains(t, body.Reason, "fix or remove the link")
	// No env was part of this save, so nothing was rolled back.
	assert.NotContains(t, body.Reason, "rolled back")
}

func TestEnvPut_DanglingLinkAnswers409_4hxa(t *testing.T) {
	router, dir := stackWithComposePath4hxa(t, ".env", func(dir string) {
		must4hxa(os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose4hxa), 0o644))
		must4hxa(os.Symlink("gone.env", filepath.Join(dir, ".env")))
	})

	w := doJSONSiou(t, router, http.MethodPut, "/api/stack1/env", map[string]string{"raw": "A=1\n"})

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "fix or remove the link")
	_, err := os.Lstat(filepath.Join(dir, "gone.env"))
	assert.True(t, os.IsNotExist(err))
}

func TestEnvCreate_DanglingLinkAnswers409_4hxa(t *testing.T) {
	router, _ := stackWithComposePath4hxa(t, "", func(dir string) {
		must4hxa(os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose4hxa), 0o644))
		must4hxa(os.Symlink("gone.env", filepath.Join(dir, ".env")))
	})

	w := doJSONSiou(t, router, http.MethodPost, "/api/stack1/env", map[string]string{"filename": ".env", "raw": "A=1\n"})

	// Create refuses an existing .env entry before writing in some shapes;
	// whichever check answers, it is a 4xx naming the problem, never a 500.
	require.GreaterOrEqual(t, w.Code, 400, "body: %s", w.Body.String())
	require.Less(t, w.Code, 500, "body: %s", w.Body.String())
}

func TestWriteRefusal_NotRegularFileIs409_4hxa(t *testing.T) {
	router, _ := stackWithComposePath4hxa(t, "", func(dir string) {
		must4hxa(os.MkdirAll(filepath.Join(dir, "compose.yaml"), 0o755))
	})

	w := doJSONSiou(t, router, http.MethodPut, "/api/stack1/compose", map[string]string{"content": compose4hxa})

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "not a regular file")
}

// A looping link is not tested over HTTP: lockStacksSharingFiles
// (operation_lock.go) resolves the path's links before the write and answers
// its own 500 for a loop, so the write's refusal is not reached on that route.
// The refusal itself is pinned by TestWriteFileAtomic_RefusesUnwritableLinkShapes_dwmz
// ("cannot be followed") and by TestWriteRefusalIsTyped_4hxa below.

func TestWriteRefusalIsTyped_4hxa(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Symlink("other.yaml", filepath.Join(dir, "compose.yaml")))
	require.NoError(t, os.Symlink("compose.yaml", filepath.Join(dir, "other.yaml")))

	err := writeComposeFileAtomic(filepath.Join(dir, "compose.yaml"), []byte(compose4hxa))
	require.Error(t, err)
	appErr := writeFailureError(err, "WRITE_ERROR", "Failed to write compose file")
	assert.Equal(t, http.StatusConflict, appErr.Status)
	assert.Contains(t, appErr.Message, "cannot be followed")

	// The other side, same helper: a fault that is not a refusal stays 500.
	appErr = writeFailureError(os.ErrPermission, "WRITE_ERROR", "Failed to write compose file")
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	assert.Equal(t, "Failed to write compose file", appErr.Message)
}

// The other side: a write fault that is not about the user's file stays a
// 500 with the generic message. The fault is the temp file's name: the link's
// target is a regular file whose 250-byte name fits, while "."+name+".tmp-N"
// does not, so CreateTemp fails with ENAMETOOLONG. That holds for root too,
// unlike a read-only directory, and passes every check before the write
// (ENOTDIR at the path already fails the lock's link resolution).
func TestWriteFault_TempCreateStays500_4hxa(t *testing.T) {
	router, _ := stackWithComposePath4hxa(t, "", func(dir string) {
		long := strings.Repeat("c", 250)
		must4hxa(os.WriteFile(filepath.Join(dir, long), []byte(compose4hxa), 0o644))
		must4hxa(os.Symlink(long, filepath.Join(dir, "compose.yaml")))
	})

	w := doJSONSiou(t, router, http.MethodPut, "/api/stack1/compose", map[string]string{"content": compose4hxa})

	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	var body models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "WRITE_ERROR", body.Code)
	assert.Equal(t, "Failed to write compose file", body.Message)
}

func TestGlobalEnvPut_DanglingLinkAnswers409_4hxa(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, os.Symlink("gone.env", filepath.Join(dataDir, "global.env")))
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	h := NewSettingsHandler(db, "", "secret-4hxa", false, nil, &config.Config{DataDir: dataDir})
	router := gin.New()
	router.PUT("/settings/global-env", unlocked4hxa, h.UpdateGlobalEnv)

	w := doJSONSiou(t, router, http.MethodPut, "/settings/global-env", map[string]any{
		"vars": []map[string]string{{"key": "A", "value": "1"}},
	})

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "fix or remove the link")
}

// The guard: every write*FileAtomic call in the handlers either routes its
// error through writeFailureError / renderWriteFailure, or carries
// //writerefusal:ignore with the reason. go:embed, so an -overlay mutant of a
// handler file is what this reads.
//
//go:embed *.go
var handlerSources4hxa embed.FS

var writeCallRe4hxa = regexp.MustCompile(`\bwrite(Compose|Env)?FileAtomic\(`)

func TestWriteHelperCallsMapRefusals_4hxa(t *testing.T) {
	entries, err := handlerSources4hxa.ReadDir(".")
	require.NoError(t, err)

	sites := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") || name == "atomic_write.go" {
			continue
		}
		src, err := handlerSources4hxa.ReadFile(name)
		require.NoError(t, err)
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			if !writeCallRe4hxa.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			sites++
			if strings.Contains(line, "//writerefusal:ignore") {
				continue
			}
			// The window is the call's error branch: up to the first return,
			// and never past the next write call.
			mapped := false
			for j := i; j < len(lines) && j < i+12; j++ {
				if j > i && writeCallRe4hxa.MatchString(lines[j]) {
					break
				}
				if strings.Contains(lines[j], "writeFailureError(") || strings.Contains(lines[j], "renderWriteFailure(") {
					mapped = true
					break
				}
				if strings.Contains(lines[j], "return") {
					break
				}
			}
			assert.True(t, mapped, "%s:%d: a write helper's error must go through writeFailureError or renderWriteFailure (a refusal is a 409 with its text), or carry //writerefusal:ignore <reason>", name, i+1)
		}
	}
	// Not vacuous: the handlers hold ten such calls today.
	assert.GreaterOrEqual(t, sites, 10, "the guard saw too few write calls; did the helper names change?")
}
