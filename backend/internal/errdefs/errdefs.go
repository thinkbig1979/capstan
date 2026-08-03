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
