// Package errdefs holds error sentinels shared across packages that cannot
// import one another directly, so each side can alias the same identity
// instead of declaring its own errors.New with matching text.
package errdefs

import "errors"

// ErrEncryptionUnavailable is returned when no STORAGE_KEY or JWT_SECRET was
// configured at startup, so no TokenEncryptor could be built.
//
// Background (agent-os-2fb): internal/services and internal/database each
// declared their own ErrEncryptionUnavailable with identical text, because
// internal/services imports internal/database (scanner, backup_*, actionlog,
// scheduler) and the reverse import would cycle. Two distinct error values
// meant errors.Is(databaseErr, servicesErr) was false: a DB built directly via
// database.New/NewWithMigrations (bypassing
// services.NewTokenEncryptorOrDefault) surfaced its encryption failures as a
// generic 500 in handlers/respond.go instead of the actionable 422 the
// services-side sentinel gets mapped to. This package gives both sides one
// underlying identity to alias, so errors.Is matches regardless of which
// package's noEncryptor produced the error.
var ErrEncryptionUnavailable = errors.New("no encryption key configured: set STORAGE_KEY or JWT_SECRET")

// ErrNotFound is the single not-found identity for the whole backend. Every
// internal/database getter that can answer "there is no such row" returns an
// error matching it, so no consumer has to know that SQLite reports absence as
// sql.ErrNoRows.
//
// Background (agent-os-ymyc): the getters used to return sql.ErrNoRows raw, so
// every consumer rebuilt the absent/faulted discrimination by hand — about a
// dozen got it wrong one at a time (7lg1, 3h9x, 1gqn, 8tqd, g482, r1by, xzoe,
// obgr, l42o, uacg, koy9, 89ut, rltu), each read "the database could not tell
// me" as "the row is not there". Discriminating here instead means a fault
// keeps its own shape and only a genuine absence matches.
//
// Nothing outside internal/database may name sql.ErrNoRows; golangci's
// forbidigo rule enforces that.
var ErrNotFound = errors.New("not found")

// NotFoundError is the concrete absence carried by a database getter. Kind
// names the entity so the HTTP boundary can pick the wire code and the
// client-facing message in ONE place instead of at every call site; Key is the
// lookup key, kept for the log line and deliberately never put in a response
// body.
//
// It matches errors.Is(err, ErrNotFound) via Is below, so a caller that only
// cares "absent or not" never needs the concrete type.
type NotFoundError struct {
	Kind string
	Key  string
}

func (e *NotFoundError) Error() string {
	if e.Key == "" {
		return e.Kind + " not found"
	}
	return e.Kind + " not found: " + e.Key
}

// Is makes every NotFoundError match ErrNotFound. It deliberately does NOT
// unwrap to sql.ErrNoRows: chaining the SQL sentinel would leave the old
// hand-written discrimination silently working, which is the thing
// agent-os-ymyc removes.
func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }
