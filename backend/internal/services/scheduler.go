package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
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

// autoApplyTimeout bounds one RunAutoUpdates pass, whichever path started it.
// Each update in the pass holds its stack's operation lock, so without a bound a
// hung docker pull or compose up would hold that lock until the server
// restarted (agent-os-o1jg).
const autoApplyTimeout = 10 * time.Minute

// applySchedule is the scheduler's resolved view of the update_apply_* settings.
// A zero value means immediate mode, which is both the seeded default and the
// fallback for an absent key or an unparseable stored value (see
// loadApplySchedule).
//
// unreadable is the THIRD state, and it is not a flavour of immediate: it means
// the settings could not be read at all, so the scheduler knows neither that the
// operator wants immediate mode nor when their maintenance window is. Every
// consumer must treat it as "apply nothing this pass" rather than falling
// through to the zero value, which would apply updates NOW on the strength of a
// database fault (agent-os-rltu).
type applySchedule struct {
	scheduled  bool
	schedule   DailySchedule
	unreadable bool
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

	// applyTimeout bounds one RunAutoUpdates pass; production leaves it at
	// autoApplyTimeout and a test shrinks it so a hung update ends in
	// milliseconds.
	applyTimeout time.Duration

	// applyNextAt is the instant the apply loop is currently waiting for, or
	// the zero time when nothing is scheduled. The loop publishes it under mu
	// on every (re-)arm so that a caller can tell a pending re-arm from one
	// already picked up — which is what makes the timer tests deterministic
	// instead of sleep-and-hope.
	applyNextAt time.Time

	// lifecycleMu serialises Start, Stop, Restart and Close, so Restart's
	// stop-then-start is one transition and no other one can land between its
	// halves (agent-os-z91e.5). It is separate from mu on purpose: Stop holds
	// it across its up-to-10s wait for in-flight scans, and the tick, scan and
	// apply goroutines take mu but never lifecycleMu, so that wait cannot
	// deadlock against the goroutines it is waiting for. Order: lifecycleMu
	// before mu, never the reverse.
	lifecycleMu sync.Mutex
	// closed is the terminal latch set by Close at process shutdown. Once set,
	// Start and Restart refuse, so a settings save that lands during shutdown
	// cannot re-arm the scan ticker or the apply loop. Guarded by lifecycleMu;
	// nothing clears it, it ends with the process.
	closed bool
	// onTransition is a TEST-ONLY seam, nil in production: called under
	// lifecycleMu at the end of every applied transition with whether the
	// scheduler is now running, so a test can tell which of several concurrent
	// calls was applied last.
	onTransition func(running bool)

	// opLock is the per-stack operation lock: auto-apply skips a container
	// whose stack is held instead of updating it under a running backup or
	// lifecycle op (agent-os-a1ye.4). Set by SetOperationLock; nil (tests)
	// means no locking.
	opLock *OperationLock
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
		applyTimeout:  autoApplyTimeout,
	}
}

// SetOperationLock installs the per-stack operation lock shared with the
// handlers and the backup service. main.go passes the same instance.
func (s *SchedulerService) SetOperationLock(l *OperationLock) {
	s.opLock = l
}

func (s *SchedulerService) Start(interval time.Duration) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.refuseIfClosed("Start") {
		return
	}
	s.startLocked(interval)
	s.noteTransition(true)
}

// refuseIfClosed reports whether Close has latched the scheduler shut, logging
// the refused call. The caller holds lifecycleMu.
func (s *SchedulerService) refuseIfClosed(op string) bool {
	if !s.closed {
		return false
	}
	s.logger.Warn("Scheduler is closed for shutdown; ignoring "+op, "op", op)
	return true
}

// noteTransition calls the test-only onTransition seam. The caller holds
// lifecycleMu.
func (s *SchedulerService) noteTransition(running bool) {
	if s.onTransition != nil {
		s.onTransition(running)
	}
}

// startLocked arms the scan ticker and the apply loop. The caller holds
// lifecycleMu.
func (s *SchedulerService) startLocked(interval time.Duration) {
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
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.stopLocked()
	s.noteTransition(false)
}

