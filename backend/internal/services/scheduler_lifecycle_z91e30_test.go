package services

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// agent-os-z91e.30: the backup and Docker-cleanup schedulers get the shape
// agent-os-z91e.5 gave SchedulerService: a lifecycle lock that makes
// stop-then-start one transition, and a terminal Close latch.

func z91e30DailySchedule(t *testing.T) DailySchedule {
	t.Helper()
	sched, err := ParseDailySchedule("02:00", "0,1,2,3,4,5,6")
	require.NoError(t, err)
	return sched
}

// TestBackupScheduler_AfterCloseStaysStopped_z91e30: main.go closes the
// scheduler before srv.Shutdown, so a settings save still being served can call
// Start, StartScheduled or Restart afterwards. Each must leave it stopped. The
// "armed before Close" and "Stop does not latch" halves are the controls: they
// show the same calls DO arm the scheduler when it is not closed.
func TestBackupScheduler_AfterCloseStaysStopped_z91e30(t *testing.T) {
	svc := newTestBackupScheduler(t, &fakeBackupRunner{})
	t.Cleanup(svc.Close)

	svc.Start(time.Minute)
	require.True(t, svc.IsRunning(), "control: Start must arm an open scheduler")
	svc.Stop()
	require.False(t, svc.IsRunning())
	svc.Restart(time.Minute)
	require.True(t, svc.IsRunning(), "control: Stop must not latch, Restart re-arms after it")

	svc.Close()
	require.False(t, svc.IsRunning(), "Close must stop the scheduler")

	svc.Start(time.Minute)
	require.False(t, svc.IsRunning(), "Start after Close re-armed the interval ticker (agent-os-z91e.30)")
	svc.StartScheduled(z91e30DailySchedule(t))
	require.False(t, svc.IsRunning(), "StartScheduled after Close re-armed the timer (agent-os-z91e.30)")
	svc.Restart(time.Minute)
	require.False(t, svc.IsRunning(), "Restart after Close re-armed the scheduler (agent-os-z91e.30)")

	svc.Close() // idempotent
}

// TestBackupScheduler_LifecycleCallsTakeTheLifecycleLock_z91e30 holds the
// lifecycle lock and shows each entry point blocks on it, then completes once it
// is released. That is what makes Restart's stop and start one transition: a
// call that skipped the lock could land between its halves.
func TestBackupScheduler_LifecycleCallsTakeTheLifecycleLock_z91e30(t *testing.T) {
	ops := map[string]func(*BackupSchedulerService, DailySchedule){
		"Start":          func(s *BackupSchedulerService, _ DailySchedule) { s.Start(time.Minute) },
		"StartScheduled": func(s *BackupSchedulerService, d DailySchedule) { s.StartScheduled(d) },
		"Restart":        func(s *BackupSchedulerService, _ DailySchedule) { s.Restart(time.Minute) },
		"Stop":           func(s *BackupSchedulerService, _ DailySchedule) { s.Stop() },
		"Close":          func(s *BackupSchedulerService, _ DailySchedule) { s.Close() },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			svc := newTestBackupScheduler(t, &fakeBackupRunner{})
			t.Cleanup(svc.Close)
			sched := z91e30DailySchedule(t)
			z91e30AssertBlocksOnLifecycleMu(t, &svc.lifecycleMu, func() { op(svc, sched) })
		})
	}
}

func TestBackupScheduler_ConcurrentRestartAndCloseEndsStopped_z91e30(t *testing.T) {
	for i := 0; i < 10; i++ {
		svc := newTestBackupScheduler(t, &fakeBackupRunner{})
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, op := range []func(){
			func() { svc.Restart(time.Minute) },
			func() { svc.Restart(2 * time.Minute) },
			svc.Close,
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				op()
			}()
		}
		close(start)
		wg.Wait()
		require.False(t, svc.IsRunning(), "iteration %d: Close is terminal, but a concurrent Restart left the scheduler running", i)
	}
}

func TestDockerCleanupScheduler_AfterCloseStaysStopped_z91e30(t *testing.T) {
	db := fn7x3MemoryDB(t)
	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))
	s, _ := fn7x3Scheduler(t, db, &fn7x3FakeCleanupRunner{})
	t.Cleanup(s.Close)

	s.StartFromPolicy()
	require.True(t, s.IsRunning(), "control: StartFromPolicy must arm an open scheduler with an enabled policy")
	s.Stop()
	require.False(t, s.IsRunning())
	s.Start(time.Hour)
	require.True(t, s.IsRunning(), "control: Stop must not latch, Start re-arms after it")

	s.Close()
	require.False(t, s.IsRunning(), "Close must stop the scheduler")

	s.StartFromPolicy() // a policy PUT served after shutdown began
	require.False(t, s.IsRunning(), "StartFromPolicy after Close re-armed the ticker (agent-os-z91e.30)")
	s.Start(time.Hour)
	require.False(t, s.IsRunning(), "Start after Close re-armed the ticker (agent-os-z91e.30)")

	s.Close() // idempotent
}

func TestDockerCleanupScheduler_LifecycleCallsTakeTheLifecycleLock_z91e30(t *testing.T) {
	ops := map[string]func(*DockerCleanupSchedulerService){
		"Start":           func(s *DockerCleanupSchedulerService) { s.Start(time.Hour) },
		"StartFromPolicy": func(s *DockerCleanupSchedulerService) { s.StartFromPolicy() },
		"Stop":            func(s *DockerCleanupSchedulerService) { s.Stop() },
		"Close":           func(s *DockerCleanupSchedulerService) { s.Close() },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			s, _ := fn7x3Scheduler(t, fn7x3MemoryDB(t), &fn7x3FakeCleanupRunner{})
			t.Cleanup(s.Close)
			z91e30AssertBlocksOnLifecycleMu(t, &s.lifecycleMu, func() { op(s) })
		})
	}
}

func TestDockerCleanupScheduler_ConcurrentPolicyStartAndCloseEndsStopped_z91e30(t *testing.T) {
	for i := 0; i < 10; i++ {
		db := fn7x3MemoryDB(t)
		require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))
		s, _ := fn7x3Scheduler(t, db, &fn7x3FakeCleanupRunner{})
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, op := range []func(){s.StartFromPolicy, s.StartFromPolicy, s.Close} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				op()
			}()
		}
		close(start)
		wg.Wait()
		require.False(t, s.IsRunning(), "iteration %d: Close is terminal, but a concurrent StartFromPolicy left the ticker running", i)
	}
}

// z91e30AssertBlocksOnLifecycleMu runs op while the test holds mu, requires it
// to still be blocked after a grace period, then releases mu and requires it to
// finish. A call that never takes the lock completes during the grace period.
func z91e30AssertBlocksOnLifecycleMu(t *testing.T, mu *sync.Mutex, op func()) {
	t.Helper()
	mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		op()
	}()
	select {
	case <-done:
		mu.Unlock()
		t.Fatal("call completed while the lifecycle lock was held, so it does not take it")
	case <-time.After(150 * time.Millisecond):
	}
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("call did not finish after the lifecycle lock was released")
	}
}
