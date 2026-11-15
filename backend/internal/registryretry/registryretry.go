// Package registryretry is a test helper: it retries a call that reaches an
// image registry when the registry did not answer in time, throttled, or
// failed on its side, and returns at once on anything else (agent-os-2vce).
//
// It is shared by the integration tests in package integrationtest and
// package truth; truth cannot import integrationtest, which imports truth.
// No production code imports it.
package registryretry

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

// Attempts bounds Retry: at most 2+4+6+8 s of waiting, well past ECR
// Public's one-second window.
const Attempts = 5

// pause is Retry's pause unit: the nth retry waits 2n of them. A variable so
// this package's tests need not wait.
var pause = time.Second

var (
	// A registry that answered with a verdict about the image or the
	// credentials: asking again gets the same answer.
	permanent = regexp.MustCompile(`(?i)manifest unknown|unauthorized|denied|not found`)
	// A registry that did not answer in time, could not be found (DNS),
	// throttled, or failed on its side.
	// The status codes are whole words so a digest's hex cannot match.
	transientText = regexp.MustCompile(`(?i)deadline exceeded|timeout|toomanyrequests|connection reset|connection refused|no such host|\bEOF\b|\b(429|502|503|504)\b`)
)

// Transient reports whether err, with log beside it, is a registry failure
// worth retrying. log is what the call printed, "" when it has none. The error
// chain is matched first (a deadline or a net timeout in this process); a
// daemon's error and a child's stderr arrive as text, so the text of both is
// the fallback. A permanent verdict, in either, wins over everything.
func Transient(err error, log string) bool {
	if err == nil {
		return false
	}
	text := err.Error() + "\n" + log
	if permanent.MatchString(text) {
		return false
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return true
	}
	return transientText.MatchString(text)
}

// Retry runs op, a call that reaches the image registry, up to Attempts
// times with a growing pause, while its error is Transient, and returns the
// last error. Only the transport is retried: callers still assert on what op
// produced.
//
// An *exec.ExitError in the chain contributes its Stderr: a child run with
// Output() (docker buildx imagetools inspect, in truth.RemoteRegistryDigest)
// reports only "exit status 1", and why it failed, a throttle among others, is
// in the stderr Output kept. An error that names no transient cause is not
// retried: fail closed, the log line says why.
func Retry(t testing.TB, what string, op func() error) error {
	t.Helper()

	var err error
	for attempt := 1; attempt <= Attempts; attempt++ {
		if err = op(); err == nil {
			return nil
		}
		if !Transient(err, stderrOf(err)) {
			t.Logf("%s: attempt %d/%d: %v (not a transient registry error, not retried)", what, attempt, Attempts, err)
			return err
		}
		t.Logf("%s: attempt %d/%d: %v", what, attempt, Attempts, err)
		if attempt < Attempts {
			time.Sleep(time.Duration(2*attempt) * pause)
		}
	}
	return err
}

// stderrOf returns the stderr an *exec.ExitError in err's chain kept, "" when
// there is none.
func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(exitErr.Stderr)
	}
	return ""
}
