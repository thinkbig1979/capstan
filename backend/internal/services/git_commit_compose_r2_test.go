package services

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The commit dialog shares composeSecretFiles with the history view (D145,
// D146), so it flags, and hides, the same files: an env file named through
// include:, a secrets: file:, and an absolute env_file:, which used to be
// joined under the folder and so named nothing.
func TestGetChanges_ComposeNamedSecretFiles(t *testing.T) {
	f := newCommitFix(t, false)
	abs := filepath.Join(f.top, "abs.conf")
	writeFile(t, filepath.Join(f.top, "compose.yaml"),
		"include:\n  - db.yaml\nservices:\n  app:\n    image: x\n    env_file: "+abs+"\n    secrets: [pw]\nsecrets:\n  pw:\n    file: pw.txt\n")
	writeFile(t, filepath.Join(f.top, "db.yaml"), "services:\n  db:\n    image: y\n    env_file: db.conf\n")
	for _, n := range []string{"db.conf", "pw.txt", "abs.conf", "plain.txt"} {
		writeFile(t, filepath.Join(f.top, n), "0\n")
	}
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "compose")
	for _, n := range []string{"db.conf", "pw.txt", "abs.conf", "plain.txt"} {
		writeFile(t, filepath.Join(f.top, n), "1\n")
	}

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	byPath := map[string]models.GitChange{}
	for _, c := range got.Changes {
		byPath[c.Path] = c
	}
	require.ElementsMatch(t, []string{"db.conf", "pw.txt", "abs.conf", "plain.txt"}, changePaths(got))
	assert.True(t, byPath["db.conf"].IsEnv, "named by an included compose file's env_file")
	assert.True(t, byPath["pw.txt"].IsEnv, "a compose secrets: file")
	assert.True(t, byPath["abs.conf"].IsEnv, "an absolute env_file inside the folder")
	assert.False(t, byPath["plain.txt"].IsEnv, "control: a file the compose doesn't name")

	d, err := f.svc.GetChangeDiff(f.dir, "db.conf")
	require.NoError(t, err)
	assert.True(t, d.Hidden)
}
