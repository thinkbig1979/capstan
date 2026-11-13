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
)

// agent-os-yvlh: GET /resources/containers/:id/inspect returned Config.Env,
// every value the container started with, to a session that has not
// unlocked env values. Locked: sensitive values are masked, an entry with no
// "=" is masked whole. Unlocked: the body is what Docker sent.

const inspectSecretYvlh = "yvlh-db-secret-value"

func fakeInspectDockerYvlh(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/containers/c1/json") {
			writeOrFailYvlh(t, w, []byte(`{"Id":"c1","Name":"/db","Config":{"Image":"postgres:16","Env":[`+
				`"DB_PASSWORD=`+inspectSecretYvlh+`",`+
				`"DATABASE_URL=postgres://u:`+inspectSecretYvlh+`@db/app",`+
				`"TZ=Europe/Amsterdam",`+
				`"BROKEN_ENTRY_`+inspectSecretYvlh+`"]}}`))
			return
		}
		writeOrFailYvlh(t, w, []byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeOrFailYvlh(t *testing.T, w http.ResponseWriter, body []byte) {
	if _, err := w.Write(body); err != nil {
		t.Errorf("fake docker write: %v", err)
	}
}

func inspectYvlh(t *testing.T, unlocked bool) (int, string) {
	t.Helper()
	h := NewResourcesHandler(newTestDockerServiceAgainst(t, fakeInspectDockerYvlh(t)), nil, nil)
	router := gin.New()
	if unlocked {
		router.Use(unlockedSiou)
	}
	router.GET("/resources/containers/:id/inspect", h.inspectContainer)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/resources/containers/c1/inspect", nil))
	return w.Code, w.Body.String()
}

func TestInspectLocked_yvlh(t *testing.T) {
	code, body := inspectYvlh(t, false)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, inspectSecretYvlh, "locked")

	var got struct {
		Config struct {
			Env   []string `json:"Env"`
			Image string   `json:"Image"`
		} `json:"Config"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Equal(t, []string{"DB_PASSWORD=***", "DATABASE_URL=***", "TZ=Europe/Amsterdam", "***"}, got.Config.Env)
	assert.Equal(t, "postgres:16", got.Config.Image, "the rest of the inspect is unchanged")
}

func TestInspectUnlocked_yvlh(t *testing.T) {
	code, body := inspectYvlh(t, true)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "DB_PASSWORD="+inspectSecretYvlh)
	assert.Contains(t, body, "BROKEN_ENTRY_"+inspectSecretYvlh)
}
