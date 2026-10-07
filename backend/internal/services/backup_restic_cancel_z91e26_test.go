package services

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runGoroutines counts goroutines started from inside execRunner.Run.
func runGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "(*execRunner).Run.func")
}

// Run used to start a goroutine that waited on ctx and then killed the
// child's process group. When Run returned first, that goroutine stayed parked
// until ctx ended, which for a long-lived ctx is never, and when ctx did end
// it signalled a pgid whose group had already been reaped and could have been
// reused. The group kill is cmd.Cancel now, which exec stops watching once
// Wait returns (agent-os-z91e.26).
func TestExecRunner_Run_LeavesNoKillerBehind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan StreamLine, 8)

	require.NoError(t, (&execRunner{}).Run(ctx, "sh", []string{"-c", "exit 0"}, nil, out))

	// Run's scanners have exited by the time it returns; give the scheduler
	// a moment so a goroutine on its way out is not counted.
	deadline := time.Now().Add(time.Second) // wall-clock ok: bounds a wait for a goroutine a correct Run never leaves
	for time.Now().Before(deadline) && runGoroutines() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	require.Zero(t, runGoroutines(), "a goroutine started by Run outlived it while ctx is still live")
}

// The kill moved, it must not have gone: a ctx that ends mid-run still kills
// the whole process group, including a grandchild that stayed in it.
func TestExecRunner_Run_CancelKillsTheProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan StreamLine, 8)

	done := make(chan error, 1)
	go func() {
		done <- (&execRunner{}).Run(ctx, "sh", []string{"-c", "sleep 30 & echo $!; exec sleep 30"}, nil, out)
	}()

	var pid int
	select {
	case line := <-out:
		var err error
		pid, err = strconv.Atoi(line.Line)
		require.NoError(t, err)
	case <-time.After(5 * time.Second): // wall-clock ok: the shell prints the pid at once
		t.Fatal("the child never printed its grandchild's pid")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(commandWaitDelay + 3*time.Second): // wall-clock ok: bounds a call that ends at once
		t.Fatal("Run did not return after its context ended")
	}

	deadline := time.Now().Add(3 * time.Second) // wall-clock ok: a killed process goes away in milliseconds
	for time.Now().Before(deadline) && processAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck // Cleanup of the leaked grandchild before failing.
		t.Fatalf("grandchild %d in the child's process group survived the cancel: the group kill did not run", pid)
	}
}

// processAlive reports whether pid exists and is not a zombie awaiting its reaper.
func processAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the parenthesised command name.
	s := string(stat)
	if i := strings.LastIndex(s, ") "); i >= 0 && i+2 < len(s) {
		return s[i+2] != 'Z'
	}
	return true
}
