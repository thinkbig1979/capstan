//go:build integration

package integrationtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/handlers"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// authCtx is a simple Gin middleware that sets the "userID" context key so that
// handler calls to c.Get("userID") don't panic.
func authCtx(userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("userID", userID)
		c.Next()
	}
}

// setupEnvHandlerRouter creates a minimal Gin router wired to the EnvHandler
// with all necessary DB and config scaffolding.
func setupEnvHandlerRouter(t *testing.T, stacksDir string, db *database.DB) (*gin.Engine, *handlers.EnvHandler) {
	t.Helper()
	cfg := &config.Config{StacksDir: stacksDir}
	h := handlers.NewEnvHandler(db, cfg)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/stacks")
	// Register with auth middleware so userID is available.
	group.Use(authCtx("test-user"))
	h.RegisterRoutes(group)
	return router, h
}

func setupComposeHandlerRouter(t *testing.T, stacksDir string, db *database.DB) (*gin.Engine, *handlers.ComposeHandler) {
	t.Helper()
	cfg := &config.Config{StacksDir: stacksDir}
	h := handlers.NewComposeHandler(services.NewLinterService(), db, cfg)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/stacks")
	group.Use(authCtx("test-user"))
	h.RegisterRoutes(group)
	return router, h
}

// insertTestStack inserts both the directory record (required by FK) and the stack row.
func insertTestStack(t *testing.T, db *database.DB, stackDir, stacksDir, envFile string) models.Stack {
	t.Helper()

	dir := models.Directory{
		Path:      stackDir,
		Name:      filepath.Base(stackDir),
		IsGitRepo: false,
		ScannedAt: time.Now(),
	}
	if err := db.UpsertDirectory(dir); err != nil {
		t.Fatalf("UpsertDirectory: %v", err)
	}

	stack := models.Stack{
		ID:          filepath.Base(stacksDir) + "~" + filepath.Base(stackDir) + ":default",
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     envFile,
		ProjectName: filepath.Base(stackDir) + "-default",
		Status:      "stopped",
	}
	if err := db.UpsertStack(stack); err != nil {
		t.Fatalf("UpsertStack: %v", err)
	}
	return stack
}

// ── Finding #15: env serialisation validates empty keys ─────────────────────

