package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func (d *DB) GetUpdateHistory(filters models.UpdateHistoryFilters) ([]models.UpdateHistoryEntry, int, error) {
	var whereClauses []string
	var args []interface{}

	if filters.Status != "" {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, filters.Status)
	}
	if filters.Trigger != "" {
		whereClauses = append(whereClauses, "trigger = ?")
		args = append(args, filters.Trigger)
	}
	if filters.ContainerID != "" {
		whereClauses = append(whereClauses, "container_id = ?")
		args = append(args, filters.ContainerID)
	}
	if filters.StackID != "" {
		whereClauses = append(whereClauses, "stack_id = ?")
		args = append(args, filters.StackID)
	}
	// started_at is compared as TEXT, so the bound has to be one canonical
	// spelling or it selects by spelling rather than by instant: a caller
	// sending its own offset is sending a different string for the same
	// moment. The other half -- the stored values -- is canonical too as of
	// agent-os-lmbn: canonicalTimestamp below normalises every write, and
	// migration 15 rewrote the rows that predate it. Both sides now speak one
	// spelling, so this comparison is by instant.
	if filters.From != nil {
		whereClauses = append(whereClauses, "started_at >= ?")
		args = append(args, filters.From.UTC().Format(time.RFC3339))
	}
	if filters.To != nil {
		whereClauses = append(whereClauses, "started_at <= ?")
		args = append(args, filters.To.UTC().Format(time.RFC3339))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM update_history " + whereClause
	if err := d.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filters.Limit
	if limit <= 0 {
		limit = 25
	}
	page := filters.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit

	query := `SELECT id, container_id, container_name, COALESCE(stack_id, ''), COALESCE(stack_name, ''),
	          image, old_digest, new_digest, old_image_ref, new_image_ref,
	          status, trigger, started_at, completed_at, duration_ms, error_message
	          FROM update_history ` + whereClause + ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
	queryArgs := append(args, limit, offset)

	rows, err := d.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []models.UpdateHistoryEntry
	for rows.Next() {
		var e models.UpdateHistoryEntry
		var stackID, stackName sql.NullString
		var oldDigest, newDigest, oldImageRef, newImageRef sql.NullString
		var completedAt sql.NullString
		var durationMs sql.NullInt64
		var errorMsg sql.NullString

		err := rows.Scan(&e.ID, &e.ContainerID, &e.ContainerName, &stackID, &stackName,
			&e.Image, &oldDigest, &newDigest, &oldImageRef, &newImageRef,
			&e.Status, &e.Trigger, &e.StartedAt, &completedAt, &durationMs, &errorMsg)
		if err != nil {
			return nil, 0, err
		}

		if stackID.Valid && stackID.String != "" {
			e.StackID = &stackID.String
		}
		if stackName.Valid && stackName.String != "" {
			e.StackName = &stackName.String
		}
		if oldDigest.Valid {
			e.OldDigest = &oldDigest.String
		}
		if newDigest.Valid {
			e.NewDigest = &newDigest.String
		}
		if oldImageRef.Valid {
			e.OldImageRef = &oldImageRef.String
		}
		if newImageRef.Valid {
			e.NewImageRef = &newImageRef.String
		}
		if completedAt.Valid {
			e.CompletedAt = &completedAt.String
		}
		if durationMs.Valid {
			e.DurationMs = &durationMs.Int64
		}
		if errorMsg.Valid {
			e.ErrorMessage = &errorMsg.String
		}

		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("reading update history: %w", err)
	}
	return entries, total, nil
}

