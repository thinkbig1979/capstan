package handlers

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-97pn (R2-1): while locked, a backup settings save may set only the
// fields that change neither where backups go, with what credential, nor how
// long they are kept. Every OTHER field of backupSettingsRequest is refused,
// including one added after this test was written: the predicate is an allow
// list, so a new destination field fails closed instead of slipping through.
func TestBackupSettings_LockedAllowListCoversEveryField_97pn(t *testing.T) {
	allowedLocked := map[string]bool{
		"scheduleIntervalMinutes": true, "scheduleMode": true, "scheduleTime": true, "scheduleDays": true,
		"syncAfterBackup": true, "verifyWeekly": true, "rcloneTransfers": true, "hostname": true,
	}
	typ := reflect.TypeOf(backupSettingsRequest{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		seen[name] = true
		var req backupSettingsRequest
		// The field set to a non-nil pointer to its zero value: "present",
		// which is what the gate keys on.
		reflect.ValueOf(&req).Elem().Field(i).Set(reflect.New(f.Type.Elem()))
		assert.Equal(t, !allowedLocked[name], req.changesDestinationOrRetention(),
			"field %s: a locked save refuses every field outside the allow list", name)
	}
	for name := range allowedLocked {
		assert.True(t, seen[name], "allow-list entry %s names no backupSettingsRequest field", name)
	}
	var empty backupSettingsRequest
	assert.False(t, empty.changesDestinationOrRetention(), "an empty request changes nothing")
}
