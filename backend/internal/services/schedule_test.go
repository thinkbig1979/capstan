package services

import (
	"regexp"
	"testing"
	"time"
)

// offsetPattern is only used where the expected offset depends on the host's
// own zone, so only the shape can be asserted.
var offsetPattern = regexp.MustCompile(`^[+-]([01][0-9]|2[0-3]):[0-5][0-9]$`)

// amsterdam is the location every wall-clock case below is expressed in: it has
// a DST rule, so it distinguishes a correct time.Date-based schedule from one
// built by adding 24h durations. UTC cases sit alongside to show the maths is
// not location-specific.
func amsterdam(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("LoadLocation(Europe/Amsterdam): %v", err)
	}
	return loc
}

func at(t *testing.T, loc *time.Location, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatalf("ParseInLocation(%q): %v", value, err)
	}
	return parsed
}

var (
	everyDay  = []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
	mondayFri = []time.Weekday{time.Monday, time.Friday}
)

func TestDailySchedule_NextAfter(t *testing.T) {
	ams := amsterdam(t)

	tests := []struct {
		name     string
		schedule DailySchedule
		loc      *time.Location
		from     string
		want     string // RFC3339, so the asserted UTC offset is visible in the expectation
	}{
		{
			name:     "same day, still ahead of the scheduled time",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: everyDay},
			loc:      ams,
			from:     "2026-06-10 02:00", // Wednesday
			want:     "2026-06-10T03:00:00+02:00",
		},
		{
			name:     "same day but the time already passed, rolls to tomorrow",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: everyDay},
			loc:      ams,
			from:     "2026-06-10 03:30",
			want:     "2026-06-11T03:00:00+02:00",
		},
		{
			name:     "exactly at the scheduled instant returns the next one, never t itself",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: everyDay},
			loc:      ams,
			from:     "2026-06-10 03:00",
			want:     "2026-06-11T03:00:00+02:00",
		},
		{
			name:     "one second before the scheduled instant still catches today",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: everyDay},
			loc:      ams,
			from:     "2026-06-10 02:59",
			want:     "2026-06-10T03:00:00+02:00",
		},
		{
			name:     "skips disallowed weekdays to the next allowed one",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: mondayFri},
			loc:      ams,
			from:     "2026-06-10 12:00", // Wednesday, so Thursday is skipped
			want:     "2026-06-12T03:00:00+02:00",
		},
		{
			name:     "wraps across the week boundary from Saturday to Sunday",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: []time.Weekday{time.Sunday}},
			loc:      ams,
			from:     "2026-06-13 23:00", // Saturday
			want:     "2026-06-14T03:00:00+02:00",
		},
		{
			name:     "single allowed weekday, six days ahead",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: []time.Weekday{time.Sunday}},
			loc:      ams,
			from:     "2026-06-08 12:00", // Monday
			want:     "2026-06-14T03:00:00+02:00",
		},
		{
			name:     "single allowed weekday, today but already passed, a full week ahead",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: []time.Weekday{time.Monday}},
			loc:      ams,
			from:     "2026-06-08 03:30", // Monday, past 03:00
			want:     "2026-06-15T03:00:00+02:00",
		},
		{
			name:     "single allowed weekday, today and still ahead, stays today",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: []time.Weekday{time.Monday}},
			loc:      ams,
			from:     "2026-06-08 02:30",
			want:     "2026-06-08T03:00:00+02:00",
		},
		{
			name:     "answers in the caller's location, not the server's",
			schedule: DailySchedule{Hour: 3, Minute: 0, Days: everyDay},
			loc:      time.UTC,
			from:     "2026-06-10 02:00",
			want:     "2026-06-10T03:00:00Z",
		},
		{
			name:     "unsorted, hand-built Days still resolves",
			schedule: DailySchedule{Hour: 23, Minute: 45, Days: []time.Weekday{time.Friday, time.Monday}},
			loc:      ams,
			from:     "2026-06-10 12:00", // Wednesday
			want:     "2026-06-12T23:45:00+02:00",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from := at(t, tc.loc, tc.from)

			got, ok := tc.schedule.NextAfter(from)
			if !ok {
				t.Fatalf("NextAfter(%s) returned ok=false, want a scheduled instant", from)
			}
			if formatted := got.Format(time.RFC3339); formatted != tc.want {
				t.Fatalf("NextAfter(%s) = %s, want %s", from, formatted, tc.want)
			}
			if !got.After(from) {
				t.Fatalf("NextAfter(%s) = %s, which is not strictly after t", from, got)
			}
			if got.Location() != tc.loc {
				t.Fatalf("NextAfter(%s) returned location %s, want %s", from, got.Location(), tc.loc)
			}
		})
	}
}

