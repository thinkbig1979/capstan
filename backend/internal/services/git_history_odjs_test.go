package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-odjs, history view: a past commit's absolutely-named env file
// stays hidden once the file is gone from disk.
func TestOdjsHistoryHidesAbsoluteEnvFileGoneFromDisk(t *testing.T) {
	f, linkApp := odjsAbsFix(t)
	writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=2\n")
	runGit(t, f.top, "commit", "-q", "-am", "rotate")
	oid := f.head(t)

	d, err := f.svc.GetDiff(linkApp, oid)
	require.NoError(t, err)
	assert.Equal(t, []string{"app/sub/secrets.conf"}, d.HiddenFiles, "control: present on disk")
	assert.NotContains(t, d.Diff, odjsSecret)

	require.NoError(t, os.Remove(filepath.Join(f.dir, "sub/secrets.conf")))
	d, err = f.svc.GetDiff(linkApp, oid)
	require.NoError(t, err)
	assert.Equal(t, []string{"app/sub/secrets.conf"}, d.HiddenFiles, "gone from disk")
	assert.NotContains(t, d.Diff, odjsSecret)
}

// repoRel places a path that exists exactly as before (the pull path calls it
// on an existing stack folder), and now also places a missing one reached
// through a symlinked ancestor.
func TestOdjsRepoRelThroughSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	top := filepath.Join(base, "real", "repo")
	writeFile(t, filepath.Join(top, "app", "f.txt"), "f\n")
	link := filepath.Join(base, "stacks")
	require.NoError(t, os.Symlink(filepath.Join(base, "real"), link))
	viaLink := filepath.Join(link, "repo")

	cases := []struct {
		name, abs, want string
		ok              bool
	}{
		{"existing_dir_real", filepath.Join(top, "app"), "app", true},
		{"existing_dir_link", filepath.Join(viaLink, "app"), "app", true},
		{"existing_file_link", filepath.Join(viaLink, "app", "f.txt"), "app/f.txt", true},
		{"top_itself_link", viaLink, ".", true},
		{"outside", base, "", false},
		{"missing_real", filepath.Join(top, "app", "gone.txt"), "app/gone.txt", true},
		{"missing_link", filepath.Join(viaLink, "app", "gone", "x.conf"), "app/gone/x.conf", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := repoRel(top, tc.abs)
			require.NoError(t, err)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The rule reads each past tree whole, whatever folder the request names: a
// stack below the repository top named an env file in a compose file that has
// since been deleted.
func TestOdjsHistoryReadsWholeTreeFromStackFolder(t *testing.T) {
	f := odjsFix(t, odjsRelEntry)
	writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=2\n")
	runGit(t, f.top, "commit", "-q", "-am", "rotate")
	oid := f.head(t)
	runGit(t, f.top, "rm", "-q", "app/compose.yaml", "app/sub/secrets.conf")
	runGit(t, f.top, "commit", "-q", "-m", "drop the stack's files")

	d, err := f.svc.GetDiff(f.dir, oid)
	require.NoError(t, err)
	assert.Equal(t, []string{"app/sub/secrets.conf"}, d.HiddenFiles)
	assert.NotContains(t, d.Diff, odjsSecret)
}
