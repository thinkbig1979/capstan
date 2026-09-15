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
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// envDiscriminatorFixture builds one router over four stacks that differ only
// in their env-file state, so every arm below runs on the same instrument.
//
// All four in one fixture is the point. No env file, a configured file that
// has vanished, and an existing but empty file are all "there are no entries
// to show" from a careless vantage point, and the only thing separating them
// is which answer the handler mints. Testing any one alone cannot show that.
func envDiscriminatorFixture(t *testing.T) (*gin.Engine, string, string, string, string) {
	t.Helper()

	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mk := func(name, envFile string) string {
		dir := filepath.Join(tempDir, name)
		require.NoError(t, os.MkdirAll(dir, 0755))
		createTestDirectory(t, db, dir)
		id := filepath.Base(tempDir) + "~" + name + ":default"
		require.NoError(t, db.UpsertStack(models.Stack{
			ID:          id,
			Directory:   dir,
			ComposeFile: "compose.yaml",
			EnvFile:     envFile,
			ProjectName: name + "-default",
		}))
		return id
	}

	// EnvFile "" — ordinary configuration, nobody's mistake.
	noEnvID := mk("noenv", "")
	// EnvFile set, nothing on disk — the DB and the filesystem disagree.
	missingID := mk("missingenv", ".env")
	// EnvFile set and present — the happy path.
	presentID := mk("hasenv", ".env")
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "hasenv", ".env"),
		[]byte("PORT=8080\nAPI_KEY=secret\n"),
		0600,
	))
	// EnvFile set and present but EMPTY — a real file with nothing in it. The
	// state that reaches parseEnvFile's zero-iteration path.
	emptyID := mk("emptyenv", ".env")
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "emptyenv", ".env"),
		[]byte(""),
		0600,
	))

	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})

	r := gin.New()
	group := r.Group("/api/v1/stacks")
	group.Use(envUnlockedMiddleware())
	group.GET("/:id/env", handler.Get)

	return r, noEnvID, missingID, presentID, emptyID
}

// getEnvJSON returns the decoded body AND the raw bytes. The raw form is not
// redundant: `"entries": null` and `"entries": []` both decode to something a
// len() check reports as 0, so only the raw text distinguishes them in a
// failure message.
func getEnvJSON(t *testing.T, r *gin.Engine, id string) (int, map[string]any, string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks/"+id+"/env", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	raw := w.Body.String()
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body = %q", raw)
	return w.Code, body, raw
}

// TestEnvHandler_Get_NoEnvFileIs200WithDiscriminator is agent-os-bt5y: a stack
// with no env file is a routine negative state about a resource, so the read
// path answers 200 with an explicit hasEnvFile discriminator rather than a 404
// that paints a red line in the browser console every time the Editor tab is
// opened. Same class as agent-os-4a4a and agent-os-x40a on GET /api/v1/git.
//
// The sibling arms are not decoration. "Missing from disk" is what shows the
// fix did not blanket-200 the endpoint, "present" is what shows it did not
// break the happy path, and "present but empty" pins the `entries` array
// against the nil-slice null that the new wire type forbids; a report carrying
// only the first arm proves nothing about any of them.
func TestEnvHandler_Get_NoEnvFileIs200WithDiscriminator(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r, noEnvID, missingID, presentID, emptyID := envDiscriminatorFixture(t)

	t.Run("no env file configured: 200 with hasEnvFile false", func(t *testing.T) {
		code, body, _ := getEnvJSON(t, r, noEnvID)

		if code != http.StatusOK {
			t.Fatalf("status: want 200, got %d. body = %v", code, body)
		}
		if got, ok := body["hasEnvFile"]; !ok || got != false {
			t.Fatalf("hasEnvFile: want false, got %v (present=%t). body = %v", got, ok, body)
		}
		// Absent rather than zero-valued, for x40a's reason: a `filename: ""`
		// alongside `entries: []` is indistinguishable from an empty file that
		// really exists, which is the conflation this field exists to end.
		if _, ok := body["filename"]; ok {
			t.Fatalf("filename must be absent when there is no env file. body = %v", body)
		}
		if _, ok := body["entries"]; ok {
			t.Fatalf("entries must be absent when there is no env file. body = %v", body)
		}
	})

	t.Run("configured env file missing from disk: still 404", func(t *testing.T) {
		code, body, _ := getEnvJSON(t, r, missingID)

		if code != http.StatusNotFound {
			t.Fatalf("status: want 404, got %d. body = %v", code, body)
		}
		if body["message"] != "Env file not found on disk" {
			t.Fatalf("message: want %q, got %v. body = %v", "Env file not found on disk", body["message"], body)
		}
	})

	t.Run("env file present: 200 with hasEnvFile true and entries intact", func(t *testing.T) {
		code, body, _ := getEnvJSON(t, r, presentID)

		if code != http.StatusOK {
			t.Fatalf("status: want 200, got %d. body = %v", code, body)
		}
		if got, ok := body["hasEnvFile"]; !ok || got != true {
			t.Fatalf("hasEnvFile: want true, got %v (present=%t). body = %v", got, ok, body)
		}
		if body["filename"] != ".env" {
			t.Fatalf("filename: want %q, got %v", ".env", body["filename"])
		}
		entries, ok := body["entries"].([]any)
		if !ok || len(entries) != 2 {
			t.Fatalf("entries: want 2, got %v. body = %v", body["entries"], body)
		}
		first, _ := entries[0].(map[string]any)
		if first["key"] != "PORT" || first["value"] != "8080" {
			t.Fatalf("first entry: want PORT=8080, got %v", first)
		}
		if body["raw"] != "PORT=8080\nAPI_KEY=secret\n" {
			t.Fatalf("raw: want the file contents, got %v", body["raw"])
		}
	})

	// An EXISTING but empty env file is the fourth state, and it is the one the
	// wire type gets wrong if nobody looks: parseEnvFile declares its slice with
	// `var entries []EnvEntry` and appends only inside the scan loop, so zero
	// iterations return nil and encoding/json writes `"entries": null`. The
	// frontend's EnvFilePresent declares `entries: EnvEntry[]`, non-nullable,
	// and EnvEditor maps over it unguarded — so the null is a TypeError.
	//
	// The crash predates agent-os-bt5y; what bt5y added is the FORMAL type
	// asserting the field is never null. The contract has to be made true where
	// it is minted rather than widened to `EnvEntry[] | null`, which would
	// document the crash instead of removing it and put a null back into a
	// union whose whole purpose is that reading the wrong branch is a compile
	// error.
	t.Run("env file present but empty: entries is [] and never null", func(t *testing.T) {
		code, body, raw := getEnvJSON(t, r, emptyID)

		if code != http.StatusOK {
			t.Fatalf("status: want 200, got %d. body = %v", code, body)
		}
		if got, ok := body["hasEnvFile"]; !ok || got != true {
			t.Fatalf("hasEnvFile: want true, got %v (present=%t). body = %v", got, ok, body)
		}
		// The raw check is the load-bearing one. A nil slice and an empty slice
		// both decode to a length of 0, so only the serialised text separates
		// `null` from `[]`, and `null` is the shape that crashes the client.
		if strings.Contains(raw, `"entries":null`) {
			t.Fatalf("entries serialised as null; the wire type says it is always an array. raw = %s", raw)
		}
		entries, ok := body["entries"].([]any)
		if !ok {
			t.Fatalf("entries: want a JSON array, got %v. raw = %s", body["entries"], raw)
		}
		if len(entries) != 0 {
			t.Fatalf("entries: want 0 for an empty file, got %d. raw = %s", len(entries), raw)
		}
	})
}
