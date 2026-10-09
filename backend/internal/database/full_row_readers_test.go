package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Each table below has one column list and one scan function shared by every
// full-row reader (agent-os-rh7m, the follow-up to agent-os-13xd). Each test
// writes one row whose columns all hold DIFFERENT values and reads it back
// through every full-row reader of that table, asserting every field: two
// neighbouring columns of the same type swapped in the column list would
// otherwise land in each other's fields with no error.

// rh7mSameInstant asserts the instants match, then copies want's time values
// into got, so the struct comparison after it is not tripped by a location
// pointer the driver chose.
func rh7mSameInstant(t *testing.T, label string, want time.Time, got *time.Time) {
	t.Helper()
	assert.True(t, want.Equal(*got), "%s: time %v, want %v", label, *got, want)
	*got = want
}

func TestStackReaders_ReturnEveryColumnInItsOwnField(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/rh7m", Name: "rh7m"}))

	want := models.Stack{
		ID:          "stack-rh7m",
		Directory:   "/srv/rh7m",
		ComposeFile: "compose.rh7m.yaml",
		EnvFile:     ".env.rh7m",
		ProjectName: "rh7m-project",
		Status:      "running",
		Containers:  []models.Container{},
		IsGitRepo:   true,
		GitBranch:   "rh7m-branch",
		GitCommit:   "rh7m-commit",
		GitDirty:    false,
		GitAhead:    3,
		GitBehind:   4,
	}
	require.NoError(t, db.UpsertStack(want))

	list, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, want, list[0], "ListStacks")

	byID, err := db.GetStack(want.ID)
	require.NoError(t, err)
	assert.Equal(t, want, *byID, "GetStack")

	byDir, err := db.ListStacksByDirectory(want.Directory)
	require.NoError(t, err)
	require.Len(t, byDir, 1)
	assert.Equal(t, want, byDir[0], "ListStacksByDirectory")

	byProject, err := db.GetStackByProjectName(want.ProjectName)
	require.NoError(t, err)
	assert.Equal(t, want, *byProject, "GetStackByProjectName")
}

func TestActionLogReaders_ReturnEveryColumnInItsOwnField(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)

	want := models.ActionLog{
		ID:        "action-rh7m",
		UserID:    "user-rh7m",
		StackID:   "stack-rh7m",
		Action:    "stack.start",
		Detail:    `{"rh7m":"detail"}`,
		RequestID: "request-rh7m",
		CreatedAt: time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC),
	}
	require.NoError(t, db.LogAction(want))

	check := func(label string, got []models.ActionLog) {
		t.Helper()
		require.Len(t, got, 1, label)
		rh7mSameInstant(t, label, want.CreatedAt, &got[0].CreatedAt)
		assert.Equal(t, want, got[0], label)
	}

	byStack, err := db.GetActionsByStack(want.StackID, 10)
	require.NoError(t, err)
	check("GetActionsByStack", byStack)

	recent, err := db.GetRecentActions(10)
	require.NoError(t, err)
	check("GetRecentActions", recent)

	filtered, _, err := db.ListActionLogsFiltered(10, 0, ActionLogFilter{})
	require.NoError(t, err)
	check("ListActionLogsFiltered", filtered)
}

func TestUserReaders_ReturnEveryColumnInItsOwnField(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	want := models.User{
		ID:        "user-rh7m",
		Username:  "rh7m-name",
		Password:  "rh7m-password-hash",
		CreatedAt: time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC),
		UpdatedAt: time.Date(2026, 9, 3, 4, 5, 6, 0, time.UTC),
	}
	require.NoError(t, db.CreateUser(want))

	check := func(label string, got *models.User) {
		t.Helper()
		rh7mSameInstant(t, label+" CreatedAt", want.CreatedAt, &got.CreatedAt)
		rh7mSameInstant(t, label+" UpdatedAt", want.UpdatedAt, &got.UpdatedAt)
		assert.Equal(t, want, *got, label)
	}

	byName, err := db.GetUserByUsername(want.Username)
	require.NoError(t, err)
	check("GetUserByUsername", byName)

	byID, err := db.GetUserByID(want.ID)
	require.NoError(t, err)
	check("GetUserByID", byID)

	sole, err := db.GetSoleUser()
	require.NoError(t, err)
	check("GetSoleUser", sole)
}

