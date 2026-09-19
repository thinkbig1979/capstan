package services

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockernet "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-qyg7.2 — THE FAIL-FIRST ARM FOR UpdateComposeServiceStreaming's
// container lookup.
//
// THE DEFECT. The pre-fix line was
//
//	if listErr != nil || len(containers) == 0 {
//	    ar = truth.Failed("could not find container for service "+serviceName, listErr)
//	}
//
// so a ContainerList that FAILED and a service that genuinely has NO container
// produced the same ar.Reason. That matters because ar.Reason is the sentence
// the operator reads: handlers/updates.go takes this ActionResult and records
// ar.Err into the history row's error_message ONLY when it is non-nil, while
// the reason is written either way. A docker daemon that could not be reached
// was therefore reported as a fact about the service -- "could not find
// container for service X" -- on the strength of a question that was never
// answered.
//
// WHY THIS ASSERTION AND NOT ANOTHER. Asserting `ar.Err != nil` on the failure
// arm proves nothing: listErr was already passed to truth.Failed before the
// fix, so that assertion is green on the defect. The only thing that moved is
// whether the two states are DISTINGUISHABLE, so the test compares the two
// reasons against each other and requires them to differ. That comparison is
// what fails on the pre-fix code, with both arms reporting the identical
// string.

// qyg72ListFake implements containerUpdateAPI with a programmable
// ContainerList. Every other method returns an error and records itself: both
// arms must return at the ContainerList branch, so any later call means the
// test drove a path it was not written to measure and should fail loudly
// rather than proceed on a plausible zero value.
type qyg72ListFake struct {
	containers []container.Summary
	listErr    error
	beyond     []string // calls made after the branch under test
}

var errQyg72Beyond = errors.New("qyg7.2 fake: this call is past the branch under test")

func (f *qyg72ListFake) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return f.containers, f.listErr
}

func (f *qyg72ListFake) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	f.beyond = append(f.beyond, "ContainerInspect")
	return container.InspectResponse{}, errQyg72Beyond
}

func (f *qyg72ListFake) ImageInspect(_ context.Context, _ string, _ ...dockerclient.ImageInspectOption) (image.InspectResponse, error) {
	f.beyond = append(f.beyond, "ImageInspect")
	return image.InspectResponse{}, errQyg72Beyond
}

func (f *qyg72ListFake) ContainerStop(_ context.Context, _ string, _ container.StopOptions) error {
	f.beyond = append(f.beyond, "ContainerStop")
	return errQyg72Beyond
}

func (f *qyg72ListFake) ImagePull(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
	f.beyond = append(f.beyond, "ImagePull")
	return nil, errQyg72Beyond
}

func (f *qyg72ListFake) ContainerRemove(_ context.Context, _ string, _ container.RemoveOptions) error {
	f.beyond = append(f.beyond, "ContainerRemove")
	return errQyg72Beyond
}

func (f *qyg72ListFake) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *dockernet.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
	f.beyond = append(f.beyond, "ContainerCreate")
	return container.CreateResponse{}, errQyg72Beyond
}

func (f *qyg72ListFake) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	f.beyond = append(f.beyond, "ContainerStart")
	return errQyg72Beyond
}

// qyg72RunCompose drives UpdateComposeServiceStreaming to its container-lookup
// branch and returns the ActionResult plus the fake, so a caller can assert
// that nothing past the branch ran.
func qyg72RunCompose(t *testing.T, fake *qyg72ListFake) (truth.ActionResult, *qyg72ListFake) {
	t.Helper()
	svc := &DockerService{updateClient: fake}
	stack := models.Stack{ID: "qyg72-stack", ProjectName: "qyg72proj", Directory: t.TempDir()}
	_, _, _, ar := svc.UpdateComposeServiceStreaming(
		context.Background(), stack, "web",
		func(LogLine) {}, func(Status) {},
	)
	return ar, fake
}

func TestUpdateComposeServiceStreaming_ListFaultIsNotReportedAsAbsence(t *testing.T) {
	listErr := errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")

	faultAR, faultFake := qyg72RunCompose(t, &qyg72ListFake{listErr: listErr})
	emptyAR, emptyFake := qyg72RunCompose(t, &qyg72ListFake{containers: nil})

	// Both arms must stop at the branch under test.
	if len(faultFake.beyond) != 0 {
		t.Fatalf("fault arm ran past the branch under test: %v", faultFake.beyond)
	}
	if len(emptyFake.beyond) != 0 {
		t.Fatalf("empty arm ran past the branch under test: %v", emptyFake.beyond)
	}

	// Both are failures. If either stopped being one, the test below would be
	// comparing two reasons that are equal for an unrelated reason.
	if faultAR.Outcome != truth.OutcomeFailed {
		t.Fatalf("fault arm: outcome = %v, want %v", faultAR.Outcome, truth.OutcomeFailed)
	}
	if emptyAR.Outcome != truth.OutcomeFailed {
		t.Fatalf("empty arm: outcome = %v, want %v", emptyAR.Outcome, truth.OutcomeFailed)
	}

	// THE DISCRIMINATING ASSERTION. ar.Reason is what the operator reads, and
	// before the fix both arms produced "could not find container for service
	// web" -- a statement about the service, made when the question had not
	// been answered.
	if faultAR.Reason == emptyAR.Reason {
		t.Fatalf("a failed ContainerList and an empty list report the SAME reason %q, "+
			"so the operator cannot tell an unreachable daemon from a service with no container",
			faultAR.Reason)
	}

	// And the fault arm's reason must be about the LOOKUP, not about the
	// service, or the two merely differ without the difference being honest.
	if !strings.Contains(faultAR.Reason, "could not list containers") {
		t.Fatalf("fault arm: reason = %q, want it to say the list call failed", faultAR.Reason)
	}
	if !strings.Contains(emptyAR.Reason, "could not find container") {
		t.Fatalf("empty arm: reason = %q, want it to say no container was found", emptyAR.Reason)
	}

	// The underlying cause must still reach the caller on the fault arm --
	// handlers/updates.go writes ar.Err into the history row's error_message --
	// and must NOT be invented on the empty arm.
	if !errors.Is(faultAR.Err, listErr) {
		t.Fatalf("fault arm: Err = %v, want it to wrap the ContainerList error", faultAR.Err)
	}
	if emptyAR.Err != nil {
		t.Fatalf("empty arm: Err = %v, want nil (nothing failed; the service simply has no container)", emptyAR.Err)
	}
}
