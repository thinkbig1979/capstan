package services

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockernet "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-bx43 — THE BEHAVIOURAL ARM FOR THE TWO CONTAINER-UPDATE APPLY PATHS.
//
// This file replaces docker_update_refusal_wired_test.go, agent-os-g482's
// go/ast structural guard, which existed only because DockerService.client was
// a concrete *client.Client and neither UpdateContainer nor
// UpdateContainerStreaming could be driven from this package. Both now call
// Docker through containerUpdateAPI (docker_update.go), so bx43FakeDocker below
// drives them end to end without a daemon and the decision can be asserted on
// its BEHAVIOUR rather than on the shape of the source.
//
// WHAT IT PINS. resolveUpdateStrategy and logRefusedUpdate are unit tested in
// docker_update_dbfault_test.go, but a mutation that leaves both of them
// correct and only rewires the CALL SITE — renaming `case updateRefused:` so
// the refusal falls through to `default:` — reinstates the P2 defect with every
// one of those tests still green. That mutation is M3: the `case updateRefused:`
// line in UpdateContainer and the one in UpdateContainerStreaming, each rewritten
// to `case composeUpdateStrategy(99):`. Run via `go test -overlay` against this
// package with the structural guard deleted and this file absent, it SURVIVED —
// build exit 0, test exit 0, 0 `--- FAIL`. With this file present it dies on the
// two refusal subtests. What kills it is an OBSERVED ImagePull on the fake, not
// an inference about the source.
//
// The fake is deliberately hostile on every apply call: each returns an error
// and records itself. A test that reached an apply path by accident fails
// loudly rather than proceeding on a plausible zero value.

const (
	bx43ContainerID = "bx43-container-id"
	bx43ImageID     = "sha256:bx43imageid"
	bx43ImageRef    = "ghcr.io/example/bx43:latest"
)

// errBx43Apply is what every mutating Docker call on the fake returns. A test
// asserting "the apply path was not taken" would also pass if the apply path
// were taken and silently succeeded, so it must not be able to succeed.
var errBx43Apply = errors.New("bx43 fake: apply call must not happen on the refusal path")

// bx43FakeDocker implements containerUpdateAPI. Reads answer with a fixed
// compose-labelled container; writes record themselves and fail.
type bx43FakeDocker struct {
	mu     sync.Mutex
	calls  []string
	labels map[string]string
}

func (f *bx43FakeDocker) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

// applyCalls returns the recorded calls that only an APPLY can make. It
// excludes ContainerInspect and ImageInspect, which both the refusal and the
// apply paths make before the strategy switch is reached.
func (f *bx43FakeDocker) applyCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		switch c {
		case "ContainerInspect", "ImageInspect":
		default:
			out = append(out, c)
		}
	}
	return out
}

func (f *bx43FakeDocker) ContainerInspect(_ context.Context, containerID string) (container.InspectResponse, error) {
	f.record("ContainerInspect")
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    containerID,
			Name:  "/" + containerID,
			Image: bx43ImageID,
			State: &container.State{Running: false},
		},
		Config: &container.Config{
			Image:  bx43ImageRef,
			Labels: f.labels,
		},
	}, nil
}

func (f *bx43FakeDocker) ImageInspect(_ context.Context, _ string, _ ...dockerclient.ImageInspectOption) (image.InspectResponse, error) {
	f.record("ImageInspect")
	return image.InspectResponse{
		ID:          bx43ImageID,
		RepoTags:    []string{bx43ImageRef},
		RepoDigests: []string{"ghcr.io/example/bx43@sha256:0000000000000000000000000000000000000000000000000000000000000000"},
	}, nil
}

func (f *bx43FakeDocker) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	f.record("ContainerList")
	return nil, errBx43Apply
}

func (f *bx43FakeDocker) ContainerStop(_ context.Context, _ string, _ container.StopOptions) error {
	f.record("ContainerStop")
	return errBx43Apply
}

func (f *bx43FakeDocker) ImagePull(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
	f.record("ImagePull")
	return nil, errBx43Apply
}

func (f *bx43FakeDocker) ContainerRemove(_ context.Context, _ string, _ container.RemoveOptions) error {
	f.record("ContainerRemove")
	return errBx43Apply
}

func (f *bx43FakeDocker) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *dockernet.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
	f.record("ContainerCreate")
	return container.CreateResponse{}, errBx43Apply
}

func (f *bx43FakeDocker) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	f.record("ContainerStart")
	return errBx43Apply
}

// bx43Service returns a DockerService wired to the fake, with the compose
// labels the test wants on the inspected container.
func bx43Service(labels map[string]string) (*DockerService, *bx43FakeDocker) {
	fake := &bx43FakeDocker{labels: labels}
	return &DockerService{updateClient: fake}, fake
}

func bx43ComposeLabels() map[string]string {
	return map[string]string{
		"com.docker.compose.project": g482ProjectKnown,
		"com.docker.compose.service": "web",
	}
}

