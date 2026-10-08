package database

import (
	"fmt"
	"time"
)

// storedInstantLayout is the one spelling sessions.expires_at,
// sessions.created_at and action_log.created_at are written in
// (agent-os-6exk). Those columns are compared and ORDERed as text, so the
// spelling has to sort the way the instants do: UTC, and FIXED WIDTH.
//
// Bound as a plain time.Time, the driver stores t.String() in the value's own
// zone ("2026-11-01 01:10:00 -0500 EST"). Across a DST fall-back, or after a
// TZ change, the later instant can then carry the smaller text, and both
// DeleteExpiredSessions and the ORDER BY created_at readers go wrong.
//
// Milliseconds, not whole seconds as update_history uses: action_log gets
// several rows per second and its readers order by this column. The ".000" is
// always present, which keeps the width fixed (see canonicalTimestamp for what
// a variable-width fraction does to a text sort). It is also exactly what
// SQLite's strftime('%Y-%m-%dT%H:%M:%fZ') emits, which is what migration 23
// uses to rewrite rows stored in the old spelling. The driver reads it back
// into a time.Time.
const storedInstantLayout = "2006-01-02T15:04:05.000Z"

// storedInstant is the value every write to, and every comparison against,
// those three columns binds.
func storedInstant(t time.Time) string {
	return t.UTC().Format(storedInstantLayout)
}

// tStringToStoredInstantSQL is the UPDATE migration 23 runs on one column. It
// rewrites the driver's old t.String() spelling, "2026-11-01 01:10:00.5 -0500
// EST m=+1.2", by rebuilding it as "2026-11-01T01:10:00.5-05:00" and letting
// strftime convert that to UTC.
//
// Two guards, the same rule migration 15 follows: never destroy a value it
// cannot interpret.
//   - The GLOB matches only that old spelling (date, space, time, space,
//     +-HHMM, space). Rows already in the new spelling have a 'T' in place of
//     the first space and are skipped, which also makes a re-run a no-op.
//   - strftime(...) IS NOT NULL: strftime returns NULL for what it cannot
//     read, and the UPDATE would store that NULL.
func tStringToStoredInstantSQL(table, column string) string {
	// rest is the value from the time onward; sp is where its first space is,
	// so the offset starts at character 12+sp of the whole value.
	sp := fmt.Sprintf("instr(substr(%s, 12), ' ')", column)
	rebuilt := fmt.Sprintf(
		"substr(%[1]s, 1, 10) || 'T' || substr(%[1]s, 12, %[2]s - 1) || substr(%[1]s, 12 + %[2]s, 3) || ':' || substr(%[1]s, 15 + %[2]s, 2)",
		column, sp)
	converted := fmt.Sprintf("strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', %s)", rebuilt)
	return fmt.Sprintf(`
UPDATE %[1]s
SET %[2]s = %[3]s
WHERE %[2]s GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9]* [+-][0-9][0-9][0-9][0-9] *'
  AND %[3]s IS NOT NULL;
`, table, column, converted)
}
