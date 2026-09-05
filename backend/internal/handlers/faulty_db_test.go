package handlers

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// faultyDB is for the Wave 2 5xx-conversion workers (agent-os-2mhb): the
// only way to make a *database.DB method fail with something other than the
// ordinary not-found case is a real DB in a failing state, since handlers
// hold a concrete *database.DB (see stackStore in stacks.go, auth.go:40,
// backup.go:44), not an interface with a fake-able implementation.
//
// faultyDB opens a fully migrated DB (via newMigratedDBDir, backup_test.go)
// and immediately closes its underlying connection, so every subsequent
// query returns a driver error ("sql: database is closed") — distinct from
// sql.ErrNoRows, which is what the SAME queries return against a healthy
// migrated DB for a missing row. See
// TestFaultyDB_FailsDifferentlyFromHealthyNotFound below for the two-sided
// proof this actually holds.
//
// Do not "clean this up" as unused: it has no callers in this bead's own
// scope (agent-os-2mhb converts no handler call sites), only in the sibling
// 5xx-conversion beads that land after it.
func faultyDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.New(newMigratedDBDir(t))
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db to induce failure: %v", err)
	}
	return db
}

// TestFaultyDB_FailsDifferentlyFromHealthyNotFound is the two-sided control
// the faultyDB doc comment promises: a query against faultyDB must fail with
// something that is NOT the not-found predicate the handlers use
// (errors.Is(err, sql.ErrNoRows) — see database/stacks.go:42-54's GetStack,
// which returns the raw Scan error unwrapped, and handlers/directories.go:227
// which checks the same predicate against that same shape of error), while
// the identical query against a healthy migrated DB for a missing row DOES
// satisfy that predicate. Without both sides, a faultyDB that always failed
// (even with sql.ErrNoRows) would look identical to a working one in any
// single-sided check.
func TestFaultyDB_FailsDifferentlyFromHealthyNotFound(t *testing.T) {
	broken := faultyDB(t)
	_, err := broken.GetStack("nope")
	if err == nil {
		t.Fatalf("faultyDB.GetStack returned no error; the closed connection did not induce a failure")
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("faultyDB.GetStack failed with the SAME predicate as ordinary not-found (sql.ErrNoRows): %v — this DB is not exercising the non-not-found failure path Wave 2 needs", err)
	}

	healthy, err := database.New(newMigratedDBDir(t))
	if err != nil {
		t.Fatalf("open healthy migrated db: %v", err)
	}
	t.Cleanup(func() { _ = healthy.Close() })

	_, err = healthy.GetStack("nope")
	if err == nil {
		t.Fatalf("healthy.GetStack(\"nope\") returned no error; the positive control never fired")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("healthy.GetStack(\"nope\") did not fail with sql.ErrNoRows, got: %v — the not-found predicate assumption is wrong", err)
	}
}
