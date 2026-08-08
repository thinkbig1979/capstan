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

// gateEnvFixture builds a stack with a .env holding one secret and one
// non-secret value, and returns a router carrying the real unlock middleware.
// Requests reaching it are authenticated (userID is set) but carry no unlock
// token unless the caller adds the header — which is exactly the state
// agent-os-7o5s is about.
func gateEnvFixture(t *testing.T) (*gin.Engine, *services.EnvUnlockStore, string) {
	t.Helper()

	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	stackDir := filepath.Join(tempDir, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(stackDir, ".env"),
		[]byte("DB_PASSWORD=hunter2\nTZ=Europe/Amsterdam\n"),
		0600,
	))

	createTestDirectory(t, db, stackDir)

	stackID := filepath.Base(tempDir) + "~stack1:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
	}))

	store := services.NewEnvUnlockStore()
	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})

	router := gin.New()
	group := router.Group("")
	group.Use(authContextMiddleware(gateTestUserID))
	group.Use(middleware.EnvUnlock(store, false))
	group.GET("/stacks/:id/env", handler.Get)
	group.PUT("/stacks/:id/env", handler.Put)

	return router, store, stackID
}

const gateTestUserID = "gate-test-user"

// entryValue returns the value the response reported for key, and whether the
// key was present at all.
func entryValue(t *testing.T, body []byte, key string) (string, bool) {
	t.Helper()
	var resp struct {
		Entries []EnvEntry `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	for _, e := range resp.Entries {
		if e.Key == key {
			return e.Value, true
		}
	}
	return "", false
}

// TestEnvHandler_Get_WithoutUnlockToken_WithholdsSecrets is the negative proof
// required by agent-os-7o5s: an authenticated session that never re-entered its
// password must not receive secret values.
//
// It deliberately asserts on BOTH copies of the plaintext. Before the fix,
// parseEnvFile put the same value into entries[].Value as well as Raw, so a
// gate that only withheld Raw left the secret readable at entries[].value.
func TestEnvHandler_Get_WithoutUnlockToken_WithholdsSecrets(t *testing.T) {
	router, _, stackID := gateEnvFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.Bytes()

	// The secret must appear nowhere in the payload, whatever field it hides in.
	assert.NotContains(t, string(body), "hunter2",
		"an unverified session received the plaintext secret")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))
	_, hasRaw := resp["raw"]
	assert.False(t, hasRaw, "raw env contents were returned without an unlock token")
	assert.Equal(t, true, resp["locked"], "response should mark itself locked")

	// Keys, line numbers and non-secret values still come through, so a locked
	// session can see the shape of the file.
	pw, found := entryValue(t, body, "DB_PASSWORD")
	assert.True(t, found, "the sensitive key itself should still be listed")
	assert.Equal(t, "", pw, "sensitive value should be blanked")

	tz, found := entryValue(t, body, "TZ")
	assert.True(t, found)
	assert.Equal(t, "Europe/Amsterdam", tz, "non-sensitive values should stay visible")
}

// TestEnvHandler_Get_WithUnlockToken_ReturnsSecrets is the control for the test
// above: it proves the redaction is driven by the missing token and not by a
// broken fixture. If this one ever fails alongside the negative test, the
// harness is wrong, not the gate.
func TestEnvHandler_Get_WithUnlockToken_ReturnsSecrets(t *testing.T) {
	router, store, stackID := gateEnvFixture(t)

	token, _, err := store.Mint(gateTestUserID)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.Bytes()

	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Equal(t, "DB_PASSWORD=hunter2\nTZ=Europe/Amsterdam\n", resp["raw"])
	assert.Nil(t, resp["locked"])

	pw, found := entryValue(t, body, "DB_PASSWORD")
	assert.True(t, found)
	assert.Equal(t, "hunter2", pw)
}

// TestEnvHandler_Get_TokenFromAnotherUser_WithholdsSecrets covers replay: a
// token is only good for the account that minted it.
func TestEnvHandler_Get_TokenFromAnotherUser_WithholdsSecrets(t *testing.T) {
	router, store, stackID := gateEnvFixture(t)

	token, _, err := store.Mint("somebody-else")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "hunter2")
}

// TestEnvHandler_Put_WithoutUnlockToken_Forbidden guards the corollary of
// redacting reads: a locked session holds blanked sensitive values, so allowing
// it to save would overwrite every secret in the file with "".
func TestEnvHandler_Put_WithoutUnlockToken_Forbidden(t *testing.T) {
	router, _, stackID := gateEnvFixture(t)

	body := `{"raw":"DB_PASSWORD=\nTZ=Europe/Amsterdam\n"}`
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stackID+"/env", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestEnvHandler_Put_WithUnlockToken_Saves is the control for the PUT gate.
func TestEnvHandler_Put_WithUnlockToken_Saves(t *testing.T) {
	router, store, stackID := gateEnvFixture(t)

	token, _, err := store.Mint(gateTestUserID)
	require.NoError(t, err)

	body := `{"raw":"DB_PASSWORD=changed\nTZ=Europe/Amsterdam\n"}`
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stackID+"/env", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestEnvUnlock_AuthDisabled_OpensGate covers the AUTH_DISABLED install: there
// is no password to re-check, so a second factor cannot exist and the gate must
// not lock an operator out of their own env files.
func TestEnvUnlock_AuthDisabled_OpensGate(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	stackDir := filepath.Join(tempDir, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte("DB_PASSWORD=hunter2\n"), 0600))
	createTestDirectory(t, db, stackDir)

	stackID := filepath.Base(tempDir) + "~stack1:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: stackID, Directory: stackDir, ComposeFile: "compose.yaml",
		EnvFile: ".env", ProjectName: "stack1-default",
	}))

	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})
	router := gin.New()
	group := router.Group("")
	group.Use(authContextMiddleware("anonymous"))
	group.Use(middleware.EnvUnlock(services.NewEnvUnlockStore(), true))
	group.GET("/stacks/:id/env", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "hunter2")
}

// TestEnvUnlock_NilStore_FailsClosed proves the wiring cannot be forgotten
// quietly: a gate with no store behind it redacts rather than waving requests
// through.
func TestEnvUnlock_NilStore_FailsClosed(t *testing.T) {
	router := gin.New()
	router.Use(authContextMiddleware(gateTestUserID))
	router.Use(middleware.EnvUnlock(nil, false))
	router.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"unlocked": envUnlocked(c)})
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set(middleware.EnvUnlockHeader, "anything")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.JSONEq(t, `{"unlocked":false}`, w.Body.String())
}

// TestComposeAndEnv_WithoutUnlockToken_ForbiddenOnlyWhenWritingEnv covers the
// second door onto the same .env file. PUT /:id/compose-env writes env content
// too, so gating EnvHandler.Put alone would have left an open bypass: a locked
// caller could push blanked values through this endpoint instead.
//
// A compose-only save stays ungated — no secret is involved, so demanding a
// password there would be friction for nothing.
func TestComposeAndEnv_WithoutUnlockToken_ForbiddenOnlyWhenWritingEnv(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	stackDir := filepath.Join(tempDir, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte("DB_PASSWORD=hunter2\n"), 0600))
	require.NoError(t, os.WriteFile(
		filepath.Join(stackDir, "compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx\n"),
		0644,
	))
	createTestDirectory(t, db, stackDir)

	stackID := filepath.Base(tempDir) + "~stack1:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: stackID, Directory: stackDir, ComposeFile: "compose.yaml",
		EnvFile: ".env", ProjectName: "stack1-default",
	}))

	handler := NewComposeHandler(services.NewLinterService(), db, &config.Config{StacksDir: tempDir})
	router := gin.New()
	group := router.Group("")
	group.Use(authContextMiddleware(gateTestUserID))
	group.Use(middleware.EnvUnlock(services.NewEnvUnlockStore(), false))
	handler.RegisterRoutes(group)

	// Carrying env content while locked is refused.
	req := httptest.NewRequest(http.MethodPut, "/"+stackID+"/compose-env",
		strings.NewReader(`{"composeContent":"services:\n  web:\n    image: nginx\n","envRaw":"DB_PASSWORD=\n"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"a locked caller must not be able to write env through the compose-env endpoint")

	// The secret is still on disk — the refusal happened before any write.
	//nolint:gosec // G304: stackDir is this test's own t.TempDir(), not request input
	onDisk, readErr := os.ReadFile(filepath.Join(stackDir, ".env"))
	require.NoError(t, readErr)
	assert.Equal(t, "DB_PASSWORD=hunter2\n", string(onDisk))

	// A compose-only save is unaffected.
	req = httptest.NewRequest(http.MethodPut, "/"+stackID+"/compose-env",
		strings.NewReader(`{"composeContent":"services:\n  web:\n    image: nginx:alpine\n"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"a compose-only save touches no secret and must not require an unlock token")
}

// TestVerifyPassword_MintsUsableUnlockToken is the end-to-end proof that the two
// halves are actually connected: the token POST /auth/verify-password returns is
// the same token GET /:id/env accepts. Before agent-os-7o5s this endpoint
// returned a bare {"ok": true} that nothing consumed.
func TestVerifyPassword_MintsUsableUnlockToken(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	user := createTestUser(t, db, "alice", "correct-horse-1")

	stackDir := filepath.Join(tempDir, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte("DB_PASSWORD=hunter2\n"), 0600))
	createTestDirectory(t, db, stackDir)

	stackID := filepath.Base(tempDir) + "~stack1:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: stackID, Directory: stackDir, ComposeFile: "compose.yaml",
		EnvFile: ".env", ProjectName: "stack1-default",
	}))

	store := services.NewEnvUnlockStore()
	authHandler := NewAuthHandler(db, "test-secret-key-32-chars-long!!!", false)
	authHandler.SetEnvUnlockStore(store)
	envHandler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})

	router := gin.New()
	group := router.Group("")
	group.Use(authContextMiddleware(user.ID))
	group.Use(middleware.EnvUnlock(store, false))
	group.POST("/auth/verify-password", authHandler.VerifyPassword)
	group.GET("/stacks/:id/env", envHandler.Get)

	// A wrong password mints nothing.
	req := httptest.NewRequest(http.MethodPost, "/auth/verify-password",
		strings.NewReader(`{"password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NotContains(t, w.Body.String(), "unlockToken")

	// The right password does.
	req = httptest.NewRequest(http.MethodPost, "/auth/verify-password",
		strings.NewReader(`{"password":"correct-horse-1"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var verify struct {
		OK          bool   `json:"ok"`
		UnlockToken string `json:"unlockToken"`
		ExpiresIn   int    `json:"expiresIn"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &verify))
	assert.True(t, verify.OK)
	require.NotEmpty(t, verify.UnlockToken, "verify-password must mint a token")
	assert.Equal(t, int(services.EnvUnlockTTL.Seconds()), verify.ExpiresIn)

	// And that token unlocks the env surface.
	req = httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
	req.Header.Set(middleware.EnvUnlockHeader, verify.UnlockToken)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "hunter2")
}

// TestVerifyPassword_AuthDisabled_MintsToken keeps the AUTH_DISABLED contract
// identical to the normal one: there is no password to re-check, so refusing to
// mint would leave the frontend with no token to send.
func TestVerifyPassword_AuthDisabled_MintsToken(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewAuthHandler(db, "test-secret-key-32-chars-long!!!", true)
	handler.SetEnvUnlockStore(services.NewEnvUnlockStore())

	router := gin.New()
	router.POST("/auth/verify-password", authContextMiddleware("anonymous"), handler.VerifyPassword)

	req := httptest.NewRequest(http.MethodPost, "/auth/verify-password", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "unlockToken")
}

// TestLogout_RevokesUnlockTokens: a live unlock window must not outlive the
// session that opened it.
func TestLogout_RevokesUnlockTokens(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := services.NewEnvUnlockStore()
	handler := NewAuthHandler(db, "test-secret-key-32-chars-long!!!", false)
	handler.SetEnvUnlockStore(store)

	token, _, err := store.Mint("test-user-id")
	require.NoError(t, err)
	require.True(t, store.Valid(token, "test-user-id"))

	router := gin.New()
	router.POST("/auth/logout", authContextMiddleware("test-user-id"), handler.Logout)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	assert.False(t, store.Valid(token, "test-user-id"),
		"logout left a live unlock token behind")
}

// TestSettingsHandler_GetGlobalEnv_WithholdsSecretsWhenLocked covers the second
// secret surface. Gating only the per-stack .env would move the hole here rather
// than close it: global env holds the credentials every stack shares.
func TestSettingsHandler_GetGlobalEnv_WithholdsSecretsWhenLocked(t *testing.T) {
	handler, _ := newTestSettingsHandler(t)
	require.NoError(t, os.WriteFile(
		handler.cfg.DataDir+"/global.env",
		[]byte("SHARED_API_KEY=topsecret\nTZ=Europe/Amsterdam\n"),
		0600,
	))

	store := services.NewEnvUnlockStore()
	router := gin.New()
	group := router.Group("")
	group.Use(authContextMiddleware(gateTestUserID))
	group.Use(middleware.EnvUnlock(store, false))
	group.GET("/settings/global-env", handler.GetGlobalEnv)
	group.PUT("/settings/global-env", handler.UpdateGlobalEnv)

	// Locked: the secret is withheld, the non-secret is not.
	req := httptest.NewRequest(http.MethodGet, "/settings/global-env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "topsecret")
	assert.Contains(t, w.Body.String(), "Europe/Amsterdam")
	assert.Contains(t, w.Body.String(), `"locked":true`)

	// Saving from that state would persist the blanks, so it is refused.
	req = httptest.NewRequest(http.MethodPut, "/settings/global-env",
		strings.NewReader(`{"vars":[{"key":"SHARED_API_KEY","value":""}]}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Control: with a token, both the read and the write go through.
	token, _, err := store.Mint(gateTestUserID)
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/settings/global-env", nil)
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "topsecret")

	req = httptest.NewRequest(http.MethodPut, "/settings/global-env",
		strings.NewReader(`{"vars":[{"key":"SHARED_API_KEY","value":"rotated"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}
