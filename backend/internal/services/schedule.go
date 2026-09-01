package services

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// scheduleTimePattern is the single source of truth for the accepted wall-clock
// format, shared by the parser and by handlers that want to reject a bad body
// before constructing a schedule. 24-hour, zero-padded, no seconds.
var scheduleTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// DailySchedule is a wall-clock recurrence: a time of day that fires on a set
// of weekdays, in the server's local time. Both the update scheduler and the
// backup scheduler need exactly this, so it lives here rather than being
// written twice and drifting.
type DailySchedule struct {
	Hour   int
	Minute int
	// Days uses Go's time.Weekday numbering, 0=Sunday..6=Saturday. Sorted and
	// deduped by ParseDailySchedule/ParseWeekdays; NextAfter does not rely on
	// either property, so a hand-built value still behaves.
	Days []time.Weekday
}

// ParseDailySchedule validates and builds a schedule from its two stored
// strings: "HH:MM" and a comma-separated weekday list such as "0,1,2,3,4,5,6".
// Errors are plain and human-readable; handlers map them to 400.
func ParseDailySchedule(hhmm, days string) (DailySchedule, error) {
	hour, minute, err := ParseScheduleTime(hhmm)
	if err != nil {
		return DailySchedule{}, err
	}

	weekdays, err := ParseWeekdays(days)
	if err != nil {
		return DailySchedule{}, err
	}

	return DailySchedule{Hour: hour, Minute: minute, Days: weekdays}, nil
}

// ParseScheduleTime validates a "HH:MM" wall-clock time and returns its parts.
// Exported so handlers can validate a field on its own, without a weekday list.
func ParseScheduleTime(hhmm string) (hour int, minute int, err error) {
	if !scheduleTimePattern.MatchString(hhmm) {
		return 0, 0, fmt.Errorf("invalid time %q: expected HH:MM in 24-hour form, for example 03:00", hhmm)
	}

	// The pattern already constrains both halves to their ranges, so neither
	// Atoi can fail and neither result needs a second bounds check.
	hour, _ = strconv.Atoi(hhmm[:2])
	minute, _ = strconv.Atoi(hhmm[3:])
	return hour, minute, nil
}

// ParseWeekdays validates a comma-separated list of Go weekday ints and returns
// them sorted and deduped. An empty list is an error: a schedule that fires on
// no day is silently dead, which is never what an operator meant to configure.
func ParseWeekdays(csv string) ([]time.Weekday, error) {
	seen := make(map[time.Weekday]bool, 7)
	var days []time.Weekday

	for _, field := range strings.Split(csv, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}

		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("invalid weekday %q: expected a number 0-6 (0=Sunday)", field)
		}
		if n < 0 || n > 6 {
			return nil, fmt.Errorf("invalid weekday %d: must be 0-6 (0=Sunday)", n)
		}

		day := time.Weekday(n)
		if seen[day] {
			continue
		}
		seen[day] = true
		days = append(days, day)
	}

	if len(days) == 0 {
		return nil, fmt.Errorf("no weekdays selected: expected at least one number 0-6 (0=Sunday)")
	}

	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	return days, nil
}

// FormatWeekdays renders a weekday list back into the stored form, so that
// FormatWeekdays(ParseWeekdays(s)) round-trips to a normalised s.
func FormatWeekdays(days []time.Weekday) string {
	parts := make([]string, 0, len(days))
	for _, day := range days {
		parts = append(parts, strconv.Itoa(int(day)))
	}
	return strings.Join(parts, ",")
}

// NextAfter returns the first scheduled instant strictly after t, in
// t.Location(). It never returns t itself, so a caller that fires at exactly
// the scheduled instant and immediately asks for the next one advances instead
// of looping. ok is false only when the schedule has no days.
func (s DailySchedule) NextAfter(t time.Time) (next time.Time, ok bool) {
	if len(s.Days) == 0 {
		return time.Time{}, false
	}

	loc := t.Location()
	// Walk the calendar from midday rather than from t: AddDate normalises its
	// result, and stepping from an instant that a DST transition removes could
	// otherwise skip or repeat a calendar day. Midday is never inside a
	// transition. The offset runs to 7 so a schedule with a single allowed
	// weekday, asked after today's occurrence, lands on the same weekday next
	// week.
	base := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
	for offset := 0; offset <= 7; offset++ {
		day := base.AddDate(0, 0, offset)
		if !s.allows(day.Weekday()) {
			continue
		}

		// time.Date in t.Location() is what makes this a wall-clock schedule:
		// 03:00 stays 03:00 across a DST transition. Adding 24h durations
		// instead would drift the schedule by an hour twice a year.
		candidate := time.Date(day.Year(), day.Month(), day.Day(), s.Hour, s.Minute, 0, 0, loc)
		if candidate.After(t) {
			return candidate, true
		}
	}

	// Unreachable while Days is non-empty: seven consecutive days cover every
	// weekday, and the eighth re-covers today's, so some candidate is always
	// strictly after t.
	return time.Time{}, false
}

func (s DailySchedule) allows(day time.Weekday) bool {
	for _, allowed := range s.Days {
		if allowed == day {
			return true
		}
	}
	return false
}

// FormatTime renders the time of day in the stored "HH:MM" form.
func (s DailySchedule) FormatTime() string {
	return fmt.Sprintf("%02d:%02d", s.Hour, s.Minute)
}

// FormatDays renders the weekday set in the stored comma-separated form.
func (s DailySchedule) FormatDays() string {
	return FormatWeekdays(s.Days)
}

// ServerTimezone reports the zone these schedules are interpreted in: the IANA
// name of time.Local plus its current UTC offset as "+02:00". The offset is
// "current" by design -- it is shown to an operator alongside a schedule they
// are editing now, so it should reflect the DST state they are in now.
func ServerTimezone() (name string, offset string) {
	return serverTimezoneAt(time.Now())
}

// serverTimezoneAt is ServerTimezone with the clock injected, so the DST-
// dependent half can be tested at a fixed instant instead of reassigning the
// package-global time.Local (which several tests in this package would race
// against, since they run with t.Parallel).
func serverTimezoneAt(now time.Time) (name string, offset string) {
	abbreviation, offsetSeconds := now.Zone()
	return zoneNameFor(now.Location().String(), abbreviation), formatUTCOffset(offsetSeconds)
}

// zoneNameFor prefers the IANA location name and falls back to the zone
// abbreviation. time.Local reports the name "Local" when the zone came from
// TZ-less system configuration, which names nothing an operator can act on;
// the abbreviation ("CEST", "UTC") at least identifies the zone.
func zoneNameFor(locationName, abbreviation string) string {
	if locationName == "" || locationName == "Local" {
		return abbreviation
	}
	return locationName
}

func formatUTCOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, offsetSeconds/3600, (offsetSeconds%3600)/60)
}
