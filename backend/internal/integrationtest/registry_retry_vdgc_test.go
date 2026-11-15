//go:build integration

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thinkbig1979/capstan/backend/internal/registryretry"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// timeoutErr is a net.Error that timed out, as a client-side dial would.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "dial tcp: i/o deadline" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// What the compose path returns and streams when its pull fails, as
// updateComposeContainerStreaming wraps streamComposeCmd's error
// (services/docker_update.go). Exit status 18 is the code a reviewer's probe
// observed for a failed `compose pull`.
var (
	errComposeExit     = errors.New("compose pull failed: command failed: exit status 18")
	composeLogTimeout  = "==> Pulling web\n web Pulling\n web Error Get \"https://public.ecr.aws/v2/\": context deadline exceeded\nError response from daemon: Get \"https://public.ecr.aws/v2/\": context deadline exceeded"
	composeLogManifest = "==> Pulling web\n web Pulling\n web Error manifest unknown\nError response from daemon: manifest unknown: manifest unknown"
)

func TestRegistryTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		log  string
		want bool
	}{
		// The #736 CI line, as the daemon returns it: text only.
		{"daemon deadline text", errors.New(`pulling image x: Error response from daemon: Get "https://public.ecr.aws/v2/": context deadline exceeded`), "", true},
		{"chain deadline", fmt.Errorf("pulling image x: %w", context.DeadlineExceeded), "", true},
		{"chain net timeout", fmt.Errorf("pulling image x: %w", timeoutErr{}), "", true},
		{"tls handshake timeout", errors.New("pulling image x: net/http: TLS handshake timeout"), "", true},
		{"throttled", errors.New("pulling image x: docker pull error: toomanyrequests: Rate exceeded"), "", true},
		{"503", errors.New("pulling image x: received unexpected HTTP status: 503 Service Unavailable"), "", true},
		{"connection reset", errors.New("pulling image x: read tcp 1.2.3.4:443: read: connection reset by peer"), "", true},
		{"connection refused", errors.New("pulling image x: dial tcp 1.2.3.4:443: connect: connection refused"), "", true},
		// OBSERVED on a laptop in this bead's loaded loop, during PullPinnedImage.
		{"dns lookup", errors.New(`pulling image x: Error response from daemon: Head "https://public.ecr.aws/v2/docker/library/alpine/manifests/3.21": dial tcp: lookup public.ecr.aws: no such host`), "", true},
		{"unexpected EOF", errors.New("pulling image x: docker pull error: unexpected EOF"), "", true},

		{"manifest unknown", errors.New("pulling image x: manifest unknown: manifest unknown"), "", false},
		{"unauthorized", errors.New("pulling image x: unauthorized: authentication required"), "", false},
		{"denied", errors.New("pulling image x: denied: requested access to the resource is denied"), "", false},
		{"not found", errors.New("pulling image x: Error response from daemon: repository x not found"), "", false},
		{"permanent beats a timeout word", errors.New("pulling image x: manifest unknown after timeout"), "", false},
		{"digest hex holding 502", errors.New("pulling image x: invalid reference sha256:ab502cd"), "", false},
		// The compose path's error is only an exit status (streamComposeCmd);
		// compose's stderr, in the attempt's log, says why.
		{"compose exit, log timeout", errComposeExit, composeLogTimeout, true},
		{"compose exit, log 429", errComposeExit, " web Error toomanyrequests: 429 Too Many Requests", true},
		{"compose exit, log manifest unknown", errComposeExit, composeLogManifest, false},
		{"compose exit, log without network words", errComposeExit, "==> Pulling web\n web Pulling", false},
		{"permanent in the log beats a timeout in the error", errors.New("pulling image x: context deadline exceeded"), "denied: requested access to the resource is denied", false},
		{"plain error", errors.New("stopping container: boom"), "", false},
		{"nil", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, registryretry.Transient(tc.err, tc.log), "%v / log %q", tc.err, tc.log)
		})
	}
}