// Close stops the scheduler for good: main.go calls it at shutdown instead of
// Stop, and every later Start or Restart is refused. Without the latch, a
// settings save still being served after shutdown began would Restart the
// scheduler, re-arming auto-apply in the window before process exit
// (agent-os-z91e.5).
func (s *SchedulerService) Close() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.closed = true
	s.stopLocked()
	s.noteTransition(false)
}

// stopLocked halts the ticker and the apply loop and waits (bounded) for
// in-flight scans. The caller holds lifecycleMu.
func (s *SchedulerService) stopLocked() {
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

// Restart stops and re-starts the scheduler as one transition under
// lifecycleMu, so a concurrent Stop or Restart cannot interleave between the
// halves and leave the runtime disagreeing with the last call made.
func (s *SchedulerService) Restart(interval time.Duration) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.refuseIfClosed("Restart") {
		return
	}
	s.stopLocked()
	s.startLocked(interval)
	s.noteTransition(true)
}

func (s *SchedulerService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticker != nil
}

// Settings keys that tell the operator why auto-update applied nothing, the
// apply-side counterparts of update_scan_last_error (agent-os-ehie). They are
// separate keys because they clear at different times: the arm error when the
// apply loop next arms cleanly (Start or a settings save), the pass error when
// the next auto-update pass gets past its reads. One shared key would let a
// clean re-arm wipe a pass failure that no pass has yet disproved.
const (
	applyArmErrorKey  = "update_apply_arm_error"
	applyLastErrorKey = "update_apply_last_error"
)

// recordApplyError writes (or, with "", clears) one of the keys above. Best
// effort: a pass that already failed must not fail again over its own report,
// and when this write fails the database is probably what broke, so the log is
// the only place left to say so.
func (s *SchedulerService) recordApplyError(key, msg string) {
	if err := s.db.SetSetting(key, msg); err != nil {
		s.logger.Error("Failed to record auto-update apply status", "key", key, "error", err)
	}
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
			// agent-os-zt0h: the Updates tab reads this cache, not the scan.
			StackLookupFailed:  r.StackLookupFailed,
			ComposeWorkingDir:  r.ComposeWorkingDir,
			ComposeConfigFiles: r.ComposeConfigFiles,
			LocalDigest:        r.LocalDigest,
			RemoteDigest:       r.RemoteDigest,
			ScannedAt:          now,
		})
	}

	if err := s.db.SetCachedUpdates(cachedUpdates); err != nil {
		s.logger.Error("Failed to cache updates", "error", err)
	}

	// One write: the scan time and the cleared error describe the same scan,
	// so a failed clear must not leave a fresh time next to a stale error
	// (agent-os-qz9b).
	if err := s.db.SetSettings([]database.SettingValue{
		{Key: "update_scan_last_run", Value: now},
		{Key: "update_scan_last_error", Value: ""},
	}); err != nil {
		s.logger.Error("Failed to record scan result", "error", err)
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
	// clock, so the scan tick stops here and the apply loop does the rest.
	// Immediate mode — the seeded default, an absent key, or a stored schedule
	// this code cannot parse — applies exactly as before.
	cfg := s.loadApplySchedule()
	if cfg.unreadable {
		// A settings READ fault, not a value the scheduler disagrees with. It is
		// checked BEFORE .scheduled rather than after, because the zero value of
		// applySchedule has scheduled=false: reading this state as "not
		// scheduled" is precisely how the fault used to fall through to
		// RunAutoUpdates and apply on the strength of a failed query
		// (agent-os-rltu). loadApplySchedule has already logged the cause and
		// the consequence at ERROR, so this branch stays quiet rather than
		// emitting a second line for one event. The operator still needs to
		// see it without the log, though (agent-os-ehie).
		s.recordApplyError(applyLastErrorKey,
			"the update_apply_* settings could not be read, so this scan tick applied no updates")
		return
	}
	if cfg.scheduled {
		s.logger.Info("Scheduled apply mode: updates cached, not applied on this scan tick",
			"updates_found", len(updates))
		return
	}

	s.RunAutoUpdates(ctx, updates)
}

