package handlers

import (
	"encoding/json"
	"net/http"
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
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-ly6i: masking stars any text equal to a secret, including text the
// same request sent, so a locked session that sends a list of guesses learns
// which one is the secret from where the "***" lands. Every route that sends
// the caller's own text back through the secret masking is refused while
// locked. The test sends a guess list WITH the secret and one WITHOUT it: the
// two locked responses must be byte-identical.

const secretLy6i = "4821"

// Same length as the list with the secret, so a size field cannot differ.
const (
	guessesWithLy6i    = "x4820x4821x4822"
	guessesWithoutLy6i = "x4820x4999x4822"
)

// composeEchoLy6i is compose content that compose-go rejects with an error
// quoting the guess list, so the lint message echoes the request text.
func composeEchoLy6i(guesses string) string {
	return "services:\n  web:\n    image: nginx:1.27\n    restart: always\n    ports:\n      - \"" + guesses + ":80\"\n"
}

// composeCommentLy6i is valid compose content carrying the guess list in a
// comment: it lints clean and saves, and a later locked GET masks it.
func composeCommentLy6i(guesses string) string {
	return "# " + guesses + "\nservices:\n  web:\n    image: nginx:1.27\n    restart: always\n"
}

// fixtureLy6i is a stack whose .env holds the secret, plus a global.env that
// holds it too (the stack-less lint and create see global.env alone).
type fixtureLy6i struct {
	compose *ComposeHandler
	stacks  *StacksHandler
	id      string
	root    string
}

func newFixtureLy6i(t *testing.T) fixtureLy6i {
	t.Helper()
	root := t.TempDir()
	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	stackDir := filepath.Join(root, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(composeCommentLy6i("nothing-here")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte("DB_PASSWORD="+secretLy6i+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "global.env"), []byte("ADMIN_PASSWORD="+secretLy6i+"\n"), 0o600))
	createTestDirectory(t, db, stackDir)

	id := filepath.Base(root) + "~stack1:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          id,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
	}))

	cfg := &config.Config{StacksDir: root, DataDir: dataDir}
	linter := services.NewLinterService()
	return fixtureLy6i{
		compose: NewComposeHandler(linter, db, cfg),
		stacks:  NewStacksHandler(nil, services.NewScannerService(cfg, db), linter, db, cfg, services.NewActionLogger(db), services.NewOperationLock()),
		id:      id,
		root:    root,
	}
}

// routerLy6i mounts every route under test; unlocked adds the env-unlock
// mark, as middleware.EnvUnlock does.
func (f fixtureLy6i) router(unlocked bool) *gin.Engine {
	r := gin.New()
	var mw []gin.HandlerFunc
	if unlocked {
		mw = append(mw, unlockedSiou)
	}
	with := func(h gin.HandlerFunc) []gin.HandlerFunc { return append(append([]gin.HandlerFunc(nil), mw...), h) }
	r.GET("/stacks/:id/compose", with(f.compose.Get)...)
	r.PUT("/stacks/:id/compose", with(f.compose.Put)...)
	r.POST("/stacks/:id/compose/lint", with(f.compose.Lint)...)
	r.PUT("/stacks/:id/compose-env", with(f.compose.PutComposeAndEnv)...)
	r.POST("/stacks/lint", with(f.stacks.Lint)...)
	r.POST("/compose/lint", with(f.stacks.Lint)...)
	r.POST("/stacks", with(f.stacks.Create)...)
	return r
}

// echoRoutesLy6i are the one-call routes: each sends compose text and answers
// with lint results.
func echoRoutesLy6i(f fixtureLy6i) []struct {
	name, method, path string
	body               func(compose string) any
} {
	return []struct {
		name, method, path string
		body               func(compose string) any
	}{
		{"compose lint", http.MethodPost, "/stacks/" + f.id + "/compose/lint", func(c string) any { return map[string]string{"content": c} }},
		{"compose put", http.MethodPut, "/stacks/" + f.id + "/compose", func(c string) any { return map[string]string{"content": c} }},
		{"compose-env put", http.MethodPut, "/stacks/" + f.id + "/compose-env", func(c string) any { return map[string]string{"composeContent": c} }},
		{"stacks lint", http.MethodPost, "/stacks/lint", func(c string) any { return map[string]string{"compose": c} }},
		{"compose lint (no stack)", http.MethodPost, "/compose/lint", func(c string) any { return map[string]string{"compose": c} }},
		{"stack create", http.MethodPost, "/stacks", func(c string) any {
			return map[string]any{"name": "newstack", "directory": f.root, "composeContent": c}
		}},
	}
}

func TestLockedSession_GuessListEcho_ly6i(t *testing.T) {
	probe := func(t *testing.T, method string, pathFn func(fixtureLy6i) string, bodyFn func(fixtureLy6i, string) any, guesses string) string {
		f := newFixtureLy6i(t) // fresh per call: a save must not change the next answer
		w := doJSONSiou(t, f.router(false), method, pathFn(f), bodyFn(f, guesses))
		return w.Body.String()
	}
	for i, rt := range echoRoutesLy6i(newFixtureLy6i(t)) {
		t.Run(rt.name, func(t *testing.T) {
			pathFn := func(f fixtureLy6i) string { return echoRoutesLy6i(f)[i].path }
			bodyFn := func(f fixtureLy6i, g string) any { return echoRoutesLy6i(f)[i].body(composeEchoLy6i(g)) }
			with := probe(t, rt.method, pathFn, bodyFn, guessesWithLy6i)
			without := probe(t, rt.method, pathFn, bodyFn, guessesWithoutLy6i)
			assert.Equal(t, without, with, "a locked reply must not depend on whether the guesses hold the secret")
			assert.NotContains(t, with, secretLy6i)
		})
	}
}

