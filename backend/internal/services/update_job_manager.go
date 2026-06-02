package services

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status represents the lifecycle state of an update job.
type Status string

const (
	StatusQueued     Status = "queued"
	StatusPulling    Status = "pulling"
	StatusRecreating Status = "recreating"
	StatusSuccess    Status = "success"
	StatusError      Status = "error"
)

// LogLineStream is one of the three allowed stream values for a LogLine.
type LogLineStream string

const (
	StreamStdout LogLineStream = "stdout"
	StreamStderr LogLineStream = "stderr"
	StreamStatus LogLineStream = "status"
)

// LogLine is a single timestamped output line from an update job.
// JSON field names match the API contract (ts/text/stream).
type LogLine struct {
	Ts     time.Time     `json:"ts"`
	Text   string        `json:"text"`
	Stream LogLineStream `json:"stream"`
}

// JobSpec carries the identifying metadata for a new job.
type JobSpec struct {
	TargetType string
	TargetID   string
	Name       string
	StackID    string
}

// Job is the in-memory state of a single update job.
// JSON field names are camelCase to match the API contract.
type Job struct {
	ID         string    `json:"id"`
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetId"`
	Name       string    `json:"name"`
	StackID    string    `json:"stackId"`
	Status     Status    `json:"status"`
	Lines      []LogLine `json:"lines"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

// JobEventKind describes what changed in a JobEvent.
type JobEventKind string

const (
	EventKindLine   JobEventKind = "line"
	EventKindStatus JobEventKind = "status"
	EventKindDone   JobEventKind = "done"
)

// JobEvent is delivered to per-job subscribers after the replay snapshot.
type JobEvent struct {
	Kind   JobEventKind
	Line   *LogLine
	Status Status
	Error  string
}

// maxJobLines is the ring-buffer cap per job.
const maxJobLines = 5000

// subEntry is one active subscriber for a job.
type subEntry struct {
	ch chan JobEvent
}

// jobState is the full mutable state of a job stored in the registry.
type jobState struct {
	mu          sync.Mutex
	job         Job
	subscribers []*subEntry
	finishedAt  time.Time // zero until terminal
}

// deepCopyJob returns a value copy of j.job with a new Lines slice.
// Must be called with j.mu held.
func (j *jobState) deepCopyLocked() *Job {
	cp := j.job
	if len(j.job.Lines) > 0 {
		cp.Lines = make([]LogLine, len(j.job.Lines))
		copy(cp.Lines, j.job.Lines)
	} else {
		cp.Lines = []LogLine{}
	}
	return &cp
}

// fanOutLocked sends ev to all subscribers without blocking.
// Must be called with j.mu held.
func (j *jobState) fanOutLocked(ev JobEvent) {
	for _, s := range j.subscribers {
		select {
		case s.ch <- ev:
		default:
			slog.Debug("update job subscriber channel full, dropping event", "jobId", j.job.ID, "kind", ev.Kind)
		}
	}
}

// queuedItem is enqueued work for the sequential worker.
type queuedItem struct {
	id  string
	run func(ctx context.Context, emit func(LogLine), setStatus func(Status)) error
}

// UpdateJobManager manages in-memory update jobs with sequential execution.
type UpdateJobManager struct {
	mu       sync.Mutex
	jobs     map[string]*jobState
	queue    chan queuedItem
	ttl      time.Duration
	cancelFn context.CancelFunc
	wg       sync.WaitGroup
}

// NewUpdateJobManager creates and starts the manager.
// ttl controls how long finished jobs are retained; pass 15*time.Minute for production.
func NewUpdateJobManager(ttl time.Duration) *UpdateJobManager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &UpdateJobManager{
		jobs:     make(map[string]*jobState),
		queue:    make(chan queuedItem, 256),
		ttl:      ttl,
		cancelFn: cancel,
	}
	m.wg.Add(2)
	go m.worker(ctx)
	go m.janitor(ctx)
	return m
}

// Stop shuts down the background goroutines gracefully.
func (m *UpdateJobManager) Stop() {
	m.cancelFn()
	m.wg.Wait()
}

// Enqueue creates a new job in queued state, schedules it for sequential execution,
// and returns an immediate snapshot of the job (non-blocking).
func (m *UpdateJobManager) Enqueue(spec JobSpec, run func(ctx context.Context, emit func(LogLine), setStatus func(Status)) error) *Job {
	id := uuid.New().String()
	now := time.Now().UTC()
	js := &jobState{
		job: Job{
			ID:         id,
			TargetType: spec.TargetType,
			TargetID:   spec.TargetID,
			Name:       spec.Name,
			StackID:    spec.StackID,
			Status:     StatusQueued,
			Lines:      []LogLine{},
			CreatedAt:  now,
		},
	}

	m.mu.Lock()
	m.jobs[id] = js
	m.mu.Unlock()

	m.queue <- queuedItem{id: id, run: run}

	js.mu.Lock()
	cp := js.deepCopyLocked()
	js.mu.Unlock()
	return cp
}

// Get returns a deep copy of the job with the given id, or nil if not found.
func (m *UpdateJobManager) Get(jobID string) *Job {
	m.mu.Lock()
	js, ok := m.jobs[jobID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	js.mu.Lock()
	defer js.mu.Unlock()
	return js.deepCopyLocked()
}

// List returns deep copies of all known jobs, newest first by createdAt.
func (m *UpdateJobManager) List() []*Job {
	m.mu.Lock()
	states := make([]*jobState, 0, len(m.jobs))
	for _, js := range m.jobs {
		states = append(states, js)
	}
	m.mu.Unlock()

	jobs := make([]*Job, 0, len(states))
	for _, js := range states {
		js.mu.Lock()
		jobs = append(jobs, js.deepCopyLocked())
		js.mu.Unlock()
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs
}

// Subscribe returns a snapshot of the job at subscribe time (for replay) plus a
// channel that delivers subsequent events. The snapshot and subscriber registration
// are performed atomically under the job lock so no events are missed.
// Returns nil, nil, nil if the job is not found.
func (m *UpdateJobManager) Subscribe(jobID string) (*Job, <-chan JobEvent, func()) {
	m.mu.Lock()
	js, ok := m.jobs[jobID]
	m.mu.Unlock()
	if !ok {
		return nil, nil, nil
	}

	ch := make(chan JobEvent, 64)
	sub := &subEntry{ch: ch}

	js.mu.Lock()
	snapshot := js.deepCopyLocked()
	// Only register for live events if not already terminal.
	if js.job.Status != StatusSuccess && js.job.Status != StatusError {
		js.subscribers = append(js.subscribers, sub)
	}
	js.mu.Unlock()

	unsubscribe := func() {
		js.mu.Lock()
		for i, s := range js.subscribers {
			if s == sub {
				js.subscribers = append(js.subscribers[:i], js.subscribers[i+1:]...)
				break
			}
		}
		js.mu.Unlock()
	}

	return snapshot, ch, unsubscribe
}

// worker consumes the queue and executes jobs one at a time.
func (m *UpdateJobManager) worker(ctx context.Context) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-m.queue:
			if !ok {
				return
			}
			m.runJob(ctx, item)
		}
	}
}

func (m *UpdateJobManager) runJob(ctx context.Context, item queuedItem) {
	m.mu.Lock()
	js, ok := m.jobs[item.id]
	m.mu.Unlock()
	if !ok {
		return
	}

	// Mark started.
	startedAt := time.Now().UTC()
	js.mu.Lock()
	js.job.StartedAt = startedAt
	// Status is still queued until the run func sets it via setStatus.
	js.mu.Unlock()

	emit := func(line LogLine) {
		js.mu.Lock()
		// Ring buffer: drop oldest when at capacity.
		if len(js.job.Lines) >= maxJobLines {
			js.job.Lines = js.job.Lines[1:]
		}
		js.job.Lines = append(js.job.Lines, line)
		ev := JobEvent{Kind: EventKindLine, Line: &line}
		js.fanOutLocked(ev)
		js.mu.Unlock()
	}

	setStatus := func(s Status) {
		js.mu.Lock()
		js.job.Status = s
		ev := JobEvent{Kind: EventKindStatus, Status: s}
		js.fanOutLocked(ev)
		js.mu.Unlock()
	}

	runErr := item.run(ctx, emit, setStatus)

	finishedAt := time.Now().UTC()
	js.mu.Lock()
	js.job.FinishedAt = finishedAt
	js.finishedAt = finishedAt
	if runErr != nil {
		js.job.Status = StatusError
		js.job.Error = runErr.Error()
	} else {
		js.job.Status = StatusSuccess
	}
	doneEv := JobEvent{
		Kind:   EventKindDone,
		Status: js.job.Status,
		Error:  js.job.Error,
	}
	js.fanOutLocked(doneEv)
	// Close and drain subscriber list — they get the done event and should stop reading.
	subs := js.subscribers
	js.subscribers = nil
	js.mu.Unlock()

	// Close all subscriber channels after releasing the lock.
	for _, s := range subs {
		close(s.ch)
	}
}

// janitor runs a periodic ticker to evict expired finished jobs.
func (m *UpdateJobManager) janitor(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.ttl / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			m.evictExpired(t)
		}
	}
}

// evictExpired removes finished jobs whose finishedAt is before now-ttl.
// It is exported for testing with a controlled clock.
func (m *UpdateJobManager) evictExpired(now time.Time) {
	cutoff := now.Add(-m.ttl)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, js := range m.jobs {
		js.mu.Lock()
		finished := js.finishedAt
		js.mu.Unlock()
		if !finished.IsZero() && finished.Before(cutoff) {
			delete(m.jobs, id)
		}
	}
}
