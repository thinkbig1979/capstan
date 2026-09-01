package services

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

type EventBroadcaster func(event models.StackEvent)

// Sentinel errors returned by RunScan and StartBackgroundScan. Both mean "not
// right now", not "something went wrong": callers should recognise them with
// errors.Is and degrade gracefully rather than surfacing a failure. They are
// sentinels rather than bare formatted errors because handlers.checkUpdates
// used to distinguish them by string comparison, which silently misclassified
// every new error text as a 500 (agent-os-mtbo.9).
var (
	// ErrScanInProgress means a scan is already running; the caller should
	// wait for it rather than starting another.
	ErrScanInProgress = errors.New("scan already in progress")
	// ErrSchedulerStopping means Stop() has committed to shutting down, so no
	// new scan can be registered until the next Start(). See the stopped field
	// on SchedulerService.
	ErrSchedulerStopping = errors.New("scheduler is stopping")
)

// updateChecker is the narrow interface scheduler needs from DockerService.
type updateChecker interface {
	CheckForUpdates(ctx context.Context, db DashboardDB) ([]models.ContainerUpdateInfo, error)
	UpdateContainer(ctx context.Context, containerID string, db DashboardDB) (models.UpdateResult, truth.ActionResult)
}

// containerInspector is the extra capability the SCHEDULED apply path needs and
// the immediate path does not: confirming that a cached container ID still
// resolves to a live container before applying to it. See pruneVanishedTargets.
//
// It is a separate optional interface rather than a third method on
// updateChecker so that handler-level fakes which only ever drive the scan path
// (handlers.handlerTestChecker) keep satisfying updateChecker unchanged. The
// compile-time assertion below is what keeps that from degrading silently: the
// production checker is always a *DockerService, and it must always inspect.
type containerInspector interface {
	InspectContainer(ctx context.Context, containerID string) (container.InspectResponse, error)
}

// The only updateChecker main.go ever constructs a scheduler with is
// *DockerService (cmd/server/main.go, NewSchedulerService call). If a refactor
// ever removed InspectContainer from it, the type assertion in
// pruneVanishedTargets would start failing at runtime and every scheduled apply
// would silently skip its freshness check — the exact defect that check exists
// to prevent. This line turns that into a build failure instead.
var _ containerInspector = (*DockerService)(nil)

// applyMaxSleep bounds a single arming of the apply timer.
//
// time.Timer is monotonic: it does not re-derive its deadline from the wall
// clock, so an NTP step, a manual clock correction or a host suspend/resume
// shifts the fire time by the full offset. With weekday scheduling the interval
// to the next occurrence can be seven days, which would make that offset
// arbitrarily large. Sleeping in bounded hops and re-comparing time.Now()
// against the scheduled instant on every wake caps the error at one hop.
const applyMaxSleep = 60 * time.Second

// applyRetryDelay spaces out retries of an apply that was deferred because a
// scan held the single-flight guard. Without it the loop would spin, since a
// deferred fire deliberately leaves the scheduled instant in the past.
const applyRetryDelay = 30 * time.Second

// applySchedule is the scheduler's resolved view of the update_apply_* settings.
// A zero value means immediate mode, which is both the seeded default and the
// fallback for anything unreadable or unparseable (see loadApplySchedule).
type applySchedule struct {
	scheduled bool
	schedule  DailySchedule
}