// TestUpdateApplyPathsRefuseOnUnreadableStacksTable drives both apply paths to
// the compose-vs-standalone decision through the fake and asserts the refusal
// is what the CALL SITE actually does, not merely what resolveUpdateStrategy
// returns.
//
// Two-sided, on one instrument: the same fake and the same call, differing only
// in whether the stacks table can be read.
//   - unreadable table + compose labels -> refused, and NO apply call is made.
//   - no compose labels                 -> standalone apply IS attempted
//     (ImagePull observed), proving the fake reaches the apply path at all and
//     that the refusal arm's zero apply calls is a real result rather than a
//     path that never got that far.
func TestUpdateApplyPathsRefuseOnUnreadableStacksTable(t *testing.T) {
	t.Run("UpdateContainer/refuses", func(t *testing.T) {
		svc, fake := bx43Service(bx43ComposeLabels())
		db := g482ClosedDB(t)

		_, result := svc.UpdateContainer(context.Background(), bx43ContainerID, db)

		bx43AssertRefused(t, result, fake)
	})

	t.Run("UpdateContainerStreaming/refuses", func(t *testing.T) {
		svc, fake := bx43Service(bx43ComposeLabels())
		db := g482ClosedDB(t)

		_, result := svc.UpdateContainerStreaming(
			context.Background(), bx43ContainerID, db,
			func(LogLine) {}, func(Status) {},
		)

		bx43AssertRefused(t, result, fake)
	})

	// The other side of the instrument. Without compose labels the container is
	// unambiguously standalone, so the same code must NOT refuse — it must apply.
	t.Run("UpdateContainer/applies when the container is unambiguously standalone", func(t *testing.T) {
		svc, fake := bx43Service(nil)
		db := g482ClosedDB(t)

		_, result := svc.UpdateContainer(context.Background(), bx43ContainerID, db)

		if result.Reason == refusedUpdateReason {
			t.Fatalf("standalone container: refused (%q) — the refusal arm is firing where no ambiguity exists", result.Reason)
		}
		if !bx43Called(fake, "ImagePull") {
			t.Fatalf("standalone container: no ImagePull on the fake; calls=%v — the apply path was never reached, so the refusal arm's zero apply calls proves nothing", fake.calls)
		}
	})

	t.Run("UpdateContainerStreaming/applies when the container is unambiguously standalone", func(t *testing.T) {
		svc, fake := bx43Service(nil)
		db := g482ClosedDB(t)

		_, result := svc.UpdateContainerStreaming(
			context.Background(), bx43ContainerID, db,
			func(LogLine) {}, func(Status) {},
		)

		if result.Reason == refusedUpdateReason {
			t.Fatalf("standalone container: refused (%q) — the refusal arm is firing where no ambiguity exists", result.Reason)
		}
		if !bx43Called(fake, "ImagePull") {
			t.Fatalf("standalone container: no ImagePull on the fake; calls=%v — the apply path was never reached, so the refusal arm's zero apply calls proves nothing", fake.calls)
		}
	})
}

// bx43AssertRefused is the refusal arm's whole assertion: the reported outcome
// AND the absence of any apply call. The second half is the one the call-site
// mutation trips — a refusal routed to `default:` still returns a failed
// ActionResult (the fake's apply calls all error), so a test that checked only
// the outcome would stay green against it.
func bx43AssertRefused(t *testing.T, result truth.ActionResult, fake *bx43FakeDocker) {
	t.Helper()

	if result.Outcome != truth.OutcomeFailed {
		t.Errorf("outcome: got %q, want %q", result.Outcome, truth.OutcomeFailed)
	}
	if result.Reason != refusedUpdateReason {
		t.Errorf("reason: got %q, want %q — the stacks table could not be read, so the strategy is undecidable and the update must be refused", result.Reason, refusedUpdateReason)
	}
	if applied := fake.applyCalls(); len(applied) != 0 {
		t.Errorf("apply calls made on the refusal path: %v — a compose-managed container is being RECREATED down the standalone path whenever the stacks table cannot be read (agent-os-g482's P2 defect, reinstated)", applied)
	}
	if !bx43Called(fake, "ContainerInspect") {
		t.Errorf("ContainerInspect never called; calls=%v — the path did not run at all, so this test is not measuring the refusal", fake.calls)
	}
}

func bx43Called(fake *bx43FakeDocker, name string) bool {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, c := range fake.calls {
		if c == name {
			return true
		}
	}
	return false
}

// TestBx43FakeSatisfiesTruthImageInspector pins the assignability the two apply
// paths depend on and that a `s.client.<method>`-only sweep cannot see:
// truth.ResolveContainerImage is the FIRST Docker touch on each path
// (docker_update.go:338 and :623), before either path's own ContainerInspect,
// and it takes a truth.ImageInspector. If containerUpdateAPI ever stopped
// satisfying it, the seam would be bypassed there and the fake would never be
// consulted.
func TestBx43FakeSatisfiesTruthImageInspector(t *testing.T) {
	var api containerUpdateAPI = &bx43FakeDocker{labels: bx43ComposeLabels()}
	var inspector truth.ImageInspector = api

	ref, _, imgID, err := truth.ResolveContainerImage(context.Background(), inspector, bx43ContainerID)
	if err != nil {
		t.Fatalf("ResolveContainerImage through containerUpdateAPI: %v", err)
	}
	if !strings.Contains(ref, "bx43") || imgID != bx43ImageID {
		t.Fatalf("ResolveContainerImage: got ref=%q imageID=%q, want the fake's %q / %q", ref, imgID, bx43ImageRef, bx43ImageID)
	}
}