func TestDailySchedule_NextAfter_NoDays(t *testing.T) {
	from := at(t, time.UTC, "2026-06-10 02:00")

	// ok=false is the only documented failure mode, so both sides are worth
	// showing: an empty schedule declines, and the identical schedule with one
	// day added answers.
	empty := DailySchedule{Hour: 3, Minute: 0}
	if got, ok := empty.NextAfter(from); ok {
		t.Fatalf("NextAfter on a schedule with no days = %s, ok=true; want ok=false", got)
	}

	populated := DailySchedule{Hour: 3, Minute: 0, Days: []time.Weekday{time.Wednesday}}
	got, ok := populated.NextAfter(from)
	if !ok {
		t.Fatalf("NextAfter on a schedule with one day returned ok=false, want a scheduled instant")
	}
	if formatted := got.Format(time.RFC3339); formatted != "2026-06-10T03:00:00Z" {
		t.Fatalf("NextAfter = %s, want 2026-06-10T03:00:00Z", formatted)
	}
}

// TestDailySchedule_NextAfter_DST is the case a 24h-duration implementation
// fails. Each transition row is paired with a control row a week earlier on the
// identical schedule: the control advances by exactly 24h of real time, the
// transition row does not, and both land on the same 03:00 wall clock. An
// implementation that added 24h would keep the elapsed time at 24h and move the
// wall clock to 04:00 (spring) or 02:00 (autumn) instead.
func TestDailySchedule_NextAfter_DST(t *testing.T) {
	ams := amsterdam(t)
	schedule := DailySchedule{Hour: 3, Minute: 0, Days: everyDay}

	tests := []struct {
		name        string
		from        string
		want        string
		wantElapsed time.Duration
	}{
		{
			name:        "control, one week before the spring-forward night",
			from:        "2026-03-21 03:00",
			want:        "2026-03-22T03:00:00+01:00",
			wantElapsed: 24 * time.Hour,
		},
		{
			name:        "across the spring-forward transition, 23 real hours",
			from:        "2026-03-28 03:00",
			want:        "2026-03-29T03:00:00+02:00",
			wantElapsed: 23 * time.Hour,
		},
		{
			name:        "control, one week before the autumn-back night",
			from:        "2026-10-17 03:00",
			want:        "2026-10-18T03:00:00+02:00",
			wantElapsed: 24 * time.Hour,
		},
		{
			name:        "across the autumn-back transition, 25 real hours",
			from:        "2026-10-24 03:00",
			want:        "2026-10-25T03:00:00+01:00",
			wantElapsed: 25 * time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from := at(t, ams, tc.from)

			got, ok := schedule.NextAfter(from)
			if !ok {
				t.Fatalf("NextAfter(%s) returned ok=false", from)
			}
			if formatted := got.Format(time.RFC3339); formatted != tc.want {
				t.Fatalf("NextAfter(%s) = %s, want %s", from, formatted, tc.want)
			}
			if elapsed := got.Sub(from); elapsed != tc.wantElapsed {
				t.Fatalf("NextAfter(%s) is %v of real time later, want %v", from, elapsed, tc.wantElapsed)
			}
			if hour, minute := got.Hour(), got.Minute(); hour != 3 || minute != 0 {
				t.Fatalf("NextAfter(%s) landed on wall clock %02d:%02d, want 03:00", from, hour, minute)
			}
		})
	}
}

// A schedule set to 02:30 falls in the hour Europe/Amsterdam skips on the
// spring-forward night. This documents what Go's time.Date does with a
// non-existent local time (it resolves using the pre-transition offset, so
// 02:30+01:00 == 03:30+02:00) rather than asserting a requirement: the only
// contract the scheduler needs is that some instant strictly after t comes
// back, on the right calendar day, and that the job is not skipped.
func TestDailySchedule_NextAfter_SkippedHour(t *testing.T) {
	ams := amsterdam(t)
	schedule := DailySchedule{Hour: 2, Minute: 30, Days: everyDay}
	from := at(t, ams, "2026-03-29 00:15")

	got, ok := schedule.NextAfter(from)
	if !ok {
		t.Fatalf("NextAfter(%s) returned ok=false", from)
	}
	if !got.After(from) {
		t.Fatalf("NextAfter(%s) = %s, not strictly after t", from, got)
	}
	if y, m, d := got.Date(); y != 2026 || m != time.March || d != 29 {
		t.Fatalf("NextAfter(%s) = %s, want it on 2026-03-29", from, got)
	}
	if formatted := got.Format(time.RFC3339); formatted != "2026-03-29T03:30:00+02:00" {
		t.Fatalf("NextAfter(%s) = %s, want 2026-03-29T03:30:00+02:00 (Go resolving the skipped hour with the pre-transition offset)", from, formatted)
	}
}

