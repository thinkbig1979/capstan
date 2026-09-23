package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

// TestWireNullability_DockerNetworkLabels drives ListNetworks against a fake
// Docker API. The default bridge/host/none networks carry no labels, which is
// the case that used to leave `var labelStrs []string` nil and put
// "labels":null on the wire; it must send [] (agent-os-e5pr). The labelled
// network is the other arm: the same instrument reports a populated array.
func TestWireNullability_DockerNetworkLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/networks"):
			_, _ = w.Write([]byte(`[
				{"Id":"bridge-id","Name":"bridge","Driver":"bridge","Scope":"local"},
				{"Id":"app-id","Name":"app_default","Driver":"bridge","Scope":"local",
				 "Labels":{"com.docker.compose.project":"app"}}
			]`))
		case strings.Contains(r.URL.Path, "/networks/"):
			_, _ = w.Write([]byte(`{"Id":"x","Containers":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")),
		client.WithHTTPClient(srv.Client()),
		client.WithVersion("1.45"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	nets, err := (&DockerService{client: cli}).ListNetworks(context.Background())
	require.NoError(t, err)
	require.Len(t, nets, 2)

	raw, err := json.Marshal(nets[0])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"labels":[]`, "unlabelled network")

	raw, err = json.Marshal(nets[1])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"labels":["com.docker.compose.project=app"]`, "labelled network")
}