func TestRetryUpdateAtPull(t *testing.T) {
	defer func(p time.Duration) { updateRetryPause = p }(updateRetryPause)
	updateRetryPause = time.Millisecond

	pullTimeout := truth.Failed("update apply failed", errors.New(`pulling image x: Get "https://public.ecr.aws/v2/": context deadline exceeded`))
	composeFailed := truth.Failed("update apply failed", errComposeExit)
	ok := truth.NoChange("Already up to date")

	type attempt struct {
		ar  truth.ActionResult
		log string
	}
	run := func(ctx context.Context, results []attempt, pastPull bool) (truth.ActionResult, int) {
		calls := 0
		ar := RetryUpdateAtPull(ctx, t, func() (truth.ActionResult, bool, string) {
			r := results[min(calls, len(results)-1)]
			calls++
			return r.ar, pastPull, r.log
		})
		return ar, calls
	}
	one := func(ar truth.ActionResult) []attempt { return []attempt{{ar: ar}} }
	bg := context.Background()

	t.Run("a transient pull failure is retried", func(t *testing.T) {
		ar, calls := run(bg, []attempt{{ar: pullTimeout}, {ar: composeFailed, log: composeLogTimeout}, {ar: ok}}, false)
		assert.Equal(t, truth.OutcomeNoChange, ar.Outcome)
		assert.Equal(t, 3, calls)
	})
	t.Run("attempts are bounded", func(t *testing.T) {
		ar, calls := run(bg, one(pullTimeout), false)
		assert.Equal(t, truth.OutcomeFailed, ar.Outcome)
		assert.Equal(t, registryretry.Attempts, calls)
	})
	t.Run("a permanent pull failure is not retried", func(t *testing.T) {
		_, calls := run(bg, one(truth.Failed("update apply failed", errors.New("pulling image x: manifest unknown"))), false)
		assert.Equal(t, 1, calls)
	})
	t.Run("a compose pull whose log says timeout is retried", func(t *testing.T) {
		ar, calls := run(bg, []attempt{{ar: composeFailed, log: composeLogTimeout}, {ar: ok}}, false)
		assert.Equal(t, truth.OutcomeNoChange, ar.Outcome)
		assert.Equal(t, 2, calls)
	})
	// UpdateComposeServiceStreaming names the step in the Reason, and its
	// Err is only streamComposeCmd's exit status (agent-os-18uw).
	serviceComposeFailed := truth.Failed("compose pull failed", errors.New("command failed: exit status 18"))
	t.Run("a compose-service pull whose log says timeout is retried", func(t *testing.T) {
		ar, calls := run(bg, []attempt{{ar: serviceComposeFailed, log: composeLogTimeout}, {ar: ok}}, false)
		assert.Equal(t, truth.OutcomeNoChange, ar.Outcome)
		assert.Equal(t, 2, calls)
	})
	t.Run("a compose-service up failure is not retried", func(t *testing.T) {
		_, calls := run(bg, []attempt{{ar: truth.Failed("compose up failed", errors.New("command failed: exit status 1")), log: composeLogTimeout}}, false)
		assert.Equal(t, 1, calls)
	})
	// agent-os-sgqy, accepted as is: at a failure RetryUpdateAtPull may retry,
	// the attempt's log holds only the pull step's lines (no update path
	// emits a line before "==> Pulling" but the 64me refusal, which is not a
	// pull error), so the regex has nothing else to match. A pull that failed
	// for another reason with a transient word in its own lines (a service
	// named "timeout-web") is retried to the bound and still reported failed.
	t.Run("a non-network pull failure naming timeout is retried to the bound and stays failed", func(t *testing.T) {
		log := "==> Pulling timeout-web\n timeout-web Pulling\n timeout-web Error no space left on device"
		ar, calls := run(bg, []attempt{{ar: composeFailed, log: log}}, false)
		assert.Equal(t, truth.OutcomeFailed, ar.Outcome)
		assert.Equal(t, registryretry.Attempts, calls)
	})
	t.Run("a compose pull whose log says manifest unknown is not retried", func(t *testing.T) {
		_, calls := run(bg, []attempt{{ar: composeFailed, log: composeLogManifest}}, false)
		assert.Equal(t, 1, calls)
	})
	t.Run("a compose pull with no network words in its log is not retried", func(t *testing.T) {
		_, calls := run(bg, []attempt{{ar: composeFailed, log: "==> Pulling web"}}, false)
		assert.Equal(t, 1, calls)
	})
	t.Run("a timeout after the pull is not retried", func(t *testing.T) {
		_, calls := run(bg, one(truth.Failed("update apply failed", errors.New("stopping container: context deadline exceeded"))), false)
		assert.Equal(t, 1, calls)
	})
	t.Run("an update that reported recreating is not retried", func(t *testing.T) {
		_, calls := run(bg, one(pullTimeout), true)
		assert.Equal(t, 1, calls)
	})
	t.Run("an ended ctx is not retried", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg)
		cancel()
		_, calls := run(ctx, one(pullTimeout), false)
		assert.Equal(t, 1, calls)
	})
}
