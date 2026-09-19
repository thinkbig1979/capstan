package database

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

// This file is the getter-side half of agent-os-ymyc's boundary. notFound is
// the ONE place the driver's absence signal is discriminated, so it is the one
// place worth pinning: every one of the 13 single-row getters routes through
// it, and ~50 call sites across handlers, services and middleware now trust
// that only a genuine absent row produces errdefs.ErrNotFound.
//
// The failure this guards against is the one the class was made of: an error
// meaning "the database could not answer" being reported as "the row is not
// there". That is a ONE-DIRECTIONAL hazard, so the passthrough arm below
// matters more than the wrapping arm — a notFound that wrapped everything
// would satisfy any test that only checked ErrNoRows.

func TestNotFound_WrapsOnlyErrNoRows(t *testing.T) {
	t.Parallel()

	driverFault := errors.New("sql: database is closed")

	cases := []struct {
		name  string
		in    error
		wantN bool // want errors.Is(out, errdefs.ErrNotFound)
	}{
		{"bare ErrNoRows is an absence", sql.ErrNoRows, true},
		{"wrapped ErrNoRows is still an absence", fmt.Errorf("scanning row: %w", sql.ErrNoRows), true},
		{"a driver fault is NOT an absence", driverFault, false},
		{"a wrapped driver fault is NOT an absence", fmt.Errorf("query: %w", driverFault), false},
		{"nil stays nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := notFound(tc.in, "stack", "abc")
			if gotN := errors.Is(got, errdefs.ErrNotFound); gotN != tc.wantN {
				t.Fatalf("errors.Is(notFound(%v), errdefs.ErrNotFound) = %v, want %v (got %v)", tc.in, gotN, tc.wantN, got)
			}
			if tc.wantN {
				return
			}
			// The other half, and the one that makes this a discriminator: a
			// non-absence must come back BYTE-IDENTICAL, not merely
			// non-matching. A notFound that wrapped faults in some other
			// error would pass the arm above and still destroy the cause every
			// caller logs.
			if got != tc.in {
				t.Fatalf("notFound altered a non-absence error: got %#v, want the input %#v unchanged", got, tc.in)
			}
		})
	}
}

// TestNotFound_CarriesKindAndKey pins what the HTTP boundary reads. Kind picks
// the wire code and message in handlers/respond.go's notFoundWire; a getter
// passing the wrong Kind is the one way a correct handler still answers the
// wrong code, and nothing else in the tree would notice.
func TestNotFound_CarriesKindAndKey(t *testing.T) {
	t.Parallel()

	var nf *errdefs.NotFoundError
	if !errors.As(notFound(sql.ErrNoRows, "backup run", "run-7"), &nf) {
		t.Fatal("notFound(sql.ErrNoRows, ...) did not produce an *errdefs.NotFoundError")
	}
	if nf.Kind != "backup run" || nf.Key != "run-7" {
		t.Fatalf("Kind/Key = %q/%q, want %q/%q", nf.Kind, nf.Key, "backup run", "run-7")
	}
	// The key is for logs. It must not be lost, and it must not be the only
	// thing identifying the error either.
	if got := nf.Error(); got != "backup run not found: run-7" {
		t.Fatalf("Error() = %q, want %q", got, "backup run not found: run-7")
	}
}

// TestGetters_AbsentRowIsErrNotFound drives the real getters against a healthy,
// migrated, EMPTY database. It is the arm that would catch a getter added later
// and left unwrapped: the unit above tests the helper, this tests that the
// helper is actually reached.
//
// The pair matters — a getter returning raw sql.ErrNoRows passes no arm here,
// and a getter wrapping EVERYTHING passes this one while failing the
// passthrough arm above.
func TestGetters_AbsentRowIsErrNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	cases := []struct {
		getter string
		kind   string
		call   func() error
	}{
		{"GetStack", "stack", func() error { _, err := db.GetStack("nope"); return err }},
		{"GetStackByProjectName", "stack", func() error { _, err := db.GetStackByProjectName("nope"); return err }},
		{"GetDirectory", "directory", func() error { _, err := db.GetDirectory("/nope"); return err }},
		{"GetDirectoryCredentials", "directory", func() error { _, err := db.GetDirectoryCredentials("/nope"); return err }},
		{"GetSetting", "setting", func() error { _, err := db.GetSetting("nope"); return err }},
		{"GetBackupPolicy", "backup policy", func() error { _, err := db.GetBackupPolicy("nope"); return err }},
		{"GetBackupRunByID", "backup run", func() error { _, err := db.GetBackupRunByID("nope"); return err }},
		{"GetLatestRunItemForStack", "backup run item", func() error { _, err := db.GetLatestRunItemForStack("nope"); return err }},
		{"GetAutoUpdatePolicy", "auto-update policy", func() error { _, err := db.GetAutoUpdatePolicy("stack", "nope"); return err }},
		{"GetUserByUsername", "user", func() error { _, err := db.GetUserByUsername("nope"); return err }},
		{"GetUserByID", "user", func() error { _, err := db.GetUserByID("nope"); return err }},
		{"GetSession", "session", func() error { _, err := db.GetSession("nope"); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.getter, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s against an empty database returned no error", tc.getter)
			}
			if errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("%s still returns the driver's sql.ErrNoRows: %v — it must be wrapped at the getter (agent-os-ymyc)", tc.getter, err)
			}
			var nf *errdefs.NotFoundError
			if !errors.As(err, &nf) {
				t.Fatalf("%s absence is not an *errdefs.NotFoundError: %v", tc.getter, err)
			}
			if nf.Kind != tc.kind {
				t.Fatalf("%s Kind = %q, want %q — handlers/respond.go notFoundWire keys on this", tc.getter, nf.Kind, tc.kind)
			}
		})
	}
}
