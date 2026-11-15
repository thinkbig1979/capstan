package registryretry

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// exitWithStderr is the error a child run with Output() returns: the text is
// only the exit status, the stderr sits in the ExitError (ProcessState nil
// prints "<nil>").
func exitWithStderr(stderr string) error {
	return &exec.ExitError{Stderr: []byte(stderr)}
}

func TestRetry_2vce(t *testing.T) {
	defer func(p time.Duration) { pause = p }(pause)
	pause = time.Millisecond

	run := func(errs ...error) (int, error) {
		calls := 0
		err := Retry(t, "probe", func() error {
			e := errs[min(calls, len(errs)-1)]
			calls++
			return e
		})
		return calls, err
	}

	t.Run("a permanent error is returned at once", func(t *testing.T) {
		calls, err := run(errors.New("buildx imagetools inspect: manifest unknown: manifest unknown"))
		assert.Equal(t, 1, calls)
		assert.ErrorContains(t, err, "manifest unknown")
	})
	t.Run("an error naming no transient cause is returned at once", func(t *testing.T) {
		calls, err := run(errors.New("no Digest line found in buildx imagetools output"))
		assert.Equal(t, 1, calls)
		assert.Error(t, err)
	})
	t.Run("a transient error is retried until it clears", func(t *testing.T) {
		calls, err := run(errors.New("docker pull x: toomanyrequests: Rate exceeded"), nil)
		assert.Equal(t, 2, calls)
		assert.NoError(t, err)
	})
	t.Run("attempts are bounded and the last error returned", func(t *testing.T) {
		calls, err := run(errors.New("dial tcp: i/o timeout"))
		assert.Equal(t, Attempts, calls)
		assert.ErrorContains(t, err, "timeout")
	})
	// truth.RemoteRegistryDigest's shape: the throttle is only in the stderr
	// Output() kept, the text says "exit status".
	t.Run("a throttle in an ExitError's stderr is retried", func(t *testing.T) {
		throttled := fmt.Errorf("buildx imagetools inspect --raw: %w; fallback also failed: exit status 1",
			exitWithStderr("ERROR: failed to do request: toomanyrequests: Rate exceeded\n"))
		calls, err := run(throttled, nil)
		assert.Equal(t, 2, calls)
		assert.NoError(t, err)
	})
	t.Run("a permanent verdict in an ExitError's stderr is returned at once", func(t *testing.T) {
		calls, err := run(fmt.Errorf("buildx imagetools inspect --raw: %w", exitWithStderr("ERROR: public.ecr.aws/x:y: not found\n")))
		assert.Equal(t, 1, calls)
		assert.Error(t, err)
	})
}
