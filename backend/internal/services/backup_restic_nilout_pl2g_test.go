package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// agent-os-pl2g: execRunner.Run's scanners sent every output line on the
// caller's out with a bare send. A nil out (Restore, ApplyRetention and the
// other methods pass the caller's out through) parked the scanner forever,
// and so did a non-nil out nobody reads once ctx had ended. Run then never
// returned: it waits for both scanners after cmd.Wait. These tests use the
// real execRunner, because the send that hung is in it; a fake runner would
// test its own send instead.

const bound_pl2g = 10 * time.Second

// stubRestic_pl2g puts a `restic` on PATH that prints one line and exits 0.
func stubRestic_pl2g(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'one line of restic output'\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "restic"), []byte(script), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// within_pl2g fails the test if call does not return within bound_pl2g.
func within_pl2g(t *testing.T, what string, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case <-done:
	case <-time.After(bound_pl2g):
		t.Fatalf("%s did not return within %s: a send on out parked the scanner", what, bound_pl2g)
	}
}

func TestResticNilOut_Restore_pl2g(t *testing.T) {
	stubRestic_pl2g(t)
	m := newResticManagerWithRunner(testBackupConfig(), &execRunner{}, nil)
	within_pl2g(t, "Restore with out=nil", func() error {
		return m.Restore(context.Background(), "abcd1234", "", t.TempDir(), nil, false, nil)
	})
}

func TestResticNilOut_ApplyRetention_pl2g(t *testing.T) {
	stubRestic_pl2g(t)
	cfg := testBackupConfig()
	require.Positive(t, cfg.KeepDaily, "a keep-* policy is needed, or forget is skipped and nothing runs")
	m := newResticManagerWithRunner(cfg, &execRunner{}, nil)
	within_pl2g(t, "ApplyRetention with out=nil", func() error {
		return m.ApplyRetention(context.Background(), "app", nil)
	})
}

// Pin: Backup hands the runner its own channel and skips a nil out itself, so
// it returned before this fix too.
func TestResticNilOut_Backup_pl2g(t *testing.T) {
	stubRestic_pl2g(t)
	m := newResticManagerWithRunner(testBackupConfig(), &execRunner{}, nil)
	within_pl2g(t, "Backup with out=nil", func() error {
		_, err := m.Backup(context.Background(), t.TempDir(), []string{"app"}, nil)
		return err
	})
}

// A non-nil out that nobody reads: once ctx ends, Run returns.
func TestResticNilOut_UnreadOutCtxCancelled_pl2g(t *testing.T) {
	out := make(chan StreamLine) // unbuffered, never read
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	within_pl2g(t, "Run with an unread out after ctx ended", func() error {
		return (&execRunner{}).Run(ctx, "sh", []string{"-c", "echo first; echo second; sleep 30"}, nil, out)
	})
}

// The child exits 0 with lines still in the scanners' buffers (cmd.Wait
// returns once they have been read from the pipe, not sent). When ctx ends
// in that window the rest are not delivered, and Run must not report that as
// a complete run: callers count and parse these lines (Verify's entry count,
// Backup's summary).
func TestResticNilOut_CtxEndAfterCleanExitIsNotSuccess_pl2g(t *testing.T) {
	const total = 500
	out := make(chan StreamLine) // unbuffered: the reader paces the scanners
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan int, 1)
	go func() {
		n := 0
		for range out {
			n++
			if n == 1 {
				// Long enough for the child to exit and Wait to return.
				time.Sleep(300 * time.Millisecond)
				cancel()
			}
		}
		received <- n
	}()
	var err error
	within_pl2g(t, "Run with ctx ended after a clean exit", func() error {
		err = (&execRunner{}).Run(ctx, "sh", []string{"-c", "i=0; while [ $i -lt 500 ]; do echo line$i; i=$((i+1)); done"}, nil, out)
		return err
	})
	close(out)
	n := <-received
	if err == nil {
		require.Equal(t, total, n, "Run returned nil but delivered %d of %d lines", n, total)
	} else {
		require.ErrorIs(t, err, context.Canceled)
	}
}
