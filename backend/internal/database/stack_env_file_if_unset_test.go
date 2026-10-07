package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-z91e.23: SetStackEnvFileIfUnset is EnvHandler.Create's one-column
// write. It must touch only the named row, and only while that row has no env
// file: a NULL written outside Capstan counts as none (agent-os-d1c7).
func TestSetStackEnvFileIfUnset_WritesOnlyTheNamedUnsetRow(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/app", Name: "app", ScannedAt: time.Now()}))
	for _, st := range []models.Stack{
		{ID: "a", EnvFile: ""},
		{ID: "b", EnvFile: ""},
		{ID: "c", EnvFile: "custom.env"},
		{ID: "d", EnvFile: ""},
	} {
		st.Directory, st.ComposeFile, st.ProjectName, st.Status = "/srv/app", "compose."+st.ID+".yaml", st.ID, "unknown"
		require.NoError(t, db.UpsertStack(st))
	}
	_, err := db.db.Exec(`UPDATE stacks SET env_file = NULL WHERE id = 'd'`)
	require.NoError(t, err)

	envFiles := func() map[string]string {
		got := map[string]string{}
		for _, id := range []string{"a", "b", "c", "d"} {
			st, err := db.GetStack(id)
			require.NoError(t, err)
			got[id] = st.EnvFile
		}
		return got
	}

	set, err := db.SetStackEnvFileIfUnset("a", ".env")
	require.NoError(t, err)
	assert.True(t, set, "a had no env file, so the write must land")
	assert.Equal(t, map[string]string{"a": ".env", "b": "", "c": "custom.env", "d": ""}, envFiles(),
		"only stack a may change")

	set, err = db.SetStackEnvFileIfUnset("c", ".env")
	require.NoError(t, err)
	assert.False(t, set, "c already has an env file, so the write must not land")
	assert.Equal(t, map[string]string{"a": ".env", "b": "", "c": "custom.env", "d": ""}, envFiles(),
		"a configured env file is never overwritten")

	set, err = db.SetStackEnvFileIfUnset("d", ".env")
	require.NoError(t, err)
	assert.True(t, set, "a NULL env_file counts as unset")
	assert.Equal(t, ".env", envFiles()["d"])

	set, err = db.SetStackEnvFileIfUnset("missing", ".env")
	require.NoError(t, err)
	assert.False(t, set, "an absent row is not created")
	_, err = db.GetStack("missing")
	assert.Error(t, err)
}
