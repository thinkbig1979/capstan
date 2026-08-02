package database

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// newTestDB creates an in-memory DB with all migrations applied.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// --- Backup Policy tests ---

func TestGetBackupPolicies_Empty(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

func TestUpsertBackupPolicy_Insert(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p := &models.BackupPolicy{
		ID:         "bp-001",
		TargetType: "stack",
		TargetID:   "stacks~myapp",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(p))

	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, p.ID, policies[0].ID)
	assert.Equal(t, p.TargetID, policies[0].TargetID)
	assert.True(t, policies[0].Enabled)
	assert.Equal(t, "stop", policies[0].StopPolicy)
}

func TestUpsertBackupPolicy_OverwriteOnConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	original := &models.BackupPolicy{
		ID:         "bp-001",
		TargetType: "stack",
		TargetID:   "stacks~myapp",
		Enabled:    false,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(original))

	// Upsert with same target_type+target_id but different values.
	updated := &models.BackupPolicy{
		ID:         "bp-001-updated",
		TargetType: "stack",
		TargetID:   "stacks~myapp",
		Enabled:    true,
		StopPolicy: "hot",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T01:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(updated))

	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1, "upsert must not create a duplicate row")
	assert.True(t, policies[0].Enabled, "enabled must be updated to true")
	assert.Equal(t, "hot", policies[0].StopPolicy, "stop_policy must be updated")
}

func TestGetBackupPolicy_ByTargetID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p := &models.BackupPolicy{
		ID:         "bp-002",
		TargetType: "stack",
		TargetID:   "stacks~web",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(p))

	got, err := db.GetBackupPolicy("stacks~web")
	require.NoError(t, err)
	assert.Equal(t, "bp-002", got.ID)

	_, err = db.GetBackupPolicy("stacks~nonexistent")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestGetEnabledBackupPolicies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	enabled := &models.BackupPolicy{
		ID:         "bp-en",
		TargetType: "stack",
		TargetID:   "stacks~enabled",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	disabled := &models.BackupPolicy{
		ID:         "bp-dis",
		TargetType: "stack",
		TargetID:   "stacks~disabled",
		Enabled:    false,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(enabled))
	require.NoError(t, db.UpsertBackupPolicy(disabled))

	policies, err := db.GetEnabledBackupPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "bp-en", policies[0].ID)
}

func TestDeleteBackupPolicy(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p := &models.BackupPolicy{
		ID:         "bp-del",
		TargetType: "stack",
		TargetID:   "stacks~todelete",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(p))

	require.NoError(t, db.DeleteBackupPolicy("stacks~todelete"))

	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

// --- Backup Run tests ---

func TestCreateBackupRun_AndGetBackupRuns(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:           "run-001",
		Kind:         "backup",
		Trigger:      "manual",
		Status:       "running",
		StartedAt:    "2026-05-30T10:00:00Z",
		StacksTotal:  3,
		StacksOK:     0,
		StacksFailed: 0,
	}
	require.NoError(t, db.CreateBackupRun(run))

	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "run-001", runs[0].ID)
	assert.Equal(t, "backup", runs[0].Kind)
	assert.Equal(t, "manual", runs[0].Trigger)
	assert.Equal(t, "running", runs[0].Status)
	assert.Equal(t, 3, runs[0].StacksTotal)
}

func TestUpdateBackupRun(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:        "run-upd",
		Kind:      "sync",
		Trigger:   "scheduled",
		Status:    "running",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run))

	finishedAt := "2026-05-30T10:05:00Z"
	run.Status = "success"
	run.FinishedAt = &finishedAt
	run.StacksTotal = 2
	run.StacksOK = 2
	run.StacksFailed = 0
	require.NoError(t, db.UpdateBackupRun(run))

	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "success", runs[0].Status)
	assert.Equal(t, 2, runs[0].StacksOK)
	require.NotNil(t, runs[0].FinishedAt)
	assert.Equal(t, "2026-05-30T10:05:00Z", *runs[0].FinishedAt)
}

func TestGetBackupRuns_Limit(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	for _, id := range []string{"run-a", "run-b", "run-c"} {
		r := &models.BackupRun{
			ID:        id,
			Kind:      "backup",
			Trigger:   "manual",
			Status:    "success",
			StartedAt: "2026-05-30T10:00:00Z",
		}
		require.NoError(t, db.CreateBackupRun(r))
	}

	runs, err := db.GetBackupRuns(2)
	require.NoError(t, err)
	assert.Len(t, runs, 2, "limit parameter must be respected")
}

// --- Backup Run Sweep tests (agent-os-pid) ---

