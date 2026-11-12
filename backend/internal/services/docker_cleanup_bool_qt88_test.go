package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-qt88, docker-cleanup arm: the cleanup switches read through
// resolveBoolSetting too. Kept in its own file so docker_cleanup_scheduler.go
// and its tests stay untouched.
func TestQt88_DockerCleanupSwitchesAreCaseInsensitive(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "True"))
	require.NoError(t, db.SetSetting(SettingDockerCleanupUnusedImages, "TRUE"))
	require.NoError(t, db.SetSetting(SettingDockerCleanupBuildCache, "FALSE"))
	require.NoError(t, db.SetSetting(SettingDockerCleanupDanglingImages, "yes"))

	p, err := ResolveDockerCleanupPolicy(db)
	require.NoError(t, err)
	assert.True(t, p.Enabled, "enabled=True")
	assert.True(t, p.Categories.UnusedImages, "unused images=TRUE")
	assert.False(t, p.Categories.BuildCache, "build cache=FALSE")
	assert.False(t, p.Categories.DanglingImages, "dangling images=yes is not a spelling of true")
}
