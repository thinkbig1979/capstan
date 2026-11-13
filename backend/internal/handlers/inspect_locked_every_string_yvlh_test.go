package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-yvlh review r2 (N1): locked inspect masked a named list of
// fields, so HostConfig.LogConfig.Config (a splunk-token, a loki-url with
// credentials), Binds, ExtraHosts and the rest were served as Docker sent
// them. Every string under Config and HostConfig now goes through the masker.
func TestInspectLocked_MasksEveryStringInConfigAndHostConfig_yvlh(t *testing.T) {
	body := `{"Id":"c1","Name":"/web","Config":{"Image":"app:1","User":"tok_live_ABCDEF123",` +
		`"Labels":{"com.docker.compose.project":"lmstack"}},` +
		`"HostConfig":{"LogConfig":{"Type":"splunk","Config":{"tag":"tok_live_ABCDEF123","loki-url":"https://u:lokiPass99@loki/api","splunk-format":"json"}},` +
		`"Binds":["/srv/tok_live_ABCDEF123:/data:ro"],"ExtraHosts":["tok_live_ABCDEF123.local:10.0.0.1"]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/containers/c1/json") {
			writeOrFailYvlh(t, w, []byte(body))
			return
		}
		writeOrFailYvlh(t, w, []byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	h := NewResourcesHandler(newTestDockerServiceAgainst(t, srv), stacksRevYvlh(t, 1), nil)
	for _, unlocked := range []bool{false, true} {
		router := gin.New()
		if unlocked {
			router.Use(unlockedSiou)
		}
		router.GET("/resources/containers/:id/inspect", h.inspectContainer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/resources/containers/c1/inspect", nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		got := w.Body.String()
		if unlocked {
			assert.Contains(t, got, tokenRevYvlh, "unlocked is unchanged")
			assert.Contains(t, got, "lokiPass99", "unlocked is unchanged")
			continue
		}
		assert.NotContains(t, got, tokenRevYvlh, "locked: %s", got)
		assert.NotContains(t, got, "lokiPass99", "locked: %s", got)
		assert.Contains(t, got, `"splunk-format": "json"`, "a value holding no secret is kept")
		assert.Contains(t, got, `"Type": "splunk"`)
	}
}
