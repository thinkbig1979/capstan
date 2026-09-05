package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-l42o. resolveBackupConfig discarded the error from all ten of its DB
// reads, so "no row" and "the database is closed / the value will not decrypt"
// produced the same answer: the default local repository under <DataDir> and
// the RESTIC_PASSWORD env value. Every consumer that WRITES — backup, sync,
// prune, restore, DR restore — then ran against a repository the operator never
// configured, silently and with nothing logged.
//
// The instrument throughout is the injected commandRunner (SetResticMgrFactory /
// SetRcloneMgrFactory, backup.go:231/:238): a refusal means the runner records
// ZERO calls, and the pre-fix behaviour is visible as a recorded call whose env
// carries RESTIC_REPOSITORY=<DataDir>/restic-repo.

const (
	// dbFaultTestPassword is seeded as the stored restic password. Every log
	// assertion in this file checks it is ABSENT from the captured output: the
	// file comment at backup_config.go:84-86 requires the plaintext is never
	// logged, and an absence assertion is the only check that proves a newly
	// added ERROR line did not leak it.
	dbFaultTestPassword = "l42o-stored-restic-plaintext-do-not-log"

	// dbFaultEnvPassword is the RESTIC_PASSWORD env fallback. Its appearance in
	// a restic invocation is the pre-fix symptom: the wrong secret, silently.
	dbFaultEnvPassword = "l42o-env-fallback-password"

	dbFaultKeyOne = "l42o-storage-key-one-0123456789ab"
	dbFaultKeyTwo = "l42o-storage-key-two-ba9876543210"

	// dbFaultConfiguredRepo is a configured remote repository: it is nothing
	// like <DataDir>/restic-repo, so a substitution is unmistakable.
	dbFaultConfiguredRepo = "rest:https://backup.example.invalid/capstan"
)

