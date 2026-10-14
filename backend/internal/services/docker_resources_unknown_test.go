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

// fakeDaemonService serves the Docker API from handler, so the List* methods run
// their real decode and mapping instead of a hand-built struct.
func fakeDaemonService(t *testing.T, handler http.HandlerFunc) *DockerService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")),
		client.WithHTTPClient(srv.Client()),
		client.WithVersion("1.45"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return &DockerService{client: cli}
}

func writeBody(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
}

// wireByName marshals each listed item and keys the JSON by its "name" field,
// so assertions read the wire shape the frontend receives.
func wireByName[T any](t *testing.T, items []T) map[string]map[string]any {
	t.Helper()
	out := make(map[string]map[string]any, len(items))
	for _, it := range items {
		raw, err := json.Marshal(it)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		out[m["name"].(string)] = m
	}
	return out
}

const volumeListBody = `{"Volumes":[
	{"Name":"computed","Driver":"local","Mountpoint":"/v/computed"},
	{"Name":"uncomputed","Driver":"local","Mountpoint":"/v/uncomputed"}
]}`

// TestListVolumes_UnknownUsageFailsClosed (agent-os-gkgr): Docker reports a size
// or reference count it did not compute as -1. Copied through, -1 rendered as a
// known size and RefCount -1 read as "not in use", so the Volumes list offered
// Delete on a volume nobody had checked. The computed volume is the other arm.
func TestListVolumes_UnknownUsageFailsClosed(t *testing.T) {
	svc := fakeDaemonService(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/volumes"):
			writeBody(w, volumeListBody)
		case strings.HasSuffix(r.URL.Path, "/system/df"):
			writeBody(w, `{"Volumes":[
				{"Name":"computed","UsageData":{"Size":4096,"RefCount":1}},
				{"Name":"uncomputed","UsageData":{"Size":-1,"RefCount":-1}}
			]}`)
		default:
			http.NotFound(w, r)
		}
	})

	vols, err := svc.ListVolumes(context.Background())
	require.NoError(t, err)
	got := wireByName(t, vols)

	require.Equal(t, true, got["computed"]["sizeKnown"], "computed size")
	require.Equal(t, float64(4096), got["computed"]["size"])
	require.Equal(t, true, got["computed"]["inUseKnown"], "computed refcount")
	require.Equal(t, true, got["computed"]["inUse"])

	require.Equal(t, false, got["uncomputed"]["sizeKnown"], "Size -1 is not a known size")
	require.Equal(t, float64(0), got["uncomputed"]["size"], "Size -1 must not reach the wire")
	require.Equal(t, false, got["uncomputed"]["inUseKnown"], "RefCount -1 is not a known in-use state")
}

// TestListVolumes_FailedDiskUsageFailsClosed: when the DiskUsage read fails no
// volume's in-use state was looked at, so none may read as known-unused.
func TestListVolumes_FailedDiskUsageFailsClosed(t *testing.T) {
	svc := fakeDaemonService(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/volumes"):
			writeBody(w, volumeListBody)
		case strings.HasSuffix(r.URL.Path, "/system/df"):
			http.Error(w, `{"message":"df boom"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})

	vols, err := svc.ListVolumes(context.Background())
	require.NoError(t, err)
	for name, v := range wireByName(t, vols) {
		require.Equal(t, false, v["sizeKnown"], name)
		require.Equal(t, false, v["inUseKnown"], name)
	}
}

// TestListNetworks_FailedInspectFailsClosed (agent-os-gkgr, from p8sm F1):
// NetworkList leaves Containers empty, so a failed inspect used to fall back to
// 0 and the Networks tab offered Delete on a network nobody had checked. The
// inspected network is the other arm.
func TestListNetworks_FailedInspectFailsClosed(t *testing.T) {
	svc := fakeDaemonService(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/networks"):
			writeBody(w, `[
				{"Id":"ok-id","Name":"inspected","Driver":"bridge","Scope":"local"},
				{"Id":"bad-id","Name":"uninspected","Driver":"bridge","Scope":"local"}
			]`)
		case strings.HasSuffix(r.URL.Path, "/networks/ok-id"):
			writeBody(w, `{"Id":"ok-id","Containers":{"c1":{},"c2":{}}}`)
		case strings.HasSuffix(r.URL.Path, "/networks/bad-id"):
			http.Error(w, `{"message":"inspect boom"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})

	nets, err := svc.ListNetworks(context.Background())
	require.NoError(t, err)
	got := wireByName(t, nets)

	require.Equal(t, true, got["inspected"]["containersKnown"], "inspected network")
	require.Equal(t, float64(2), got["inspected"]["containers"])

	require.Equal(t, false, got["uninspected"]["containersKnown"], "a failed inspect is not a known count")
	require.Equal(t, float64(0), got["uninspected"]["containers"])
}