type SchedulerService struct {
	docker      updateChecker
	db          *database.DB
	mu          sync.Mutex
	ticker      *time.Ticker
	done        chan struct{}
	logger      *slog.Logger
	scanning    bool
	broadcastFn EventBroadcaster
	// stopped is set under mu by Stop() before it ever calls s.wg.Wait(), and
	// checked under the same mu by every path that would call s.wg.Add(1) —
	// the tick handler in Start() and StartBackgroundScan(). Without this,
	// Add (called outside any lock Stop() also takes before its own Wait) is
	// unsynchronized with Stop's Wait from the race detector's point of view:
	// sync.WaitGroup deliberately instruments Add's first-increment and
	// Wait's first-waiter transitions as a modelled read/write on the same
	// location specifically to catch "Add concurrent with Wait" (see
	// sync/waitgroup.go), and that is exactly what happens here — a tick, or
	// a manual ?refresh=true landing in handlers.checkUpdates, arriving while
	// Stop() is unwinding. Routing both the Add and the stopped check through
	// mu gives them the real happens-before edge that was missing, and also
	// closes the behavioural half of the bug: Stop() could return while a
	// scan it never counted was still in flight (agent-os-mtbo.9, ported from
	// BackupSchedulerService/agent-os-o26).
	//
	// Any future timer added to this struct must register with s.wg the same
	// way: check stopped and call s.wg.Add(1) while still holding s.mu.
	// Releasing the lock between the check and the Add reintroduces the race
	// in a form that looks fixed.
	stopped      bool
	wg           sync.WaitGroup
	parentCtx    context.Context
	parentCancel context.CancelFunc

	// applyRearm signals the apply loop to re-read the update_apply_* settings
	// and recompute its next fire time. It is buffered with capacity 1 and only
	// ever written to with a non-blocking send, so ReloadApplySchedule never
	// blocks and never needs to hold anything but mu's read of the field
	// itself — see requirement E in the task brief: a re-arm that could block
	// while Stop() holds mu would turn Stop's wg.Wait into a 10-second stall.
	// nil while the scheduler is not running, in which case there is no apply
	// loop to signal and ReloadApplySchedule is a no-op.
	applyRearm chan struct{}

	// applyClock and applyMaxSleep are the apply loop's two test seams. They
	// are per-instance rather than package-level on purpose: several tests in
	// this package run in parallel, so a mutable global would race.
	//
	// applyClock is the wall clock the loop compares against the schedule;
	// production leaves it at time.Now. applyMaxSleep bounds one arming of the
	// timer (see the constant of the same name); a test shrinks it so a hop
	// costs milliseconds instead of a minute, then moves applyClock across the
	// scheduled instant to make the timer fire for the right reason.
	applyClock    func() time.Time
	applyMaxSleep time.Duration

	// applyNextAt is the instant the apply loop is currently waiting for, or
	// the zero time when nothing is scheduled. The loop publishes it under mu
	// on every (re-)arm so that a caller can tell a pending re-arm from one
	// already picked up — which is what makes the timer tests deterministic
	// instead of sleep-and-hope.
	applyNextAt time.Time
}

func NewSchedulerService(docker updateChecker, db *database.DB, logger *slog.Logger, broadcastFn EventBroadcaster) *SchedulerService {
	if logger == nil {
		logger = slog.Default()
	}
	//nolint:gosec // stored on the struct as parentCancel; called by Stop() in this file (or replaced by the next Start(), which cancels the old one before creating a new one)
	ctx, cancel := context.WithCancel(context.Background())
	return &SchedulerService{
		docker:        docker,
		db:            db,
		logger:        logger.With("component", "scheduler"),
		broadcastFn:   broadcastFn,
		parentCtx:     ctx,
		parentCancel:  cancel,
		applyClock:    time.Now,
		applyMaxSleep: applyMaxSleep,
	}
}

func (s *SchedulerService) Start(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.done != nil {
		close(s.done)
	}

	// Re-initialize the scan lifecycle context so a fresh Stop() works correctly.
	if s.parentCancel != nil {
		s.parentCancel()
	}
	//nolint:gosec // stored on the struct as parentCancel; called by Stop() in this file (or replaced by the next Start(), which cancels the old one before creating a new one, as above)
	s.parentCtx, s.parentCancel = context.WithCancel(context.Background())

	s.ticker = time.NewTicker(interval)
	s.done = make(chan struct{})
	s.stopped = false
	// Capacity 1 so ReloadApplySchedule's non-blocking send always lands when
	// no re-arm is already pending, and is harmlessly dropped when one is.
	s.applyRearm = make(chan struct{}, 1)

	// Capture locals so the goroutine does not race with Stop() zeroing struct fields.
	ticker := s.ticker
	done := s.done
	parentCtx := s.parentCtx
	applyRearm := s.applyRearm

	go func() {
		s.logger.Info("Scheduler started", "interval", interval)
		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				if s.stopped {
					// Stop() has already committed to shutting down (and is
					// about to, or already did, call s.wg.Wait()). Starting a
					// cycle now would call s.wg.Add outside Stop's knowledge,
					// racing Wait — see the stopped field's doc comment. Skip
					// the tick; the <-done case fires on the next iteration.
					s.mu.Unlock()
					continue
				}
				// Add while still holding mu, not after releasing it: Stop()
				// takes mu (to set stopped) before it ever calls s.wg.Wait(),
				// so this ordering gives Add and Wait a real happens-before
				// edge through the mutex instead of racing.
				s.wg.Add(1)
				s.mu.Unlock()

				go func() {
					defer s.wg.Done()
					s.runCycle(parentCtx)
				}()
			case <-done:
				s.logger.Info("Scheduler stopped")
				return
			}
		}
	}()

	// The apply loop shares the scan loop's done channel, so Stop() halts both
	// with the one close. It is deliberately owned by Start(): applying from a
	// cache that no scan ever refreshes would be worse than not applying at
	// all, so a scheduled apply exists only while scanning is enabled.
	go s.runApplyLoop(parentCtx, done, applyRearm)
}

