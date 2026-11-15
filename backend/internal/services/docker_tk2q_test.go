package services

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// agent-os-tk2q: GetContainerList called the Docker API on
// context.Background() and the client has no HTTP timeout, so a daemon that
// accepts the request and never answers blocked the caller forever.
// Producer: GetContainerList's unbounded context. Consumer: every caller (the
// scanner under its lock, Delete, the terminal membership check).

// hungDaemonService is a fake Docker daemon that accepts every request and
// never answers it, until the client gives up or the test ends.
func hungDaemonService(t *testing.T) *DockerService {
	t.Helper()
	release := make(chan struct{})
	s := fakeDaemonService(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	// Registered after the server, so it runs first and the server's Close
	// does not wait on a handler that is still blocked.
	t.Cleanup(func() { close(release) })
	return s
}

func TestGetContainerListTk2q_ReturnsWithinItsBoundAgainstAHungDaemon(t *testing.T) {
	old := dockerListBound
	dockerListBound = 200 * time.Millisecond
	t.Cleanup(func() { dockerListBound = old })

	s := hungDaemonService(t)
	type answer struct{ err error }
	done := make(chan answer, 1)
	go func() {
		_, err := s.GetContainerList("hung")
		done <- answer{err}
	}()

	select {
	case a := <-done:
		require.Error(t, a.err, "a lookup the daemon never answered must fail, not report an empty project")
	case <-time.After(5 * time.Second):
		t.Fatal("GetContainerList did not return within 5s against a hung daemon: it has no deadline")
	}
}
