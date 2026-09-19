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

// envCreateFixture wires a real router at POST /:id/env and returns a maker for
// stacks with no .env on disk, plus the temp root so arms can assert on the file.
func envCreateFixture(t *testing.T) (*gin.Engine, func(name string) (id, dir string)) {
	t.Helper()
	tempDir := t.TempDir()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mk := func(name string) (string, string) {
		dir := filepath.Join(tempDir, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		createTestDirectory(t, db, dir)
		id := filepath.Base(tempDir) + "~" + name + ":default"
		require.NoError(t, db.UpsertStack(models.Stack{
			ID:          id,
			Directory:   dir,
			ComposeFile: "compose.yaml",
			EnvFile:     "",
			ProjectName: name + "-default",
		}))
		return id, dir
	}

	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})
	r := gin.New()
	group := r.Group("/api/v1/stacks")
	group.Use(envUnlockedMiddleware())
	group.POST("/:id/env", handler.Create)
	return r, mk
}

// TestEnvHandler_Create_MalformedBodyIs400 pins agent-os-40bp.
//
// Create discarded its bind error, so a MALFORMED body bound to the zero value
// and the handler wrote an EMPTY file and answered success. The operator's
// intended initial content was silently dropped. It could never overwrite
// anything — the os.Stat guard answers 409 when the file exists — so this is a
// dropped-input bug, not data loss, which is why the bead's original "the
// existing .env content is unchanged" criterion was unsatisfiable and was
// repaired before this test was written.
//
// The discriminator is io.EOF, and it HAS to be: an absent body and a malformed
// body are indistinguishable by their RESULT, since both leave req at its zero
// value. Arms 3 and 4 are what stop the fix being "tightened" into a check on
// whether content is empty — such an implementation passes arms 1, 2 and 3 and
// fails only arm 4.
func TestEnvHandler_Create_MalformedBodyIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, mk := envCreateFixture(t)

	post := func(name, body string) (*httptest.ResponseRecorder, string) {
		id, dir := mk(name)
		var rdr *strings.Reader
		if body == "" {
			rdr = strings.NewReader("")
		} else {
			rdr = strings.NewReader(body)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks/"+id+"/env", rdr)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w, filepath.Join(dir, ".env")
	}

	// ARM 1 — the defect. A well-formed JSON object whose field is the wrong type.
	t.Run("arm1 wrong-typed field is 400 and writes nothing", func(t *testing.T) {
		w, envPath := post("wrongtype", `{"content": 123}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"a malformed body must not be accepted as an empty one; body=%s", w.Body.String())
		_, statErr := os.Stat(envPath)
		assert.True(t, os.IsNotExist(statErr),
			"nothing must be written on a rejected body; stat err = %v", statErr)
	})

	// ARM 2 — the defect, other shape. Not valid JSON at all.
	t.Run("arm2 invalid json is 400 and writes nothing", func(t *testing.T) {
		w, envPath := post("invalidjson", `{not json`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		_, statErr := os.Stat(envPath)
		assert.True(t, os.IsNotExist(statErr), "stat err = %v", statErr)
	})

	// ARM 3 — PRESERVATION CONTROL. An absent body is intentional and documented:
	// it creates an empty file. This arm cannot fail first; its evidence is the
	// mutation recorded in the bead (reject empty too, and this goes red).
	t.Run("arm3 CONTROL empty body still creates an empty file", func(t *testing.T) {
		w, envPath := post("emptybody", "")
		require.Less(t, w.Code, 300, "an absent body is intentional here; body=%s", w.Body.String())
		content, readErr := os.ReadFile(envPath)
		require.NoError(t, readErr, "the empty-body path must still create the file")
		assert.Empty(t, string(content))
	})

	// ARM 4 — PRESERVATION CONTROL, and the one an over-tightened fix breaks.
	// `{}` and `null` are VALID JSON that bind cleanly with content "". They must
	// be accepted, not rejected. MEASURED: both return err == nil from
	// ShouldBindJSON, so only a fix keyed on the BIND ERROR keeps them working.
	for _, body := range []string{`{}`, `null`} {
		t.Run("arm4 CONTROL valid json "+body+" still creates an empty file", func(t *testing.T) {
			w, envPath := post("validempty"+strings.Map(func(r rune) rune {
				if r == '{' || r == '}' {
					return -1
				}
				return r
			}, body), body)
			require.Less(t, w.Code, 300,
				"%s is valid JSON and binds cleanly; rejecting it would be a regression; body=%s", body, w.Body.String())
			content, readErr := os.ReadFile(envPath)
			require.NoError(t, readErr)
			assert.Empty(t, string(content))
		})
	}

	// ARM 5 — the unchanged arm. An existing file is refused with 409, and that
	// guard runs BEFORE the bind, which is why arms 1 and 2 cannot be about
	// overwriting anything.
	t.Run("arm5 existing file still answers 409 and is not touched", func(t *testing.T) {
		id, dir := mk("exists")
		envPath := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(envPath, []byte("KEEP=me\n"), 0o600))

		req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks/"+id+"/env",
			strings.NewReader(`{"content": 123}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
		content, readErr := os.ReadFile(envPath)
		require.NoError(t, readErr)
		assert.Equal(t, "KEEP=me\n", string(content), "the 409 path must not touch the file")
	})
}
