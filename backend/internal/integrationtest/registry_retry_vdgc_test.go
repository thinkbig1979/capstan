//go:build integration

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// timeoutErr is a net.Error that timed out, as a client-side dial would.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "dial tcp: i/o deadline" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestRegistryTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// The #736 CI line, as the daemon returns it: text only.
		{"daemon deadline text", errors.New(`pulling image x: Error response from daemon: Get "https://public.ecr.aws/v2/": context deadline exceeded`), true},
		{"chain deadline", fmt.Errorf("pulling image x: %w", context.DeadlineExceeded), true},
		{"chain net timeout", fmt.Errorf("pulling image x: %w", timeoutErr{}), true},
		{"tls handshake timeout", errors.New("pulling image x: net/http: TLS handshake timeout"), true},
		{"throttled", errors.New("pulling image x: docker pull error: toomanyrequests: Rate exceeded"), true},
		{"429", errors.New("compose pull failed: received unexpected HTTP status: 429 Too Many Requests"), true},
		{"503", errors.New("pulling image x: received unexpected HTTP status: 503 Service Unavailable"), true},
		{"connection reset", errors.New("pulling image x: read tcp 1.2.3.4:443: read: connection reset by peer"), true},
		{"connection refused", errors.New("pulling image x: dial tcp 1.2.3.4:443: connect: connection refused"), true},
		// OBSERVED on a laptop in this bead's loaded loop, during PullPinnedImage.
		{"dns lookup", errors.New(`pulling image x: Error response from daemon: Head "https://public.ecr.aws/v2/docker/library/alpine/manifests/3.21": dial tcp: lookup public.ecr.aws: no such host`), true},
		{"unexpected EOF", errors.New("pulling image x: docker pull error: unexpected EOF"), true},

		{"manifest unknown", errors.New("pulling image x: manifest unknown: manifest unknown"), false},
		{"unauthorized", errors.New("pulling image x: unauthorized: authentication required"), false},
		{"denied", errors.New("pulling image x: denied: requested access to the resource is denied"), false},
		{"not found", errors.New("pulling image x: Error response from daemon: repository x not found"), false},
		{"permanent beats a timeout word", errors.New("pulling image x: manifest unknown after timeout"), false},
		{"digest hex holding 502", errors.New("pulling image x: invalid reference sha256:ab502cd"), false},
		{"plain error", errors.New("stopping container: boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, registryTransient(tc.err), "%v", tc.err)
		})
	}
}

func TestRetryUpdateAtPull(t *testing.T) {
	defer func(p time.Duration) { updateRetryPause = p }(updateRetryPause)
	updateRetryPause = time.Millisecond

	pullTimeout := truth.Failed("update apply failed", errors.New(`pulling image x: Get "https://public.ecr.aws/v2/": context deadline exceeded`))
	composeTimeout := truth.Failed("update apply failed", errors.New("compose pull failed: context deadline exceeded"))
	ok := truth.NoChange("Already up to date")

	run := func(ctx context.Context, results []truth.ActionResult, pastPull bool) (truth.ActionResult, int) {
		calls := 0
		ar := RetryUpdateAtPull(ctx, t, func() (truth.ActionResult, bool) {
			r := results[min(calls, len(results)-1)]
			calls++
			return r, pastPull
		})
		return ar, calls
	}
	bg := context.Background()

	t.Run("a transient pull failure is retried", func(t *testing.T) {
		ar, calls := run(bg, []truth.ActionResult{pullTimeout, composeTimeout, ok}, false)
		assert.Equal(t, truth.OutcomeNoChange, ar.Outcome)
		assert.Equal(t, 3, calls)
	})
	t.Run("attempts are bounded", func(t *testing.T) {
		ar, calls := run(bg, []truth.ActionResult{pullTimeout}, false)
		assert.Equal(t, truth.OutcomeFailed, ar.Outcome)
		assert.Equal(t, registryAttempts, calls)
	})
	t.Run("a permanent pull failure is not retried", func(t *testing.T) {
		_, calls := run(bg, []truth.ActionResult{truth.Failed("update apply failed", errors.New("pulling image x: manifest unknown"))}, false)
		assert.Equal(t, 1, calls)
	})
	t.Run("a timeout after the pull is not retried", func(t *testing.T) {
		_, calls := run(bg, []truth.ActionResult{truth.Failed("update apply failed", errors.New("stopping container: context deadline exceeded"))}, false)
		assert.Equal(t, 1, calls)
	})
	t.Run("an update that reported recreating is not retried", func(t *testing.T) {
		_, calls := run(bg, []truth.ActionResult{pullTimeout}, true)
		assert.Equal(t, 1, calls)
	})
	t.Run("an ended ctx is not retried", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg)
		cancel()
		_, calls := run(ctx, []truth.ActionResult{pullTimeout}, false)
		assert.Equal(t, 1, calls)
	})
}
