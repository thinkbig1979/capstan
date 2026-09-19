package services

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The three tests below drive REAL production functions and marshal what they
// return, so the claim "this field can arrive as JSON null" is measured rather
// than asserted about a hand-built struct. models/wire_slice_nullability_test.go
// pins the whole eleven-field table; these pin the ones whose construction path
// is reachable without a Docker daemon.
//
// Each carries both arms. A test that only ever sees null cannot tell a
// genuinely nullable field from an instrument that reports null for everything.

// TestWireNullability_DiffResultFiles drives getDiffCLI through GetDiff against
// a real repository. The seed commit is `--allow-empty`, so `git diff-tree
// --name-only` prints nothing and getDiffCLI's `var files []string` is never
// assigned — the field reaches the wire as null. The second arm commits a file
// so the same instrument reports a populated array on the same code path.
func TestWireNullability_DiffResultFiles(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	t.Run("empty commit yields null", func(t *testing.T) {
		dir := repoWithCommit(t, t.TempDir())
		head := gitOutput(t, dir, "rev-parse", "HEAD")

		res, err := svc.GetDiff(dir, head)
		require.NoError(t, err)
		require.Nil(t, res.Files, "getDiffCLI left Files nil for a commit that touched nothing")

		raw, err := json.Marshal(res)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"files":null`)
	})

	t.Run("commit touching a file yields an array", func(t *testing.T) {
		dir := repoWithCommit(t, t.TempDir())
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o600))
		mustGit(t, dir, "add", "a.txt")
		mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "add a")
		head := gitOutput(t, dir, "rev-parse", "HEAD")

		res, err := svc.GetDiff(dir, head)
		require.NoError(t, err)

		raw, err := json.Marshal(res)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"files":["a.txt"]`)
	})
}

// TestWireNullability_ContainerPorts drives parsePorts, the shared builder for
// Container.Ports on the compose-ps path. Its opening make(...,0) means the
// empty case is an empty ARRAY, not null — the field is safe, and a generated
// `PortBinding[]` is honest about it.
func TestWireNullability_ContainerPorts(t *testing.T) {
	empty := parsePorts("")
	require.NotNil(t, empty, "parsePorts must never return a nil slice")

	raw, err := json.Marshal(models.Container{Ports: empty})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"ports":[]`)

	populated := parsePorts("0.0.0.0:8080->80/tcp")
	raw, err = json.Marshal(models.Container{Ports: populated})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"ports":[{`)
}

// nullabilityResticRunner answers only `restic snapshots`, which is the single
// Output call ListSnapshots makes.
type nullabilityResticRunner struct{ out []byte }

func (r *nullabilityResticRunner) Run(context.Context, string, []string, []string, chan<- StreamLine) error {
	return errors.New("wire-nullability runner: Run is not part of ListSnapshots")
}

func (r *nullabilityResticRunner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	if len(args) == 0 || args[0] != "snapshots" {
		return nil, errors.New("wire-nullability runner: unexpected restic subcommand")
	}
	return r.out, nil
}

// TestWireNullability_BackupSnapshotTags drives ListSnapshots over restic's own
// JSON. restic omits the "tags" key entirely for an untagged snapshot, and
// ListSnapshots copies the field straight through with no guard, so tags
// reaches the wire as null. The tagged arm proves the same instrument reports
// an array when restic sends one.
func TestWireNullability_BackupSnapshotTags(t *testing.T) {
	newMgr := func(out string) *ResticManager {
		return newResticManagerWithRunner(
			BackupConfig{ResticRepository: t.TempDir(), ResticPassword: "pw"},
			&nullabilityResticRunner{out: []byte(out)},
			slog.New(slog.DiscardHandler),
		)
	}

	t.Run("untagged snapshot yields null tags", func(t *testing.T) {
		m := newMgr(`[{"id":"abc","short_id":"abc","time":"2026-01-01T00:00:00Z","hostname":"h","paths":["/srv"]}]`)
		snaps, err := m.ListSnapshots(context.Background(), "", 0)
		require.NoError(t, err)
		require.Len(t, snaps, 1)
		require.Nil(t, snaps[0].Tags)

		raw, err := json.Marshal(snaps[0])
		require.NoError(t, err)
		require.Contains(t, string(raw), `"tags":null`)
		require.Contains(t, string(raw), `"paths":["/srv"]`)
	})

	t.Run("tagged snapshot yields an array", func(t *testing.T) {
		m := newMgr(`[{"id":"abc","short_id":"abc","time":"2026-01-01T00:00:00Z","hostname":"h","tags":["stack-a"],"paths":["/srv"]}]`)
		snaps, err := m.ListSnapshots(context.Background(), "", 0)
		require.NoError(t, err)
		require.Len(t, snaps, 1)

		raw, err := json.Marshal(snaps[0])
		require.NoError(t, err)
		require.Contains(t, string(raw), `"tags":["stack-a"]`)
	})
}
