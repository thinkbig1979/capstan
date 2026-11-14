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

// agent-os-mwgg: both routes that store a git HTTPS credential refuse a
// control character in the username or token. The credential helper prints
// both in git's line protocol, so a line break added protocol lines (host=)
// to what git read. The refusal names the field and never echoes the value.

type mwggCase struct {
	name, field, user, token string
}

// JSON escapes, so the body carries the raw control characters.
var mwggCases = []mwggCase{
	{"TokenLF", "httpsToken", "mwgguser", `mwggtok\nhost=evil`},
	{"UserCR", "httpsUser", `mwgguser\rhost=evil`, "mwggtok"},
	{"UserNUL", "httpsUser", `mwgguser\u0000evil`, "mwggtok"},
	{"TokenTab", "httpsToken", "mwgguser", `mwgg\ttok`},
	{"TokenDEL", "httpsToken", "mwgguser", `mwgg\u007ftok`},
}

func assertMwggRefusal(t *testing.T, w *httptest.ResponseRecorder, field string) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, models.ErrValidation, resp.Code)
	assert.Contains(t, resp.Message, field, "the message names the field")
	body := w.Body.String()
	for _, leak := range []string{"evil", "mwggtok", "mwgguser", "mwgg"} {
		assert.NotContains(t, body, leak, "the refused value must not be echoed")
	}
}

func TestGitSettings_Mwgg_ControlCharRefusedNothingStored(t *testing.T) {
	for _, tc := range mwggCases {
		t.Run(tc.name, func(t *testing.T) {
			h, router := newTestSettingsHandler(t)
			require.NoError(t, h.db.SetSettings([]database.SettingValue{
				{Key: "git_https_user", Value: "before-user"},
				{Key: "git_https_token", Value: "before-token"},
			}))

			body := `{"httpsUser":"` + tc.user + `","httpsToken":"` + tc.token + `"}`
			req := httptest.NewRequest(http.MethodPut, "/settings/git", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assertMwggRefusal(t, w, tc.field)
			user, err := h.db.GetSetting("git_https_user")
			require.NoError(t, err)
			token, err := h.db.GetSetting("git_https_token")
			require.NoError(t, err)
			assert.Equal(t, "before-user", user, "the stored username is unchanged")
			assert.Equal(t, "before-token", token, "the stored token is unchanged")
		})
	}
}

func TestDirectoryCredentials_Mwgg_ControlCharRefusedNothingStored(t *testing.T) {
	for _, tc := range mwggCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := services.NewTokenEncryptorOrDefault("", "test-secret-key-32-chars-long!!!")
			db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
			require.NoError(t, err)
			t.Cleanup(func() { db.Close() })
			handler := NewDirectoriesHandler(services.NewScannerService(&config.Config{StacksDir: "/tmp/test"}, db), db)
			require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/opt/stacks/app", Name: "app", IsGitRepo: true, ScannedAt: testTime}))

			router := gin.New()
			handler.RegisterRoutes(router.Group("/directories"))
			body := `{"path":"/opt/stacks/app","authType":"https","httpsUser":"` + tc.user + `","httpsToken":"` + tc.token + `"}`
			req := httptest.NewRequest(http.MethodPut, "/directories/credentials", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assertMwggRefusal(t, w, tc.field)
			stored, err := db.GetDirectoryCredentials("/opt/stacks/app")
			require.NoError(t, err)
			assert.Empty(t, stored.GitAuthType, "nothing stored for the directory")
			assert.Empty(t, stored.GitHTTPSUser)
			assert.Empty(t, stored.GitHTTPSToken)
		})
	}
}

// Positive control on the same routes: a clean credential is still stored.
func TestGitCredentials_Mwgg_CleanValuesStillStored(t *testing.T) {
	h, router := newTestSettingsHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/settings/git", strings.NewReader(`{"httpsUser":"mwgg-user","httpsToken":"ghp_mwgg123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	token, err := h.db.GetSetting("git_https_token")
	require.NoError(t, err)
	assert.Equal(t, "ghp_mwgg123", token)
}