func TestParseDailySchedule(t *testing.T) {
	tests := []struct {
		name       string
		hhmm       string
		days       string
		wantHour   int
		wantMinute int
		wantDays   []time.Weekday
		wantErr    bool
	}{
		{name: "midnight", hhmm: "00:00", days: "0", wantHour: 0, wantMinute: 0, wantDays: []time.Weekday{time.Sunday}},
		{name: "last minute of the day", hhmm: "23:59", days: "6", wantHour: 23, wantMinute: 59, wantDays: []time.Weekday{time.Saturday}},
		{name: "the migration default", hhmm: "03:00", days: "0,1,2,3,4,5,6", wantHour: 3, wantMinute: 0, wantDays: everyDay},
		{name: "days are sorted", hhmm: "03:00", days: "5,1,3", wantHour: 3, wantMinute: 0, wantDays: []time.Weekday{time.Monday, time.Wednesday, time.Friday}},
		{name: "days are deduped", hhmm: "03:00", days: "2,2,2", wantHour: 3, wantMinute: 0, wantDays: []time.Weekday{time.Tuesday}},
		{name: "surrounding whitespace is tolerated", hhmm: "03:00", days: " 1 , 2 ", wantHour: 3, wantMinute: 0, wantDays: []time.Weekday{time.Monday, time.Tuesday}},

		{name: "hour out of range", hhmm: "24:00", days: "0", wantErr: true},
		{name: "minute out of range", hhmm: "03:60", days: "0", wantErr: true},
		{name: "missing colon", hhmm: "0300", days: "0", wantErr: true},
		{name: "unpadded hour", hhmm: "3:00", days: "0", wantErr: true},
		{name: "non-numeric time", hhmm: "aa:bb", days: "0", wantErr: true},
		{name: "seconds are not accepted", hhmm: "03:00:00", days: "0", wantErr: true},
		{name: "empty time", hhmm: "", days: "0", wantErr: true},

		{name: "weekday 7 is out of range", hhmm: "03:00", days: "7", wantErr: true},
		{name: "negative weekday", hhmm: "03:00", days: "-1", wantErr: true},
		{name: "empty weekday list", hhmm: "03:00", days: "", wantErr: true},
		{name: "weekday list of only separators", hhmm: "03:00", days: ",,", wantErr: true},
		{name: "non-numeric weekday", hhmm: "03:00", days: "mon", wantErr: true},
		{name: "one bad weekday poisons the whole list", hhmm: "03:00", days: "1,2,9", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDailySchedule(tc.hhmm, tc.days)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDailySchedule(%q, %q) = %+v, want an error", tc.hhmm, tc.days, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDailySchedule(%q, %q): %v", tc.hhmm, tc.days, err)
			}
			if got.Hour != tc.wantHour || got.Minute != tc.wantMinute {
				t.Fatalf("ParseDailySchedule(%q, %q) time = %02d:%02d, want %02d:%02d",
					tc.hhmm, tc.days, got.Hour, got.Minute, tc.wantHour, tc.wantMinute)
			}
			if !weekdaysEqual(got.Days, tc.wantDays) {
				t.Fatalf("ParseDailySchedule(%q, %q) days = %v, want %v", tc.hhmm, tc.days, got.Days, tc.wantDays)
			}
		})
	}
}

func TestDailySchedule_FormatRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		hhmm     string
		days     string
		wantTime string
		wantDays string
	}{
		{name: "already normalised", hhmm: "03:00", days: "0,1,2,3,4,5,6", wantTime: "03:00", wantDays: "0,1,2,3,4,5,6"},
		{name: "normalises order and duplicates", hhmm: "23:59", days: "6,1,6", wantTime: "23:59", wantDays: "1,6"},
		{name: "midnight keeps its padding", hhmm: "00:05", days: "3", wantTime: "00:05", wantDays: "3"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseDailySchedule(tc.hhmm, tc.days)
			if err != nil {
				t.Fatalf("ParseDailySchedule(%q, %q): %v", tc.hhmm, tc.days, err)
			}
			if got := parsed.FormatTime(); got != tc.wantTime {
				t.Fatalf("FormatTime() = %q, want %q", got, tc.wantTime)
			}
			if got := parsed.FormatDays(); got != tc.wantDays {
				t.Fatalf("FormatDays() = %q, want %q", got, tc.wantDays)
			}

			// Re-parsing the formatted form must be a fixed point.
			reparsed, err := ParseDailySchedule(parsed.FormatTime(), parsed.FormatDays())
			if err != nil {
				t.Fatalf("re-parsing %q/%q: %v", parsed.FormatTime(), parsed.FormatDays(), err)
			}
			if reparsed.Hour != parsed.Hour || reparsed.Minute != parsed.Minute || !weekdaysEqual(reparsed.Days, parsed.Days) {
				t.Fatalf("round trip changed the schedule: %+v -> %+v", parsed, reparsed)
			}
		})
	}
}

