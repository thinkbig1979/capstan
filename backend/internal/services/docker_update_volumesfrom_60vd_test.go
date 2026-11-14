package services

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-60vd: a --rm container's volumes that come from --volumes-from are
// held by the source container too, so stopping the --rm container does not
// delete them (OBSERVED on Docker 26.1.5, named and anonymous source volumes
// alike). The 64me refusal must not fire for them; it still fires for the
// container's own anonymous volumes, and when a source cannot be inspected.

type volumesFrom60vdFake struct {
	*qags20Fake
	sources map[string]container.InspectResponse
}

func (f *volumesFrom60vdFake) ContainerInspect(_ context.Context, id string) (container.InspectResponse, error) {
	f.record("ContainerInspect")
	if in, ok := f.sources[id]; ok {
		return in, nil
	}
	return container.InspectResponse{}, errors.New("60vd fake: no such container " + id)
}

var (
	named60vd = container.MountPoint{Type: mount.TypeVolume, Name: "dbdata", Destination: "/named", RW: true}
	anon60vd  = container.MountPoint{Type: mount.TypeVolume, Name: "8e74675f0d9a", Destination: "/anon", RW: true}
	own60vd   = container.MountPoint{Type: mount.TypeVolume, Name: "ffee0011", Destination: "/own", RW: true}
)

func source60vd() container.InspectResponse {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: "src-id", Name: "/src"},
		Mounts:            []container.MountPoint{named60vd, anon60vd},
	}
}

func inspect60vd(mounts ...container.MountPoint) container.InspectResponse {
	in := inspect64me(true, nil, mounts)
	in.HostConfig.VolumesFrom = []string{"src:ro"}
	return in
}

func TestStandaloneUpdate_60vd_VolumesFromNotRefused(t *testing.T) {
	t.Parallel()

	fake := &volumesFrom60vdFake{qags20Fake: &qags20Fake{}, sources: map[string]container.InspectResponse{"src": source60vd()}}
	id, err := (&DockerService{updateClient: fake}).recreateStandaloneContainer(context.Background(),
		inspect60vd(named60vd, anon60vd), true, func(LogLine) {}, func(Status) {})

	require.NoError(t, err, "volumes held by the --volumes-from source survive the stop")
	assert.Equal(t, "qags20-new-container", id)
	assert.True(t, fake.called("ContainerCreate"))
}

func TestStandaloneUpdate_60vd_OwnAnonymousVolumeStillRefused(t *testing.T) {
	t.Parallel()

	fake := &volumesFrom60vdFake{qags20Fake: &qags20Fake{}, sources: map[string]container.InspectResponse{"src": source60vd()}}
	_, err := (&DockerService{updateClient: fake}).recreateStandaloneContainer(context.Background(),
		inspect60vd(named60vd, anon60vd, own60vd), true, func(LogLine) {}, func(Status) {})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "(/own)", "names only the container's own anonymous volume")
	for _, call := range []string{"ImagePull", "ContainerStop", "ContainerRename", "ContainerCreate"} {
		assert.False(t, fake.called(call), "refused before %s", call)
	}
}

func TestStandaloneUpdate_60vd_SourceInspectFailureRefuses(t *testing.T) {
	t.Parallel()

	fake := &volumesFrom60vdFake{qags20Fake: &qags20Fake{}}
	_, err := (&DockerService{updateClient: fake}).recreateStandaloneContainer(context.Background(),
		inspect60vd(named60vd), true, func(LogLine) {}, func(Status) {})

	require.Error(t, err, "a source that cannot be read keeps the refusal")
	assert.Contains(t, err.Error(), "--rm")
	for _, call := range []string{"ImagePull", "ContainerStop", "ContainerRename", "ContainerCreate"} {
		assert.False(t, fake.called(call), "refused before %s", call)
	}
}
