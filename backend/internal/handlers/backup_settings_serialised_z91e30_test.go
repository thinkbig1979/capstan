package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// z91e30RecordingScheduler records the order of Stop and Start calls. Stop
// sleeps so that two unserialised saves overlap inside it.
type z91e30RecordingScheduler struct {
	mu     sync.Mutex
	events []string
}

func (s *z91e30RecordingScheduler) record(e string) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *z91e30RecordingScheduler) Start(_ time.Duration)                   { s.record("start") }
func (s *z91e30RecordingScheduler) StartScheduled(_ services.DailySchedule) { s.record("start") }
func (s *z91e30RecordingScheduler) Stop() {
	s.record("stop")
	time.Sleep(30 * time.Millisecond)
}

// TestUpdateSettings_ConcurrentSchedulerSavesAreOneTransitionEach_z91e30: two
// saves that both change the schedule must each stop and then start, as a pair.
// Unserialised they interleave as stop, stop, start, start, and the second
// start can resolve the schedule before the other save wrote it, leaving the
// running scheduler disagreeing with the stored setting. The sequential
// control proves the recorder sees a clean stop,start for one save.
func TestUpdateSettings_ConcurrentSchedulerSavesAreOneTransitionEach_z91e30(t *testing.T) {
	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	sched := &z91e30RecordingScheduler{}
	svc.SetScheduler(sched)

	save := func(minutes int) {
		req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
			"scheduleIntervalMinutes": minutes,
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	save(30)
	require.Equal(t, []string{"stop", "start"}, sched.events, "control: one save is stop then start")

	sched.events = nil
	var wg sync.WaitGroup
	for _, m := range []int{45, 60} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			save(m)
		}()
	}
	wg.Wait()

	assert.Equal(t, []string{"stop", "start", "stop", "start"}, sched.events,
		"two concurrent saves interleaved their stop and start (agent-os-z91e.30)")
}
