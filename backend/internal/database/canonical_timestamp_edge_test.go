package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// agent-os-bp68: two inputs the RFC3339 layout handles wrongly, plus the
// control arm that must hold on both sides of the fix. Without the control, a
// change that rejected everything would satisfy the two defended cases.
func TestCanonicalTimestamp_EdgeInputs(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		// Converting to UTC carries the year past 9999. The five-digit result
		// is not RFC3339, cannot be re-parsed, and text-sorts below every 2xxx
		// row, so the input is kept as given instead.
		{"year overflow kept as given", "9999-12-31T23:59:59-12:00", "9999-12-31T23:59:59-12:00"},
		// The same carry in the other direction, below year 0000.
		{"year underflow kept as given", "0000-01-01T00:30:00+01:00", "0000-01-01T00:30:00+01:00"},
		// RFC 3339 section 5.6 allows lowercase t and z; Go's layout does not.
		{"lowercase t and z normalised", "2026-02-28t23:30:00z", "2026-02-28T23:30:00Z"},
		{"lowercase t with offset normalised", "2026-03-01t00:30:00+02:00", "2026-02-28T22:30:00Z"},
		// Control arm.
		{"unparseable passes through byte-identical", "not-a-date", "not-a-date"},
		{"offset normalised", "2026-03-01T00:30:00+02:00", "2026-02-28T22:30:00Z"},
		{"latest representable instant still canonical", "9999-12-31T23:59:59Z", "9999-12-31T23:59:59Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, canonicalTimestamp(tc.in))
		})
	}
}
