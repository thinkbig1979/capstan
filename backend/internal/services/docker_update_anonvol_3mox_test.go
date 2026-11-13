package services

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockernet "github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-3mox. A standalone update created the new container from the old
// one's HostConfig verbatim, which names no source for an anonymous volume (a
// `-v /data`, an image-declared VOLUME, a source-less `--mount type=volume`).
// The daemon gave each of those a fresh, empty volume and the old one, still
// holding the data, was left dangling for the next volume prune.
//
// anonvolDocker wraps the ohiy fake with the daemon's create-time mount
// allocation: a bind or a mount with a source is used as named, every other
// volume mount point (HostConfig.Mounts without a source, Config.Volumes, the
// image's VOLUMEs) gets a fresh volume, and two mounts at one destination are
// refused, as the daemon refuses them. Destinations are compared path.Clean'ed,
// as the daemon's Linux mount parser cleans them, and a one-part bind ("/data")
// is an anonymous volume, as that parser reads it.
type anonvolDocker struct {
	*ohiyDocker

	mu      sync.Mutex
	mounts  map[string][]container.MountPoint // by container id
	hosts   []container.HostConfig            // what each ContainerCreate received
	removes []container.RemoveOptions         // every ContainerRemove's options
	fresh   int
}

// Volume names of the old container's mounts.
const (
	anonData   = "anon0000data"    // -v /data
	anonImg    = "anon0000imgdata" // the image's VOLUME /imgdata
	anonMnt2   = "anon0000mnt2"    // --mount type=volume,target=/mnt2
	namedBind  = "named"           // -v named:/named
	namedMount = "named2"          // --mount type=volume,source=named2,target=/named2
	anonRemote = "anon0000remote"  // -v /remote with --volume-driver=example-driver
)

// newAnonvolDockerWith holds one running container with the given HostConfig
// binds, mounts and volumes-from, and the mount points the daemon reports for it.
func newAnonvolDockerWith(t *testing.T, own container.Config, binds []string, mounts []mount.Mount, volumesFrom []string, points []container.MountPoint) *anonvolDocker {
	t.Helper()
	d := newOhiyDocker(t, own)
	old := d.containers[ohiyOldID]
	old.host.Binds = binds
	old.host.Mounts = mounts
	old.host.VolumesFrom = volumesFrom
	return &anonvolDocker{ohiyDocker: d, mounts: map[string][]container.MountPoint{ohiyOldID: points}}
}

func newAnonvolDocker(t *testing.T) *anonvolDocker {
	t.Helper()
	d := newOhiyDocker(t, container.Config{
		Volumes: map[string]struct{}{"/data": {}, "/imgdata": {}, "/remote": {}},
	})
	// Both image versions declare VOLUME /imgdata, so withoutImageDefaults drops
	// it from the new Config and the new image's VOLUME is what allocates it.
	for id, img := range d.images {
		img.Config.Volumes = map[string]struct{}{"/imgdata": {}}
		d.images[id] = img
	}
	old := d.containers[ohiyOldID]
	old.host.Binds = []string{namedBind + ":/named", "/host/dir:/hostbind:ro"}
	old.host.Mounts = []mount.Mount{
		{Type: mount.TypeVolume, Target: "/mnt2", VolumeOptions: &mount.VolumeOptions{Labels: map[string]string{"w2-3mox": "1"}}},
		{Type: mount.TypeVolume, Source: namedMount, Target: "/named2"},
		{Type: mount.TypeTmpfs, Target: "/scratch"},
	}
	return &anonvolDocker{
		ohiyDocker: d,
		mounts: map[string][]container.MountPoint{ohiyOldID: {
			{Type: mount.TypeVolume, Name: anonData, Destination: "/data", Driver: "local", RW: true},
			{Type: mount.TypeVolume, Name: anonImg, Destination: "/imgdata", Driver: "local", RW: true},
			{Type: mount.TypeVolume, Name: anonMnt2, Destination: "/mnt2", Driver: "local", RW: true},
			{Type: mount.TypeVolume, Name: anonRemote, Destination: "/remote", Driver: "example-driver", RW: true},
			{Type: mount.TypeVolume, Name: namedBind, Destination: "/named", Driver: "local", RW: true},
			{Type: mount.TypeBind, Source: "/host/dir", Destination: "/hostbind"},
			{Type: mount.TypeVolume, Name: namedMount, Destination: "/named2", Driver: "local", RW: true},
			{Type: mount.TypeTmpfs, Destination: "/scratch", RW: true},
			// A bind and a tmpfs that no Binds or Mounts entry names (the daemon
			// reports e.g. --tmpfs and runtime mounts this way): never turned
			// into a volume mount.
			{Type: mount.TypeTmpfs, Destination: "/tmpfs-only", RW: true},
			{Type: mount.TypeBind, Source: "/host/y", Destination: "/bind-only", RW: true},
		}},
	}
}