func TestDirectoryReaders_ReturnEveryColumnInItsOwnField(t *testing.T) {
	t.Parallel()
	// A sensitive token is refused without an encryptor (fail closed).
	db := newTestDBWithEncryptor(t)

	scanned := time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path:      "/srv/rh7m",
		Name:      "rh7m-name",
		RootDir:   "/srv",
		IsGitRepo: true,
		GitRemote: "https://example.invalid/rh7m.git",
		GitBranch: "rh7m-branch",
		ScannedAt: scanned,
	}))
	require.NoError(t, db.UpdateDirectoryCredentials("/srv/rh7m", "https", "/keys/rh7m", "rh7m-user", "rh7m-token"))

	// The token never leaves the full-row readers; only its presence does.
	want := models.Directory{
		Path:          "/srv/rh7m",
		Name:          "rh7m-name",
		RootDir:       "/srv",
		IsGitRepo:     true,
		GitRemote:     "https://example.invalid/rh7m.git",
		GitBranch:     "rh7m-branch",
		GitAuthType:   "https",
		GitSSHKeyPath: "/keys/rh7m",
		GitHTTPSUser:  "rh7m-user",
		HasHTTPSToken: true,
		ScannedAt:     scanned,
	}

	list, err := db.ListDirectories()
	require.NoError(t, err)
	require.Len(t, list, 1)
	rh7mSameInstant(t, "ListDirectories", want.ScannedAt, &list[0].ScannedAt)
	assert.Equal(t, want, list[0], "ListDirectories")

	byPath, err := db.GetDirectory(want.Path)
	require.NoError(t, err)
	rh7mSameInstant(t, "GetDirectory", want.ScannedAt, &byPath.ScannedAt)
	assert.Equal(t, want, *byPath, "GetDirectory")
}

func TestBackupPolicyReaders_ReturnEveryColumnInItsOwnField(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)

	want := models.BackupPolicy{
		ID:         "policy-rh7m",
		TargetType: "stack",
		TargetID:   "stack-rh7m",
		Enabled:    true,
		StopPolicy: "hot",
		CreatedAt:  "2026-09-02T03:04:05Z",
		UpdatedAt:  "2026-09-03T04:05:06Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(&want))

	all, err := db.GetBackupPolicies()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, want, all[0], "GetBackupPolicies")

	one, err := db.GetBackupPolicy(want.TargetID)
	require.NoError(t, err)
	assert.Equal(t, want, *one, "GetBackupPolicy")

	enabled, err := db.GetEnabledBackupPolicies()
	require.NoError(t, err)
	require.Len(t, enabled, 1)
	assert.Equal(t, want, enabled[0], "GetEnabledBackupPolicies")
}

func TestAutoUpdatePolicyReaders_ReturnEveryColumnInItsOwnField(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)

	// paused stays false so GetEnabledAutoUpdatePolicies returns the row;
	// enabled true against paused false still tells the two bools apart.
	want := models.AutoUpdatePolicy{
		ID:                  "auto-rh7m",
		TargetType:          "container",
		TargetID:            "container-rh7m",
		Enabled:             true,
		ConsecutiveFailures: 2,
		Paused:              false,
		CreatedAt:           "2026-09-02T03:04:05Z",
		UpdatedAt:           "2026-09-03T04:05:06Z",
	}
	require.NoError(t, db.UpsertAutoUpdatePolicy(&want))

	all, err := db.GetAutoUpdatePolicies()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, want, all[0], "GetAutoUpdatePolicies")

	one, err := db.GetAutoUpdatePolicy(want.TargetType, want.TargetID)
	require.NoError(t, err)
	assert.Equal(t, want, *one, "GetAutoUpdatePolicy")

	enabled, err := db.GetEnabledAutoUpdatePolicies()
	require.NoError(t, err)
	require.Len(t, enabled, 1)
	assert.Equal(t, want, enabled[0], "GetEnabledAutoUpdatePolicies")
}

func TestBackupRunItemReaders_ReturnEveryColumnInItsOwnField(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)

	require.NoError(t, db.CreateBackupRun(&models.BackupRun{
		ID: "run-rh7m", Kind: "backup", Trigger: "manual", Status: "failed",
		StartedAt: "2026-09-02T03:04:05Z", ErrorMessage: "run-level error",
	}))
	want := models.BackupRunItem{
		ID:           "item-rh7m",
		RunID:        "run-rh7m",
		StackID:      "stack-rh7m",
		Status:       "success",
		SnapshotID:   "snapshot-rh7m",
		StopApplied:  true,
		DurationMs:   1234,
		ErrorMessage: "item-level error",
	}
	require.NoError(t, db.AddBackupRunItem(&want))

	byRun, err := db.GetBackupRunItems(want.RunID)
	require.NoError(t, err)
	require.Len(t, byRun, 1)
	assert.Equal(t, want, byRun[0], "GetBackupRunItems")

	// The JOINed reader: backup_runs has its own id, status and error_message,
	// which must not stand in for the item's.
	latest, err := db.GetLatestRunItemForStack(want.StackID)
	require.NoError(t, err)
	assert.Equal(t, want, *latest, "GetLatestRunItemForStack")
}
