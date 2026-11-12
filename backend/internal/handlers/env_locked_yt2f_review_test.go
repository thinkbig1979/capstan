package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Review findings on agent-os-yt2f (review-redact.md S1, S2, S3, S7).

// globalEnvBodies returns the global env GET body unlocked and locked for a
// global.env holding content.
func globalEnvBodies(t *testing.T, content string) (unlocked, locked string) {
	t.Helper()
	handler, _ := newTestSettingsHandler(t)
	require.NoError(t, os.WriteFile(handler.cfg.DataDir+"/global.env", []byte(content), 0600))

	store := services.NewEnvUnlockStore()
	router := gin.New()
	group := router.Group("")
	group.Use(authContextMiddleware(gateTestUserID))
	group.Use(middleware.EnvUnlock(store, false))
	group.GET("/settings/global-env", handler.GetGlobalEnv)

	get := func(token string) string {
		req := httptest.NewRequest(http.MethodGet, "/settings/global-env", nil)
		if token != "" {
			req.Header.Set(middleware.EnvUnlockHeader, token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}
	token, _, err := store.Mint(gateTestUserID)
	require.NoError(t, err)
	return get(token), get("")
}

// S2: the global env parser splits every line holding "=" at its first "=",
// so a multi-line secret's body lines with base64 padding came back as rows
// whose KEY is the secret text, and "KEY: a=b" as a key holding the value.
// Only values were masked.
func TestGlobalEnvLocked_Yt2f_MultilineBodyAndColonKeyHidden(t *testing.T) {
	const content = "TLS_PRIVATE_KEY=\"-----BEGIN KEY-----\n" +
		"AAAAyt2fglobalbodyBB==\n" +
		"yt2fglobalbodytwo=x\n" +
		"-----END KEY-----\"\n" +
		"DB_PASSWORD: yt2fcolonsecret=def\n" +
		"TZ=Europe/Amsterdam\n"
	secrets := []string{"yt2fglobalbody", "yt2fglobalbodytwo", "yt2fcolonsecret"}

	unlocked, locked := globalEnvBodies(t, content)
	for _, s := range secrets {
		require.Contains(t, unlocked, s)
	}
	for _, s := range secrets {
		assert.NotContains(t, locked, s)
	}
	assert.Contains(t, locked, "Europe/Amsterdam", "a variable after the closed secret stays visible")
	assert.Contains(t, locked, `"key":"TLS_PRIVATE_KEY"`)
}

// S1: a multi-line secret commented out line by line. Only the opener was a
// commented assignment; the body comment rows were shown verbatim.
func TestEnvLocked_Yt2f_CommentedMultilineSecretHidden(t *testing.T) {
	const content = "#OLD_PRIVATE_KEY=\"-----BEGIN PRIVATE KEY-----\n" +
		"#MIIEvQyt2fcommentbody\n" +
		"#-----END PRIVATE KEY-----\"\n" +
		"#OLD_TOKEN='yt2fopenercomment\n" +
		"yt2funcommentedbody\n" +
		"end'\n" +
		"TZ=Europe/Amsterdam\n" +
		"# a plain comment yt2fstillvisible\n"
	secrets := []string{"yt2fcommentbody", "END PRIVATE KEY", "yt2fopenercomment", "yt2funcommentedbody"}
	router, stackID, token, _ := envQuoteFixture(t, content)

	unlocked := getEnvBody(t, router, stackID, token)
	for _, s := range secrets {
		require.Contains(t, unlocked, s)
	}
	locked := getEnvBody(t, router, stackID, "")
	for _, s := range secrets {
		assertBodyOmits(t, locked, s)
	}
	assert.Contains(t, locked, `"value":"#OLD_PRIVATE_KEY="`)
	assert.Contains(t, locked, "Europe/Amsterdam")
	assert.Contains(t, locked, "yt2fstillvisible")
}

// S3: a credential in an opaque URL (no "://"), which RedactURLUserinfo
// handles, was not seen by the token scan in front of it.
func TestEnvLocked_Yt2f_OpaqueURLCredentialHidden(t *testing.T) {
	const content = "RESTIC_REPOSITORY=sftp:u:yt2fopaquepw@backup.example.com:/r\nTZ=Europe/Amsterdam\n"
	router, stackID, token, _ := envQuoteFixture(t, content)

	require.Contains(t, getEnvBody(t, router, stackID, token), "yt2fopaquepw")
	locked := getEnvBody(t, router, stackID, "")
	assertBodyOmits(t, locked, "yt2fopaquepw")
	assert.Contains(t, locked, "Europe/Amsterdam")
}

// S7: without the raw text the body rows cannot be located, so every row
// after a sensitive one is hidden rather than none.
func TestEnvLocked_Yt2f_BodiesWithoutRawFailClosed(t *testing.T) {
	entries := (&EnvHandler{}).parseEnvFile("API_TOKEN=\"yt2fopen\nyt2fbodynoraw\n\"\n")
	resp := EnvResponse{Entries: entries}
	redactEnvResponse(&resp)
	for _, e := range resp.Entries {
		assert.NotContains(t, e.Key+e.Value, "yt2fbodynoraw")
	}
}

// S2's fail-closed arm: rows that do not match the file text (it changed
// between the two reads, or the second read failed) are all blanked.
func TestGlobalEnvLocked_Yt2f_UnmatchedRowsFailClosed(t *testing.T) {
	for _, raw := range []string{"", "OTHER=1\nTZ=Europe/Amsterdam\n"} {
		vars := []map[string]string{
			{"key": "yt2fkeyone", "value": "yt2fvalueone"},
			{"key": "TZ", "value": "Europe/Amsterdam"},
		}
		redactGlobalEnvVars(vars, raw)
		for _, v := range vars {
			assert.Equal(t, map[string]string{"key": "", "value": ""}, v, "raw %q", raw)
		}
	}
	// The other side: rows that match keep what is not secret.
	vars := []map[string]string{{"key": "TZ", "value": "Europe/Amsterdam"}}
	redactGlobalEnvVars(vars, "TZ=Europe/Amsterdam\n")
	assert.Equal(t, "Europe/Amsterdam", vars[0]["value"])
}