// closedDBWithSettings seeds settings into a fully migrated on-disk database and
// then closes its connection, so every later read fails with a driver error
// rather than sql.ErrNoRows. This is the faultyDB(t) shape of agent-os-2mhb
// (handlers/faulty_db_test.go:28), reproduced here because that helper lives in
// package handlers.
//
// It models the unencrypted-key fault: a closed, locked or otherwise unreadable
// database. It is NOT the instrument for restic_password — see rotatedKeyDB.
func closedDBWithSettings(t *testing.T, settings map[string]string) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrationsAndEncryptor(t.TempDir(), NewTokenEncryptorOrDefault(dbFaultKeyOne, ""))
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	for k, v := range settings {
		if err := db.SetSetting(k, v); err != nil {
			t.Fatalf("seed setting %q: %v", k, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db to induce failure: %v", err)
	}
	return db
}

// rotatedKeyDB seeds restic_repository (never encrypted) and restic_password
// (encrypted) under STORAGE_KEY A, then reopens the same directory under key B.
// The database is healthy and unlocked; only the password fails, in
// GetSetting's encryptor branch (database/settings.go:21-24).
//
// A closed database never reaches that branch, and a STORAGE_KEY rotation is
// the realistic production fault, so this — not closedDBWithSettings — is the
// instrument for the password key. sensitiveSettingKeys is exactly
// {git_https_token, restic_password} (database/settings.go:9-12), so the
// rotation produces this fault for the password key ONLY.
func rotatedKeyDB(t *testing.T, repo string) *database.DB {
	t.Helper()
	dataDir := t.TempDir()

	db1, err := database.NewWithMigrationsAndEncryptor(dataDir, NewTokenEncryptorOrDefault(dbFaultKeyOne, ""))
	if err != nil {
		t.Fatalf("open database under key one: %v", err)
	}
	if repo != "" {
		if err := db1.SetSetting("restic_repository", repo); err != nil {
			t.Fatalf("seed restic_repository: %v", err)
		}
	}
	if err := db1.SetSetting("restic_password", dbFaultTestPassword); err != nil {
		t.Fatalf("seed restic_password: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close database under key one: %v", err)
	}

	db2, err := database.NewWithMigrationsAndEncryptor(dataDir, NewTokenEncryptorOrDefault(dbFaultKeyTwo, ""))
	if err != nil {
		t.Fatalf("reopen database under key two: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	return db2
}

// dbFaultSvc builds a BackupService whose restic and rclone managers are built
// from the RESOLVED BackupConfig (not a fixed one), so a recorded call's env
// reveals which repository the resolver actually chose. The logger writes into
// the returned buffer rather than the process-global sink, which keeps the
// assertions independent of every other test in this package.
func dbFaultSvc(t *testing.T, db *database.DB, cfg *config.Config) (*BackupService, *fakeRunner, *fakeRunner, *bytes.Buffer, *fakeScheduler) {
	t.Helper()

	var logBuf bytes.Buffer
	restic := &fakeRunner{}
	rclone := &fakeRunner{outputData: []byte("config")}
	sched := &fakeScheduler{}

	svc := &BackupService{
		cfg:              cfg,
		db:               db,
		docker:           &fakeDocker{},
		opLock:           NewOperationLock(),
		actions:          NewActionLogger(db),
		logger:           slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		resticBin:        "/usr/bin/restic",
		rcloneBin:        "/usr/bin/rclone",
		sched:            sched,
		resticMgrFactory: func(bc BackupConfig) *ResticManager { return newResticManagerWithRunner(bc, restic, nil) },
		rcloneMgrFactory: func(bc BackupConfig) *RcloneManager { return newRcloneManagerWithRunner(bc, rclone, nil) },
	}
	return svc, restic, rclone, &logBuf, sched
}

// drainedOut returns a buffered StreamLine channel that is continuously drained,
// so a consumer under test never blocks on it.
func drainedOut(t *testing.T) chan StreamLine {
	t.Helper()
	out := make(chan StreamLine, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range out { //nolint:revive // drain
		}
	}()
	t.Cleanup(func() {
		close(out)
		<-done
	})
	return out
}

// repoEnvOf returns the RESTIC_REPOSITORY value from a recorded call's env, or
// "" when the call carries none.
func repoEnvOf(c fakeCall) string {
	for _, e := range c.Env {
		if strings.HasPrefix(e, "RESTIC_REPOSITORY=") {
			return strings.TrimPrefix(e, "RESTIC_REPOSITORY=")
		}
	}
	return ""
}

// assertNoResticInvocation is the "no restic ran" half of a refusal. It names
// the repository the runner was pointed at, because a report of "1 call" with
// no path does not say whether the wrong repository was touched.
func assertNoResticInvocation(t *testing.T, r *fakeRunner, what string) {
	t.Helper()
	if len(r.calls) != 0 {
		t.Fatalf("%s: restic was invoked %d time(s) despite an unreadable setting; first call repository=%q args=%v",
			what, len(r.calls), repoEnvOf(r.calls[0]), r.calls[0].Args)
	}
}

// assertRefusalLogged requires exactly the shape acceptance asks for: at least
// one ERROR line carrying a cause= attribute.
func assertRefusalLogged(t *testing.T, logs *bytes.Buffer, what string) {
	t.Helper()
	out := logs.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "cause=") {
		t.Fatalf("%s: expected an ERROR line carrying cause=; captured log was:\n%s", what, out)
	}
}

// assertNoPlaintextLeak is the absence arm demanded by acceptance item 3. It
// covers both secrets: the stored plaintext and the env fallback.
func assertNoPlaintextLeak(t *testing.T, logs *bytes.Buffer, what string) {
	t.Helper()
	out := logs.String()
	if strings.Contains(out, dbFaultTestPassword) {
		t.Fatalf("%s: the stored restic password plaintext appeared in the log:\n%s", what, out)
	}
	if strings.Contains(out, dbFaultEnvPassword) {
		t.Fatalf("%s: the env-fallback restic password appeared in the log:\n%s", what, out)
	}
}

// --- fixture controls -------------------------------------------------------

// TestClosedDBFailsDifferentlyFromHealthyNotFound is the two-sided proof that
// closedDBWithSettings exercises a NON-not-found failure. Without both arms a
// database that always failed with sql.ErrNoRows would be indistinguishable
// from a working one, and every refusal test below would pass for the wrong
// reason.
func TestClosedDBFailsDifferentlyFromHealthyNotFound(t *testing.T) {
	broken := closedDBWithSettings(t, map[string]string{"restic_repository": dbFaultConfiguredRepo})
	_, err := broken.GetSetting("restic_repository")
	if err == nil {
		t.Fatal("closed db returned no error; the fixture did not induce a failure")
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("closed db failed with sql.ErrNoRows (%v) — indistinguishable from an absent row", err)
	}

	healthy := newTestDB(t)
	_, err = healthy.GetSetting("restic_repository")
	if err == nil {
		t.Fatal("healthy db returned no error for a missing row; the positive control never fired")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("healthy db missing row did not yield sql.ErrNoRows, got %v", err)
	}
}

// TestRotatedKeyDBIsHealthyButPasswordUnreadable proves the password fixture is
// discriminating in BOTH directions: the unencrypted repository key still reads
// correctly (so the database is genuinely healthy and unlocked) while the
// encrypted password key fails.
func TestRotatedKeyDBIsHealthyButPasswordUnreadable(t *testing.T) {
	db := rotatedKeyDB(t, dbFaultConfiguredRepo)

	repo, err := db.GetSetting("restic_repository")
	if err != nil {
		t.Fatalf("restic_repository must still read on a healthy rotated-key db, got %v", err)
	}
	if repo != dbFaultConfiguredRepo {
		t.Fatalf("restic_repository = %q; want %q", repo, dbFaultConfiguredRepo)
	}

	pw, err := db.GetSetting("restic_password")
	if err == nil {
		t.Fatalf("restic_password still decrypted under the rotated key (got %q); the fixture is not discriminating", pw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("restic_password failed with sql.ErrNoRows (%v) — that is absence, not a decrypt failure", err)
	}
}

// --- the refusal arms -------------------------------------------------------

// dbFaultCase drives one write-path consumer.
type dbFaultCase struct {
	name string
	// call invokes the consumer and returns the error it reported, or nil when
	// the consumer has no error return (StartScheduler, CheckRepository).
	call func(t *testing.T, svc *BackupService, out chan StreamLine) error
	// wantErr is false only for the consumers whose refusal is carried by a
	// value rather than an error.
	wantErr bool
	// extra runs consumer-specific assertions after the call.
	extra func(t *testing.T, svc *BackupService, sched *fakeScheduler)
}

func dbFaultCases() []dbFaultCase {
	return []dbFaultCase{
		{
			name:    "RunBackup",
			wantErr: true,
			call: func(_ *testing.T, svc *BackupService, out chan StreamLine) error {
				_, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
				return err
			},
		},
		{
			name:    "RunBackupWithRunID",
			wantErr: true,
			call: func(t *testing.T, svc *BackupService, out chan StreamLine) error {
				_, err := svc.RunBackupWithRunID(context.Background(), "l42o-run", nil, false, "manual", out)
				return err
			},
		},
		{
			name:    "RunSync",
			wantErr: true,
			call: func(_ *testing.T, svc *BackupService, out chan StreamLine) error {
				return svc.RunSync(context.Background(), out)
			},
		},
		{
			name:    "RunRestore",
			wantErr: true,
			call: func(_ *testing.T, svc *BackupService, out chan StreamLine) error {
				return svc.RunRestore(context.Background(), "stack-1", "snap-1", "", out)
			},
		},
		{
			name:    "RunDRRestore",
			wantErr: true,
			call: func(_ *testing.T, svc *BackupService, out chan StreamLine) error {
				return svc.RunDRRestore(context.Background(), out)
			},
		},
		{
			name:    "Prune",
			wantErr: true,
			call: func(_ *testing.T, svc *BackupService, out chan StreamLine) error {
				return svc.Prune(context.Background(), false, out)
			},
		},
		{
			name:    "CheckRepository",
			wantErr: false,
			call: func(t *testing.T, svc *BackupService, _ chan StreamLine) error {
				av := svc.CheckRepository(context.Background())
				if av.Available || av.RepoReachable {
					t.Fatalf("CheckRepository reported Available=%v RepoReachable=%v on an unreadable setting; "+
						"this is the gate the restore pre-check and Prune consult", av.Available, av.RepoReachable)
				}
				if av.Message == "" {
					t.Fatal("CheckRepository refused without a Message naming the cause class")
				}
				return nil
			},
		},
		{
			name:    "StartScheduler",
			wantErr: false,
			call: func(_ *testing.T, svc *BackupService, _ chan StreamLine) error {
				svc.StartScheduler()
				return nil
			},
			extra: func(t *testing.T, svc *BackupService, sched *fakeScheduler) {
				if svc.SchedulerRunning() {
					t.Fatal("StartScheduler marked the scheduler active from an unreadable configuration")
				}
				sched.mu.Lock()
				started := sched.started
				sched.mu.Unlock()
				if started {
					t.Fatal("StartScheduler started the scheduler from an unreadable configuration")
				}
			},
		},
	}
}

// TestBackupWritePaths_RefuseWhenRepositorySettingUnreadable is the closed-DB
// arm: a configured restic_repository exists on disk but cannot be read. Every
// consumer must refuse rather than fall through to <DataDir>/restic-repo.
func TestBackupWritePaths_RefuseWhenRepositorySettingUnreadable(t *testing.T) {
	for _, tc := range dbFaultCases() {
		t.Run(tc.name, func(t *testing.T) {
			db := closedDBWithSettings(t, map[string]string{
				"restic_repository": dbFaultConfiguredRepo,
			})
			cfg := &config.Config{
				DataDir:        t.TempDir(),
				ResticPassword: dbFaultEnvPassword,
				RcloneRemote:   "l42o-remote",
				RclonePath:     "l42o/path",
			}
			svc, restic, rclone, logs, sched := dbFaultSvc(t, db, cfg)

			err := tc.call(t, svc, drainedOut(t))
			if tc.wantErr && err == nil {
				t.Fatalf("%s returned no error on an unreadable restic_repository", tc.name)
			}

			assertNoResticInvocation(t, restic, tc.name)
			if len(rclone.calls) != 0 {
				t.Fatalf("%s: rclone was invoked %d time(s) despite an unreadable setting", tc.name, len(rclone.calls))
			}
			assertRefusalLogged(t, logs, tc.name)
			assertNoPlaintextLeak(t, logs, tc.name)

			if tc.extra != nil {
				tc.extra(t, svc, sched)
			}
		})
	}
}

// TestBackupWritePaths_RefuseWhenResticPasswordCannotBeDecrypted is the
// password arm, on its own instrument. The database is healthy and unlocked;
// only the encrypted restic_password fails, because STORAGE_KEY was rotated.
//
// Pre-fix this is the silent-substitution case in its purest form: the stored
// password is unreadable, so the resolver hands restic the RESTIC_PASSWORD env
// value instead — a different secret against a repository the operator does
// believe is theirs.
func TestBackupWritePaths_RefuseWhenResticPasswordCannotBeDecrypted(t *testing.T) {
	for _, tc := range dbFaultCases() {
		t.Run(tc.name, func(t *testing.T) {
			db := rotatedKeyDB(t, dbFaultConfiguredRepo)
			cfg := &config.Config{
				DataDir:        t.TempDir(),
				ResticPassword: dbFaultEnvPassword,
				RcloneRemote:   "l42o-remote",
				RclonePath:     "l42o/path",
			}
			svc, restic, rclone, logs, sched := dbFaultSvc(t, db, cfg)

			err := tc.call(t, svc, drainedOut(t))
			if tc.wantErr && err == nil {
				t.Fatalf("%s returned no error on an undecryptable restic_password", tc.name)
			}

			assertNoResticInvocation(t, restic, tc.name)
			if len(rclone.calls) != 0 {
				t.Fatalf("%s: rclone was invoked %d time(s) despite an undecryptable password", tc.name, len(rclone.calls))
			}
			assertRefusalLogged(t, logs, tc.name)
			assertNoPlaintextLeak(t, logs, tc.name)

			if tc.extra != nil {
				tc.extra(t, svc, sched)
			}
		})
	}
}

// TestResolveBackupConfig_PasswordRefusalDoesNotStringifyDecryptError requires
// the password branch to log a fixed cause CLASS, never the underlying error.
// handlers/directories.go:281-282 refuses to log a decrypt error in as many
// words: it "can wrap crypto output, and logging it risks writing ciphertext or
// derived key material to disk for no operator benefit". The same reasoning
// applies here, so the ERROR line must not carry the decrypt error's text.
func TestResolveBackupConfig_PasswordRefusalDoesNotStringifyDecryptError(t *testing.T) {
	db := rotatedKeyDB(t, dbFaultConfiguredRepo)

	// The exact error text GetSetting returns for this row, captured from the
	// same database the service will read.
	_, rawErr := db.GetSetting("restic_password")
	if rawErr == nil {
		t.Fatal("fixture is not discriminating: restic_password decrypted under the rotated key")
	}

	cfg := &config.Config{DataDir: t.TempDir(), ResticPassword: dbFaultEnvPassword}
	svc, _, _, logs, _ := dbFaultSvc(t, db, cfg)

	if err := svc.Prune(context.Background(), false, drainedOut(t)); err == nil {
		t.Fatal("Prune did not refuse on an undecryptable restic_password")
	}

	out := logs.String()
	if strings.Contains(out, rawErr.Error()) {
		t.Fatalf("the refusal log stringified the decrypt error %q:\n%s", rawErr.Error(), out)
	}
	assertNoPlaintextLeak(t, logs, "Prune password refusal")
}

// --- the controls: absence must keep today's behaviour byte-for-byte ---------

// TestResolveBackupConfig_HealthyDBNoRowKeepsFallbackChain is the first control
// on the SAME instrument as the refusal arms. A healthy database with no
// restic_repository row must still produce <DataDir>/restic-repo and must NOT
// log an ERROR for the operation.
func TestResolveBackupConfig_HealthyDBNoRowKeepsFallbackChain(t *testing.T) {
	db := newTestDB(t)
	dataDir := t.TempDir()
	cfg := &config.Config{DataDir: dataDir, ResticPassword: dbFaultEnvPassword}
	svc, restic, _, logs, _ := dbFaultSvc(t, db, cfg)

	if err := svc.Prune(context.Background(), false, drainedOut(t)); err != nil {
		t.Fatalf("Prune on a healthy db with no configured repository must succeed, got %v", err)
	}
	if len(restic.calls) == 0 {
		t.Fatal("restic was never invoked; the control never fired")
	}
	want := filepath.Join(dataDir, "restic-repo")
	if got := repoEnvOf(restic.calls[0]); got != want {
		t.Fatalf("repository = %q; want the unchanged fallback %q", got, want)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("an absent row must not log an ERROR for this operation:\n%s", logs.String())
	}
}

// TestResolveBackupConfig_HealthyDBConfiguredRepoIsUsed is the second control:
// a healthy database WITH a configured remote must reach restic unchanged.
func TestResolveBackupConfig_HealthyDBConfiguredRepoIsUsed(t *testing.T) {
	db := newTestDB(t)
	if err := db.SetSetting("restic_repository", dbFaultConfiguredRepo); err != nil {
		t.Fatalf("seed restic_repository: %v", err)
	}
	cfg := &config.Config{DataDir: t.TempDir(), ResticPassword: dbFaultEnvPassword}
	svc, restic, _, logs, _ := dbFaultSvc(t, db, cfg)

	if err := svc.Prune(context.Background(), false, drainedOut(t)); err != nil {
		t.Fatalf("Prune on a healthy db with a configured repository must succeed, got %v", err)
	}
	if len(restic.calls) == 0 {
		t.Fatal("restic was never invoked; the control never fired")
	}
	if got := repoEnvOf(restic.calls[0]); got != dbFaultConfiguredRepo {
		t.Fatalf("repository = %q; want the configured %q", got, dbFaultConfiguredRepo)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("a healthy configured read must not log an ERROR:\n%s", logs.String())
	}
}

// --- the typed helpers and RepoSettingSources, on their own instrument ------
//
// The two fault arms above both trip on the resolver's FIRST or SECOND read
// (restic_repository under a closed DB, restic_password under a rotated key),
// so neither ever drives an error INTO resolveIntSetting / resolveStringSetting
// / resolveBoolSetting or into RepoSettingSources. That left eight of the ten
// converted keys — retention, auto-prune, the four schedule keys, sync-after,
// rclone transfers — and the whole of RepoSettingSources pinned by nothing: a
// mutation restoring the pre-fix `dbVal, _ := db.GetSetting(key)` in those four
// functions passed the entire suite.
//
// A fixture that faults ONE key part-way through the resolver is not
// constructible against the current schema: only restic_password is encrypted,
// so the rotated-key trick reaches no other key, and settings.value is NOT NULL,
// so a per-row unreadable value cannot be stored (probed: the INSERT is rejected
// with "NOT NULL constraint failed: settings.value"). These functions are
// unexported and this test is in their own package, so the direct call is both
// available and the tightest instrument: it exercises exactly the units a
// mutation would target.

// TestTypedSettingHelpers_RefuseOnUnreadableDB is the fault arm for the three
// typed helpers. Each must propagate the error rather than answer an unreadable
// database with its default.
func TestTypedSettingHelpers_RefuseOnUnreadableDB(t *testing.T) {
	broken := closedDBWithSettings(t, nil)

	t.Run("resolveIntSetting", func(t *testing.T) {
		got, err := resolveIntSetting(broken, "backup_keep_daily", "9", 7)
		if err == nil {
			t.Fatalf("resolveIntSetting returned no error on an unreadable database; got %d", got)
		}
		// The value must not be a plausible retention: a caller that ignores
		// the error should get something obviously wrong, not the default.
		if got != 0 {
			t.Fatalf("resolveIntSetting = %d on failure; want the zero value", got)
		}
	})

	t.Run("resolveStringSetting", func(t *testing.T) {
		got, err := resolveStringSetting(broken, "backup_schedule_mode", "scheduled", ScheduleModeInterval)
		if err == nil {
			t.Fatalf("resolveStringSetting returned no error on an unreadable database; got %q", got)
		}
		if got != "" {
			t.Fatalf("resolveStringSetting = %q on failure; want the zero value", got)
		}
	})

	t.Run("resolveBoolSetting", func(t *testing.T) {
		got, err := resolveBoolSetting(broken, "backup_auto_prune", "true", true)
		if err == nil {
			t.Fatalf("resolveBoolSetting returned no error on an unreadable database; got %v", got)
		}
		if got {
			t.Fatalf("resolveBoolSetting = %v on failure; want the zero value", got)
		}
	})
}

// TestTypedSettingHelpers_AbsentRowKeepsFallbackChain is the other side of the
// same instrument. Without it "returns an error" and "always fails" would be
// indistinguishable, and the fault arm above would pass against a helper that
// had simply stopped working.
func TestTypedSettingHelpers_AbsentRowKeepsFallbackChain(t *testing.T) {
	healthy := newTestDB(t)

	t.Run("resolveIntSetting/env", func(t *testing.T) {
		got, err := resolveIntSetting(healthy, "backup_keep_daily", "9", 7)
		if err != nil {
			t.Fatalf("absent row must not be an error: %v", err)
		}
		if got != 9 {
			t.Fatalf("resolveIntSetting = %d; want the env fallback 9", got)
		}
	})

	t.Run("resolveIntSetting/default", func(t *testing.T) {
		got, err := resolveIntSetting(healthy, "backup_keep_daily", "", 7)
		if err != nil {
			t.Fatalf("absent row must not be an error: %v", err)
		}
		if got != 7 {
			t.Fatalf("resolveIntSetting = %d; want the default 7", got)
		}
	})

	t.Run("resolveStringSetting/default", func(t *testing.T) {
		got, err := resolveStringSetting(healthy, "backup_schedule_mode", "", ScheduleModeInterval)
		if err != nil {
			t.Fatalf("absent row must not be an error: %v", err)
		}
		if got != ScheduleModeInterval {
			t.Fatalf("resolveStringSetting = %q; want %q", got, ScheduleModeInterval)
		}
	})

	t.Run("resolveBoolSetting/default", func(t *testing.T) {
		got, err := resolveBoolSetting(healthy, "backup_auto_prune", "", true)
		if err != nil {
			t.Fatalf("absent row must not be an error: %v", err)
		}
		if !got {
			t.Fatalf("resolveBoolSetting = %v; want the default true", got)
		}
	})
}

// TestRepoSettingSources_RefusesOnUnreadableDB is the fault arm for
// RepoSettingSources, whose five existing tests are all healthy-DB. Reporting
// "default"/"no password" for a database that could not be read would tell the
// operator nothing is configured when a repository and a password are.
func TestRepoSettingSources_RefusesOnUnreadableDB(t *testing.T) {
	broken := closedDBWithSettings(t, map[string]string{
		"restic_repository": dbFaultConfiguredRepo,
	})
	cfg := &config.Config{ResticRepository: "env-repo", ResticPassword: dbFaultEnvPassword}

	repoSrc, pwSrc, hasPassword, err := RepoSettingSources(broken, cfg)
	if err == nil {
		t.Fatalf("RepoSettingSources returned no error on an unreadable database; "+
			"repoSource=%q pwSource=%q hasPassword=%v", repoSrc, pwSrc, hasPassword)
	}
	// The classifications must not be reported alongside the error: an "env"
	// or "default" verdict derived from a read that never happened is the
	// silent substitution this bead exists to remove.
	if repoSrc != "" || pwSrc != "" || hasPassword {
		t.Fatalf("RepoSettingSources reported classifications on failure: repoSource=%q pwSource=%q hasPassword=%v",
			repoSrc, pwSrc, hasPassword)
	}
}

// TestRepoSettingSources_HealthyDBStillClassifies is the passing side of the
// same instrument: the fault arm above must not be satisfiable by a function
// that has simply stopped classifying anything.
func TestRepoSettingSources_HealthyDBStillClassifies(t *testing.T) {
	healthy := newTestDB(t)
	cfg := &config.Config{ResticRepository: "env-repo", ResticPassword: dbFaultEnvPassword}

	repoSrc, pwSrc, hasPassword, err := RepoSettingSources(healthy, cfg)
	if err != nil {
		t.Fatalf("healthy database must classify without an error: %v", err)
	}
	if repoSrc != settingSourceEnv {
		t.Fatalf("repoSource = %q; want %q", repoSrc, settingSourceEnv)
	}
	if pwSrc != settingSourceEnv {
		t.Fatalf("pwSource = %q; want %q", pwSrc, settingSourceEnv)
	}
	if !hasPassword {
		t.Fatal("hasPassword = false; the env password must still be seen")
	}
}