func TestEnv_Put_EmptyKey_Rejected(t *testing.T) {
	// An entry with empty key + non-empty value must be rejected, NOT silently
	// written as "=value" with Saved:true (finding #15).
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "myapp")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envPath := filepath.Join(stackDir, ".env")
	if err := os.WriteFile(envPath, []byte("GOOD=ok\n"), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupEnvHandlerRouter(t, stacksDir, db)

	// Submit entries where one entry has an empty key but a non-empty value.
	reqBody := map[string]interface{}{
		"entries": []map[string]interface{}{
			{"key": "GOOD", "value": "ok", "line": 1, "comment": false},
			{"key": "", "value": "bad-value", "line": 2, "comment": false},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Must NOT be 200 success — should be 500 (failed) due to validation error.
	if w.Code == http.StatusOK {
		// Check if the response says "success" — that would be the bug.
		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["outcome"] == "success" {
			t.Errorf("FAIL: empty-key entry was silently accepted as success (finding #15 not fixed)\nbody: %s", w.Body.String())
		}
	}

	// Verify the file on disk was not silently corrupted.
	persisted, _ := os.ReadFile(envPath)
	if bytes.Contains(persisted, []byte("=bad-value")) {
		t.Errorf("FAIL: corrupt '=bad-value' line written to disk (finding #15)\ncontent: %s", string(persisted))
	}

	t.Logf("response status=%d body=%s", w.Code, w.Body.String())
}

func TestEnv_Put_RoundTrip(t *testing.T) {
	// A normal save with valid entries round-trips correctly.
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "rt")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envPath := filepath.Join(stackDir, ".env")
	if err := os.WriteFile(envPath, []byte(""), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupEnvHandlerRouter(t, stacksDir, db)

	intended := []map[string]interface{}{
		{"key": "FOO", "value": "bar", "line": 1, "comment": false},
		{"key": "BAZ", "value": "qux", "line": 2, "comment": false},
	}
	reqBody := map[string]interface{}{"entries": intended}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["outcome"] != string(truth.OutcomeSuccess) {
		t.Errorf("expected outcome=success, got %v", resp["outcome"])
	}

	// Verify the file was persisted and parses back to the intended entries.
	persisted, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(persisted, []byte("FOO=bar")) {
		t.Errorf("expected FOO=bar in persisted file, got: %s", string(persisted))
	}
	if !bytes.Contains(persisted, []byte("BAZ=qux")) {
		t.Errorf("expected BAZ=qux in persisted file, got: %s", string(persisted))
	}
}

func TestEnv_Put_NewlineInKey_Rejected(t *testing.T) {
	// An entry whose key contains a literal newline must be rejected, not
	// silently split across lines on serialisation (finding B4).
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "newlinekey")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envPath := filepath.Join(stackDir, ".env")
	if err := os.WriteFile(envPath, []byte("GOOD=ok\n"), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupEnvHandlerRouter(t, stacksDir, db)

	reqBody := map[string]interface{}{
		"entries": []map[string]interface{}{
			{"key": "GOOD", "value": "ok", "line": 1, "comment": false},
			{"key": "BAD\nKEY", "value": "bad-value", "line": 2, "comment": false},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["outcome"] == "success" {
			t.Errorf("FAIL: entry with newline in key was silently accepted as success\nbody: %s", w.Body.String())
		}
	}

	// Verify the file on disk was not corrupted with a split line.
	persisted, _ := os.ReadFile(envPath)
	if bytes.Contains(persisted, []byte("BAD\nKEY")) || bytes.Contains(persisted, []byte("KEY=bad-value")) {
		t.Errorf("FAIL: corrupt split key line written to disk\ncontent: %s", string(persisted))
	}
}

func TestEnv_Put_CarriageReturnInValue_Rejected(t *testing.T) {
	// An entry whose value contains a literal carriage return must be
	// rejected, not silently written and corrupting the file (finding B4).
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "crvalue")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envPath := filepath.Join(stackDir, ".env")
	if err := os.WriteFile(envPath, []byte("GOOD=ok\n"), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupEnvHandlerRouter(t, stacksDir, db)

	reqBody := map[string]interface{}{
		"entries": []map[string]interface{}{
			{"key": "GOOD", "value": "ok", "line": 1, "comment": false},
			{"key": "BAD", "value": "bad\rvalue", "line": 2, "comment": false},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["outcome"] == "success" {
			t.Errorf("FAIL: entry with carriage return in value was silently accepted as success\nbody: %s", w.Body.String())
		}
	}

	// Verify the file on disk was not corrupted.
	persisted, _ := os.ReadFile(envPath)
	if bytes.Contains(persisted, []byte("bad\rvalue")) {
		t.Errorf("FAIL: corrupt carriage-return value written to disk\ncontent: %s", string(persisted))
	}
}

// ── agent-os-gfd: Put must write env files at 0600, not 0644 ───────────────

func TestEnv_Put_SetsFileMode0600(t *testing.T) {
	// Env files may contain secrets; Put must persist at 0600 regardless of
	// the pre-existing file's mode (os.WriteFile alone would not tighten the
	// mode of a file that already exists).
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "permstest")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envPath := filepath.Join(stackDir, ".env")
	// Pre-existing file at the looser 0644 mode, as if written by older code.
	if err := os.WriteFile(envPath, []byte("OLD=value\n"), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupEnvHandlerRouter(t, stacksDir, db)

	reqBody := map[string]interface{}{
		"entries": []map[string]interface{}{
			{"key": "NEW", "value": "value", "line": 1, "comment": false},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("expected env file mode 0600 after Put, got %o", got)
	}
}

// ── Finding #16: create env endpoint ────────────────────────────────────────

func TestEnv_Create_WhenAbsent(t *testing.T) {
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "newstack")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No .env file yet.

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	// Stack has no EnvFile set.
	stack := insertTestStack(t, db, stackDir, stacksDir, "")
	router, _ := setupEnvHandlerRouter(t, stacksDir, db)

	reqBody := map[string]interface{}{"content": "MY_VAR=hello\n"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/stacks/"+stack.ID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["outcome"] != string(truth.OutcomeSuccess) {
		t.Errorf("expected outcome=success, got %v", resp["outcome"])
	}

	// Verify file exists on disk.
	envPath := filepath.Join(stackDir, ".env")
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("expected .env to exist on disk after create: %v", err)
	}
	content, _ := os.ReadFile(envPath)
	if !bytes.Contains(content, []byte("MY_VAR=hello")) {
		t.Errorf("expected content 'MY_VAR=hello' in .env, got: %s", string(content))
	}
}

func TestEnv_Create_WhenPresent_Returns409(t *testing.T) {
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "existing")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	envPath := filepath.Join(stackDir, ".env")
	if err := os.WriteFile(envPath, []byte("EXISTING=yes\n"), 0600); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupEnvHandlerRouter(t, stacksDir, db)

	req := httptest.NewRequest(http.MethodPost, "/stacks/"+stack.ID+"/env", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["outcome"] != string(truth.OutcomeNoChange) {
		t.Errorf("expected outcome=no_change, got %v", resp["outcome"])
	}

	// Original file must be untouched.
	content, _ := os.ReadFile(envPath)
	if !bytes.Contains(content, []byte("EXISTING=yes")) {
		t.Errorf("existing file was modified: %s", string(content))
	}
}

// ── Finding #11: atomic compose+env endpoint ─────────────────────────────────

func TestComposeEnv_Atomic_Success(t *testing.T) {
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "atomicok")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Seed both files.
	composeOrig := "services:\n  web:\n    image: alpine:3.21\n"
	envOrig := "PORT=8080\n"
	writeFileRaw(t, filepath.Join(stackDir, "compose.yaml"), composeOrig)
	writeFileRaw(t, filepath.Join(stackDir, ".env"), envOrig)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupComposeHandlerRouter(t, stacksDir, db)

	composeNew := "services:\n  web:\n    image: alpine:3.21\n  # updated\n"
	envNew := "PORT=9090\n"

	reqBody := map[string]interface{}{
		"composeContent": composeNew,
		"envRaw":         envNew,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/compose-env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["outcome"] != string(truth.OutcomeSuccess) {
		t.Errorf("expected outcome=success, got %v: %s", resp["outcome"], w.Body.String())
	}

	composePersisted, _ := os.ReadFile(filepath.Join(stackDir, "compose.yaml"))
	if string(composePersisted) != composeNew {
		t.Errorf("compose not updated: got %q", string(composePersisted))
	}
	envPersisted, _ := os.ReadFile(filepath.Join(stackDir, ".env"))
	if string(envPersisted) != envNew {
		t.Errorf("env not updated: got %q", string(envPersisted))
	}
}

func TestComposeEnv_Atomic_EnvValidationFails_ComposeUnchanged(t *testing.T) {
	// When env entries fail validation, compose must NOT be written (finding #11).
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "atomicfail")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	composeOrig := "services:\n  web:\n    image: alpine:3.21\n"
	envOrig := "GOOD=yes\n"
	writeFileRaw(t, filepath.Join(stackDir, "compose.yaml"), composeOrig)
	writeFileRaw(t, filepath.Join(stackDir, ".env"), envOrig)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupComposeHandlerRouter(t, stacksDir, db)

	// Bad env: entry with empty key + non-empty value.
	reqBody := map[string]interface{}{
		"composeContent": "services:\n  web:\n    image: alpine:3.21\n  # new\n",
		"envEntries": []map[string]interface{}{
			{"key": "", "value": "corrupt", "line": 1, "comment": false},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/compose-env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should fail — not 200 success.
	if w.Code == http.StatusOK {
		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["outcome"] == "success" {
			t.Errorf("FAIL: atomic endpoint reported success with bad env entries (finding #11)")
		}
	}

	// Compose must be unchanged.
	composePersisted, _ := os.ReadFile(filepath.Join(stackDir, "compose.yaml"))
	if string(composePersisted) != composeOrig {
		t.Errorf("compose was modified when env validation failed (atomicity broken): got %q want %q",
			string(composePersisted), composeOrig)
	}
}

// TestComposeEnv_Atomic_EnvRestoredOnComposeWriteFailure asserts that when the
// compose write fails after .env has already been atomically replaced, the
// original .env content is restored (should-fix for the rollback data-loss bug).
//
// We induce the compose write failure by making the stack directory read-only
// after the .env has been written, so os.WriteFile(composePath) returns EACCES.
// Root is detected and the test is skipped (root bypasses permission checks).
func TestComposeEnv_Atomic_EnvRestoredOnComposeWriteFailure(t *testing.T) {
	// Skip if running as root — chmod has no effect on root writes.
	if os.Getuid() == 0 {
		t.Skip("running as root: cannot test write-permission failures")
	}

	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "rollback")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	composeOrig := "services:\n  web:\n    image: alpine:3.21\n"
	envOrig := "ORIGINAL=yes\nPORT=8080\n"
	composePath := filepath.Join(stackDir, "compose.yaml")
	envPath := filepath.Join(stackDir, ".env")
	writeFileRaw(t, composePath, composeOrig)
	writeFileRaw(t, envPath, envOrig)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	stack := insertTestStack(t, db, stackDir, stacksDir, ".env")
	router, _ := setupComposeHandlerRouter(t, stacksDir, db)

	// Make the compose file read-only so the write fails.
	// We chmod only the compose file (not the directory) because the .env rename
	// happens in the directory — if the directory is read-only, the rename also
	// fails, which is a different code path. We want to fail specifically on the
	// compose write that happens after the .env rename succeeds.
	if err := os.Chmod(composePath, 0444); err != nil {
		t.Fatalf("chmod compose read-only: %v", err)
	}
	// Restore compose permissions on cleanup so t.TempDir() can clean up.
	t.Cleanup(func() { os.Chmod(composePath, 0644) })

	reqBody := map[string]interface{}{
		"composeContent": "services:\n  web:\n    image: alpine:3.21\n  # new\n",
		"envRaw":         "UPDATED=yes\nPORT=9090\n",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/compose-env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// The request must fail (compose write error).
	if w.Code == http.StatusOK {
		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["outcome"] == "success" {
			t.Errorf("FAIL: reported success when compose write should have failed (permissions test setup issue?)\nbody: %s", w.Body.String())
		}
	}

	// THE CRITICAL ASSERTION: .env must be restored to original content.
	// Before the fix, this would contain "UPDATED=yes\nPORT=9090\n" (new content),
	// or the file would be absent — both are data loss.
	envAfter, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("FAIL (.env rollback): .env does not exist after failed compose write — file was destroyed during rollback: %v", err)
	}
	if string(envAfter) != envOrig {
		t.Errorf("FAIL (.env rollback): .env was not restored to original content after compose write failure\nwant: %q\ngot:  %q",
			envOrig, string(envAfter))
	}
}

func writeFileRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}
