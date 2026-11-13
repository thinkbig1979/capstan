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
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-yvlh review (sec D3): inspect masked only Config.Env, and only by
// key name. The same value under a plain name (CONN=<API_TOKEN's value>), a
// flag in Cmd/Args, a label and a healthcheck all showed it. Locked, every one
// of them is now masked by the stack's secret values and by the masker's
// name and flag passes; the stack is found from the compose project label.

const (
	tokenRevYvlh = "tok_live_ABCDEF123"
	flagRevYvlh  = "dbpw-S3-value"
	nextRevYvlh  = "hunter2XYZ"
)

func inspectBodyRevYvlh(label string) string {
	labels := `"x":"` + tokenRevYvlh + `","app.tier":"web"`
	if label != "" {
		labels = `"com.docker.compose.project":"` + label + `",` + labels
	}
	return `{"Id":"c1","Name":"/web","Path":"app","Args":["--db-password=` + flagRevYvlh + `","--token","` + nextRevYvlh + `"],` +
		`"Config":{"Image":"app:1","Env":["CONN=` + tokenRevYvlh + `","API_TOKEN=` + tokenRevYvlh + `","TZ=UTC"],` +
		`"Cmd":["serve","--db-password=` + flagRevYvlh + `","--name","web"],` +
		`"Entrypoint":["app","--token","` + nextRevYvlh + `"],` +
		`"Labels":{` + labels + `},` +
		`"Healthcheck":{"Test":["CMD-SHELL","curl -H 'X: ` + tokenRevYvlh + `' --password ` + nextRevYvlh + ` http://localhost"]}}}`
}

func fakeInspectRevYvlh(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/stacked/json"):
			writeOrFailYvlh(t, w, []byte(inspectBodyRevYvlh("lmstack")))
		case strings.HasSuffix(r.URL.Path, "/containers/standalone/json"):
			writeOrFailYvlh(t, w, []byte(inspectBodyRevYvlh("")))
		default:
			writeOrFailYvlh(t, w, []byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stacksRevYvlh records n stacks with compose project "lmstack", each with a
// .env holding API_TOKEN.
func stacksRevYvlh(t *testing.T, n int) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, "s"+string(rune('a'+i)))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("API_TOKEN="+tokenRevYvlh+"\n"), 0o600))
		createTestDirectory(t, db, dir)
		require.NoError(t, db.UpsertStack(models.Stack{
			ID: filepath.Base(root) + "~s" + string(rune('a'+i)) + ":default", Directory: dir,
			ComposeFile: "compose.yaml", EnvFile: ".env", ProjectName: "lmstack",
		}))
	}
	return db
}

func inspectRevYvlh(t *testing.T, db *database.DB, id string, unlocked bool) (int, string) {
	t.Helper()
	h := NewResourcesHandler(newTestDockerServiceAgainst(t, fakeInspectRevYvlh(t)), db, nil)
	router := gin.New()
	if unlocked {
		router.Use(unlockedSiou)
	}
	router.GET("/resources/containers/:id/inspect", h.inspectContainer)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/resources/containers/"+id+"/inspect", nil))
	return w.Code, w.Body.String()
}

type inspectRevYvlhShape struct {
	Path   string   `json:"Path"`
	Args   []string `json:"Args"`
	Config struct {
		Env         []string          `json:"Env"`
		Cmd         []string          `json:"Cmd"`
		Entrypoint  []string          `json:"Entrypoint"`
		Labels      map[string]string `json:"Labels"`
		Healthcheck struct {
			Test []string `json:"Test"`
		} `json:"Healthcheck"`
	} `json:"Config"`
}

func TestInspectLocked_MasksEveryFieldByStackSecrets_yvlh(t *testing.T) {
	code, body := inspectRevYvlh(t, stacksRevYvlh(t, 1), "stacked", false)
	require.Equal(t, http.StatusOK, code, body)
	for _, s := range []string{tokenRevYvlh, flagRevYvlh, nextRevYvlh} {
		assert.NotContains(t, body, s, "locked")
	}
	var got inspectRevYvlhShape
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Equal(t, []string{"CONN=***", "API_TOKEN=***", "TZ=UTC"}, got.Config.Env)
	assert.Equal(t, []string{"serve", "--db-password=***", "--name", "web"}, got.Config.Cmd)
	assert.Equal(t, []string{"app", "--token", "***"}, got.Config.Entrypoint)
	assert.Equal(t, []string{"--db-password=***", "--token", "***"}, got.Args)
	assert.Equal(t, "***", got.Config.Labels["x"])
	assert.Equal(t, "web", got.Config.Labels["app.tier"], "a label holding no secret is kept")
	assert.Equal(t, "lmstack", got.Config.Labels["com.docker.compose.project"])
	assert.Equal(t, "app", got.Path)
	require.Len(t, got.Config.Healthcheck.Test, 2)
	assert.Equal(t, "CMD-SHELL", got.Config.Healthcheck.Test[0])
}

// No stack: only the name and flag passes apply, so a value under a plain
// name is not known to be a secret (two-sided: the flag is still masked).
func TestInspectLocked_StandaloneGetsNameAndFlagPasses_yvlh(t *testing.T) {
	code, body := inspectRevYvlh(t, stacksRevYvlh(t, 1), "standalone", false)
	require.Equal(t, http.StatusOK, code, body)
	var got inspectRevYvlhShape
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Equal(t, []string{"CONN=" + tokenRevYvlh, "API_TOKEN=***", "TZ=UTC"}, got.Config.Env)
	assert.Equal(t, []string{"serve", "--db-password=***", "--name", "web"}, got.Config.Cmd)
	assert.Equal(t, []string{"app", "--token", "***"}, got.Config.Entrypoint)
	assert.NotContains(t, body, flagRevYvlh)
	assert.NotContains(t, body, nextRevYvlh)
}

// Which stack's secrets apply cannot be told: refuse, as the container
// actions do, rather than mask with the wrong set.
func TestInspectLocked_AmbiguousProjectIs409_yvlh(t *testing.T) {
	code, body := inspectRevYvlh(t, stacksRevYvlh(t, 2), "stacked", false)
	require.Equal(t, http.StatusConflict, code, body)
	assert.NotContains(t, body, tokenRevYvlh)
}

func TestInspectLocked_StackLookupFailureIs500_yvlh(t *testing.T) {
	db := stacksRevYvlh(t, 1)
	require.NoError(t, db.Close())
	code, body := inspectRevYvlh(t, db, "stacked", false)
	require.Equal(t, http.StatusInternalServerError, code, body)
	assert.NotContains(t, body, tokenRevYvlh)
}

func TestInspectUnlocked_UnchangedAndNoLookup_yvlh(t *testing.T) {
	db := stacksRevYvlh(t, 2) // ambiguous: an unlocked read never looks it up
	code, body := inspectRevYvlh(t, db, "stacked", true)
	require.Equal(t, http.StatusOK, code, body)
	for _, s := range []string{"CONN=" + tokenRevYvlh, "--db-password=" + flagRevYvlh, nextRevYvlh} {
		assert.Contains(t, body, s, "unlocked")
	}
}
