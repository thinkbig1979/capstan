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

// agent-os-yt2f: a locked env GET masked by key name only, and only rows the
// parser marked as variables. A URL credential under an innocent key, a
// commented-out secret and the body lines of a multi-line secret all reached
// a session that had not re-entered its password.
func TestEnvLocked_Yt2f_HidesURLCommentedAndMultilineSecrets(t *testing.T) {
	const content = "PASSWORD=yt2fbaresecret\n" +
		"DATABASE_URL=postgres://u:yt2furlpw@db.internal/app\n" +
		"#OLD_SECRET=yt2fcommentedsecret\n" +
		"# mirror at https://admin:yt2ffreetextpw@git.example.com/repo\n" +
		"SSH_PRIVATE_KEY=\"-----BEGIN KEY-----\n" +
		"yt2fBodyLineOne\n" +
		"yt2f body line two\n" +
		"-----END KEY-----\"\n" +
		"MOTD=\"hello\n" +
		"yt2fvisibleworld\"\n" +
		"TZ=Europe/Amsterdam\n" +
		"HOMEPAGE=https://example.com/yt2fvisiblepath\n" +
		"# a plain comment yt2fvisiblecomment\n"
	secrets := []string{
		"yt2fbaresecret", "yt2furlpw", "yt2fcommentedsecret", "yt2ffreetextpw",
		"BEGIN KEY", "yt2fBodyLineOne", "yt2f body line two", "END KEY",
	}
	visible := []string{
		"Europe/Amsterdam", "https://example.com/yt2fvisiblepath",
		"yt2fvisibleworld", "yt2fvisiblecomment", "OLD_SECRET", "DATABASE_URL",
	}
	router, stackID, token, _ := envQuoteFixture(t, content)

	// Positive side: unlocked, the fixture really carries every value.
	unlocked := getEnvBody(t, router, stackID, token)
	for _, s := range append(append([]string{}, secrets...), visible...) {
		require.Contains(t, unlocked, s)
	}

	locked := getEnvBody(t, router, stackID, "")
	for _, s := range secrets {
		assertBodyOmits(t, locked, s)
	}
	for _, s := range visible {
		assert.Contains(t, locked, s, "a non-secret must stay visible to a locked session")
	}
	// The masked shapes: a commented assignment keeps its key, a free-text
	// comment keeps everything but the credential.
	assert.Contains(t, locked, `"value":"#OLD_SECRET="`)
	assert.Contains(t, locked, "# mirror at https://***@git.example.com/repo")
}

// A quote that never closes makes compose read to the end of the file, so the
// locked view hides every row after it rather than guess where it ends.
func TestEnvLocked_Yt2f_UnclosedMultilineSecretFailsClosed(t *testing.T) {
	const content = "TZ=Europe/Amsterdam\n" +
		"API_TOKEN='yt2fopening\n" +
		"yt2fleakone\n" +
		"NEXT=yt2fleaktwo\n"
	router, stackID, token, _ := envQuoteFixture(t, content)

	unlocked := getEnvBody(t, router, stackID, token)
	require.Contains(t, unlocked, "yt2fleaktwo")

	locked := getEnvBody(t, router, stackID, "")
	for _, s := range []string{"yt2fopening", "yt2fleakone", "yt2fleaktwo"} {
		assertBodyOmits(t, locked, s)
	}
	assert.Contains(t, locked, "Europe/Amsterdam")
}

// The global env page masks with the same rule as the stack env editor.
func TestGlobalEnvLocked_Yt2f_HidesURLCredential(t *testing.T) {
	handler, _ := newTestSettingsHandler(t)
	require.NoError(t, os.WriteFile(handler.cfg.DataDir+"/global.env",
		[]byte("DATABASE_URL=postgres://u:yt2fglobalpw@db/app\nDB_PASSWORD2=yt2fglobalnum\nTZ=Europe/Amsterdam\n"), 0600))

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
	unlocked := get(token)
	require.Contains(t, unlocked, "yt2fglobalpw")
	require.Contains(t, unlocked, "yt2fglobalnum")

	locked := get("")
	assert.NotContains(t, locked, "yt2fglobalpw")
	assert.NotContains(t, locked, "yt2fglobalnum")
	assert.Contains(t, locked, "Europe/Amsterdam")
}
