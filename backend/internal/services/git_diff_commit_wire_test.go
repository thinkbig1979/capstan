package services

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// TestGetDiff_CommitIsAlwaysOnTheWire drives the only production constructor of
// DiffResult. Its success path always fills Commit, which is why
// models.DiffResult declares it as a value and the generated TypeScript as a
// required `commit: GitCommit` (agent-os-apmw). The empty commit is the case
// most likely to leave something unset (it already left Files nil before
// agent-os-e5pr), so it is the arm that matters.
func TestGetDiff_CommitIsAlwaysOnTheWire(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	dir := repoWithCommit(t, t.TempDir())
	head := gitOutput(t, dir, "rev-parse", "HEAD")

	res, err := svc.GetDiff(dir, head)
	require.NoError(t, err)

	raw, err := json.Marshal(res)
	require.NoError(t, err)
	var decoded struct {
		Commit map[string]any `json:"commit"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotNil(t, decoded.Commit, "commit must be an object, got %s", raw)
	require.Equal(t, head, decoded.Commit["hash"])
	require.Len(t, decoded.Commit["short"], 7)
}
