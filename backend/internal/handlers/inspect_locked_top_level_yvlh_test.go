package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-yvlh review r3 (Q1): locked inspect starred a bind source in
// HostConfig.Binds and served the same path raw in the top-level
// Mounts[].Source. The stack-controlled strings outside Config and
// HostConfig (Mounts, NetworkSettings, Name, the security labels, State.Error
// and the healthcheck output) now go through the masker too.
func TestInspectLocked_MasksStackStringsOutsideConfigAndHostConfig_yvlh(t *testing.T) {
	tok := tokenRevYvlh
	body := `{"Id":"c1","Name":"/web-` + tok + `","AppArmorProfile":"prof-` + tok + `",` +
		`"State":{"Status":"running","Error":"mount /srv/` + tok + ` failed",` +
		`"Health":{"Status":"healthy","Log":[{"ExitCode":0,"Output":"auth ok for ` + tok + `"}]}},` +
		`"Config":{"Image":"app:1","Labels":{"com.docker.compose.project":"lmstack"}},` +
		`"HostConfig":{"Binds":["/srv/` + tok + `:/data:ro"]},` +
		`"Mounts":[{"Type":"bind","Source":"/srv/` + tok + `","Destination":"/data","Mode":"ro","RW":false}],` +
		`"NetworkSettings":{"Networks":{"lm_default":{"Aliases":["web","` + tok + `"],` +
		`"DriverOpts":{"com.example.opt":"` + tok + `"},"IPAddress":"172.18.0.2"}}}}`
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

		var got container.InspectResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		require.NotNil(t, got.ContainerJSONBase)
		require.Len(t, got.Mounts, 1)
		require.NotNil(t, got.State)
		require.NotNil(t, got.State.Health)
		require.Len(t, got.State.Health.Log, 1)
		require.NotNil(t, got.NetworkSettings)
		ep := got.NetworkSettings.Networks["lm_default"]
		require.NotNil(t, ep)

		fields := map[string]string{
			"Mounts[0].Source":            got.Mounts[0].Source,
			"Name":                        got.Name,
			"AppArmorProfile":             got.AppArmorProfile,
			"State.Error":                 got.State.Error,
			"State.Health.Log[0].Output":  got.State.Health.Log[0].Output,
			"NetworkSettings.Aliases":     strings.Join(ep.Aliases, ","),
			"NetworkSettings.DriverOpts":  ep.DriverOpts["com.example.opt"],
			"HostConfig.Binds (baseline)": strings.Join(got.HostConfig.Binds, ","),
		}
		for name, v := range fields {
			if unlocked {
				assert.Contains(t, v, tok, "unlocked %s is unchanged", name)
			} else {
				assert.NotContains(t, v, tok, "locked %s", name)
			}
		}
		if !unlocked {
			assert.Equal(t, 0, strings.Count(w.Body.String(), tok), "locked body still holds the token")
			assert.Equal(t, "/data", got.Mounts[0].Destination, "a string holding no secret is kept")
			assert.Equal(t, "172.18.0.2", ep.IPAddress, "a string holding no secret is kept")
			assert.Equal(t, container.StateRunning, got.State.Status)
		}
	}
}