func (w *anonvolDocker) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	r, err := w.ohiyDocker.ContainerInspect(ctx, id)
	if err != nil {
		return r, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	r.Mounts = append([]container.MountPoint(nil), w.mounts[id]...)
	return r, nil
}

func (w *anonvolDocker) ContainerCreate(ctx context.Context, cfg *container.Config, host *container.HostConfig, net *dockernet.NetworkingConfig, p *ocispec.Platform, name string) (container.CreateResponse, error) {
	if host != nil {
		w.mu.Lock()
		w.hosts = append(w.hosts, *host)
		w.mu.Unlock()
	}
	w.ohiyDocker.mu.Lock()
	imageID := cfg.Image
	if imageID == ohiyRef {
		imageID = w.tag
	}
	var imgVolumes map[string]struct{}
	if ic := w.images[imageID].Config; ic != nil {
		imgVolumes = ic.Volumes
	}
	w.ohiyDocker.mu.Unlock()

	mps, err := w.allocate(cfg, host, imgVolumes)
	if err != nil {
		return container.CreateResponse{}, err
	}
	resp, err := w.ohiyDocker.ContainerCreate(ctx, cfg, host, net, p, name)
	if err != nil {
		return resp, err
	}
	w.mu.Lock()
	w.mounts[resp.ID] = mps
	w.mu.Unlock()
	return resp, nil
}

func (w *anonvolDocker) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	w.mu.Lock()
	w.removes = append(w.removes, opts)
	w.mu.Unlock()
	return w.ohiyDocker.ContainerRemove(ctx, id, opts)
}

// allocate is the daemon's create-time mount allocation.
func (w *anonvolDocker) allocate(cfg *container.Config, host *container.HostConfig, imgVolumes map[string]struct{}) ([]container.MountPoint, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []container.MountPoint
	taken := map[string]bool{}
	add := func(mp container.MountPoint) error {
		if taken[mp.Destination] {
			return fmt.Errorf("anonvol fake: Duplicate mount point: %s", mp.Destination)
		}
		taken[mp.Destination] = true
		out = append(out, mp)
		return nil
	}
	freshName := func() string {
		w.fresh++
		return fmt.Sprintf("fresh%04d", w.fresh)
	}
	if host != nil {
		for _, b := range host.Binds {
			parts := strings.Split(b, ":")
			var mp container.MountPoint
			switch {
			case len(parts) == 1:
				mp = container.MountPoint{Type: mount.TypeVolume, Name: freshName(), Destination: path.Clean(parts[0]), RW: true}
			case strings.HasPrefix(parts[0], "/"):
				mp = container.MountPoint{Type: mount.TypeBind, Source: parts[0], Destination: path.Clean(parts[1])}
			default:
				mp = container.MountPoint{Type: mount.TypeVolume, Name: parts[0], Destination: path.Clean(parts[1]), RW: true}
			}
			if err := add(mp); err != nil {
				return nil, err
			}
		}
		for _, m := range host.Mounts {
			mp := container.MountPoint{Type: m.Type, Destination: path.Clean(m.Target), RW: !m.ReadOnly}
			switch m.Type {
			case mount.TypeVolume:
				mp.Name = m.Source
				if mp.Name == "" {
					mp.Name = freshName()
				}
			case mount.TypeBind:
				mp.Source = m.Source
			}
			if err := add(mp); err != nil {
				return nil, err
			}
		}
	}
	for _, vols := range []map[string]struct{}{cfg.Volumes, imgVolumes} {
		for spec := range vols {
			dest := path.Clean(spec)
			if taken[dest] {
				continue // a mount at this destination takes precedence
			}
			taken[dest] = true
			out = append(out, container.MountPoint{Type: mount.TypeVolume, Name: freshName(), Destination: dest, RW: true})
		}
	}
	return out, nil
}

