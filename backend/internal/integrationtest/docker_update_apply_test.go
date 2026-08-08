//go:build integration

package integrationtest

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Coverage for the three apply paths in services/docker_update.go that sat at
// 0.0% (agent-os-541b): updateStandaloneContainer, updateStandaloneContainerStreaming
// and updateComposeContainerStreaming.
//
// ROUTE A of the two the bead offers — integration coverage against a real
// daemon — rather than route B, introducing an interface over the Docker SDK.
// The bead's own note is the deciding argument: DockerService reaches 25 distinct
// SDK methods across 54 call sites, so route B "touches the whole service and
// wants its own review rather than riding along with a test bead".
//
// All three target functions are UNEXPORTED, and this package is not
// `package services`, so they are driven through the exported entry points that
// actually reach them in production — UpdateContainer and
// UpdateContainerStreaming. That is deliberate: it keeps these tests in
// ./internal/integrationtest/, which is one of the two paths
// .github/workflows/integration.yml actually runs. A test placed in
// ./internal/services/ instead would compile, pass locally, and silently never
// run in CI, because that workflow has no ./... discovery.
//
// Routing inside those entry points, which is what each test below selects:
//
//	compose labels present AND db resolves the stack -> updateCompose*Streaming
//	anything else                                    -> updateStandalone*
//
// alpine:3.21 is the harness's standard test image, so the pull is a no-op
// against the local cache and the "update" converges to no_change — which is the
// honest outcome for an image that is already current, and still executes every
// statement in the apply path.

const updateTestImage = "alpine:3.21"

// stackByProject is a DashboardDB that resolves exactly one project name. It is
// what flips UpdateContainerStreaming onto the compose branch.
type stackByProject struct {
	project string
	stack   *models.Stack
}

func (s stackByProject) GetStackByProjectName(projectName string) (*models.Stack, error) {
	if projectName == s.project {
		return s.stack, nil
	}
	return nil, nil
}

// runStandaloneContainer starts a plain `docker run` container — no compose
// labels — and returns its id. Registered for removal via t.Cleanup.
func runStandaloneContainer(t *testing.T, name string) string {
	t.Helper()

	// --rm is deliberately NOT used: the apply path removes and recreates the
	// container itself, and a --rm container would race that.
	out, err := exec.Command("docker", "run", "-d", "--name", name,
		updateTestImage, "sleep", "600").CombinedOutput()
	require.NoError(t, err, "docker run failed: %s", strings.TrimSpace(string(out)))

	id := strings.TrimSpace(string(out))
	require.NotEmpty(t, id)

	t.Cleanup(func() {
		// The apply path recreates the container under the same name, so remove
		// by name rather than by the original id.
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	return id
}

// collector gathers the streamed log lines and statuses so the streaming paths
// can be asserted on by what they actually emitted.
type collector struct {
	mu       sync.Mutex
	lines    []string
	statuses []services.Status
}

func (c *collector) emit(l services.LogLine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, l.Text)
}

func (c *collector) setStatus(s services.Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statuses = append(c.statuses, s)
}

func (c *collector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

func (c *collector) sawStatus(want services.Status) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.statuses {
		if s == want {
			return true
		}
	}
	return false
}

// Test_UpdateContainer_Standalone_AppliesAndConverges drives
// updateStandaloneContainer: a container with no compose labels, and a nil
// DashboardDB, takes the standalone branch.
//
// DEFECT, tracked in agent-os-ekmk: the apply genuinely succeeds — the container
// is removed, recreated under the same name and started — and then the result is
// reported as `failed`, because the post-update verification inspects the
// container id it started with. That id no longer exists. Only the compose branch
// re-resolves the id (via findComposeContainer), so this hits every standalone
// container update.
//
// The two assertions below are deliberately in tension, and that IS the finding:
// the container is running afterwards, and the caller was told the update failed.
// Pinned rather than fixed here so the fix lands as a visible, reviewable
// behaviour change rather than riding along inside a coverage bead — the same
// approach agent-os-t9up used.
func Test_UpdateContainer_Standalone_AppliesAndConverges(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	svc := newDockerService(t, t.TempDir())
	id := runStandaloneContainer(t, "capstan-it-standalone-apply")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// db is nil, so the compose branch cannot be taken whatever the labels say.
	result, ar := svc.UpdateContainer(ctx, id, nil)

	assert.NotEmpty(t, result.OldDigest, "the pre-update image id must be captured")

	// THE APPLY WORKED. The container was removed and recreated under the same
	// name, so it exists and is running again.
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}",
		"capstan-it-standalone-apply").CombinedOutput()
	require.NoError(t, err, "the container must exist after the update: %s", strings.TrimSpace(string(out)))
	assert.Equal(t, "true", strings.TrimSpace(string(out)),
		"a container that was running before the update must be running after it")

	// AND YET the caller is told it failed. agent-os-ekmk.
	assert.Equal(t, "failed", string(ar.Outcome),
		"DEFECT (agent-os-ekmk): a successful standalone update is reported as failed")
	assert.Contains(t, ar.Reason, "post-update container inspect failed",
		"DEFECT (agent-os-ekmk): the failure comes from inspecting the pre-update "+
			"container id, which the apply path has already removed")
}

