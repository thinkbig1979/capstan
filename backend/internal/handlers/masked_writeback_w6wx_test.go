package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-w6wx review (cor D2), the server half: an editor that undid back
// to the masked text, then saved after unlocking, wrote "***" over the
// secret. An UNLOCKED save is refused (409) when a line of it equals the
// masked form of an on-disk line the masker changed.

// List form and a quoted value: both mask to valid YAML ("- KEY=***",
// "'***'"), so the lint passes the masked text. A plain `KEY: ***` would not
// even lint (`*` starts a YAML alias).
const writebackOnDiskW6wx = "services:\n  db:\n    image: postgres:16\n    environment:\n      - POSTGRES_PASSWORD=hunter2W6WXwb\n      - TZ=UTC\n    labels:\n      api_token: 'tok-W6WX-label'\n"

func writebackRouterW6wx(t *testing.T) (*gin.Engine, string, string) {
	t.Helper()
	h, id, dir := composeStackSiou(t, writebackOnDiskW6wx, "")
	router := gin.New()
	router.PUT("/stacks/:id/compose", unlockedSiou, h.Put)
	router.PUT("/stacks/:id/compose-env", unlockedSiou, h.PutComposeAndEnv)
	return router, id, dir
}

func readComposeW6wx(t *testing.T, dir string) string {
	t.Helper()
	got, err := os.ReadFile( //nolint:gosec // G304: the test reads back the fixture file it wrote
		filepath.Join(dir, "compose.yaml"))
	require.NoError(t, err)
	return string(got)
}

func TestComposePut_UnlockedMaskedWritebackIs409_w6wx(t *testing.T) {
	router, id, dir := writebackRouterW6wx(t)
	cases := map[string]string{
		"list item":    "services:\n  db:\n    image: postgres:16\n    environment:\n      - POSTGRES_PASSWORD=***\n      - TZ=Europe/Amsterdam\n    labels:\n      api_token: 'tok-W6WX-label'\n",
		"quoted value": "services:\n  db:\n    image: postgres:16\n    environment:\n      - POSTGRES_PASSWORD=hunter2W6WXwb\n      - TZ=Europe/Amsterdam\n    labels:\n      api_token: '***'\n",
	}
	for name, masked := range cases {
		w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": masked})
		require.Equal(t, http.StatusConflict, w.Code, "%s: %s", name, w.Body.String())
		assert.Contains(t, w.Body.String(), "reload", name)
		assert.Equal(t, writebackOnDiskW6wx, readComposeW6wx(t, dir), "%s: nothing written", name)

		w = doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose-env", map[string]string{"composeContent": masked})
		require.Equal(t, http.StatusConflict, w.Code, "%s: %s", name, w.Body.String())
		assert.Equal(t, writebackOnDiskW6wx, readComposeW6wx(t, dir), "%s: nothing written", name)
	}
}

// Two-sided: a real edit saves, and so does a "***" that is not the masked
// form of any line on disk.
func TestComposePut_UnlockedRealEditSaves_w6wx(t *testing.T) {
	router, id, dir := writebackRouterW6wx(t)
	for _, content := range []string{
		"services:\n  db:\n    image: postgres:16\n    environment:\n      - POSTGRES_PASSWORD=hunter2W6WXwb\n      - TZ=Europe/Amsterdam\n",
		"services:\n  db:\n    image: postgres:16\n    command: echo ***\n    environment:\n      - POSTGRES_PASSWORD=newSecret99\n",
	} {
		w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": content})
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, content, readComposeW6wx(t, dir))
	}
}
