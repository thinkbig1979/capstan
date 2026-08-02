package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type TokenEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

type DB struct {
	db        *sql.DB
	encryptor TokenEncryptor
}

func New(dataDir string) (*DB, error) {
	return NewWithEncryptor(dataDir, nil)
}

func NewWithEncryptor(dataDir string, encryptor TokenEncryptor) (*DB, error) {
	var dbPath string
	if dataDir == ":memory:" {
		dbPath = ":memory:"
	} else {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, err
		}
		dbPath = dataDir + "/capstan.db"
	}

	// foreign_keys and busy_timeout are connection-scoped SQLite pragmas: a
	// PRAGMA run via db.Exec only applies to whichever single connection
	// happens to service that call, not to every connection the pool later
	// opens. Putting them in the DSN's _pragma query parameter makes the
	// driver (modernc.org/sqlite) re-apply them on every new connection it
	// opens, so enforcement holds across the whole pool rather than just the
	// first connection. This works the same way for the ":memory:" DSN (the
	// driver strips the "?..." suffix before opening and applies the pragmas
	// afterward), so no separate "file::memory:" form is needed here.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if dataDir == ":memory:" {
		// In-memory SQLite has a per-connection database: each new connection gets
		// an independent empty DB. Force a single connection so that all callers
		// (including goroutines in background) share the same database.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(5 * time.Minute)
		db.SetConnMaxIdleTime(1 * time.Minute)
	}

	// journal_mode is database-scoped, not connection-scoped (it's persisted
	// in the database file/shared-memory region), so a single Exec on any one
	// pooled connection is sufficient here.
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		return nil, err
	}

	return &DB{db: db, encryptor: encryptor}, nil
}

func NewWithMigrations(dataDir string) (*DB, error) {
	return NewWithMigrationsAndEncryptor(dataDir, nil)
}

func NewWithMigrationsAndEncryptor(dataDir string, encryptor TokenEncryptor) (*DB, error) {
	db, err := NewWithEncryptor(dataDir, encryptor)
	if err != nil {
		return nil, err
	}
	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, err
	}

	// Run before anything else touches backup_runs: a row this process never
	// started can only be left over from a crash or a restore of a mid-run
	// snapshot (agent-os-pid), and either way it must not be shown as running.
	if n, err := db.SweepInterruptedBackupRuns(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sweep interrupted backup runs: %w", err)
	} else if n > 0 {
		slog.Warn("Marked interrupted backup runs as failed on startup", "count", n)
	}

	return db, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

// VacuumInto writes a consistent point-in-time copy of the database to dest
// using SQLite's `VACUUM INTO`.
//
// This is not a convenience wrapper around a file copy — it is the only correct
// way to snapshot this database. capstan.db runs in WAL mode (see the
// journal_mode pragma above), so the .db file on disk is not self-contained:
// recent commits live in the -wal sidecar until a checkpoint. Copying the file
// while writes are in flight yields a torn or stale database. VACUUM INTO runs
// through the SQL layer, so it observes a single consistent snapshot and emits
// a standalone, already-compacted database with no sidecar files.
//
// It does not block writers.
//
// dest must not already exist — SQLite refuses to overwrite, and that refusal
// is deliberate rather than something to work around: silently clobbering a
// previous snapshot would destroy the artifact a restore depends on. Callers
// remove a stale file explicitly if they intend to replace it.
func (d *DB) VacuumInto(dest string) error {
	// Parameter binding is not permitted for VACUUM INTO in SQLite, so the path
	// is interpolated. Callers construct dest from configuration, never from
	// user input; the quote-doubling keeps a path containing a single quote from
	// terminating the literal.
	quoted := "'" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := d.db.Exec("VACUUM INTO " + quoted); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	return nil
}