// The two-call carrier: a locked save of a comment holding the guesses, then
// a locked GET that masks the saved file.
func TestLockedSession_SavedCommentThenGet_ly6i(t *testing.T) {
	getAfterSave := func(guesses string) (int, map[string]any) {
		f := newFixtureLy6i(t)
		r := f.router(false)
		put := doJSONSiou(t, r, http.MethodPut, "/stacks/"+f.id+"/compose", map[string]string{"content": composeCommentLy6i(guesses)})
		get := doJSONSiou(t, r, http.MethodGet, "/stacks/"+f.id+"/compose", nil)
		require.Equal(t, http.StatusOK, get.Code, get.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(get.Body.Bytes(), &body))
		delete(body, "lastModified") // a file time, not a reply to the guesses
		return put.Code, body
	}
	withCode, with := getAfterSave(guessesWithLy6i)
	withoutCode, without := getAfterSave(guessesWithoutLy6i)
	assert.Equal(t, withoutCode, withCode)
	assert.Equal(t, without, with, "a locked GET must not depend on whether a locked save held the secret")
}

// Every refused route answers the same ENV_LOCKED refusal, before reading
// the request's text or the disk.
func TestLockedSession_RefusedWithEnvLocked_ly6i(t *testing.T) {
	f := newFixtureLy6i(t)
	for _, rt := range echoRoutesLy6i(f) {
		t.Run(rt.name, func(t *testing.T) {
			w := doJSONSiou(t, f.router(false), rt.method, rt.path, rt.body(composeEchoLy6i(guessesWithLy6i)))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			var resp models.AppError
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "ENV_LOCKED", resp.Code, "the wire code the frontend keys on")
		})
	}
	_, err := os.Stat(filepath.Join(f.root, "newstack"))
	assert.True(t, os.IsNotExist(err), "a refused create writes nothing")
}

// A locked create used to write the request's .env text with no lock check,
// a second door around the env editor's guard.
func TestLockedSession_CreateWritesNoEnv_ly6i(t *testing.T) {
	f := newFixtureLy6i(t)
	w := doJSONSiou(t, f.router(false), http.MethodPost, "/stacks", map[string]any{
		"name": "envstack", "directory": f.root, "composeContent": composeCommentLy6i("ok"), "envContent": "TOKEN=abc\n",
	})
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	_, err := os.Stat(filepath.Join(f.root, "envstack", ".env"))
	assert.True(t, os.IsNotExist(err), "a locked create must not write a .env")
}

// Unlocked behaviour is unchanged: the lint still runs and a secret the
// content spells out is still starred.
func TestUnlockedSession_LintStillRedacts_ly6i(t *testing.T) {
	f := newFixtureLy6i(t)
	w := doJSONSiou(t, f.router(true), http.MethodPost, "/stacks/"+f.id+"/compose/lint", map[string]string{"content": composeEchoLy6i(guessesWithLy6i)})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "x4820x***x4822", "the unlocked lint still redacts: %s", body)
	assert.False(t, strings.Contains(body, secretLy6i), body)
}

// The guard found a third locked env write: POST /stacks/:id/env created the
// file with the request's text and no lock check. A locked create WITH text is
// refused and writes nothing; an empty create carries no text and still works.
func TestLockedSession_EnvCreateWithContent_ly6i(t *testing.T) {
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mk := func(name string) (string, string) {
		dir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		createTestDirectory(t, db, dir)
		id := filepath.Base(root) + "~" + name + ":default"
		require.NoError(t, db.UpsertStack(models.Stack{ID: id, Directory: dir, ComposeFile: "compose.yaml", ProjectName: name + "-default"}))
		return id, dir
	}
	h := NewEnvHandler(db, &config.Config{StacksDir: root})
	r := gin.New()
	r.POST("/stacks/:id/env", h.Create) // no unlock: locked

	for _, field := range []string{"content", "raw"} {
		id, dir := mk("withtext-" + field)
		w := doJSONSiou(t, r, http.MethodPost, "/stacks/"+id+"/env", map[string]string{field: "FOO=x4820x4821x4822\n"})
		assert.Equal(t, http.StatusForbidden, w.Code, "%s: %s", field, w.Body.String())
		assert.Contains(t, w.Body.String(), `"code":"ENV_LOCKED"`)
		_, err := os.Stat(filepath.Join(dir, ".env"))
		assert.True(t, os.IsNotExist(err), "%s: a refused create writes nothing", field)
	}

	id, dir := mk("empty")
	w := doJSONSiou(t, r, http.MethodPost, "/stacks/"+id+"/env", map[string]string{})
	assert.Less(t, w.Code, 300, "an empty locked create carries no text and still works: %s", w.Body.String())
	_, err = os.Stat(filepath.Join(dir, ".env"))
	assert.NoError(t, err)
}