// Test_UpdateContainerStreaming_Standalone_EmitsPullProgress drives
// updateStandaloneContainerStreaming, and asserts on what reached the WS log
// rather than only on the return value.
func Test_UpdateContainerStreaming_Standalone_EmitsPullProgress(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	svc := newDockerService(t, t.TempDir())
	id := runStandaloneContainer(t, "capstan-it-standalone-stream")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	c := &collector{}
	_, ar := svc.UpdateContainerStreaming(ctx, id, nil, c.emit, c.setStatus)

	// Same DEFECT as the non-streaming path above (agent-os-ekmk): the pull and
	// the recreate both succeed, and the verification step then inspects an id
	// that no longer exists.
	assert.Equal(t, "failed", string(ar.Outcome),
		"DEFECT (agent-os-ekmk): a successful standalone streaming update is reported as failed")
	assert.Contains(t, ar.Reason, "post-update container inspect failed",
		"DEFECT (agent-os-ekmk)")

	// The status transition is what drives the UI's progress indicator.
	assert.True(t, c.sawStatus(services.StatusPulling),
		"the caller must be told a pull started; statuses seen: %v", c.statuses)

	log := c.text()
	assert.Contains(t, log, "==> Pulling "+updateTestImage,
		"the pull must be announced with the image being pulled")
	assert.Contains(t, log, "Pull complete",
		"the pull must be reported as finished; this is the line that used to be "+
			"swallowed by io.Copy(io.Discard)")
}

// Test_UpdateContainerStreaming_Compose_UsesComposePull drives
// updateComposeContainerStreaming: a real compose project plus a DashboardDB that
// resolves it, which is the only combination that selects the compose branch.
func Test_UpdateContainerStreaming_Compose_UsesComposePull(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	composeYAML := "services:\n  web:\n    image: " + updateTestImage +
		"\n    command: [\"sleep\",\"600\"]\n    restart: \"no\"\n"
	dir, project, cleanup := NewTempStack(t, composeYAML)
	defer cleanup()

	RunCompose(t, dir, project, "up", "-d")
	AssertContainerState(t, dir, project, "web", true)

	// The container id compose created, which is what the handler would pass in.
	idOut := RunCompose(t, dir, project, "ps", "-q", "web")
	containerID := strings.TrimSpace(idOut)
	require.NotEmpty(t, containerID, "compose must report a container id for web")

	svc := newDockerService(t, dir)
	db := stackByProject{
		project: project,
		stack: &models.Stack{
			ID:          project + ":default",
			Directory:   dir,
			ComposeFile: "compose.yaml",
			ProjectName: project,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := &collector{}
	_, ar := svc.UpdateContainerStreaming(ctx, containerID, db, c.emit, c.setStatus)

	assert.NotEqual(t, "failed", string(ar.Outcome),
		"compose streaming apply must not fail: %s", ar.Reason)
	assert.True(t, c.sawStatus(services.StatusPulling),
		"the compose path must also report a pull; statuses seen: %v", c.statuses)
	assert.Contains(t, c.text(), "==> Pulling web",
		"the compose path announces the SERVICE name, not the image reference")

	// The service must still be up: this path shells out to `docker compose pull`
	// then `up`, and a half-applied update would leave it down.
	AssertContainerState(t, dir, project, "web", true)
}

// Test_UpdateContainerStreaming_ComposeLabelsButNoStack_FallsBackToStandalone
// covers the fallback inside the compose branch: the labels are present, but the
// DashboardDB cannot resolve the project, so the standalone path is used rather
// than the update being abandoned.
func Test_UpdateContainerStreaming_ComposeLabelsButNoStack_FallsBackToStandalone(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	composeYAML := "services:\n  web:\n    image: " + updateTestImage +
		"\n    command: [\"sleep\",\"600\"]\n    restart: \"no\"\n"
	dir, project, cleanup := NewTempStack(t, composeYAML)
	defer cleanup()

	RunCompose(t, dir, project, "up", "-d")
	containerID := strings.TrimSpace(RunCompose(t, dir, project, "ps", "-q", "web"))
	require.NotEmpty(t, containerID)

	svc := newDockerService(t, dir)
	// Resolves a DIFFERENT project, so GetStackByProjectName returns nil for this
	// container's project and the compose branch falls through.
	db := stackByProject{project: "some-other-project", stack: nil}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	c := &collector{}
	_, ar := svc.UpdateContainerStreaming(ctx, containerID, db, c.emit, c.setStatus)

	assert.NotEqual(t, "failed", string(ar.Outcome),
		"an unresolvable project must fall back, not fail: %s", ar.Reason)
	// The standalone path announces the IMAGE; the compose path announces the
	// service name. That is how the two are told apart from the log alone.
	assert.Contains(t, c.text(), "==> Pulling "+updateTestImage,
		"the fallback must take the standalone path, which names the image")
}
