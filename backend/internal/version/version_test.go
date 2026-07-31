package version

import "testing"

// TestDefaultsAreHonest guards the "empty string reads as a bug" requirement: an
// unstamped binary must say something, and that something must be "dev".
func TestDefaultsAreHonest(t *testing.T) {
	info := Get()

	if info.Version != "dev" {
		t.Errorf("Version = %q, want %q for an unstamped build", info.Version, "dev")
	}
	if info.Commit == "" {
		t.Error("Commit is empty; an unstamped build must still report something")
	}
	if info.BuildDate == "" {
		t.Error("BuildDate is empty; an unstamped build must still report something")
	}
}

func TestGetReflectsStampedValues(t *testing.T) {
	origV, origC, origD := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = origV, origC, origD })

	Version, Commit, BuildDate = "1.2.3", "abc1234", "2026-07-31T12:00:00Z"

	info := Get()
	if info.Version != "1.2.3" || info.Commit != "abc1234" || info.BuildDate != "2026-07-31T12:00:00Z" {
		t.Errorf("Get() = %+v, want the stamped values", info)
	}
}