// TestSweepInterruptedBackupRuns_OpenedAtStartup_ReachesTerminalState pins the
// startup path end-to-end rather than calling the sweep function directly: it
// writes a real file-backed database with a 'running' row still in it (as a
// killed process or a mid-run snapshot would leave behind), closes it, then
// reopens it exactly the way main.go does. The reopen must alone be enough to
// bring the row to a terminal state — nothing else runs between NewWithMigrations
// returning and this assertion.
func TestSweepInterruptedBackupRuns_OpenedAtStartup_ReachesTerminalState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seed, err := NewWithMigrations(dir)
	require.NoError(t, err)

	stuck := &models.BackupRun{
		ID:        "run-stuck",
		Kind:      "backup",
		Trigger:   "scheduled",
		Status:    "running",
		StartedAt: "2026-07-31T17:50:00Z",
	}
	require.NoError(t, seed.CreateBackupRun(stuck))
	require.NoError(t, seed.Close())

	// Reopen exactly as main.go does (database.NewWithMigrationsAndEncryptor
	// wraps NewWithMigrations with an encryptor argument; NewWithMigrations
	// exercises the same startup path).
	restarted, err := NewWithMigrations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { restarted.Close() })

	got, err := restarted.GetBackupRunByID("run-stuck")
	require.NoError(t, err)
	assert.Equal(t, "failed", got.Status, "a run still 'running' at startup must reach a terminal state")
	require.NotNil(t, got.FinishedAt, "finished_at must be set, not left null")
	assert.NotEmpty(t, *got.FinishedAt)
	assert.NotEmpty(t, got.ErrorMessage, "an operator needs to know why the run never completed")
}

// TestSweepInterruptedBackupRuns_Idempotent_PreservesTerminalRows asserts that
// running the sweep twice does not corrupt or re-touch a row that already
// reached a real terminal state on its own (e.g. a completed 'success' run).
func TestSweepInterruptedBackupRuns_Idempotent_PreservesTerminalRows(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	finishedAt := "2026-05-30T10:05:00Z"
	completed := &models.BackupRun{
		ID:         "run-done",
		Kind:       "backup",
		Trigger:    "manual",
		Status:     "success",
		StartedAt:  "2026-05-30T10:00:00Z",
		FinishedAt: &finishedAt,
	}
	require.NoError(t, db.CreateBackupRun(completed))

	stuck := &models.BackupRun{
		ID:        "run-stuck",
		Kind:      "sync",
		Trigger:   "scheduled",
		Status:    "running",
		StartedAt: "2026-05-30T11:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(stuck))

	n, err := db.SweepInterruptedBackupRuns()
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the running row should be touched")

	// Second pass must be a no-op: nothing left at 'running' to touch.
	n, err = db.SweepInterruptedBackupRuns()
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a second sweep must not re-touch already-terminal rows")

	stillDone, err := db.GetBackupRunByID("run-done")
	require.NoError(t, err)
	assert.Equal(t, "success", stillDone.Status, "an already-successful run must not be reclassified as failed")
	require.NotNil(t, stillDone.FinishedAt)
	assert.Equal(t, finishedAt, *stillDone.FinishedAt, "an already-terminal row's finished_at must not be overwritten")

	nowFailed, err := db.GetBackupRunByID("run-stuck")
	require.NoError(t, err)
	assert.Equal(t, "failed", nowFailed.Status)
}

// TestSweepInterruptedBackupRuns_NeverTouchesTerminalRows asserts the sweep's
// WHERE clause — not just repeated calls — is what protects history. This is a
// different property from idempotency: a database that never had a 'running'
// row to begin with must come through a single pass byte-identical. If the
// WHERE clause were ever widened (e.g. to match on kind or a date range
// instead of status), this is what would catch it; the idempotency test above
// would not, since after one correct sweep there is nothing left at 'running'
// for a second call to expose.
func TestSweepInterruptedBackupRuns_NeverTouchesTerminalRows(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	finishedAt := "2026-05-30T10:05:00Z"
	completed := &models.BackupRun{
		ID:         "run-done-only",
		Kind:       "backup",
		Trigger:    "manual",
		Status:     "success",
		StartedAt:  "2026-05-30T10:00:00Z",
		FinishedAt: &finishedAt,
	}
	require.NoError(t, db.CreateBackupRun(completed))

	n, err := db.SweepInterruptedBackupRuns()
	require.NoError(t, err)
	assert.Equal(t, 0, n, "there is no 'running' row for the sweep to touch")

	got, err := db.GetBackupRunByID("run-done-only")
	require.NoError(t, err)
	assert.Equal(t, "success", got.Status, "status must be untouched")
	require.NotNil(t, got.FinishedAt)
	assert.Equal(t, finishedAt, *got.FinishedAt, "finished_at must be byte-identical to what was written")
	assert.Empty(t, got.ErrorMessage, "a successful run must not gain an error_message")
}

// --- Backup Run Item tests ---

func TestAddBackupRunItem_AndGetBackupRunItems(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:        "run-items",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "success",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run))

	item := &models.BackupRunItem{
		ID:          "item-001",
		RunID:       "run-items",
		StackID:     "stacks~myapp",
		Status:      "success",
		SnapshotID:  "abc12345",
		StopApplied: true,
		DurationMs:  4200,
	}
	require.NoError(t, db.AddBackupRunItem(item))

	items, err := db.GetBackupRunItems("run-items")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "item-001", items[0].ID)
	assert.Equal(t, "stacks~myapp", items[0].StackID)
	assert.Equal(t, "abc12345", items[0].SnapshotID)
	assert.True(t, items[0].StopApplied)
	assert.Equal(t, int64(4200), items[0].DurationMs)
}

