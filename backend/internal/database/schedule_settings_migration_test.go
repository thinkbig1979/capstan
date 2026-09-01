package database

import (
	"testing"
)

// A database created BEFORE migration 14 must upgrade to
// behaviour identical to today's. Two-sided: an untouched DB gets the safe
// defaults, and an operator who already chose a window keeps it.
func TestMigration14_UpgradeIsBehaviourPreserving(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Simulate a pre-14 database: drop the three seeded rows and the version stamp.
	for _, k := range []string{"update_apply_mode", "update_apply_time", "update_apply_days"} {
		if _, err := db.db.Exec("DELETE FROM settings WHERE key = ?", k); err != nil {
			t.Fatalf("unseed %s: %v", k, err)
		}
	}
	if _, err := db.db.Exec("DELETE FROM schema_migrations WHERE version = 14"); err != nil {
		t.Fatalf("unstamp: %v", err)
	}

	if err := RunMigrations(db); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	for _, tc := range []struct{ key, want string }{
		{"update_apply_mode", "immediate"},
		{"update_apply_time", "03:00"},
		{"update_apply_days", "0,1,2,3,4,5,6"},
	} {
		var got string
		if err := db.db.QueryRow("SELECT value FROM settings WHERE key = ?", tc.key).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", tc.key, err)
		}
		if got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}

	// No backup_ key may be seeded: that would shadow the BACKUP_* env fallbacks.
	for _, k := range []string{"backup_schedule_mode", "backup_schedule_time", "backup_schedule_days"} {
		var v string
		err := db.db.QueryRow("SELECT value FROM settings WHERE key = ?", k).Scan(&v)
		if err == nil {
			t.Fatalf("%s must NOT be seeded (it would kill the BACKUP_* env fallback), got %q", k, v)
		}
	}

	// Side B: an operator-set value survives a re-run of migration 14.
	if err := db.SetSetting("update_apply_mode", "scheduled"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := db.db.Exec("DELETE FROM schema_migrations WHERE version = 14"); err != nil {
		t.Fatalf("unstamp2: %v", err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("re-migrate2: %v", err)
	}
	var after string
	if err := db.db.QueryRow("SELECT value FROM settings WHERE key='update_apply_mode'").Scan(&after); err != nil {
		t.Fatalf("read: %v", err)
	}
	if after != "scheduled" {
		t.Fatalf("operator value clobbered: got %q", after)
	}
}
