package database

import (
	"database/sql"
	"os"
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
	return db, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) decryptToken(token string) string {
	if d.encryptor != nil && token != "" {
		decrypted, err := d.encryptor.Decrypt(token)
		if err != nil {
			return token
		}
		return decrypted
	}
	return token
}