func (w *anonvolDocker) lastHost(t *testing.T) container.HostConfig {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	require.NotEmpty(t, w.hosts, "ContainerCreate was called")
	return w.hosts[len(w.hosts)-1]
}

func (w *anonvolDocker) mountsOf(id string) map[string]container.MountPoint {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]container.MountPoint{}
	for _, mp := range w.mounts[id] {
		out[mp.Destination] = mp
	}
	return out
}

// assertCarried checks one create's HostConfig and the container it produced.
func assertCarried(t *testing.T, w *anonvolDocker, host container.HostConfig, newID string) {
	t.Helper()

	// Binds and the user's own mounts reach the create unchanged.
	assert.Equal(t, []string{namedBind + ":/named", "/host/dir:/hostbind:ro"}, host.Binds)

	byTarget := map[string]mount.Mount{}
	for _, m := range host.Mounts {
		_, dup := byTarget[m.Target]
		assert.False(t, dup, "one mount per destination, %s twice: %+v", m.Target, host.Mounts)
		byTarget[m.Target] = m
	}
	for _, b := range host.Binds {
		dest := strings.Split(b, ":")[1]
		_, dup := byTarget[dest]
		assert.False(t, dup, "a mount repeats the bind's destination %s: %+v", dest, host.Mounts)
	}

	for target, name := range map[string]string{"/data": anonData, "/imgdata": anonImg, "/mnt2": anonMnt2} {
		m, ok := byTarget[target]
		if assert.True(t, ok, "the old anonymous volume at %s reaches the create: %+v", target, host.Mounts) {
			assert.Equal(t, mount.TypeVolume, m.Type, target)
			assert.Equal(t, name, m.Source, "%s is mounted from the old volume", target)
			assert.False(t, m.ReadOnly, target)
		}
	}
	if m, ok := byTarget["/mnt2"]; ok && assert.NotNil(t, m.VolumeOptions) {
		assert.Equal(t, map[string]string{"w2-3mox": "1"}, m.VolumeOptions.Labels, "the --mount keeps its own options")
	}
	if m, ok := byTarget["/remote"]; assert.True(t, ok, "the volume on a plugin driver is carried: %+v", host.Mounts) {
		assert.Equal(t, mount.Mount{Type: mount.TypeVolume, Source: anonRemote, Target: "/remote",
			VolumeOptions: &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: "example-driver"}}}, m,
			"a volume on another driver is looked up under that driver")
	}
	assert.Equal(t, mount.Mount{Type: mount.TypeVolume, Source: namedMount, Target: "/named2"}, byTarget["/named2"])
	assert.Equal(t, mount.Mount{Type: mount.TypeTmpfs, Target: "/scratch"}, byTarget["/scratch"])
	assert.NotContains(t, byTarget, "/tmpfs-only", "a tmpfs is never turned into a volume mount")
	assert.NotContains(t, byTarget, "/bind-only", "a bind is never turned into a volume mount")
	assert.NotContains(t, byTarget, "/named", "a named bind is not repeated as a mount")
	assert.NotContains(t, byTarget, "/hostbind", "a host bind is not repeated as a mount")

	// What the daemon made of it: every volume is the old one.
	got := w.mountsOf(newID)
	for dest, name := range map[string]string{
		"/data": anonData, "/imgdata": anonImg, "/mnt2": anonMnt2, "/remote": anonRemote, "/named": namedBind, "/named2": namedMount,
	} {
		assert.Equal(t, name, got[dest].Name, "the new container's volume at %s", dest)
	}
}

