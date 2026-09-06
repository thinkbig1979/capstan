package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// WHY THIS TEST DRIVES A FAULT RATHER THAN A VALUE. update_scan_interval and
// update_apply_mode are read from the SAME source, and the second read exists
// precisely to warn that the first produced nothing. So one fault reaches both:
// the scheduler does not start AND the warning that exists to say so is
// silenced by the very fault it would have reported. A test that drives only
// one of the two settings cannot see that, because either one alone still
// leaves the other arm apparently working.
//
// WHY RECORDS AND NOT FORMATTED TEXT. Asserting on a rendered log line is
// satisfiable by a PREFIX or by an unrelated record, and that exact mistake has
// shipped in this repo. Here the injected error is compared by IDENTITY
// (errors.Is against a sentinel nothing else constructs) and the ERROR-level
// record COUNT is asserted, so an extra or a different record fails rather than
// passes.

type capturedRecord struct {
	level slog.Level
	msg   string
	attrs map[string]slog.Value
}

type captureHandler struct {
	mu   *sync.Mutex
	recs *[]capturedRecord
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]slog.Value, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.recs = append(*h.recs, capturedRecord{level: r.Level, msg: r.Message, attrs: attrs})
	return nil
}

func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h captureHandler) WithGroup(string) slog.Handler      { return h }

func newCaptureLogger() (*slog.Logger, func() []capturedRecord) {
	var mu sync.Mutex
	var recs []capturedRecord
	log := slog.New(captureHandler{mu: &mu, recs: &recs})
	return log, func() []capturedRecord {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedRecord, len(recs))
		copy(out, recs)
		return out
	}
}

func recordsAtLevel(recs []capturedRecord, level slog.Level) []capturedRecord {
	var out []capturedRecord
	for _, r := range recs {
		if r.level == level {
			out = append(out, r)
		}
	}
	return out
}

// carriesError reports whether the record's "error" attribute wraps target.
// Identity, not text: a rendered message could be reproduced by accident, an
// unexported sentinel cannot.
func (r capturedRecord) carriesError(target error) bool {
	v, ok := r.attrs["error"]
	if !ok {
		return false
	}
	err, ok := v.Any().(error)
	return ok && errors.Is(err, target)
}

// fakeSettings injects a per-key fault and records the read order, so a test
// can assert that a source already known to be broken is not consulted again
// as if its answer meant something.
type fakeSettings struct {
	values map[string]string
	errs   map[string]error
	reads  []string
}

func (f *fakeSettings) GetSetting(key string) (string, error) {
	f.reads = append(f.reads, key)
	if err := f.errs[key]; err != nil {
		return "", err
	}
	return f.values[key], nil
}

type recordingScheduler struct{ intervals []time.Duration }

func (r *recordingScheduler) Start(interval time.Duration) {
	r.intervals = append(r.intervals, interval)
}

// errSettingsFault is constructed nowhere else, so a record carrying it can
// only have come from the injected read.
var errSettingsFault = errors.New("settings store unreadable (test sentinel)")

// MUST-PASS ARM. A readable interval still starts the scheduler with exactly
// that interval and logs nothing at ERROR. Without this arm a change that
// simply logged an error unconditionally would pass every other case here.
func TestStartUpdateScheduler_StartsWhenIntervalIsConfigured(t *testing.T) {
	db := &fakeSettings{values: map[string]string{"update_scan_interval": "30"}}
	sched := &recordingScheduler{}
	log, records := newCaptureLogger()

	if started := startUpdateScheduler(sched, db, log); !started {
		t.Fatal("startUpdateScheduler reported not started for a readable interval of 30")
	}
	if len(sched.intervals) != 1 || sched.intervals[0] != 30*time.Minute {
		t.Fatalf("scheduler intervals = %v, want exactly one 30m start", sched.intervals)
	}
	if errs := recordsAtLevel(records(), slog.LevelError); len(errs) != 0 {
		t.Fatalf("a healthy boot logged %d ERROR record(s): %+v", len(errs), errs)
	}
}

// MUST-PASS ARM. The pre-existing "apply is scheduled but the interval is 0"
// warning still fires, with the apply time it read. This is the behaviour the
// fix must not trade away while adding fault reporting.
func TestStartUpdateScheduler_ScheduledApplyWithZeroIntervalStillWarns(t *testing.T) {
	db := &fakeSettings{values: map[string]string{
		"update_scan_interval": "0",
		"update_apply_mode":    "scheduled",
		"update_apply_time":    "03:00",
	}}
	sched := &recordingScheduler{}
	log, records := newCaptureLogger()

	if started := startUpdateScheduler(sched, db, log); started {
		t.Fatal("startUpdateScheduler reported started for an interval of 0")
	}
	if len(sched.intervals) != 0 {
		t.Fatalf("scheduler was started with %v despite a 0 interval", sched.intervals)
	}
	warns := recordsAtLevel(records(), slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("want exactly 1 WARN for a scheduled apply with a 0 interval, got %d: %+v", len(warns), warns)
	}
	if got := warns[0].attrs["apply_time"].String(); got != "03:00" {
		t.Fatalf("warning apply_time = %q, want %q", got, "03:00")
	}
	if errs := recordsAtLevel(records(), slog.LevelError); len(errs) != 0 {
		t.Fatalf("a readable-but-zero interval logged %d ERROR record(s): %+v", len(errs), errs)
	}
}

