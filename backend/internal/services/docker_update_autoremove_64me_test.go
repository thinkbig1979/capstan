package services

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-64me: a standalone container started with --rm (HostConfig.AutoRemove)
// is removed by the daemon when the update stops it, and its anonymous volumes
// go with it. The recreate then named a volume that no longer existed, so the
// daemon made a new, empty one under the old name and the data was gone.
// Edwin's ruling: refuse that update before touching anything.

// autoRemove64meFake is qags20Fake with an inspect, so UpdateContainer and
// UpdateContainerStreaming can reach the standalone apply path.
type autoRemove64meFake struct {
	*qags20Fake
	inspect container.InspectResponse
}

func (f *autoRemove64meFake) ContainerInspect(context.Context, string) (container.InspectResponse, error) {
	return f.inspect, nil
}

func inspect64me(autoRemove bool, binds []string, mounts []container.MountPoint) container.InspectResponse {
	in := qags20Inspect()
	in.Image = "sha256:old64me"
	in.State = &container.State{Running: true}
	in.HostConfig = &container.HostConfig{AutoRemove: autoRemove, Binds: binds}
	in.Mounts = mounts
	return in
}

var anonData64me = container.MountPoint{Type: mount.TypeVolume, Name: "0123abcd", Destination: "/data", RW: true}

func TestStandaloneUpdate_64me_RefusesAutoRemoveWithAnonymousVolume(t *testing.T) {
	t.Parallel()

	for _, wasRunning := range []bool{true, false} {
		fake := &qags20Fake{}
		_, err := (&DockerService{updateClient: fake}).recreateStandaloneContainer(context.Background(),
			inspect64me(true, nil, []container.MountPoint{anonData64me}), wasRunning,
			func(LogLine) {}, func(Status) {})

		require.Error(t, err, "wasRunning=%v", wasRunning)
		assert.Contains(t, err.Error(), "--rm", "wasRunning=%v", wasRunning)
		assert.Contains(t, err.Error(), "/data", "wasRunning=%v: names the volume's destination", wasRunning)
		for _, call := range []string{"ImagePull", "ContainerStop", "ContainerRename", "ContainerCreate"} {
			assert.False(t, fake.called(call), "wasRunning=%v: refused before %s", wasRunning, call)
		}
	}
}

// The refusal reaches the user through the same result path as every other
// apply error, and the live job log says why.
func TestStandaloneUpdate_64me_RefusalReachesBothResultPaths(t *testing.T) {
	t.Parallel()

	in := inspect64me(true, nil, []container.MountPoint{anonData64me})

	fake := &autoRemove64meFake{qags20Fake: &qags20Fake{}, inspect: in}
	_, ar := (&DockerService{updateClient: fake}).UpdateContainer(context.Background(), qags20OldID, nil)
	require.Equal(t, truth.OutcomeFailed, ar.Outcome)
	require.Error(t, ar.Err)
	assert.Contains(t, ar.Err.Error(), "--rm")
	assert.False(t, fake.called("ContainerStop"))

	sfake := &autoRemove64meFake{qags20Fake: &qags20Fake{}, inspect: in}
	var mu sync.Mutex
	var logged []string
	_, sar := (&DockerService{updateClient: sfake}).UpdateContainerStreaming(context.Background(), qags20OldID, nil,
		func(l LogLine) {
			mu.Lock()
			defer mu.Unlock()
			if l.Stream == StreamStderr {
				logged = append(logged, l.Text)
			}
		}, func(Status) {})
	require.Equal(t, truth.OutcomeFailed, sar.Outcome)
	require.Error(t, sar.Err)
	assert.Contains(t, sar.Err.Error(), "--rm")
	assert.False(t, sfake.called("ContainerStop"))
	mu.Lock()
	defer mu.Unlock()
	assert.True(t, len(logged) == 1 && strings.Contains(logged[0], "--rm"), "one stderr log line names --rm: %q", logged)
}

// Controls: containers this ruling does not cover still update.
func TestStandaloneUpdate_64me_OtherContainersStillUpdate(t *testing.T) {
	t.Parallel()

	cases := map[string]container.InspectResponse{
		"--rm with only a named volume": inspect64me(true, []string{"mydata:/data"},
			[]container.MountPoint{{Type: mount.TypeVolume, Name: "mydata", Destination: "/data", RW: true}}),
		"--rm with only a bind mount": inspect64me(true, []string{"/srv/x:/data"},
			[]container.MountPoint{{Type: mount.TypeBind, Source: "/srv/x", Destination: "/data", RW: true}}),
		"no --rm, anonymous volume": inspect64me(false, nil, []container.MountPoint{anonData64me}),
	}
	for name, in := range cases {
		fake := &qags20Fake{}
		id, err := (&DockerService{updateClient: fake}).recreateStandaloneContainer(context.Background(), in, true,
			func(LogLine) {}, func(Status) {})
		require.NoError(t, err, name)
		assert.Equal(t, "qags20-new-container", id, name)
		assert.True(t, fake.called("ContainerCreate"), name)
	}
}