func (w *anonvolDocker) assertNoRemoveDeletesVolumes(t *testing.T) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	require.NotEmpty(t, w.removes, "the update removed a container")
	for i, opts := range w.removes {
		assert.False(t, opts.RemoveVolumes, "ContainerRemove #%d must not delete volumes: %+v", i, opts)
	}
}

// TestStandaloneUpdateCarriesAnonymousVolumesOhiy: every anonymous volume of
// the old container reaches the new one by name at the same path, on the first
// update and again on the next; binds, named volumes, tmpfs and bind mounts are
// left as they were.
func TestStandaloneUpdateCarriesAnonymousVolumesOhiy(t *testing.T) {
	for _, entry := range ohiyEntries() {
		t.Run(entry.name, func(t *testing.T) {
			w := newAnonvolDocker(t)
			s := &DockerService{updateClient: w}

			ar := entry.run(context.Background(), s, func(LogLine) {})
			require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
			first := w.snapshot()[ohiyOldName]
			require.NotEqual(t, ohiyOldID, first.id, "the container was recreated")
			assertCarried(t, w, w.lastHost(t), first.id)

			// The next update starts from the container this one made, whose
			// HostConfig now names the volumes: still one mount per path.
			_, ar = s.UpdateContainer(context.Background(), first.id, nil)
			require.Equal(t, truth.OutcomeNoChange, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
			second := w.snapshot()[ohiyOldName]
			require.NotEqual(t, first.id, second.id, "the container was recreated again")
			assertCarried(t, w, w.lastHost(t), second.id)

			w.assertNoRemoveDeletesVolumes(t)
		})
	}
}

// TestStandaloneUpdateAnonymousVolumeRollbackKeepsVolumesOhiy: on a failed
// create or start the rollback removes the new container without its volumes,
// so the carried volumes, which the old container still uses, survive it. A pin
// as much as a test: no remove has ever set RemoveVolumes, and none may.
func TestStandaloneUpdateAnonymousVolumeRollbackKeepsVolumesOhiy(t *testing.T) {
	cases := []struct {
		name  string
		setup func(d *ohiyDocker)
	}{
		{"create fails", func(d *ohiyDocker) { d.createErr = errOhiyDaemon }},
		{"start fails", func(d *ohiyDocker) { d.startNewErr = errOhiyDaemon }},
	}
	for _, entry := range ohiyEntries() {
		for _, tc := range cases {
			t.Run(entry.name+"/"+tc.name, func(t *testing.T) {
				w := newAnonvolDocker(t)
				tc.setup(w.ohiyDocker)

				ar := entry.run(context.Background(), &DockerService{updateClient: w}, func(LogLine) {})
				require.Equal(t, truth.OutcomeFailed, ar.Outcome)
				require.True(t, errors.Is(ar.Err, errOhiyDaemon), "%v", ar.Err)

				got := w.snapshot()
				require.Len(t, got, 1, "only the old container remains: %v", got)
				assert.Equal(t, ohiyOldID, got[ohiyOldName].id)
				assert.Equal(t, anonData, w.mountsOf(ohiyOldID)["/data"].Name, "the old container keeps its volume")

				if tc.name == "start fails" {
					w.assertNoRemoveDeletesVolumes(t)
				}
			})
		}
	}
}

// TestStandaloneUpdateAnonymousVolumeDestinationShapesOhiy: destination shapes
// the daemon accepts but stores differently from how it reports them. A
// HostConfig keeps a destination as typed ("/pg/") while the mount point
// reports it cleaned ("/pg"), and the API accepts a one-part bind ("/single")
// as an anonymous volume. Each must be recognised as the mount it already is,
// or the carry adds a second mount at the same path and every update is refused
// with "Duplicate mount point", including for containers with only named
// volumes. Each runs two updates: the second starts from what the first made.
func TestStandaloneUpdateAnonymousVolumeDestinationShapesOhiy(t *testing.T) {
	cases := []struct {
		name        string
		binds       []string
		mounts      []mount.Mount
		volumesFrom []string
		points      []container.MountPoint
		want        map[string]string // destination -> volume name after each update
		check       func(t *testing.T, first container.HostConfig)
	}{
		{
			name:   "named -v with a trailing slash",
			binds:  []string{"pgdata:/var/lib/postgresql/data/"},
			points: []container.MountPoint{{Type: mount.TypeVolume, Name: "pgdata", Destination: "/var/lib/postgresql/data", Driver: "local", RW: true}},
			want:   map[string]string{"/var/lib/postgresql/data": "pgdata"},
			check: func(t *testing.T, h container.HostConfig) {
				assert.Equal(t, []string{"pgdata:/var/lib/postgresql/data/"}, h.Binds)
				assert.Empty(t, h.Mounts, "a named volume is not carried a second time")
			},
		},
		{
			name:   "named --mount with a trailing slash",
			mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "n", Target: "/n/"}},
			points: []container.MountPoint{{Type: mount.TypeVolume, Name: "n", Destination: "/n", Driver: "local", RW: true}},
			want:   map[string]string{"/n": "n"},
			check: func(t *testing.T, h container.HostConfig) {
				assert.Equal(t, []mount.Mount{{Type: mount.TypeVolume, Source: "n", Target: "/n/"}}, h.Mounts)
			},
		},
		{
			name:   "anonymous --mount with a trailing slash",
			mounts: []mount.Mount{{Type: mount.TypeVolume, Target: "/mnt3/", VolumeOptions: &mount.VolumeOptions{Labels: map[string]string{"k": "v"}}}},
			points: []container.MountPoint{{Type: mount.TypeVolume, Name: "anon0000mnt3", Destination: "/mnt3", Driver: "local", RW: true}},
			want:   map[string]string{"/mnt3": "anon0000mnt3"},
			check: func(t *testing.T, h container.HostConfig) {
				assert.Equal(t, []mount.Mount{{Type: mount.TypeVolume, Source: "anon0000mnt3", Target: "/mnt3/",
					VolumeOptions: &mount.VolumeOptions{Labels: map[string]string{"k": "v"}}}}, h.Mounts,
					"the user's own mount gets the source; no second mount")
			},
		},
		{
			name:   "one-part API bind",
			binds:  []string{"/single"},
			points: []container.MountPoint{{Type: mount.TypeVolume, Name: "anon0000single", Destination: "/single", Driver: "local", RW: true}},
			want:   map[string]string{"/single": "anon0000single"},
			check: func(t *testing.T, h container.HostConfig) {
				assert.Equal(t, []string{"anon0000single:/single"}, h.Binds, "the bind names the old volume")
				assert.Empty(t, h.Mounts, "carried through the bind, not a second mount")
			},
		},
		{
			name:        "read-only volume from --volumes-from",
			volumesFrom: []string{"other:ro"},
			points:      []container.MountPoint{{Type: mount.TypeVolume, Name: "anon0000ro", Destination: "/ro", Driver: "local", RW: false}},
			want:        map[string]string{"/ro": "anon0000ro"},
			check: func(t *testing.T, h container.HostConfig) {
				assert.Equal(t, []mount.Mount{{Type: mount.TypeVolume, Source: "anon0000ro", Target: "/ro", ReadOnly: true}}, h.Mounts,
					"a read-only volume stays read-only")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newAnonvolDockerWith(t, container.Config{}, tc.binds, tc.mounts, tc.volumesFrom, tc.points)
			s := &DockerService{updateClient: w}

			id := ohiyOldID
			for round := 1; round <= 2; round++ {
				_, ar := s.UpdateContainer(context.Background(), id, nil)
				require.NotEqual(t, truth.OutcomeFailed, ar.Outcome, "update %d: reason %q err %v", round, ar.Reason, ar.Err)
				c := w.snapshot()[ohiyOldName]
				require.NotEqual(t, id, c.id, "update %d recreated the container", round)
				id = c.id
				if round == 1 {
					tc.check(t, w.lastHost(t))
				}
				got := w.mountsOf(id)
				for dest, name := range tc.want {
					assert.Equal(t, name, got[dest].Name, "update %d: the volume at %s", round, dest)
				}
				if dest := "/ro"; tc.want[dest] != "" {
					assert.False(t, got[dest].RW, "update %d: %s stays read-only", round, dest)
				}
			}
			w.assertNoRemoveDeletesVolumes(t)
		})
	}
}