func TestFormatWeekdays(t *testing.T) {
	if got := FormatWeekdays(nil); got != "" {
		t.Fatalf("FormatWeekdays(nil) = %q, want an empty string", got)
	}
	if got := FormatWeekdays([]time.Weekday{time.Sunday}); got != "0" {
		t.Fatalf("FormatWeekdays([Sunday]) = %q, want \"0\"", got)
	}
	if got := FormatWeekdays(everyDay); got != "0,1,2,3,4,5,6" {
		t.Fatalf("FormatWeekdays(everyDay) = %q, want \"0,1,2,3,4,5,6\"", got)
	}
}

func TestParseScheduleTime(t *testing.T) {
	hour, minute, err := ParseScheduleTime("03:07")
	if err != nil {
		t.Fatalf("ParseScheduleTime(03:07): %v", err)
	}
	if hour != 3 || minute != 7 {
		t.Fatalf("ParseScheduleTime(03:07) = %d:%d, want 3:7", hour, minute)
	}
	if _, _, err := ParseScheduleTime("25:00"); err == nil {
		t.Fatal("ParseScheduleTime(25:00) returned no error, want one")
	}
}

func TestServerTimezone(t *testing.T) {
	ams := amsterdam(t)

	// Both DST states of the same location, at fixed instants, so the offset
	// assertions do not depend on when the suite runs.
	if name, offset := serverTimezoneAt(time.Date(2026, time.January, 10, 12, 0, 0, 0, ams)); name != "Europe/Amsterdam" || offset != "+01:00" {
		t.Fatalf("serverTimezoneAt(winter) = (%q, %q), want (\"Europe/Amsterdam\", \"+01:00\")", name, offset)
	}
	if name, offset := serverTimezoneAt(time.Date(2026, time.June, 10, 12, 0, 0, 0, ams)); name != "Europe/Amsterdam" || offset != "+02:00" {
		t.Fatalf("serverTimezoneAt(summer) = (%q, %q), want (\"Europe/Amsterdam\", \"+02:00\")", name, offset)
	}
	if name, offset := serverTimezoneAt(time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)); name != "UTC" || offset != "+00:00" {
		t.Fatalf("serverTimezoneAt(UTC) = (%q, %q), want (\"UTC\", \"+00:00\")", name, offset)
	}

	// ServerTimezone itself reads time.Local, whose value depends on the host,
	// so only the shape is assertable here.
	name, offset := ServerTimezone()
	if name == "" {
		t.Fatal("ServerTimezone() returned an empty zone name")
	}
	if !offsetPattern.MatchString(offset) {
		t.Fatalf("ServerTimezone() offset = %q, want a +HH:MM / -HH:MM offset", offset)
	}
}

func TestZoneNameFor(t *testing.T) {
	tests := []struct {
		locationName string
		abbreviation string
		want         string
	}{
		{locationName: "Europe/Amsterdam", abbreviation: "CEST", want: "Europe/Amsterdam"},
		{locationName: "UTC", abbreviation: "UTC", want: "UTC"},
		// A TZ-less host: time.Local is named "Local" but its abbreviation
		// still identifies the zone. This pair cannot be produced through the
		// public time API -- time.FixedZone makes String() and the
		// abbreviation the same string -- so the rule is exercised here
		// directly rather than through a constructed Location.
		{locationName: "Local", abbreviation: "CEST", want: "CEST"},
		{locationName: "", abbreviation: "UTC", want: "UTC"},
	}

	for _, tc := range tests {
		if got := zoneNameFor(tc.locationName, tc.abbreviation); got != tc.want {
			t.Fatalf("zoneNameFor(%q, %q) = %q, want %q", tc.locationName, tc.abbreviation, got, tc.want)
		}
	}
}

func TestFormatUTCOffset(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{seconds: 0, want: "+00:00"},
		{seconds: 3600, want: "+01:00"},
		{seconds: 7200, want: "+02:00"},
		{seconds: -18000, want: "-05:00"},
		{seconds: 19800, want: "+05:30"},  // Asia/Kolkata
		{seconds: -12600, want: "-03:30"}, // America/St_Johns
		{seconds: 50400, want: "+14:00"},  // Pacific/Kiritimati
	}

	for _, tc := range tests {
		if got := formatUTCOffset(tc.seconds); got != tc.want {
			t.Fatalf("formatUTCOffset(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func weekdaysEqual(a, b []time.Weekday) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