// ReloadApplySchedule tells a running apply loop to re-read the update_apply_*
// settings and recompute its next fire time. Handlers call it after saving
// those settings; it is a no-op when the scheduler is not running.
//
// It deliberately does no work of its own beyond a non-blocking channel send.
// The re-arm must never be performed while holding s.mu on a path the timer
// callback could also be waiting on, or Stop() — which holds mu before its
// wg.Wait() — could deadlock until its 10-second timeout.
func (s *SchedulerService) ReloadApplySchedule() {
	s.mu.Lock()
	rearm := s.applyRearm
	s.mu.Unlock()

	if rearm == nil {
		return
	}
	select {
	case rearm <- struct{}{}:
	default:
		// A re-arm is already queued; it will pick up the same settings.
	}
}

func (s *SchedulerService) Stop() {
	s.mu.Lock()

	// Commit to shutdown before releasing mu (and long before the s.wg.Wait()
	// call below). Any tick handler or StartBackgroundScan that acquires mu
	// after this point sees stopped and skips s.wg.Add entirely, so Add can
	// never be invoked concurrently with Wait — see the stopped field's doc
	// comment.
	s.stopped = true

	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
	if s.done != nil {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
		s.done = nil
	}
	// The apply loop is watching the channel just closed and will return; drop
	// the re-arm handle so a ReloadApplySchedule racing shutdown is a no-op
	// rather than a send into a channel nobody reads again.
	s.applyRearm = nil
	s.applyNextAt = time.Time{}

	// Cancel any in-flight background scan contexts.
	if s.parentCancel != nil {
		s.parentCancel()
	}

	s.mu.Unlock()

	// Wait for in-flight background scan goroutines to finish.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		s.logger.Warn("Timed out waiting for in-flight scan during shutdown")
	}
}

func (s *SchedulerService) Restart(interval time.Duration) {
	s.Stop()
	s.Start(interval)
}

func (s *SchedulerService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticker != nil
}

