package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

// negotiatingDaemonService is fakeDaemonService without a pinned version: the
// client negotiates from the API-Version header the fake daemon's _ping sends.
func negotiatingDaemonService(t *testing.T, apiVersion string) *DockerService {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", apiVersion)
			writeBody(w, "OK")
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")),
		client.WithHTTPClient(srv.Client()),
		client.WithAPIVersionNegotiation(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return &DockerService{client: cli}
}

// TestPruneVolumes_DefaultScopeFollowsAPIVersion (agent-os-ny0c): below API 1.42
// the daemon turns a volume prune without all=true into "every unused volume",
// so the Volumes page must warn about the default prune too.
func TestPruneVolumes_DefaultScopeFollowsAPIVersion(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		want          bool
	}{
		{"negotiated 1.41 removes named volumes", "1.41", true},
		{"negotiated 1.45 removes anonymous only", "1.45", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := negotiatingDaemonService(t, tc.version).VolumePruneRemovesAllUnused(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// A pinned version (DOCKER_API_VERSION, here WithVersion) is not negotiated up:
// a 1.41 pin on a daemon that speaks 1.45 still gets the old prune.
func TestPruneVolumes_DefaultScopeHonoursPinnedVersion(t *testing.T) {
	ping := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.45")
		writeBody(w, "OK")
	}
	for _, tc := range []struct {
		pin  string
		want bool
	}{{"1.41", true}, {"1.45", false}} {
		srv := httptest.NewServer(http.HandlerFunc(ping))
		cli, err := client.NewClientWithOpts(
			client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")),
			client.WithHTTPClient(srv.Client()),
			client.WithVersion(tc.pin),
			client.WithAPIVersionNegotiation(),
		)
		require.NoError(t, err)
		got, err := (&DockerService{client: cli}).VolumePruneRemovesAllUnused(context.Background())
		_ = cli.Close()
		srv.Close()
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "pin %s", tc.pin)
	}
}

// A failed ping is an error, never "anonymous only": the page then runs the
// impact check anyway.
func TestPruneVolumes_DefaultScopeFailsClosedOnPingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")),
		client.WithHTTPClient(srv.Client()),
		client.WithAPIVersionNegotiation(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	_, err = (&DockerService{client: cli}).VolumePruneRemovesAllUnused(context.Background())
	require.Error(t, err)

	var nilSvc *DockerService
	_, err = nilSvc.VolumePruneRemovesAllUnused(context.Background())
	require.ErrorIs(t, err, ErrDockerUnavailable)
}