// ─────────────────────────────────────────────────────────────
// Ciphertext-at-rest assertion (Parked Follow-up 2 / a70.3)
// ─────────────────────────────────────────────────────────────

// testAESGCMEncryptor is a minimal AES-GCM TokenEncryptor for DB tests.
// It is intentionally local to avoid importing the services package (which
// would create an import cycle). Its behaviour is identical to the production
// services.TokenEncryptor.
type testAESGCMEncryptor struct {
	aead cipher.AEAD
}

func newTestEncryptor(t *testing.T, secret string) *testAESGCMEncryptor {
	t.Helper()
	hash := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(hash[:])
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)
	return &testAESGCMEncryptor{aead: aead}
}

func (e *testAESGCMEncryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := e.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (e *testAESGCMEncryptor) Decrypt(encoded string) (string, error) {
	ct, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	ns := e.aead.NonceSize()
	if len(ct) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	pt, err := e.aead.Open(nil, ct[:ns], ct[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// rawSettingValue reads the raw (un-decrypted) value from the settings table.
// It bypasses GetSetting so we can assert the stored bytes are ciphertext.
func rawSettingValue(t *testing.T, db *DB, key string) string {
	t.Helper()
	var val string
	err := db.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	require.NoError(t, err)
	return val
}

// TestResticPasswordCiphertextAtRest asserts that SetSetting("restic_password", ...)
// stores ciphertext (not plaintext) in the settings table, satisfying the gate
// "plaintext never in DB". GetSetting decrypts transparently; the raw row value
// must not equal the original plaintext.
func TestResticPasswordCiphertextAtRest(t *testing.T) {
	t.Parallel()

	const plaintext = "super-secret-restic-passphrase"
	const secret = "test-aes-gcm-key-32chars-padding"

	enc := newTestEncryptor(t, secret)
	db, err := NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.SetSetting("restic_password", plaintext))

	// The raw DB value must NOT be the plaintext.
	raw := rawSettingValue(t, db, "restic_password")
	assert.NotEqual(t, plaintext, raw,
		"raw DB value must be ciphertext, not the plaintext password")

	// The raw value must be valid base64 (as produced by AES-GCM + base64 encoding).
	_, decodeErr := base64.StdEncoding.DecodeString(raw)
	assert.NoError(t, decodeErr, "raw DB value must be base64-encoded ciphertext")

	// GetSetting must still return the original plaintext (transparent decryption).
	got, err := db.GetSetting("restic_password")
	require.NoError(t, err)
	assert.Equal(t, plaintext, got, "GetSetting must return the original plaintext via transparent decryption")
}

// TestGitHTTPSTokenCiphertextAtRest performs the same ciphertext gate for the
// git_https_token sensitive key, confirming the generalised sensitive-key set
// works correctly for both keys.
func TestGitHTTPSTokenCiphertextAtRest(t *testing.T) {
	t.Parallel()

	const plaintext = "ghp_SomeGitHubPersonalAccessToken"
	const secret = "test-aes-gcm-key-32chars-padding"

	enc := newTestEncryptor(t, secret)
	db, err := NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.SetSetting("git_https_token", plaintext))

	raw := rawSettingValue(t, db, "git_https_token")
	assert.NotEqual(t, plaintext, raw,
		"raw DB value must be ciphertext for git_https_token")

	got, err := db.GetSetting("git_https_token")
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

// TestNonSensitiveSettingNotEncrypted confirms that non-sensitive keys (e.g.
// restic_repository) are stored as plaintext in the DB row.
func TestNonSensitiveSettingNotEncrypted(t *testing.T) {
	t.Parallel()

	const value = "/data/restic-repo"
	const secret = "test-aes-gcm-key-32chars-padding"

	enc := newTestEncryptor(t, secret)
	db, err := NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.SetSetting("restic_repository", value))

	raw := rawSettingValue(t, db, "restic_repository")
	assert.Equal(t, value, raw,
		"non-sensitive setting must be stored as plaintext")
}

func TestGetBackupRunItems_EmptyForUnknownRun(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	items, err := db.GetBackupRunItems("nonexistent-run")
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestGetLatestRunItemForStack(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run1 := &models.BackupRun{
		ID:        "run-first",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "success",
		StartedAt: "2026-05-30T09:00:00Z",
	}
	run2 := &models.BackupRun{
		ID:        "run-second",
		Kind:      "backup",
		Trigger:   "scheduled",
		Status:    "success",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run1))
	require.NoError(t, db.CreateBackupRun(run2))

	item1 := &models.BackupRunItem{
		ID:         "item-first",
		RunID:      "run-first",
		StackID:    "stacks~myapp",
		Status:     "success",
		SnapshotID: "snap-old",
		DurationMs: 1000,
	}
	item2 := &models.BackupRunItem{
		ID:         "item-second",
		RunID:      "run-second",
		StackID:    "stacks~myapp",
		Status:     "success",
		SnapshotID: "snap-new",
		DurationMs: 800,
	}
	require.NoError(t, db.AddBackupRunItem(item1))
	require.NoError(t, db.AddBackupRunItem(item2))

	got, err := db.GetLatestRunItemForStack("stacks~myapp")
	require.NoError(t, err)
	assert.Equal(t, "item-second", got.ID, "must return the item from the most recent run")
	assert.Equal(t, "snap-new", got.SnapshotID)
}

func TestGetLatestRunItemForStack_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := db.GetLatestRunItemForStack("stacks~nonexistent")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestFKCascade_DeleteRunRemovesItems(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:        "run-cascade",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "success",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run))

	for i, sid := range []string{"stacks~a", "stacks~b"} {
		item := &models.BackupRunItem{
			ID:      fmt.Sprintf("item-casc-%d", i),
			RunID:   "run-cascade",
			StackID: sid,
			Status:  "success",
		}
		require.NoError(t, db.AddBackupRunItem(item))
	}

	items, err := db.GetBackupRunItems("run-cascade")
	require.NoError(t, err)
	assert.Len(t, items, 2)

	_, err = db.db.Exec("DELETE FROM backup_runs WHERE id = ?", "run-cascade")
	require.NoError(t, err)

	items, err = db.GetBackupRunItems("run-cascade")
	require.NoError(t, err)
	assert.Empty(t, items, "deleting a run must cascade-delete its items")
}

