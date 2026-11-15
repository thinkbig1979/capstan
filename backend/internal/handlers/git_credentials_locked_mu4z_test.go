package handlers

import (
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

// agent-os-mu4z: a session with env values locked must not change the
// credential git uses to reach the remote, in the global git settings or in
// a folder's own credentials. It is refused with ENV_LOCKED even when the
// value equals the stored one, so the answer says nothing about what is
// stored. The commit author stays editable while locked.

func gitSettingsRouterMu4z(h *SettingsHandler, unlocked bool) *gin.Engine {
	r := gin.New()
	if unlocked {
		r.Use(envUnlockedMiddleware())
	}
	r.PUT("/settings/git", h.UpdateGitSettings)
	return r
}

func putJSONMu4z(r *gin.Engine, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestGitSettings_LockedRefusesCredentials_mu4z(t *testing.T) {
	stored := []database.SettingValue{
		{Key: "git_ssh_key", Value: "/keys/owner"},
		{Key: "git_https_user", Value: "owner"},
		{Key: "git_https_token", Value: "owner-token"},
	}
	for _, tc := range []struct{ field, value, key string }{
		{"sshKey", "/keys/attacker", "git_ssh_key"},
		{"sshKey", "/keys/owner", "git_ssh_key"}, // the same value: still refused
		{"httpsUser", "attacker", "git_https_user"},
		{"httpsToken", "attacker-token", "git_https_token"},
	} {
		t.Run(tc.field+"="+tc.value, func(t *testing.T) {
			h, _ := newTestSettingsHandler(t)
			require.NoError(t, h.db.SetSettings(stored))
			before := map[string]string{}
			for _, s := range stored {
				before[s.Key] = settingOrAbsentH71i(t, h.db, s.Key)
			}
			w := putJSONMu4z(gitSettingsRouterMu4z(h, false), "/settings/git", `{"`+tc.field+`":"`+tc.value+`","authorName":"Someone"}`)
			assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body.String())
			assert.Equal(t, models.ErrEnvLocked, envLockedCodeH71i(t, w))
			assert.Contains(t, w.Body.String(), "Unlock env values to change the git credentials")
			for k, v := range before {
				assert.Equal(t, v, settingOrAbsentH71i(t, h.db, k), "a refused save changes nothing: %s", k)
			}
			assert.Equal(t, "<absent>", settingOrAbsentH71i(t, h.db, models.SettingGitAuthorName), "nothing of a refused save is written")
		})
	}
}

func TestGitSettings_LockedAuthorStillSaves_mu4z(t *testing.T) {
	h, _ := newTestSettingsHandler(t)
	w := putJSONMu4z(gitSettingsRouterMu4z(h, false), "/settings/git", `{"authorName":"Locked Author","authorEmail":"a@example.com"}`)
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	assert.Equal(t, "Locked Author", settingOrAbsentH71i(t, h.db, models.SettingGitAuthorName))
	assert.Equal(t, "a@example.com", settingOrAbsentH71i(t, h.db, models.SettingGitAuthorEmail))
}

func TestGitSettings_UnlockedCredentialsSave_mu4z(t *testing.T) {
	h, _ := newTestSettingsHandler(t)
	w := putJSONMu4z(gitSettingsRouterMu4z(h, true), "/settings/git", `{"sshKey":"/keys/new","httpsUser":"u","httpsToken":"t"}`)
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	assert.Equal(t, "/keys/new", settingOrAbsentH71i(t, h.db, "git_ssh_key"))
	assert.Equal(t, "u", settingOrAbsentH71i(t, h.db, "git_https_user"))
	assert.Equal(t, "t", settingOrAbsentH71i(t, h.db, "git_https_token"))
}

func directoryCredsRouterMu4z(t *testing.T, unlocked bool) (*gin.Engine, *database.DB) {
	t.Helper()
	enc := services.NewTokenEncryptorOrDefault("", "test-secret-key-32-chars-long!!!")
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/opt/stacks/app", Name: "app", IsGitRepo: true, ScannedAt: testTime}))
	h := NewDirectoriesHandler(services.NewScannerService(&config.Config{StacksDir: "/tmp/test"}, db), db)
	r := gin.New()
	if unlocked {
		r.Use(envUnlockedMiddleware())
	}
	h.RegisterRoutes(r.Group("/directories"))
	return r, db
}

func TestDirectoryCredentials_LockedRefused_mu4z(t *testing.T) {
	for _, body := range []string{
		`{"path":"/opt/stacks/app","authType":"https","httpsUser":"attacker","httpsToken":"attacker-token"}`,
		`{"path":"/opt/stacks/app","authType":"ssh","sshKeyPath":"/keys/attacker"}`,
		`{"path":"/opt/stacks/app","authType":"inherit"}`,
	} {
		t.Run(body, func(t *testing.T) {
			r, db := directoryCredsRouterMu4z(t, false)
			w := putJSONMu4z(r, "/directories/credentials", body)
			assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body.String())
			assert.Equal(t, models.ErrEnvLocked, envLockedCodeH71i(t, w))
			stored, err := db.GetDirectoryCredentials("/opt/stacks/app")
			require.NoError(t, err)
			assert.Empty(t, stored.GitAuthType, "nothing stored for the directory")
			assert.Empty(t, stored.GitHTTPSUser)
			assert.Empty(t, stored.GitHTTPSToken)
			assert.Empty(t, stored.GitSSHKeyPath)
		})
	}
}

func TestDirectoryCredentials_UnlockedSaves_mu4z(t *testing.T) {
	r, db := directoryCredsRouterMu4z(t, true)
	w := putJSONMu4z(r, "/directories/credentials", `{"path":"/opt/stacks/app","authType":"https","httpsUser":"u","httpsToken":"t"}`)
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	stored, err := db.GetDirectoryCredentials("/opt/stacks/app")
	require.NoError(t, err)
	assert.Equal(t, "https", stored.GitAuthType)
	assert.Equal(t, "u", stored.GitHTTPSUser)
}
