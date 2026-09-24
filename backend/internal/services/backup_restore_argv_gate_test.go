package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-tyl6 candidate 5: a snapshot id reaches `restic restore` only after
// validateSnapshotBelongsToStack has matched it EXACTLY against an id restic
// itself listed for the stack, so a flag-shaped id never becomes the ref.
func TestRunRestore_FlagShapedSnapshotIDNeverReachesRestore(t *testing.T) {
	t.Parallel()

	restoreCalls := func(r *fakeRunner) [][]string {
		var got [][]string
		for _, c := range r.calls {
			if len(c.Args) > 0 && c.Args[0] == "restore" {
				got = append(got, c.Args)
			}
		}
		return got
	}

	for _, id := range []string{"--help", "--target=/", "-h", "latest"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			db := newBackupTestDB(t)
			runner := &fakeRunner{outputData: snapshotJSON("aa1e6e99ac47207dc3f58912a79c11f2ca1140c3976899a589954f833e6b997f", "aa1e6e99", "myapp")}
			svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, runner, runner)
			seedStack(t, db, "myapp", "stop")

			err := svc.RunRestore(context.Background(), "myapp", id, "", make(chan StreamLine, 128))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "does not belong to stack")
			assert.Empty(t, restoreCalls(runner))
		})
	}

	t.Run("control: a listed short id does reach restore", func(t *testing.T) {
		t.Parallel()
		db := newBackupTestDB(t)
		runner := &fakeRunner{outputData: snapshotJSON("aa1e6e99ac47207dc3f58912a79c11f2ca1140c3976899a589954f833e6b997f", "aa1e6e99", "myapp")}
		svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, runner, runner)
		seedStack(t, db, "myapp", "stop")

		require.NoError(t, svc.RunRestore(context.Background(), "myapp", "aa1e6e99", "", make(chan StreamLine, 128)))
		calls := restoreCalls(runner)
		require.Len(t, calls, 1)
		assert.Equal(t, "aa1e6e99:/opt/stacks/myapp", calls[0][1])
	})
}