// performScan executes the update scan body. It does not touch s.mu or s.scanning.
// On success it broadcasts update_scan_complete; on failure it broadcasts update_scan_failed.
// Finding #7: local/remote digests are persisted when available.
func (s *SchedulerService) performScan(ctx context.Context) ([]models.CachedUpdate, error) {
	results, err := s.docker.CheckForUpdates(ctx, s.db)
	if err != nil {
		if dbErr := s.db.SetSetting("update_scan_last_error", err.Error()); dbErr != nil {
			s.logger.Error("Failed to record scan error", "error", dbErr)
		}
		s.logger.Error("Scan failed", "error", err)
		if s.broadcastFn != nil {
			s.broadcastFn(models.StackEvent{Type: "update_scan_failed", Timestamp: time.Now()})
		}
		return nil, err
	}

	var cachedUpdates []models.CachedUpdate
	now := time.Now().Format(time.RFC3339)
	for _, r := range results {
		// Finding #7: persist both digests we already computed during detection.
		// selectUpdates resolved the remote index digest to decide local != remote,
		// so it travels through on the result — no re-fetch needed.
		cachedUpdates = append(cachedUpdates, models.CachedUpdate{
			ID:            uuid.New().String(),
			ContainerID:   r.ContainerID,
			ContainerName: r.ContainerName,
			Image:         r.ImageRef,
			ImageRef:      r.ImageRef,
			State:         r.State,
			StackID:       r.StackID,
			ProjectName:   r.ProjectName,
			ServiceName:   r.ServiceName,
			IsCompose:     r.IsCompose,
			LocalDigest:   r.LocalDigest,
			RemoteDigest:  r.RemoteDigest,
			ScannedAt:     now,
		})
	}

	if err := s.db.SetCachedUpdates(cachedUpdates); err != nil {
		s.logger.Error("Failed to cache updates", "error", err)
	}

	if err := s.db.SetSetting("update_scan_last_run", now); err != nil {
		s.logger.Error("Failed to record scan time", "error", err)
	}
	if err := s.db.SetSetting("update_scan_last_error", ""); err != nil {
		s.logger.Error("Failed to clear scan error", "error", err)
	}

	s.logger.Info("Scan completed", "updates_found", len(cachedUpdates))

	if s.broadcastFn != nil {
		s.broadcastFn(models.StackEvent{Type: "update_scan_complete", Timestamp: time.Now()})
	}

	return cachedUpdates, nil
}

func (s *SchedulerService) RunScan(ctx context.Context) ([]models.CachedUpdate, error) {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return nil, ErrScanInProgress
	}
	s.scanning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
	}()

	scanCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	return s.performScan(scanCtx)
}

func (s *SchedulerService) IsScanning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanning
}

func (s *SchedulerService) StartBackgroundScan() error {
	s.mu.Lock()
	if s.stopped {
		// Stop() has committed to shutting down; admitting a scan now would
		// call s.wg.Add behind Stop's back — see the stopped field's doc
		// comment. Refuse instead. Start() clears the latch, so this only
		// affects the shutdown window (and the gap inside Restart()).
		s.mu.Unlock()
		return ErrSchedulerStopping
	}
	if s.scanning {
		s.mu.Unlock()
		return ErrScanInProgress
	}
	s.scanning = true
	parentCtx := s.parentCtx // capture under lock to avoid data race
	// Add while still holding mu — same happens-before argument as the tick
	// handler in Start().
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			s.scanning = false
			s.mu.Unlock()
		}()

		scanCtx, cancel := context.WithTimeout(parentCtx, 10*time.Minute)
		defer cancel()

		if _, err := s.performScan(scanCtx); err != nil {
			s.logger.Error("Background scan failed", "error", err)
		}
	}()

	return nil
}

func (s *SchedulerService) runCycle(ctx context.Context) {
	updates, err := s.RunScan(ctx)
	if err != nil {
		s.logger.Error("Scheduler scan cycle failed", "error", err)
		return
	}

	if updates == nil {
		updates = []models.CachedUpdate{}
	}

	// Scheduled mode keeps scanning on the interval but moves APPLYING to the
	// clock, so the scan tick stops here and the apply loop does the rest. Every
	// other case — immediate mode, an unreadable setting, an unparseable stored
	// schedule — resolves to immediate and applies exactly as before.
	if s.loadApplySchedule().scheduled {
		s.logger.Info("Scheduled apply mode: updates cached, not applied on this scan tick",
			"updates_found", len(updates))
		return
	}

	s.RunAutoUpdates(ctx, updates)
}

// loadApplySchedule resolves the update_apply_* settings into an applySchedule.
//
// Requirement C of the task brief: it NEVER falls back to "no schedule". Every
// failure path — an unreadable setting, an unparseable time or weekday list —
// returns immediate mode and says so in the log. Falling back to a dead
// schedule would mean the apply timer never fires and nothing tells the
// operator their updates stopped landing.
func (s *SchedulerService) loadApplySchedule() applySchedule {
	immediate := applySchedule{}

	mode, err := s.db.GetSetting("update_apply_mode")
	if err != nil {
		// A missing key is the pre-migration-14 state, and immediate is exactly
		// what migration 14 seeds, so that case is not worth shouting about.
		if !errors.Is(err, sql.ErrNoRows) {
			s.logger.Error("Failed to read update_apply_mode; applying updates immediately", "error", err)
		}
		return immediate
	}
	if mode != "scheduled" {
		return immediate
	}

	hhmm, timeErr := s.db.GetSetting("update_apply_time")
	days, daysErr := s.db.GetSetting("update_apply_days")
	if timeErr != nil || daysErr != nil {
		s.logger.Error("Apply mode is scheduled but its schedule could not be read; applying updates immediately",
			"time_error", timeErr, "days_error", daysErr)
		return immediate
	}

	schedule, err := ParseDailySchedule(hhmm, days)
	if err != nil {
		s.logger.Error("Apply mode is scheduled but the stored schedule is invalid; applying updates immediately",
			"update_apply_time", hhmm, "update_apply_days", days, "error", err)
		return immediate
	}

	return applySchedule{scheduled: true, schedule: schedule}
}

