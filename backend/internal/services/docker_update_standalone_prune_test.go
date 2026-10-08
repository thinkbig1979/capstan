package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockernet "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-qags.20: a standalone container has no stack, so no operation lock
// covers it. A container prune that lands between the update's own stop and its
// remove makes ContainerRemove answer "no such container"; the update used to
// return "removing container: ..." there and never reach the recreate, so the
// user's container was gone and the update reported a failure.
//
// The fake below lets every call succeed except the ones a case names, so the
// recreate is reachable. (bx43FakeDocker fails every apply call on purpose and
// cannot get that far.)

const qags20OldID = "qags20-old-container"

var errQags20Daemon = errors.New("qags20 fake: daemon exploded")

type qags20Fake struct {
	mu        sync.Mutex
	calls     []string
	stopErr   error
	removeErr error
}

func (f *qags20Fake) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *qags20Fake) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (f *qags20Fake) ContainerInspect(context.Context, string) (container.InspectResponse, error) {
	return container.InspectResponse{}, errors.New("qags20 fake: unexpected ContainerInspect")
}

func (f *qags20Fake) ImageInspect(context.Context, string, ...dockerclient.ImageInspectOption) (image.InspectResponse, error) {
	return image.InspectResponse{}, errors.New("qags20 fake: unexpected ImageInspect")
}

func (f *qags20Fake) ContainerList(context.Context, container.ListOptions) ([]container.Summary, error) {
	return nil, errors.New("qags20 fake: unexpected ContainerList")
}

func (f *qags20Fake) ImagePull(context.Context, string, image.PullOptions) (io.ReadCloser, error) {
	f.record("ImagePull")
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *qags20Fake) ContainerStop(context.Context, string, container.StopOptions) error {
	f.record("ContainerStop")
	return f.stopErr
}

func (f *qags20Fake) ContainerRemove(context.Context, string, container.RemoveOptions) error {
	f.record("ContainerRemove")
	return f.removeErr
}

func (f *qags20Fake) ContainerCreate(context.Context, *container.Config, *container.HostConfig, *dockernet.NetworkingConfig, *ocispec.Platform, string) (container.CreateResponse, error) {
	f.record("ContainerCreate")
	return container.CreateResponse{ID: "qags20-new-container"}, nil
}

func (f *qags20Fake) ContainerStart(context.Context, string, container.StartOptions) error {
	f.record("ContainerStart")
	return nil
}

// qags20NotFound is the shape the Docker client returns for a missing
// container: an error that cerrdefs.IsNotFound recognises.
func qags20NotFound() error {
	return fmt.Errorf("Error response from daemon: No such container: %s: %w", qags20OldID, cerrdefs.ErrNotFound)
}

func qags20Inspect() container.InspectResponse {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: qags20OldID, Name: "/qags20"},
		Config:            &container.Config{Image: "ghcr.io/example/qags20:latest"},
	}
}

// qags20Apply is one of the two standalone apply functions behind one
// signature, so each case runs against both.
type qags20Apply struct {
	name string
	run  func(*DockerService, bool) (string, error)
}

func qags20Applies() []qags20Apply {
	return []qags20Apply{
		{"updateStandaloneContainer", func(s *DockerService, wasRunning bool) (string, error) {
			return s.updateStandaloneContainer(context.Background(), qags20Inspect(), wasRunning)
		}},
		{"updateStandaloneContainerStreaming", func(s *DockerService, wasRunning bool) (string, error) {
			return s.updateStandaloneContainerStreaming(context.Background(), qags20Inspect(), wasRunning,
				func(LogLine) {}, func(Status) {})
		}},
	}
}

func TestStandaloneUpdate_RemoveNotFoundAfterOwnStop(t *testing.T) {
	t.Parallel()

	for _, apply := range qags20Applies() {
		t.Run(apply.name, func(t *testing.T) {
			t.Parallel()

			// A prune removed the container after our stop: it is already gone,
			// so the recreate must run and the update must succeed.
			t.Run("prune between stop and remove: recreate runs", func(t *testing.T) {
				t.Parallel()
				fake := &qags20Fake{removeErr: qags20NotFound()}
				id, err := apply.run(&DockerService{updateClient: fake}, true)

				require.NoError(t, err)
				assert.Equal(t, "qags20-new-container", id)
				assert.True(t, fake.called("ContainerStop"), "the stop this case is about must have happened")
				assert.True(t, fake.called("ContainerCreate"), "the recreate must run")
				assert.True(t, fake.called("ContainerStart"), "the container was running, so the recreate starts it")
			})

			// The same instrument, the other side: a container gone BEFORE our
			// stop is not our prune window, so today's error stays.
			t.Run("gone before the stop: error, no recreate", func(t *testing.T) {
				t.Parallel()
				fake := &qags20Fake{stopErr: qags20NotFound()}
				_, err := apply.run(&DockerService{updateClient: fake}, true)

				require.Error(t, err)
				assert.Contains(t, err.Error(), "stopping container")
				assert.False(t, fake.called("ContainerRemove"))
				assert.False(t, fake.called("ContainerCreate"))
			})

			// Not running: we stopped nothing, so a not-found gives no proof the
			// container was not deleted on purpose. Recreating would resurrect it.
			t.Run("not running, remove not-found: error, no recreate", func(t *testing.T) {
				t.Parallel()
				fake := &qags20Fake{removeErr: qags20NotFound()}
				_, err := apply.run(&DockerService{updateClient: fake}, false)

				require.Error(t, err)
				assert.Contains(t, err.Error(), "removing container")
				assert.False(t, fake.called("ContainerStop"))
				assert.False(t, fake.called("ContainerCreate"))
			})

			// Only not-found is forgiven.
			t.Run("any other remove error: error, no recreate", func(t *testing.T) {
				t.Parallel()
				fake := &qags20Fake{removeErr: errQags20Daemon}
				_, err := apply.run(&DockerService{updateClient: fake}, true)

				require.Error(t, err)
				assert.Contains(t, err.Error(), "removing container")
				assert.False(t, fake.called("ContainerCreate"))
			})
		})
	}
}