// loadApplySchedule resolves the update_apply_* settings into an applySchedule.
//
// It distinguishes THREE outcomes, and the line between them is "could the
// setting be read at all", not "did it give me a schedule":
//
//   - scheduled — the operator configured a window and it parsed.
//   - immediate — a legitimately known "no window": an absent key (the
//     pre-migration-14 state, where immediate is exactly what migration 14
//     seeds) or a stored value this code cannot parse. A corrupt value is one
//     the operator themselves set and can see in the settings UI, so acting on
//     the documented default is honest.
//   - unreadable — a genuine database fault. NOTHING is applied on this pass.
//
// The third case used to resolve to immediate, on the reasoning (Requirement C
// of the original task brief) that falling back to a dead schedule would stop
// updates landing with nobody told. That reasoning covered the wrong risk: it
// traded a loud stop for a silent override of an explicit maintenance window,
// so an operator who asked for 03:00 got production containers restarted mid-
// afternoon because a query failed. agent-os-r1kc settled the identical shape
// for retention the other way — refuse rather than act at a default when the
// setting cannot be read — and this now matches it (agent-os-rltu). The stop is
// not silent: every unreadable branch below logs at ERROR naming both the cause
// and the consequence.
func (s *SchedulerService) loadApplySchedule() applySchedule {
	immediate := applySchedule{}
	unreadable := applySchedule{unreadable: true}

	mode, err := s.db.GetSetting("update_apply_mode")
	if err != nil {
		// A missing key is the pre-migration-14 state, and immediate is exactly
		// what migration 14 seeds, so that case is not worth shouting about AND
		// is not a fault: it stays on the immediate path deliberately. Only a
		// genuine read fault refuses.
		if errors.Is(err, errdefs.ErrNotFound) {
			return immediate
		}
		s.logger.Error("Failed to read update_apply_mode; no update will be applied on this pass, "+
			"because applying now could override a maintenance window this fault is hiding",
			"error", err)
		return unreadable
	}
	if mode != "scheduled" {
		return immediate
	}

	hhmm, timeErr := s.db.GetSetting("update_apply_time")
	days, daysErr := s.db.GetSetting("update_apply_days")
	if timeErr != nil || daysErr != nil {
		s.logger.Error("Apply mode is scheduled but its schedule could not be read; no update will be applied "+
			"until it can be read, rather than applying now outside the operator's maintenance window",
			"time_error", timeErr, "days_error", daysErr)
		return unreadable
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
// means nothing is scheduled: immediate mode, an unreadable settings store
// (agent-os-rltu — in which case nothing is applied on the scan tick either),
// or no running scheduler.
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
	// Ordered before the !scheduled test for the same reason runCycle checks it
	// first: unreadable also carries scheduled=false, so reporting it as
	// "immediate (applying on the scan tick)" would tell the operator the exact
	// opposite of what the scheduler is now doing (agent-os-rltu).
	if cfg.unreadable {
		s.logger.Error("Auto-update apply mode is unknown: the update_apply_* settings could not be read, " +
			"so nothing will be applied on the scan tick and no apply is scheduled")
		s.recordApplyError(applyArmErrorKey,
			"the update_apply_* settings could not be read, so no update apply is scheduled")
		return
	}
	// Every branch below is a known state, so it clears an arm error left by an
	// earlier arming. The loop does not re-read its settings until the next
	// Start or settings save, so an arm error stays true until one of those.
	if !cfg.scheduled {
		s.logger.Info("Auto-update apply mode: immediate (applying on the scan tick)")
		s.recordApplyError(applyArmErrorKey, "")
		return
	}
	if !armed {
		s.logger.Error("Auto-update apply mode is scheduled but no next run could be computed; no update will be applied",
			"apply_time", cfg.schedule.FormatTime(), "apply_days", cfg.schedule.FormatDays())
		s.recordApplyError(applyArmErrorKey,
			"apply mode is scheduled but no next run could be computed, so no update apply is scheduled")
		return
	}
	s.recordApplyError(applyArmErrorKey, "")
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

	// The same bound as RunAutoUpdates' own, started here so it also covers
	// pruneVanishedTargets. RunAutoUpdates derives its deadline from this one,
	// so the pass as a whole still ends applyTimeout after this line.
	applyCtx, cancel := withCommandDeadline(ctx, s.applyTimeout)
	defer cancel()

	updates, err := s.db.GetCachedUpdates()
	if err != nil {
		s.logger.Error("Scheduled auto-update apply: failed to read cached updates", "error", err)
		s.recordApplyError(applyLastErrorKey, "the scheduled apply could not read the cached updates: "+err.Error())
		return true
	}

	s.logger.Info("Scheduled auto-update apply starting", "cached_updates", len(updates))
	live, unresolved := s.pruneVanishedTargets(applyCtx, updates)
	s.runAutoUpdates(applyCtx, live, unresolved)
	return true
}

// unresolvedUpdate is a cached update pruneVanishedTargets left out of a pass
// because its container could not be inspected, with the inspect error.
type unresolvedUpdate struct {
	update models.CachedUpdate
	err    error
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
// Those are returned as unresolved, so the pass can give each one with a
// policy a 'skipped' history row (agent-os-z91e.47); policies are not read
// here, so this function cannot tell which of them would have run.
func (s *SchedulerService) pruneVanishedTargets(ctx context.Context, updates []models.CachedUpdate) ([]models.CachedUpdate, []unresolvedUpdate) {
	inspector, ok := s.docker.(containerInspector)
	if !ok {
		// Cannot happen in production: the compile-time assertion above pins
		// *DockerService as a containerInspector, and it is the only checker
		// main.go builds a scheduler with. Reachable only from a test double.
		s.logger.Warn("Update checker cannot inspect containers; applying cached updates without a freshness check")
		return updates, nil
	}

	live := make([]models.CachedUpdate, 0, len(updates))
	var unresolved []unresolvedUpdate
	evicted := 0

	for _, update := range updates {
		_, err := inspector.InspectContainer(ctx, update.ContainerID)
		switch {
		case err == nil:
			live = append(live, update)

		case cerrdefs.IsNotFound(err):
			evicted++
			s.logger.Info("Evicting cached update: container no longer exists",
				"container", update.ContainerName, "containerID", update.ContainerID)
			if delErr := s.db.DeleteCachedUpdate(update.ContainerID); delErr != nil {
				s.logger.Warn("Failed to evict cached update for a vanished container",
					"containerID", update.ContainerID, "error", delErr)
			}

		default:
			unresolved = append(unresolved, unresolvedUpdate{update: update, err: err})
			s.logger.Warn("Skipping cached update: container could not be inspected",
				"container", update.ContainerName, "containerID", update.ContainerID, "error", err)
		}
	}

	if evicted > 0 || len(unresolved) > 0 {
		s.logger.Info("Scheduled auto-update apply: cache re-resolved",
			"live", len(live), "evicted", evicted, "unresolved", len(unresolved))
	}

	return live, unresolved
}

// notStartedCause names why a pass's ended ctx left items unstarted: its own
// deadline, or Stop() cancelling the parent.
func notStartedCause(ctx context.Context) string {
	if commandTimedOut(ctx) {
		return "pass deadline reached"
	}
	return "shutdown"
}

// NewSkippedUpdateEntry builds a 'skipped' update_history row for an update
// the app decided not to run, with reason as its error_message; the caller
// fills in which container, image and stack it is. completed_at is set, equal
// to started_at, because retention and the manual clear both delete by
// completed_at: a row without one would never age out. Shared by the
// auto-update pass and the manual stack update (agent-os-z91e.47).
func NewSkippedUpdateEntry(trigger, reason string) *models.UpdateHistoryEntry {
	now := time.Now().Format(time.RFC3339)
	return &models.UpdateHistoryEntry{
		ID:           uuid.New().String(),
		Status:       "skipped",
		Trigger:      trigger,
		StartedAt:    now,
		CompletedAt:  &now,
		ErrorMessage: &reason,
	}
}

// recordSkippedUpdate writes the 'skipped' update_history row for an item
// RunAutoUpdates did not run (agent-os-z91e.32). A failed insert is only
// logged; the skip itself has already been decided and counted.
func (s *SchedulerService) recordSkippedUpdate(update models.CachedUpdate, reason string) {
	entry := NewSkippedUpdateEntry("auto", reason)
	entry.ContainerID = update.ContainerID
	entry.ContainerName = update.ContainerName
	entry.Image = update.ImageRef
	if update.StackID != "" {
		entry.StackID = &update.StackID
	}
	if err := s.db.InsertUpdateHistory(entry); err != nil {
		s.logger.Error("Failed to insert skipped update history",
			"container", update.ContainerName, "error", err)
	}
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
	s.runAutoUpdates(ctx, updates, nil)
}

// runAutoUpdates is RunAutoUpdates plus the items the scheduled path's
// freshness check could not inspect. Those are not applied; each one that has
// a policy, so would otherwise have run, gets a 'skipped' row once the
// policies are read (agent-os-z91e.47).
func (s *SchedulerService) runAutoUpdates(ctx context.Context, updates []models.CachedUpdate, unresolved []unresolvedUpdate) {
	// Bounded here rather than by each caller, so no path can apply without a
	// deadline: the immediate path used to pass the scheduler's cancel-only
	// context, and a hung pull then held the stack's lock until restart
	// (agent-os-o1jg). withCommandDeadline's cause lets timeoutError tell this
	// deadline from Stop() cancelling the parent.
	ctx, cancel := withCommandDeadline(ctx, s.applyTimeout)
	defer cancel()

	autoEnabledStr, err := s.db.GetSetting("auto_update_enabled")
	if err != nil {
		// agent-os-koy9. The read error used to be merged into the value test
		// with a single `||`, so a settings read that FAILED and an operator
		// who turned auto-updates OFF took the same bare return, with no log
		// line at any level. A database fault therefore disabled automatic updates
		// silently, and the operator found out when a container they believed
		// was being patched had not been patched for weeks.
		//
		// Migration 3 seeds the key (migrations.go:248, 'false'), so after
		// migrations an absent row cannot happen; it only means a
		// pre-migration-3 database, where 'off' is what the seed would have
		// said anyway. That case stays quiet, exactly as loadApplySchedule
		// treats a missing update_apply_mode.
		if !errors.Is(err, errdefs.ErrNotFound) {
			s.logger.Error("Failed to read auto_update_enabled; skipping this auto-update run, so no container will be patched until this read succeeds",
				"error", err)
			s.recordApplyError(applyLastErrorKey, "could not read auto_update_enabled, so this auto-update run was skipped: "+err.Error())
			return
		}
		s.recordApplyError(applyLastErrorKey, "")
		return
	}
	if autoEnabledStr != "true" {
		// Switched off, and read cleanly: nothing is expected to apply, so an
		// error from an earlier pass no longer describes the system.
		s.recordApplyError(applyLastErrorKey, "")
		return
	}

	policies, err := s.db.GetEnabledAutoUpdatePolicies()
	if err != nil {
		s.logger.Error("Failed to get auto-update policies", "error", err)
		s.recordApplyError(applyLastErrorKey, "could not read the auto-update policies, so this auto-update run was skipped: "+err.Error())
		return
	}
	// Past every read that can stop the whole pass. Per-container failures
	// below have their own update_history rows, so they do not touch this key.
	s.recordApplyError(applyLastErrorKey, "")

	containerPolicies := make(map[string]*models.AutoUpdatePolicy)
	stackPolicies := make(map[string]*models.AutoUpdatePolicy)
	for i := range policies {
		p := &policies[i]
		switch p.TargetType {
		case string(JobTargetTypeContainer):
			containerPolicies[p.TargetID] = p
		case string(JobTargetTypeStack):
			stackPolicies[p.TargetID] = p
		}
	}

	policyFor := func(update models.CachedUpdate) (*models.AutoUpdatePolicy, bool) {
		if p, ok := containerPolicies[update.ContainerID]; ok {
			return p, true
		}
		if update.StackID != "" {
			p, ok := stackPolicies[update.StackID]
			return p, ok
		}
		return nil, false
	}

	for _, u := range unresolved {
		if _, ok := policyFor(u.update); ok {
			s.recordSkippedUpdate(u.update, "skipped: the container could not be inspected ("+u.err.Error()+"); retried next pass")
		}
	}

	succeeded := 0
	failed := 0
	skipped := 0
	// Stacks whose lock was held when their update came up. Their cached
	// update rows are left in place, so the next pass tries them again.
	var busyStacks []string // "stack <id>" or "container <name>", the turn that was held
	busySkipped := 0
	// Items never started because ctx had already ended; see the loop.
	notStarted := 0
	// Items whose stack the scan could not look up; see the loop.
	lookupSkipped := 0

	for _, update := range updates {
		policy, hasPolicy := policyFor(update)
		if !hasPolicy {
			skipped++
			continue
		}

		// The pass's context ended before this item began (the pass deadline,
		// or Stop() cancelling the parent). Running it would fail at once on
		// the ended context, record a failed run and add to its policy's
		// ConsecutiveFailures, so one hung container paused unrelated policies
		// after three passes (agent-os-z91e.21). Only the item that was in
		// flight when the context ended is that failure. This one leaves its
		// policy alone, keeps its cached row so the next pass retries it, and
		// is reported twice: a 'skipped' history row naming why
		// (agent-os-z91e.32), and update_apply_last_error after the loop.
		if ctx.Err() != nil {
			skipped++
			notStarted++
			s.recordSkippedUpdate(update, "not started: "+notStartedCause(ctx)+"; retried next pass")
			continue
		}

		// The scan could not look this container's stack up, so StackID is
		// empty and the lock below would be skipped. UpdateContainer looks the
		// stack up again, and a fault that cleared in between would run
		// compose for that stack without its lock (agent-os-z91e.40). Like a
		// busy stack: not a failure, policy untouched, cached row kept so the
		// next pass retries it with a fresh scan.
		if update.StackLookupFailed {
			skipped++
			lookupSkipped++
			s.recordSkippedUpdate(update, "skipped: the stack for this container could not be looked up during the scan; retried next pass")
			continue
		}

		// Taken before the history insert, so a skipped update leaves a
		// 'skipped' row rather than a pending one, and held across
		// UpdateContainer and its verification. The skip is also reported
		// through update_apply_last_error after the loop.
		//
		// A container no managed stack owns has no stack to lock, so it takes its
		// own turn keyed on the container ID: its update removes the old container
		// and then creates and starts the new one, and a container prune between
		// the create and the start leaves no container (agent-os-qags.30).
		releaseLock := func() {}
		if s.opLock != nil {
			lockKey, subject := update.StackID, "stack "+update.StackID
			if lockKey == "" {
				lockKey, subject = update.ContainerID, "container "+update.ContainerName
			}
			token, lockErr := s.opLock.Acquire(lockKey, OpKindUpdate)
			if lockErr != nil {
				s.logger.Warn("Auto-update skipped: another operation holds its turn; retried next pass",
					"container", update.ContainerName, "lock_key", lockKey, "holder", lockErr.Error())
				skipped++
				busySkipped++
				s.recordSkippedUpdate(update, fmt.Sprintf("skipped: %s is busy (%s); retried next pass",
					subject, lockErr.Error()))
				if !slices.Contains(busyStacks, subject) {
					busyStacks = append(busyStacks, subject)
				}
				continue
			}
			releaseLock = func() { s.opLock.Release(lockKey, token) }
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
			releaseLock()
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
			failErr := ar.Err
			if failErr == nil {
				failErr = errors.New(ar.Reason)
			}
			errMsg := timeoutError(ctx, failErr, "auto-update").Error()
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

				// completed_at = started_at, as on a skipped row: retention
				// and the manual clear both delete by completed_at, so a row
				// without one would never go (agent-os-z91e.46).
				pausedAt := time.Now().Format(time.RFC3339)
				pausedHistory := &models.UpdateHistoryEntry{
					ID:            uuid.New().String(),
					ContainerID:   update.ContainerID,
					ContainerName: update.ContainerName,
					Image:         update.ImageRef,
					Status:        "paused",
					Trigger:       "auto",
					StartedAt:     pausedAt,
					CompletedAt:   &pausedAt,
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
		releaseLock()
	}

	var applyNotes []string
	if len(busyStacks) > 0 {
		applyNotes = append(applyNotes, fmt.Sprintf(
			"%d auto-update(s) skipped: another operation in progress on %s; retried next pass",
			busySkipped, strings.Join(busyStacks, ", ")))
	}
	if lookupSkipped > 0 {
		applyNotes = append(applyNotes, fmt.Sprintf(
			"%d auto-update(s) skipped: their stack could not be looked up during the scan; retried next pass", lookupSkipped))
	}
	if notStarted > 0 {
		applyNotes = append(applyNotes, fmt.Sprintf(
			"%d auto-update(s) not started: %s; retried next pass", notStarted, notStartedCause(ctx)))
	}
	if len(applyNotes) > 0 {
		s.recordApplyError(applyLastErrorKey, strings.Join(applyNotes, "; "))
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