// nextApplyInstant reports when the schedule next fires after now.
func nextApplyInstant(cfg applySchedule, now time.Time) (time.Time, bool) {
	if !cfg.scheduled {
		return time.Time{}, false
	}
	// NextAfter takes its zone from now.Location(), so passing time.Now() gives
	// wall-clock, server-local behaviour: 03:00 stays 03:00 across DST.
	next, ok := cfg.schedule.NextAfter(now)
	if !ok {
		// Not tested — inferred from schedule.go: ParseWeekdays rejects an empty
		// day list ("no weekdays selected"), so a schedule that reached here via
		// loadApplySchedule always has at least one day, and NextAfter's own
		// closing comment records that ok=false is unreachable in that case.
		return time.Time{}, false
	}
	return next, true
}

// applyWait is how long to sleep before the next wake, given the instant we are
// waiting for. It is capped at applyMaxSleep so the loop re-derives its decision
// from the wall clock at least that often; see that constant's doc comment.
func applyWait(nextAt time.Time, armed bool, now time.Time, maxSleep time.Duration) time.Duration {
	if !armed {
		// Nothing scheduled. Keep hopping anyway so that a re-arm is never the
		// only thing that can wake the loop.
		return maxSleep
	}
	remaining := nextAt.Sub(now)
	switch {
	case remaining <= 0:
		// Only reachable after a fire was deferred (a scan held the
		// single-flight guard), which leaves nextAt in the past deliberately so
		// the apply is retried rather than lost. Back off instead of spinning,
		// but never sleep longer than one hop.
		return min(applyRetryDelay, maxSleep)
	case remaining > maxSleep:
		return maxSleep
	default:
		return remaining
	}
}

// resetTimer re-arms t for d, stopping and draining it first. Safe whether or
// not t has already fired.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// runApplyLoop is the scheduled-apply half of the scheduler: a single
// re-armable time.Timer (never a ticker) that wakes in bounded hops, compares
// the wall clock against the next scheduled instant, and applies the CACHED
// updates when that instant has passed. It shares the scan loop's done channel,
// so Stop() halts it with the same close.
//
// There is no catch-up: a fire missed because the process was down is simply
// not run.
func (s *SchedulerService) runApplyLoop(parentCtx context.Context, done chan struct{}, rearm chan struct{}) {
	now, maxSleep := s.applySeams()

	cfg := s.loadApplySchedule()
	nextAt, armed := nextApplyInstant(cfg, now())
	s.publishNextApplyAt(nextAt, armed)
	s.logApplyArming(cfg, nextAt, armed)

	timer := time.NewTimer(maxSleep)
	defer timer.Stop()

	for {
		resetTimer(timer, applyWait(nextAt, armed, now(), maxSleep))

		select {
		case <-done:
			return

		case <-rearm:
			cfg = s.loadApplySchedule()
			nextAt, armed = nextApplyInstant(cfg, now())
			s.publishNextApplyAt(nextAt, armed)
			s.logApplyArming(cfg, nextAt, armed)

		case <-timer.C:
			if !armed || now().Before(nextAt) {
				// A bounded hop, not the scheduled instant. Re-arm and wait.
				continue
			}
			if !s.applyNow(parentCtx) {
				// Deferred, not done. Leave nextAt in the past so the next hop
				// retries rather than skipping the night entirely.
				continue
			}
			nextAt, armed = nextApplyInstant(cfg, now())
			s.publishNextApplyAt(nextAt, armed)
		}
	}
}