// --- Audit-log filtering tests ---

func seedActionLogs(t *testing.T, db *DB) {
	t.Helper()
	// Global actions (e.g. backup) are logged with a placeholder actor label
	// ("system") that has no matching users row. action_log.user_id is a
	// plain column with no foreign key (migration v9), so these insert
	// cleanly regardless of foreign_keys enforcement.
	entries := []models.ActionLog{
		{ID: "al-1", UserID: "system", Action: "backup", Detail: `{"status":"success"}`, CreatedAt: mustTime(t, "2026-05-29T10:00:00Z")},
		{ID: "al-2", UserID: "admin", Action: "stack_start", Detail: `{"stack":"web"}`, CreatedAt: mustTime(t, "2026-05-30T11:00:00Z")},
		{ID: "al-3", UserID: "admin", Action: "stack_stop", Detail: `{"stack":"web"}`, CreatedAt: mustTime(t, "2026-05-31T12:00:00Z")},
		{ID: "al-4", UserID: "system", Action: "backup", Detail: `{"status":"failed"}`, CreatedAt: mustTime(t, "2026-05-31T13:00:00Z")},
	}
	for _, e := range entries {
		require.NoError(t, db.LogAction(e))
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}

func TestListActionLogsFiltered_ByAction(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	seedActionLogs(t, db)

	rows, total, err := db.ListActionLogsFiltered(50, 0, ActionLogFilter{Action: "backup"})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, "backup", r.Action)
	}
}

func TestListActionLogsFiltered_Search(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	seedActionLogs(t, db)

	rows, total, err := db.ListActionLogsFiltered(50, 0, ActionLogFilter{Search: "failed"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, "al-4", rows[0].ID)
}

func TestListActionLogsFiltered_DateRange(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	seedActionLogs(t, db)

	rows, total, err := db.ListActionLogsFiltered(50, 0, ActionLogFilter{DateFrom: "2026-05-31", DateTo: "2026-05-31"})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "only the two 2026-05-31 entries match")
	assert.Len(t, rows, 2)
}

func TestListActionLogsFiltered_EmptyFilterReturnsAll(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	seedActionLogs(t, db)

	rows, total, err := db.ListActionLogsFiltered(50, 0, ActionLogFilter{})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Len(t, rows, 4)
}

func TestDistinctActionLogActions(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	seedActionLogs(t, db)

	actions, err := db.DistinctActionLogActions()
	require.NoError(t, err)
	assert.Equal(t, []string{"backup", "stack_start", "stack_stop"}, actions)
}
