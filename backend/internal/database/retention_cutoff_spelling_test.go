package database

import (
	"testing"
	"time"
	_ "time/tzdata" // the off-UTC action_log test needs a real zone in any image

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-h8qa: the prune cutoffs were compared as text against a stored value
// spelled differently, so the separator or the zone decided the rows near the
// cutoff, not the instant.

// onCutoffDateUTC is midnight UTC of the retention cutoff's own calendar date:
// older than the cutoff by instant, on the same date by construction. Seeding
// "cutoff minus an hour" instead would land on the previous date whenever the
// run starts in the first hour of a UTC day, where the date prefix alone decides
// correctly and the arm passes against the bug it exists to catch (the trap
// agent-os-91jb's fixture recorded).
func onCutoffDateUTC(retentionDays int) string {
	c := time.Now().UTC().AddDate(0, 0, -retentionDays)
	return time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

func TestDeleteOldUpdateHistory_PrunesRowOnTheCutoffDate(t *testing.T) {
	db := newRetentionTestDB(t)
	onCutoff := onCutoffDateUTC(90)
	newer := time.Now().UTC().AddDate(0, 0, -90).Add(time.Hour).Format(time.RFC3339)
	for _, r := range []struct{ id, at string }{{"on-cutoff", onCutoff}, {"newer", newer}} {
		if _, err := db.db.Exec(`INSERT INTO update_history
			(id, container_id, container_name, image, status, trigger, started_at, completed_at)
			VALUES (?, 'c', 'n', 'img:latest', 'success', 'auto', ?, ?)`, r.id, r.at, r.at); err != nil {
			t.Fatalf("seed %s: %v", r.id, err)
		}
	}

	deleted, err := db.DeleteOldUpdateHistory(90)
	if err != nil {
		t.Fatalf("DeleteOldUpdateHistory: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted %d rows, want 1: the row completed at %s is older than 90 days", deleted, onCutoff)
	}
	var left string
	if err := db.db.QueryRow(`SELECT id FROM update_history`).Scan(&left); err != nil || left != "newer" {
		t.Errorf("remaining row = %q (err %v), want only the one newer than the cutoff", left, err)
	}
}

func TestDeleteOldBackupRuns_PrunesRowOnTheCutoffDate(t *testing.T) {
	db := newRetentionTestDB(t)
	onCutoff := onCutoffDateUTC(90)
	newer := time.Now().UTC().AddDate(0, 0, -90).Add(time.Hour).Format(time.RFC3339)
	for _, r := range []struct{ id, at string }{{"on-cutoff", onCutoff}, {"newer", newer}} {
		if _, err := db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at)
			VALUES (?, 'backup', 'scheduled', 'success', ?)`, r.id, r.at); err != nil {
			t.Fatalf("seed %s: %v", r.id, err)
		}
	}

	deleted, err := db.DeleteOldBackupRuns(90)
	if err != nil {
		t.Fatalf("DeleteOldBackupRuns: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted %d runs, want 1: the run started at %s is older than 90 days", deleted, onCutoff)
	}
	var left string
	if err := db.db.QueryRow(`SELECT id FROM backup_runs`).Scan(&left); err != nil || left != "newer" {
		t.Errorf("remaining run = %q (err %v), want only the one newer than the cutoff", left, err)
	}
}

// TestDeleteOldActionLogs_HonoursTheInstantOffUTC writes through LogAction, the
// production writer, so the rows carry the driver's real spelling: time.Time
// bound directly is stored as t.String() in the process's local zone, e.g.
// "2026-06-26 19:02:30.59 -0400 EDT m=-7768799.96". A zone west of UTC made
// those rows look older than they are to a UTC datetime() cutoff, so a row
// still inside the retention window was deleted; east of UTC, rows were kept
// past it. Both directions are asserted in each zone.
func TestDeleteOldActionLogs_HonoursTheInstantOffUTC(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Europe/Amsterdam", "UTC"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatalf("load %s: %v", zone, err)
			}
			saved := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = saved })

			db := newRetentionTestDB(t)
			now := time.Now()
			for _, r := range []struct {
				id  string
				age time.Duration
			}{
				{"inside", (89*24 + 22) * time.Hour}, // 2h inside a 90-day window: kept
				{"outside", (90*24 + 2) * time.Hour}, // 2h past it: deleted
			} {
				if err := db.LogAction(models.ActionLog{
					ID: r.id, UserID: "u", Action: "test.action", Detail: "seeded",
					CreatedAt: now.Add(-r.age),
				}); err != nil {
					t.Fatalf("LogAction %s: %v", r.id, err)
				}
			}

			if err := db.DeleteOldActionLogs(90); err != nil {
				t.Fatalf("DeleteOldActionLogs: %v", err)
			}
			rows, err := db.db.Query(`SELECT id FROM action_log ORDER BY id`)
			if err != nil {
				t.Fatalf("list action_log: %v", err)
			}
			defer rows.Close()
			var left []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatalf("scan: %v", err)
				}
				left = append(left, id)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}
			if len(left) != 1 || left[0] != "inside" {
				t.Errorf("TZ=%s: remaining rows %v, want [inside]", zone, left)
			}
		})
	}
}
