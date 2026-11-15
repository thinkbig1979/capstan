package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-2geb: stackDotEnv now resolves through resolveComposeEnv
// (composeEnvScannedName) instead of its own dotenv.GetEnvFromFile call. The
// behaviour is meant to be unchanged: the project directory's .env is read and
// the process environment is not.
func TestScannedName2geb_ReadsDotEnvNotTheProcessEnv(t *testing.T) {
	// XDG_CACHE_HOME is on dockerenv.AllowedEnvVars, so it reaches the
	// compose child; an arbitrary variable would not.
	t.Setenv("XDG_CACHE_HOME", "fromproc")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("CAPSTAN_2GEB_FILE=fromfile\nCAPSTAN_2GEB_REF=${XDG_CACHE_HOME-unset}\n"), 0o600))

	// Control: the variable does reach the compose child's environment, so
	// the scanned-name arm below can see a merge if one happens.
	exact, err := resolveComposeEnv(models.Stack{Directory: dir}, "", composeEnvExact)
	require.NoError(t, err)
	require.Equal(t, "fromproc", exact[0]["XDG_CACHE_HOME"], "control: exact mode merges the process env")

	compose := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.WriteFile(compose, []byte("name: ${CAPSTAN_2GEB_FILE}-${XDG_CACHE_HOME-unset}\nservices: {}\n"), 0o600))
	name, err := composeFileProjectName(context.Background(), compose)
	require.NoError(t, err)
	assert.Equal(t, "fromfile-unset", name, ".env is read; the process env is not merged into the scanned name")

	// Nor does it seed a ${VAR} reference inside .env itself.
	assert.Equal(t, "unset", stackDotEnv(dir)["CAPSTAN_2GEB_REF"], "the process env must not seed interpolation inside .env")
}

// A .env compose-go rejects still degrades to an empty map with a warning, as
// before (agent-os-d5ff's neighbour branch).
func TestScannedName2geb_UnparseableDotEnvDegradesToEmpty(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("A=\"unterminated\n"), 0o600))
	assert.Empty(t, stackDotEnv(dir))
}

// agent-os-2geb round 2 (review F1): compose-go's parse error quotes the
// offending .env value (`unterminated quoted value "hunter2secret`), and
// resolveComposeEnv's doc says that error is not for a log. The WARN names the
// file only.
func TestScannedName2geb_ParseWarnCarriesNoValueText(t *testing.T) {
	logs := u2CaptureLogs(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("USER=a\nPASSWORD=\"hunter2secret\n"), 0o600))

	assert.Empty(t, stackDotEnv(dir))
	out := logs.String()
	require.Contains(t, out, "level=WARN", "control: the parse fault is still warned about")
	require.Contains(t, out, filepath.Join(dir, ".env"), "control: the warning names the file")
	assert.NotContains(t, out, "hunter2secret", "the warning must not quote the .env value")
}