// canonicalTimestamp is the WRITE-side chokepoint for update_history's
// started_at and completed_at (agent-os-lmbn). Both columns are compared as
// text (GetUpdateHistory's From/To bounds, DeleteUpdateHistoryOlderThan) and
// ORDERed as text (ORDER BY started_at DESC above), so two rows written in
// different spellings of the same instant do not order by instant. Every
// caller reaches SQL through InsertUpdateHistory or UpdateUpdateHistory --
// verified exhaustively:
//
//	/usr/bin/grep -rn "INSERT INTO update_history\|UPDATE update_history" backend/ --include='*.go' | grep -v _test.go
//
// returns exactly three hits: the two in this file plus migrations.go's
// stack-ID rewrite, which touches no timestamp. Normalising here therefore
// covers all 16 call sites in handlers/updates.go and services/scheduler.go,
// and a call site added later cannot reintroduce the defect.
//
// The value arrives as a string, not a time.Time
// (models.UpdateHistoryEntry.StartedAt is a string), so it has to be
// re-parsed rather than merely .UTC()'d.
//
// A value that does not parse is returned UNCHANGED. That is deliberate and
// is the reason this does not share a parser with migration 15's SQL: the two
// act at different times on different populations. This chokepoint governs NEW
// writes, which are all produced by time.Now().Format(time.RFC3339) and so
// always parse in Go. The migration governs whatever is ALREADY stored, in
// whatever shape, and SQLite's strftime accepts several shapes Go's RFC3339
// rejects (zone-less and space-separated forms among them). Forcing one parser
// on both over-constrains both. The requirement they DO share: neither may
// destroy a value it cannot interpret -- a normaliser that zeroes an
// unparseable value turns a display bug into data loss.
//
// RFC3339Nano, not RFC3339, is the output layout: .Format(time.RFC3339)
// truncates sub-second precision, so "2026-02-28T23:30:00.123Z" would be
// silently rewritten to "2026-02-28T23:30:00Z" (OBSERVED). No current writer
// emits sub-second values, so this is a trap for the next caller rather than a
// live bug.
//
// THE GUARANTEE THIS MAKES IS "the instant is preserved exactly and the
// spelling is canonicalised". It is NOT "the bytes never change" -- do not
// build on byte-stability. RFC3339Nano drops TRAILING ZEROS, which is itself a
// byte change on a fractional value that was already canonical (OBSERVED, and
// every one of these is the same instant in and out):
//
//	"2026-02-28T23:30:00Z"      -> "2026-02-28T23:30:00Z"     unchanged
//	"2026-02-28T23:30:00.123Z"  -> "2026-02-28T23:30:00.123Z" unchanged
//	"2026-02-28T23:30:00.120Z"  -> "2026-02-28T23:30:00.12Z"  CHANGED
//	"2026-02-28T23:30:00.100Z"  -> "2026-02-28T23:30:00.1Z"   CHANGED
//	"2026-02-28T23:30:00.000Z"  -> "2026-02-28T23:30:00Z"     CHANGED
//
// That is an accepted canonicalisation, not a defect, and it is deliberately
// NOT worked around: a short-circuit to preserve trailing zeros would trade
// the real guarantee above for a cosmetic one. Note that migration 15 does the
// OPPOSITE with a stored ".120Z" -- its GLOB guard skips it, so that row keeps
// its trailing zero. The two sides genuinely differ on this one input, and
// that is intended for the reason given above: they act at different times on
// different populations, and neither is permitted to lose an instant.
func canonicalTimestamp(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func (d *DB) InsertUpdateHistory(entry *models.UpdateHistoryEntry) error {
	query := `INSERT INTO update_history (id, container_id, container_name, stack_id, stack_name,
	          image, old_digest, new_digest, old_image_ref, new_image_ref,
	          status, trigger, started_at, completed_at, duration_ms, error_message)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var stackID, stackName interface{}
	if entry.StackID != nil {
		stackID = *entry.StackID
	}
	if entry.StackName != nil {
		stackName = *entry.StackName
	}

	// Normalised into locals, never back into *entry: the caller owns that
	// struct and several callers reuse it after the insert.
	startedAt := canonicalTimestamp(entry.StartedAt)
	var completedAt interface{}
	if entry.CompletedAt != nil {
		completedAt = canonicalTimestamp(*entry.CompletedAt)
	}

	_, err := d.db.Exec(query, entry.ID, entry.ContainerID, entry.ContainerName,
		stackID, stackName, entry.Image,
		entry.OldDigest, entry.NewDigest, entry.OldImageRef, entry.NewImageRef,
		entry.Status, entry.Trigger, startedAt,
		completedAt, entry.DurationMs, entry.ErrorMessage)
	return err
}

func (d *DB) UpdateUpdateHistory(id string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	allowedColumns := map[string]bool{
		"status":        true,
		"completed_at":  true,
		"duration_ms":   true,
		"error_message": true,
		"new_digest":    true,
		"new_image_ref": true,
	}

	var setClauses []string
	var args []interface{}
	for key, val := range updates {
		if !allowedColumns[key] {
			continue
		}
		// completed_at is the only timestamp reachable here: started_at is
		// absent from allowedColumns above, so a started_at branch would be
		// dead code. See canonicalTimestamp for why an unparseable value is
		// passed through rather than rejected.
		if key == "completed_at" {
			if str, ok := val.(string); ok {
				val = canonicalTimestamp(str)
			}
		}
		setClauses = append(setClauses, key+" = ?")
		args = append(args, val)
	}

	if len(setClauses) == 0 {
		return nil
	}

	args = append(args, id)

	//nolint:gosec // setClauses is built only from keys present in the allowedColumns allowlist above (6 fixed names); values are bound parameters via args, never concatenated
	query := "UPDATE update_history SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	_, err := d.db.Exec(query, args...)
	return err
}

func (d *DB) DeleteUpdateHistoryOlderThan(before time.Time) (int, error) {
	// .UTC() is load-bearing here, not cosmetic: completed_at is compared as
	// text, so an unnormalised bound deletes by spelling rather than by
	// instant. A positive offset destroys rows the caller asked to keep and a
	// negative one leaves rows it asked to remove (agent-os-hxra).
	result, err := d.db.Exec("DELETE FROM update_history WHERE completed_at < ?", before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

func (d *DB) GetUpdateStats() (enabledContainers int, last7Days int, last30Days int, err error) {
	err = d.db.QueryRow("SELECT COUNT(*) FROM auto_update_policies WHERE enabled = TRUE").Scan(&enabledContainers)
	if err != nil {
		return
	}

	err = d.db.QueryRow("SELECT COUNT(*) FROM update_history WHERE status = 'success' AND started_at >= datetime('now', '-7 days')").Scan(&last7Days)
	if err != nil {
		return
	}

	err = d.db.QueryRow("SELECT COUNT(*) FROM update_history WHERE status = 'success' AND started_at >= datetime('now', '-30 days')").Scan(&last30Days)
	if err != nil {
		return
	}

	return
}
