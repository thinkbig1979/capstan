package registryretry

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This package imports "testing" and exists for tests. Nothing stops a
// production import of it, which would put package testing into the server
// binary, so this checks the server's dependency list (review F7).
func TestRegistryRetryIsNotAServerDependency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "list", "-deps",
		"github.com/thinkbig1979/capstan/backend/cmd/server").CombinedOutput()
	require.NoError(t, err, "go list: %s", out)
	deps := map[string]bool{}
	for _, line := range strings.Fields(string(out)) {
		deps[line] = true
	}
	// Positive side: the list holds a package the server does use.
	require.True(t, deps["github.com/thinkbig1979/capstan/backend/internal/truth"], "go list -deps saw the server's own packages")
	require.False(t, deps["github.com/thinkbig1979/capstan/backend/internal/registryretry"],
		"cmd/server depends on registryretry, a test helper: import it from tests only")
}
