package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// credentialStatusTestKeyOne and credentialStatusTestKeyTwo mirror the
// rotated-STORAGE_KEY fixture in services/git_credentials_decrypt_test.go:
// a directory credential is written under key one, then the same on-disk
// database is reopened under key two so the stored ciphertext no longer
// decrypts — a genuine "unreadable" state, not a simulated one.
const (
	credentialStatusTestKeyOne = "credstatus-key-one-0123456789ab"
	credentialStatusTestKeyTwo = "credstatus-key-two-fedcba987654"
)

func TestDirectoriesHandler_List_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	dir := models.Directory{
		Path:      "/tmp/test/stack1",
		Name:      "stack1",
		IsGitRepo: false,
		ScannedAt: testTime,
	}
	err = db.UpsertDirectory(dir)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/directories", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/directories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	directories := response["directories"].([]interface{})
	assert.Len(t, directories, 1)
}

func TestDirectoriesHandler_Scan_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	router := gin.New()
	router.POST("/directories/scan", handler.Scan)

	req := httptest.NewRequest(http.MethodPost, "/directories/scan", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotNil(t, response["directories"])
	assert.NotNil(t, response["hasGlobalEnv"])
	assert.NotNil(t, response["scannedAt"])
}

// TestDirectoriesHandler_UpdateCredentials_Success exercises the full route
// table via RegisterRoutes (not a hand-picked single route like the tests
// above) with an absolute, slash-containing directory path — the shape every
// real deployment uses. Bug agent-os-p7r: the frontend previously tried to
// PUT /directories/:path/credentials, which 404s for any path with slashes
// because gin's decoded-path matching can't route a "/" through a single
// wildcard segment. The fix keeps the path out of the URL entirely: it goes
// in the JSON body of the existing static PUT /directories/credentials route.
func TestDirectoriesHandler_UpdateCredentials_Success(t *testing.T) {
	enc := services.NewTokenEncryptorOrDefault("", "test-secret-key-32-chars-long!!!")
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	dir := models.Directory{
		Path:      "/opt/stacks/app",
		Name:      "app",
		IsGitRepo: true,
		ScannedAt: testTime,
	}
	err = db.UpsertDirectory(dir)
	require.NoError(t, err)

	router := gin.New()
	group := router.Group("/directories")
	handler.RegisterRoutes(group)

	body := `{"path":"/opt/stacks/app","authType":"https","httpsUser":"git","httpsToken":"ghp_secret"}`
	req := httptest.NewRequest(http.MethodPut, "/directories/credentials", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	updatedDir, ok := response["directory"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "https", updatedDir["gitAuthType"])
	assert.Equal(t, "git", updatedDir["gitHttpsUser"])

	stored, err := db.GetDirectory("/opt/stacks/app")
	require.NoError(t, err)
	assert.Equal(t, "https", stored.GitAuthType)
	assert.True(t, stored.HasHTTPSToken)
}

// TestDirectoriesHandler_CredentialStatus_None covers a directory that was
// scanned but never had a credential configured (authType ""). It also
// exercises the route through RegisterRoutes, with an absolute,
// slash-containing path — the p7r/8a5 test convention — and the query
// parameter shape (not a URL segment) required by finding B.
func TestDirectoriesHandler_CredentialStatus_None(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	dir := models.Directory{
		Path:      "/opt/stacks/app",
		Name:      "app",
		IsGitRepo: true,
		ScannedAt: testTime,
	}
	require.NoError(t, db.UpsertDirectory(dir))

	router := gin.New()
	group := router.Group("/directories")
	handler.RegisterRoutes(group)

	req := httptest.NewRequest(http.MethodGet, "/directories/credential-status?path=/opt/stacks/app", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "none", response["status"])
}

// TestDirectoriesHandler_CredentialStatus_OK covers a directory with a
// working, decryptable https credential.
func TestDirectoriesHandler_CredentialStatus_OK(t *testing.T) {
	enc := services.NewTokenEncryptorOrDefault("", "test-secret-key-32-chars-long!!!")
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	dir := models.Directory{
		Path:      "/opt/stacks/app",
		Name:      "app",
		IsGitRepo: true,
		ScannedAt: testTime,
	}
	require.NoError(t, db.UpsertDirectory(dir))
	require.NoError(t, db.UpdateDirectoryCredentials(dir.Path, "https", "", "git", "ghp_secret"))

	router := gin.New()
	group := router.Group("/directories")
	handler.RegisterRoutes(group)

	req := httptest.NewRequest(http.MethodGet, "/directories/credential-status?path=/opt/stacks/app", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "ok", response["status"])
	assert.NotContains(t, w.Body.String(), "ghp_secret", "the token must never appear in the probe response")
}

// TestDirectoriesHandler_CredentialStatus_Empty covers the fourth state
// (finding H): authType "https" with no token ever saved. Documented at
// git_credentials.go's httpsCredentials as otherwise indistinguishable from
// "everything is fine" until a remote git operation fails.
func TestDirectoriesHandler_CredentialStatus_Empty(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	dir := models.Directory{
		Path:      "/opt/stacks/app",
		Name:      "app",
		IsGitRepo: true,
		ScannedAt: testTime,
	}
	require.NoError(t, db.UpsertDirectory(dir))
	require.NoError(t, db.UpdateDirectoryCredentials(dir.Path, "https", "", "git", ""))

	router := gin.New()
	group := router.Group("/directories")
	handler.RegisterRoutes(group)

	req := httptest.NewRequest(http.MethodGet, "/directories/credential-status?path=/opt/stacks/app", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "empty", response["status"])
}

// TestDirectoriesHandler_CredentialStatus_Unreadable is the regression test
// for agent-os-8a5: a directory whose stored credential can no longer be
// decrypted (rotated STORAGE_KEY) must be distinguishable in the API
// response from "none" and "ok", without ListDirectories/GetDirectory
// gaining a decrypt step. Uses the same rotated-key fixture pattern as
// services/git_credentials_decrypt_test.go so the failure is genuine, not
// simulated.
func TestDirectoriesHandler_CredentialStatus_Unreadable(t *testing.T) {
	dataDir := t.TempDir()
	const dirPath = "/opt/stacks/rotated-key"

	db1, err := database.NewWithMigrationsAndEncryptor(dataDir, services.NewTokenEncryptorOrDefault(credentialStatusTestKeyOne, ""))
	require.NoError(t, err)
	require.NoError(t, db1.UpsertDirectory(models.Directory{
		Path: dirPath, Name: "rotated-key", RootDir: "/opt/stacks", ScannedAt: testTime,
	}))
	require.NoError(t, db1.UpdateDirectoryCredentials(dirPath, "https", "", "git", "ghp_secret"))
	require.NoError(t, db1.Close())

	db2, err := database.NewWithMigrationsAndEncryptor(dataDir, services.NewTokenEncryptorOrDefault(credentialStatusTestKeyTwo, ""))
	require.NoError(t, err)
	t.Cleanup(func() { db2.Close() })

	// Guard the fixture itself: without a genuine decrypt failure this test
	// would pass for the wrong reason.
	if _, err := db2.GetDirectoryCredentials(dirPath); err == nil {
		t.Fatal("fixture is not discriminating: the credential still decrypts under the rotated key")
	}

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db2)
	handler := NewDirectoriesHandler(scanner, db2)

	router := gin.New()
	group := router.Group("/directories")
	handler.RegisterRoutes(group)

	req := httptest.NewRequest(http.MethodGet, "/directories/credential-status?path="+dirPath, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "unreadable", response["status"])
	assert.NotContains(t, w.Body.String(), "ghp_secret", "the token/ciphertext must never appear in the probe response")
}