// applySeams reads the loop's clock and hop bound once, under mu, so the loop
// goroutine never reads those fields concurrently with a test writing them.
func (s *SchedulerService) applySeams() (func() time.Time, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.applyClock
	if now == nil {
		now = time.Now
	}
	maxSleep := s.applyMaxSleep
	if maxSleep <= 0 {
		maxSleep = applyMaxSleep
	}
	return now, maxSleep
}

// NextApplyAt reports the instant the apply loop is waiting for. The zero time
// means nothing is scheduled: immediate mode, or no running scheduler.
func (s *SchedulerService) NextApplyAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyNextAt
}

func (s *SchedulerService) publishNextApplyAt(nextAt time.Time, armed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if armed {
		s.applyNextAt = nextAt
	} else {
		s.applyNextAt = time.Time{}
	}
}

func (s *SchedulerService) logApplyArming(cfg applySchedule, nextAt time.Time, armed bool) {
	if !cfg.scheduled {
		s.logger.Info("Auto-update apply mode: immediate (applying on the scan tick)")
		return
	}
	if !armed {
		s.logger.Error("Auto-update apply mode is scheduled but no next run could be computed; no update will be applied",
			"apply_time", cfg.schedule.FormatTime(), "apply_days", cfg.schedule.FormatDays())
		return
	}
	s.logger.Info("Auto-update apply scheduled",
		"apply_time", cfg.schedule.FormatTime(),
		"apply_days", cfg.schedule.FormatDays(),
		"next_apply_at", nextAt.Format(time.RFC3339))
}

// applyNow runs one scheduled apply over the CACHED updates. It reports whether
// the apply actually ran: false means it was deferred (a scan holds the
// single-flight guard) or refused (the scheduler is stopping), and the caller
// retries on its next hop.
//
// RunAutoUpdates does not read s.scanning on its own, so without this guard a
// scheduled apply could run concurrently with a scan. That matters because
// SetCachedUpdates does DELETE-then-INSERT in one transaction: an apply could
// evict a row the scan had just written, while both paths wrote the same policy
// rows (requirement B).
func (s *SchedulerService) applyNow(ctx context.Context) bool {
	s.mu.Lock()
	if s.stopped {
		// Stop() has committed to shutting down and is about to call
		// s.wg.Wait(); registering work now would race it. See the stopped
		// field's doc comment — this is that comment's "any future timer".
		s.mu.Unlock()
		return false
	}
	if s.scanning {
		s.mu.Unlock()
		s.logger.Warn("Scheduled auto-update apply deferred: a scan is in progress")
		return false
	}
	s.scanning = true
	// Add while still holding mu, not after releasing it: same happens-before
	// argument as the tick handler in Start().
	s.wg.Add(1)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
		s.wg.Done()
	}()

	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	updates, err := s.db.GetCachedUpdates()
	if err != nil {
		s.logger.Error("Scheduled auto-update apply: failed to read cached updates", "error", err)
		return true
	}

	s.logger.Info("Scheduled auto-update apply starting", "cached_updates", len(updates))
	s.RunAutoUpdates(applyCtx, s.pruneVanishedTargets(applyCtx, updates))
	return true
}