// MUST-FAIL-BEFORE ARM, and the whole point of the bead. One fault reaches
// both settings: the scheduler cannot start, and the else-branch that exists to
// warn about exactly that reads "" from the same broken source and stays quiet.
// Before the fix this function returns false having emitted NOTHING at all.
func TestStartUpdateScheduler_SettingsFaultIsReportedNotSilentlySkipped(t *testing.T) {
	db := &fakeSettings{errs: map[string]error{
		"update_scan_interval": errSettingsFault,
		"update_apply_mode":    errSettingsFault,
		"update_apply_time":    errSettingsFault,
	}}
	sched := &recordingScheduler{}
	log, records := newCaptureLogger()

	started := startUpdateScheduler(sched, db, log)

	if started {
		t.Fatal("startUpdateScheduler reported started even though no setting could be read")
	}
	if len(sched.intervals) != 0 {
		t.Fatalf("scheduler was started with %v from an unreadable setting", sched.intervals)
	}

	errs := recordsAtLevel(records(), slog.LevelError)
	if len(errs) != 1 {
		t.Fatalf("an unreadable settings store produced %d ERROR record(s), want exactly 1; "+
			"0 means the operator gets no update scheduler and no signal that anything went wrong: %+v",
			len(errs), errs)
	}
	if !errs[0].carriesError(errSettingsFault) {
		t.Fatalf("the ERROR record does not carry the injected fault, so it is reporting something else: %+v", errs[0])
	}
	if !strings.Contains(errs[0].msg, "update scheduler was not started") {
		t.Fatalf("the ERROR record does not name the consequence (%q), only the cause: %q",
			"update scheduler was not started", errs[0].msg)
	}
	if len(db.reads) == 0 || db.reads[0] != "update_scan_interval" {
		t.Fatalf("read order = %v, want update_scan_interval first", db.reads)
	}
}

// MUST-FAIL-BEFORE ARM, the else-branch in isolation. The interval reads
// cleanly as 0, so the apply-mode branch is the only thing that could speak,
// and its own read faults. Before the fix applyMode is "" and nothing is
// logged: a configured scheduled apply sits dead and unmentioned.
func TestStartUpdateScheduler_ApplyModeFaultIsReported(t *testing.T) {
	db := &fakeSettings{
		values: map[string]string{"update_scan_interval": "0"},
		errs:   map[string]error{"update_apply_mode": errSettingsFault},
	}
	sched := &recordingScheduler{}
	log, records := newCaptureLogger()

	if started := startUpdateScheduler(sched, db, log); started {
		t.Fatal("startUpdateScheduler reported started for an interval of 0")
	}

	errs := recordsAtLevel(records(), slog.LevelError)
	if len(errs) != 1 {
		t.Fatalf("an unreadable update_apply_mode produced %d ERROR record(s), want exactly 1; "+
			"0 means a scheduled apply that will never run is never mentioned: %+v", len(errs), errs)
	}
	if !errs[0].carriesError(errSettingsFault) {
		t.Fatalf("the ERROR record does not carry the injected fault: %+v", errs[0])
	}
}

// MUST-FAIL-BEFORE ARM, the third discard in the same block. A stored interval
// that is not a number is discarded by `err == nil` and leaves the scheduler
// off with nothing logged, which looks identical to "not configured".
func TestStartUpdateScheduler_UnparseableIntervalIsReported(t *testing.T) {
	db := &fakeSettings{values: map[string]string{"update_scan_interval": "not-a-number"}}
	sched := &recordingScheduler{}
	log, records := newCaptureLogger()

	if started := startUpdateScheduler(sched, db, log); started {
		t.Fatal("startUpdateScheduler reported started for an unparseable interval")
	}
	if len(sched.intervals) != 0 {
		t.Fatalf("scheduler was started with %v from an unparseable interval", sched.intervals)
	}

	errs := recordsAtLevel(records(), slog.LevelError)
	if len(errs) != 1 {
		t.Fatalf("an unparseable interval produced %d ERROR record(s), want exactly 1: %+v", len(errs), errs)
	}
	if got := errs[0].attrs["value"].String(); got != "not-a-number" {
		t.Fatalf("the ERROR record's value attribute = %q, want the offending setting %q", got, "not-a-number")
	}
}
