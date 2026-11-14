package services

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealRestic_PasswordFileIsTrimmed_1zpq pins what agent-os-1zpq's probe
// found (restic 0.18.0): restic trims leading and trailing whitespace from
// RESTIC_PASSWORD_FILE, but not from RESTIC_PASSWORD. Capstan only ever uses
// the file, so it always opens what it made; the trimmed password is the key,
// and a manual restore that pastes the untrimmed one into RESTIC_PASSWORD is
// refused. If restic stops trimming, this fails and withPasswordFile's doc is
// wrong.
func TestRealRestic_PasswordFileIsTrimmed_1zpq(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	t.Setenv("RESTIC_CACHE_DIR", filepath.Join(dir, "restic-cache"))
	const trimmed = "pw-1zpq"
	cfg := testBackupConfig()
	cfg.ResticRepository = repo
	cfg.ResticPassword = " " + trimmed + "\r\n"
	m := NewResticManager(cfg, nil)
	ctx := context.Background()
	require.NoError(t, m.EnsureRepository(ctx), "init through Capstan's password file")

	openWith := func(env ...string) error {
		return runDrained(ctx, &execRunner{}, "restic", []string{"cat", "config", "--no-lock", "--quiet"},
			append([]string{"RESTIC_REPOSITORY=" + repo}, env...))
	}
	pwFile, cleanup, err := m.withPasswordFile()
	require.NoError(t, err)
	defer cleanup()

	assert.NoError(t, openWith("RESTIC_PASSWORD_FILE="+pwFile), "Capstan's own file opens what Capstan made")
	assert.NoError(t, openWith("RESTIC_PASSWORD="+trimmed), "the trimmed password is the repository key")
	assert.Error(t, openWith("RESTIC_PASSWORD="+cfg.ResticPassword), "restic does not trim RESTIC_PASSWORD")
}