// pruneVanishedTargets drops cached rows whose container no longer exists, and
// evicts them from the cache.
//
// This is the freshness check the immediate path gets for free. There, the scan
// hands RunAutoUpdates its own return value seconds later, so the container IDs
// are current by proximity. Scheduled mode replaces that with a DB read of rows
// that can be up to a week old, and nothing else checks: cached_updates.scanned_at
// is written and round-tripped but has no consumers.
//
// Without this, a container recreated between the scan and the apply — a stack
// redeploy is enough — leaves a dead ID in the cache, UpdateContainer fails at
// truth.ResolveContainerImage, and RunAutoUpdates' default branch increments
// policy.ConsecutiveFailures. Three nights of that sets policy.Paused and
// auto-update is off for good, visible only as history rows reading like a
// transient Docker hiccup. A container that is gone is an EVICTION, never a
// failure.
//
// A container we cannot resolve for any OTHER reason (daemon unreachable,
// timeout) is neither applied nor evicted: it is left in the cache for the next
// run, because evicting on a daemon outage would empty the cache wholesale.
func (s *SchedulerService) pruneVanishedTargets(ctx context.Context, updates []models.CachedUpdate) []models.CachedUpdate {
	inspector, ok := s.docker.(containerInspector)
	if !ok {
		// Cannot happen in production: the compile-time assertion above pins
		// *DockerService as a containerInspector, and it is the only checker
		// main.go builds a scheduler with. Reachable only from a test double.
		s.logger.Warn("Update checker cannot inspect containers; applying cached updates without a freshness check")
		return updates
	}

	live := make([]models.CachedUpdate, 0, len(updates))
	evicted, unresolved := 0, 0

	for _, update := range updates {
		_, err := inspector.InspectContainer(ctx, update.ContainerID)
		switch {
		case err == nil:
			live = append(live, update)

		case client.IsErrNotFound(err):
			evicted++
			s.logger.Info("Evicting cached update: container no longer exists",
				"container", update.ContainerName, "containerID", update.ContainerID)
			if delErr := s.db.DeleteCachedUpdate(update.ContainerID); delErr != nil {
				s.logger.Warn("Failed to evict cached update for a vanished container",
					"containerID", update.ContainerID, "error", delErr)
			}

		default:
			unresolved++
			s.logger.Warn("Skipping cached update: container could not be inspected",
				"container", update.ContainerName, "containerID", update.ContainerID, "error", err)
		}
	}

	if evicted > 0 || unresolved > 0 {
		s.logger.Info("Scheduled auto-update apply: cache re-resolved",
			"live", len(live), "evicted", evicted, "unresolved", unresolved)
	}

	return live
}

// RunAutoUpdates applies auto-update policies to the given update candidates.
//
// Finding #8 fix: uses typed truth.ActionResult so that:
//   - success (image advanced) → reset consecutive failure counter
//   - no_change (confirmed up-to-date) → do NOT reset counter; log and skip
//     to avoid infinite churn re-applying an image that will never advance
//   - failed → increment counter toward pause (unchanged behavior)
//
// Eviction (finding #4): on success or no_change, the cached_updates row is
// deleted so the frontend list converges without waiting for the next scan.
func (s *SchedulerService) RunAutoUpdates(ctx context.Context, updates []models.CachedUpdate) {
	autoEnabledStr, err := s.db.GetSetting("auto_update_enabled")
	if err != nil || autoEnabledStr != "true" {
		return
	}

	policies, err := s.db.GetEnabledAutoUpdatePolicies()
	if err != nil {
		s.logger.Error("Failed to get auto-update policies", "error", err)
		return
	}

	containerPolicies := make(map[string]*models.AutoUpdatePolicy)
	stackPolicies := make(map[string]*models.AutoUpdatePolicy)
	for i := range policies {
		p := &policies[i]
		switch p.TargetType {
		case "container":
			containerPolicies[p.TargetID] = p
		case "stack":
			stackPolicies[p.TargetID] = p
		}
	}

	succeeded := 0
	failed := 0
	skipped := 0

	for _, update := range updates {
		policy, hasPolicy := containerPolicies[update.ContainerID]
		if !hasPolicy {
			if update.StackID != "" {
				policy, hasPolicy = stackPolicies[update.StackID]
			}
		}

		if !hasPolicy {
			skipped++
			continue
		}

		historyID := uuid.New().String()
		now := time.Now().Format(time.RFC3339)

		historyEntry := &models.UpdateHistoryEntry{
			ID:            historyID,
			ContainerID:   update.ContainerID,
			ContainerName: update.ContainerName,
			Image:         update.ImageRef,
			OldDigest:     nil,
			Status:        "pending",
			Trigger:       "auto",
			StartedAt:     now,
		}
		if update.StackID != "" {
			historyEntry.StackID = &update.StackID
		}

		if err := s.db.InsertUpdateHistory(historyEntry); err != nil {
			s.logger.Error("Failed to insert update history", "error", err)
			continue
		}

		result, ar := s.docker.UpdateContainer(ctx, update.ContainerID, s.db)

		switch ar.Outcome {
		case truth.OutcomeSuccess:
			// Image actually advanced — record success, reset failure counter.
			succeeded++
			if err := s.db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":       "success",
				"old_digest":   result.OldDigest,
				"new_digest":   result.NewDigest,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  result.DurationMs,
			}); err != nil {
				s.logger.Error("Failed to update success history", "error", err)
			}
			// Convergence: evict from cache.
			if evictErr := s.db.DeleteCachedUpdate(update.ContainerID); evictErr != nil {
				s.logger.Warn("Failed to evict cached update entry after auto-update",
					"containerID", update.ContainerID, "error", evictErr)
			}
			policy.ConsecutiveFailures = 0
			policy.UpdatedAt = time.Now().Format(time.RFC3339)
			if err := s.db.UpsertAutoUpdatePolicy(policy); err != nil {
				s.logger.Error("Failed to reset policy failures", "error", err)
			}

		case truth.OutcomeNoChange:
			// Pull succeeded but image did not advance — it was already current.
			// Finding #8: do NOT reset consecutive failure counter; log and move on.
			// The item is evicted so it leaves the pending list without triggering
			// an infinite re-apply churn.
			s.logger.Info("Auto-update: image already up to date (no_change), skipping reset",
				"container", update.ContainerName,
				"reason", ar.Reason)
			if err := s.db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":       "success",
				"old_digest":   result.OldDigest,
				"new_digest":   result.NewDigest,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  result.DurationMs,
			}); err != nil {
				s.logger.Error("Failed to update no-change history", "error", err)
			}
			// Convergence: evict from cache so this item leaves the list.
			if evictErr := s.db.DeleteCachedUpdate(update.ContainerID); evictErr != nil {
				s.logger.Warn("Failed to evict cached update entry after no_change",
					"containerID", update.ContainerID, "error", evictErr)
			}
			// Do NOT increment succeeded (no real update) and do NOT touch
			// consecutive failure counter.

		default: // OutcomeFailed
			failed++
			errMsg := ar.Reason
			if ar.Err != nil {
				errMsg = ar.Err.Error()
			}
			if err := s.db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":        "failed",
				"error_message": errMsg,
				"completed_at":  time.Now().Format(time.RFC3339),
				"duration_ms":   result.DurationMs,
			}); err != nil {
				s.logger.Error("Failed to update failure history", "error", err)
			}

			policy.ConsecutiveFailures++
			if policy.ConsecutiveFailures >= 3 {
				policy.Paused = true
				policy.UpdatedAt = time.Now().Format(time.RFC3339)
				if err := s.db.UpsertAutoUpdatePolicy(policy); err != nil {
					s.logger.Error("Failed to update paused policy", "error", err)
				}

				pausedHistory := &models.UpdateHistoryEntry{
					ID:            uuid.New().String(),
					ContainerID:   update.ContainerID,
					ContainerName: update.ContainerName,
					Image:         update.ImageRef,
					Status:        "paused",
					Trigger:       "auto",
					StartedAt:     time.Now().Format(time.RFC3339),
				}
				if update.StackID != "" {
					pausedHistory.StackID = &update.StackID
				}
				if err := s.db.InsertUpdateHistory(pausedHistory); err != nil {
					s.logger.Error("Failed to insert paused history", "error", err)
				}

				s.logger.Warn("Auto-update paused after 3 consecutive failures",
					"container", update.ContainerName,
					"target_type", policy.TargetType,
					"target_id", policy.TargetID)
			} else {
				policy.UpdatedAt = time.Now().Format(time.RFC3339)
				if err := s.db.UpsertAutoUpdatePolicy(policy); err != nil {
					s.logger.Error("Failed to update policy", "error", err)
				}
			}
		}
	}

	s.logger.Info("Auto-update cycle completed",
		"succeeded", succeeded,
		"failed", failed,
		"skipped", skipped)

	if s.broadcastFn != nil {
		s.broadcastFn(models.StackEvent{Type: "update_policy_changed", Timestamp: time.Now()})
		if succeeded > 0 || failed > 0 {
			s.broadcastFn(models.StackEvent{Type: "resource_changed", Event: "container_update", Timestamp: time.Now()})
		}
	}
}
