package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"slices"
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
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-ohiy. A standalone update recreated the container from the OLD
// container's inspected Config, which already holds the old image's Env, Cmd,
// Entrypoint and labels merged in, so a new image version's changed defaults
// never applied. And it removed the old container before the new one was
// created, so a failed create or start left no container at all.
//
// ohiyDocker is a small in-memory daemon: containers by id with unique names, an
// image tag that moves on pull, and Docker's create-time merge of the image's
// defaults under the container's Config. Every call fails with ctx's error once
// ctx has ended, as the real client does.

const (
	ohiyRef     = "example/app:latest"
	ohiyOldImg  = "sha256:ohiy-old-image"
	ohiyNewImg  = "sha256:ohiy-new-image"
	ohiyOldID   = "ohiy0000old0000container"
	ohiyOldName = "app"
)

var errOhiyDaemon = errors.New("ohiy fake: daemon refused")

type ohiyCtr struct {
	id      string
	name    string
	image   string
	config  container.Config
	host    container.HostConfig
	running bool
	restart container.RestartPolicyMode
}

type ohiyDocker struct {
	mu         sync.Mutex
	containers map[string]*ohiyCtr
	images     map[string]image.InspectResponse
	tag        string // the image id ohiyRef points at
	created    int

	createErr      error              // ContainerCreate fails
	startNewErr    error              // ContainerStart fails for a container the update created
	cancelOnNew    context.CancelFunc // called when the new container is started, before failing
	cancelAfterNew context.CancelFunc // called when the new container is started, which then SUCCEEDS
	removeOldErr   error              // ContainerRemove fails for the original container
	removeNewErr   error              // ContainerRemove fails for a container the update created
	renameAsideErr error              // ContainerRename of the original to a different name fails
	renameBackErr  error              // ContainerRename of the original back to its own name fails
	oldImageErr    error              // ImageInspect of the old image fails
	updateErr      error              // ContainerUpdate fails
	startNewRuns   bool               // the new container's start fails client-side AFTER the daemon started it
	pruneOldOnNew  bool               // a prune removes the set-aside original once the new container has started

	createdConfigs []container.Config // the Config each ContainerCreate received, before the merge
}

// ohiyImages builds the two image versions from JSON, the shape the daemon
// returns, so the test needs no import of the image-spec module (go.mod lists
// it as indirect).
func ohiyImages() map[string]image.InspectResponse {
	cfg := func(entry, cmd, mode, version, wd string) image.InspectResponse {
		var r image.InspectResponse
		raw := fmt.Sprintf(`{"Config":{"Entrypoint":[%q],"Cmd":[%q],"Env":["PATH=/usr/bin","APP_MODE=%s"],"Labels":{"org.example.version":%q},"WorkingDir":%q}}`,
			entry, cmd, mode, version, wd)
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			panic(err)
		}
		return r
	}
	old := cfg("/old-entrypoint", "old-cmd", "old", "1", "/srv/old")
	// The old image EXPOSEs two ports; the new one EXPOSEs none.
	if err := json.Unmarshal([]byte(`{"80/tcp":{},"9000/tcp":{}}`), &old.Config.ExposedPorts); err != nil {
		panic(err)
	}
	return map[string]image.InspectResponse{
		ohiyOldImg: old,
		ohiyNewImg: cfg("/new-entrypoint", "new-cmd", "new", "2", "/srv/new"),
	}
}

// newOhiyDocker holds one running container created from the old image. own
// is the container's own Config, before the image's defaults are merged in.
func newOhiyDocker(t *testing.T, own container.Config) *ohiyDocker {
	t.Helper()
	d := &ohiyDocker{containers: map[string]*ohiyCtr{}, images: ohiyImages(), tag: ohiyOldImg}
	own.Image = ohiyRef
	d.containers[ohiyOldID] = &ohiyCtr{id: ohiyOldID, name: ohiyOldName, image: ohiyOldImg, config: d.merge(own, ohiyOldImg), running: true,
		restart: container.RestartPolicyAlways,
		host:    container.HostConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways}}}
	return d
}

// merge is the daemon's create-time merge: the image's defaults under the
// container's own settings.
func (d *ohiyDocker) merge(cfg container.Config, imageID string) container.Config {
	img := d.images[imageID].Config
	if len(cfg.Entrypoint) == 0 {
		cfg.Entrypoint = img.Entrypoint
		if len(cfg.Cmd) == 0 {
			cfg.Cmd = img.Cmd
		}
	}
	env := slices.Clone(cfg.Env)
	for _, e := range img.Env {
		key := strings.SplitN(e, "=", 2)[0]
		if !slices.ContainsFunc(env, func(have string) bool { return strings.SplitN(have, "=", 2)[0] == key }) {
			env = append(env, e)
		}
	}
	cfg.Env = env
	labels := map[string]string{}
	for k, v := range img.Labels {
		labels[k] = v
	}
	for k, v := range cfg.Labels {
		labels[k] = v
	}
	cfg.Labels = labels
	if cfg.WorkingDir == "" {
		cfg.WorkingDir = img.WorkingDir
	}
	return cfg
}

func (d *ohiyDocker) byName(name string) *ohiyCtr {
	for _, c := range d.containers {
		if c.name == name {
			return c
		}
	}
	return nil
}

func ohiyNotFound(id string) error {
	return fmt.Errorf("No such container: %s: %w", id, cerrdefs.ErrNotFound)
}

func (d *ohiyDocker) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if err := ctx.Err(); err != nil {
		return container.InspectResponse{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[id]
	if !ok {
		return container.InspectResponse{}, ohiyNotFound(id)
	}
	cfg := c.config
	host := c.host
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID: c.id, Name: "/" + c.name, Image: c.image,
			State:      &container.State{Running: c.running},
			HostConfig: &host,
		},
		Config: &cfg,
	}, nil
}

func (d *ohiyDocker) ImageInspect(ctx context.Context, ref string, _ ...dockerclient.ImageInspectOption) (image.InspectResponse, error) {
	if err := ctx.Err(); err != nil {
		return image.InspectResponse{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	id := ref
	if ref == ohiyRef {
		id = d.tag
	}
	if id == ohiyOldImg && d.oldImageErr != nil {
		return image.InspectResponse{}, d.oldImageErr
	}
	img, ok := d.images[id]
	if !ok {
		return image.InspectResponse{}, fmt.Errorf("no such image %s: %w", ref, cerrdefs.ErrNotFound)
	}
	img.ID = id
	img.RepoDigests = []string{"example/app@" + id}
	return img, nil
}

func (d *ohiyDocker) ContainerList(context.Context, container.ListOptions) ([]container.Summary, error) {
	return nil, errors.New("ohiy fake: unexpected ContainerList")
}

func (d *ohiyDocker) ImagePull(ctx context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.tag = ohiyNewImg
	d.mu.Unlock()
	return io.NopCloser(strings.NewReader("")), nil
}

func (d *ohiyDocker) ContainerStop(ctx context.Context, id string, _ container.StopOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[id]
	if !ok {
		return ohiyNotFound(id)
	}
	c.running = false
	return nil
}

func (d *ohiyDocker) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[id]
	if !ok {
		return ohiyNotFound(id)
	}
	if id == ohiyOldID && d.removeOldErr != nil {
		return d.removeOldErr
	}
	if id != ohiyOldID && d.removeNewErr != nil {
		return d.removeNewErr
	}
	if c.running && !opts.Force {
		return errors.New("ohiy fake: cannot remove a running container")
	}
	delete(d.containers, id)
	return nil
}

func (d *ohiyDocker) ContainerRename(ctx context.Context, id, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[id]
	if !ok {
		return ohiyNotFound(id)
	}
	if id == ohiyOldID && name != ohiyOldName && d.renameAsideErr != nil {
		return d.renameAsideErr
	}
	if id == ohiyOldID && name == ohiyOldName && d.renameBackErr != nil {
		return d.renameBackErr
	}
	if other := d.byName(name); other != nil && other != c {
		return fmt.Errorf("ohiy fake: name %s is in use", name)
	}
	c.name = name
	return nil
}

func (d *ohiyDocker) ContainerCreate(ctx context.Context, cfg *container.Config, host *container.HostConfig, _ *dockernet.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	if err := ctx.Err(); err != nil {
		return container.CreateResponse{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.createdConfigs = append(d.createdConfigs, *cfg)
	if d.createErr != nil {
		return container.CreateResponse{}, d.createErr
	}
	if d.byName(name) != nil {
		return container.CreateResponse{}, fmt.Errorf("ohiy fake: Conflict. The container name %q is already in use", name)
	}
	imageID := cfg.Image
	if imageID == ohiyRef {
		imageID = d.tag
	}
	d.created++
	id := fmt.Sprintf("ohiy%04dnew0000container", d.created)
	c := &ohiyCtr{id: id, name: name, image: imageID, config: d.merge(*cfg, imageID)}
	if host != nil {
		c.host = *host
		c.restart = host.RestartPolicy.Name
	}
	d.containers[id] = c
	return container.CreateResponse{ID: id}, nil
}

func (d *ohiyDocker) ContainerStart(ctx context.Context, id string, _ container.StartOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[id]
	if !ok {
		return ohiyNotFound(id)
	}
	if id != ohiyOldID {
		if d.cancelOnNew != nil {
			d.cancelOnNew()
			return context.Canceled
		}
		if d.cancelAfterNew != nil {
			d.cancelAfterNew()
		}
		if d.startNewRuns {
			c.running = true
			return errors.New("ohiy fake: start request failed after the daemon started it")
		}
		if d.pruneOldOnNew {
			delete(d.containers, ohiyOldID)
		}
		if d.startNewErr != nil {
			return d.startNewErr
		}
	}
	c.running = true
	return nil
}

func (d *ohiyDocker) ContainerUpdate(ctx context.Context, id string, upd container.UpdateConfig) (container.UpdateResponse, error) {
	if err := ctx.Err(); err != nil {
		return container.UpdateResponse{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[id]
	if !ok {
		return container.UpdateResponse{}, ohiyNotFound(id)
	}
	if d.updateErr != nil {
		return container.UpdateResponse{}, d.updateErr
	}
	c.restart = upd.RestartPolicy.Name
	return container.UpdateResponse{}, nil
}

func (d *ohiyDocker) snapshot() map[string]ohiyCtr {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]ohiyCtr{}
	for _, c := range d.containers {
		out[c.name] = *c
	}
	return out
}

// ohiyEntry is one of the two public standalone update paths.
type ohiyEntry struct {
	name string
	run  func(ctx context.Context, s *DockerService, emit func(LogLine)) truth.ActionResult
}

func ohiyEntries() []ohiyEntry {
	return []ohiyEntry{
		{"UpdateContainer", func(ctx context.Context, s *DockerService, _ func(LogLine)) truth.ActionResult {
			_, ar := s.UpdateContainer(ctx, ohiyOldID, nil)
			return ar
		}},
		{"UpdateContainerStreaming", func(ctx context.Context, s *DockerService, emit func(LogLine)) truth.ActionResult {
			_, ar := s.UpdateContainerStreaming(ctx, ohiyOldID, nil, emit, func(Status) {})
			return ar
		}},
	}
}

func ohiyRun(t *testing.T, entry ohiyEntry, d *ohiyDocker, ctx context.Context) (truth.ActionResult, []string) {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	ar := entry.run(ctx, &DockerService{updateClient: d}, func(l LogLine) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, l.Text)
	})
	return ar, lines
}

// TestStandaloneUpdateAppliesNewImageDefaultsOhiy: an image whose new version
// changes Entrypoint, Cmd, an Env default, a label and WorkingDir yields a
// container with the new values, while the user's own settings survive.
func TestStandaloneUpdateAppliesNewImageDefaultsOhiy(t *testing.T) {
	for _, entry := range ohiyEntries() {
		t.Run(entry.name, func(t *testing.T) {
			d := newOhiyDocker(t, container.Config{
				Env:    []string{"USER_SET=1"},
				Labels: map[string]string{"mine": "yes"},
			})

			ar, _ := ohiyRun(t, entry, d, context.Background())
			require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)

			got := d.snapshot()
			require.Len(t, got, 1, "exactly one container after the update: %v", got)
			c, ok := got[ohiyOldName]
			require.True(t, ok, "the container keeps its name")
			assert.True(t, c.running)
			assert.Equal(t, ohiyNewImg, c.image)
			assert.Equal(t, []string{"/new-entrypoint"}, []string(c.config.Entrypoint))
			assert.Equal(t, []string{"new-cmd"}, []string(c.config.Cmd))
			assert.Contains(t, c.config.Env, "APP_MODE=new")
			assert.NotContains(t, c.config.Env, "APP_MODE=old")
			assert.Contains(t, c.config.Env, "USER_SET=1", "the user's own variable is kept")
			assert.Equal(t, "2", c.config.Labels["org.example.version"])
			assert.Equal(t, "yes", c.config.Labels["mine"], "the user's own label is kept")
			assert.Equal(t, "/srv/new", c.config.WorkingDir)
		})
	}
}

// TestStandaloneUpdateKeepsCustomEntrypointAndItsCmdOhiy: a container that set
// its own Entrypoint keeps it and keeps its Cmd too, even when that Cmd equals
// the old image's default. Docker clears the image's Cmd beside a custom
// Entrypoint, so dropping the Cmd would leave the container with no command.
func TestStandaloneUpdateKeepsCustomEntrypointAndItsCmdOhiy(t *testing.T) {
	d := newOhiyDocker(t, container.Config{
		Entrypoint: []string{"/custom"},
		Cmd:        []string{"old-cmd"},
	})

	ar, _ := ohiyRun(t, ohiyEntries()[0], d, context.Background())
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)

	c := d.snapshot()[ohiyOldName]
	assert.Equal(t, []string{"/custom"}, []string(c.config.Entrypoint))
	assert.Equal(t, []string{"old-cmd"}, []string(c.config.Cmd))
}

// TestStandaloneUpdateRestoresOldContainerOnFailureOhiy: the old container is
// kept until the new one starts, and is back under its own name, running, when
// the create or the start fails, including when the update's own context has
// already ended at the failure.
func TestStandaloneUpdateRestoresOldContainerOnFailureOhiy(t *testing.T) {
	cases := []struct {
		name  string
		setup func(d *ohiyDocker, cancel context.CancelFunc)
	}{
		{"create fails", func(d *ohiyDocker, _ context.CancelFunc) { d.createErr = errOhiyDaemon }},
		{"start fails", func(d *ohiyDocker, _ context.CancelFunc) { d.startNewErr = errOhiyDaemon }},
		{"start fails because the update's context ended", func(d *ohiyDocker, cancel context.CancelFunc) { d.cancelOnNew = cancel }},
	}
	for _, entry := range ohiyEntries() {
		for _, tc := range cases {
			t.Run(entry.name+"/"+tc.name, func(t *testing.T) {
				d := newOhiyDocker(t, container.Config{})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				tc.setup(d, cancel)

				ar, _ := ohiyRun(t, entry, d, ctx)
				require.Equal(t, truth.OutcomeFailed, ar.Outcome)

				got := d.snapshot()
				require.Len(t, got, 1, "only the old container remains: %v", got)
				c, ok := got[ohiyOldName]
				require.True(t, ok, "the old container has its name back: %v", got)
				assert.Equal(t, ohiyOldID, c.id)
				assert.True(t, c.running, "the old container runs again")
				assert.Equal(t, ohiyOldImg, c.image)
			})
		}
	}
}

// TestStandaloneUpdateLeftoverOldContainerIsAWarningOhiy: when the new
// container runs but the old one cannot be removed, the update succeeded; the
// leftover is named in a warning, not turned into a failure.
func TestStandaloneUpdateLeftoverOldContainerIsAWarningOhiy(t *testing.T) {
	d := newOhiyDocker(t, container.Config{})
	d.removeOldErr = errOhiyDaemon

	ar, lines := ohiyRun(t, ohiyEntries()[1], d, context.Background())
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)

	got := d.snapshot()
	require.Len(t, got, 2)
	assert.True(t, got[ohiyOldName].running, "the new container runs under the name")
	leftover := ohiyOldName + "-capstan-old-" + ohiyOldID[:12]
	require.Contains(t, got, leftover)
	assert.False(t, got[leftover].running)
	assert.True(t, slices.ContainsFunc(lines, func(l string) bool {
		return strings.Contains(l, leftover) && strings.Contains(l, "could not be removed")
	}), "a warning names the leftover container: %q", lines)
}

// TestStandaloneUpdateIsOneFunctionOhiy: the streaming and non-streaming update
// paths used to be two near-copies, and the fix had to be made twice. Exactly
// one function in this package calls ContainerCreate. Read through go:embed
// (packageSources), so a `go test -overlay` mutant is what this sees.
func TestStandaloneUpdateIsOneFunctionOhiy(t *testing.T) {
	var callers []string
	fset := token.NewFileSet()
	err := fs.WalkDir(packageSources, ".", func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := packageSources.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ContainerCreate" {
					callers = append(callers, path+":"+fn.Name.Name)
				}
				return true
			})
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"docker_update.go:recreateStandaloneContainer"}, callers)
}

// ohiyRecreate drives recreateStandaloneContainer directly, from the fake's own
// inspect of the running original, so a test can end ctx after the new
// container started without the caller's post-update verification failing
// first on that same ctx.
func ohiyRecreate(t *testing.T, d *ohiyDocker, ctx context.Context) (string, []string, error) {
	t.Helper()
	inspect, err := d.ContainerInspect(context.Background(), ohiyOldID)
	require.NoError(t, err)
	var lines []string
	id, err := (&DockerService{updateClient: d}).recreateStandaloneContainer(ctx, inspect, true,
		func(l LogLine) { lines = append(lines, l.Text) }, func(Status) {})
	return id, lines, err
}

var ohiyLeftover = ohiyOldName + "-capstan-old-" + ohiyOldID[:12]

// TestStandaloneUpdateRemovesOldAfterLateDeadlineOhiy: the update's context
// ends right after the new container started. The old container is still
// removed: the removal runs on its own deadline, like the rollback.
func TestStandaloneUpdateRemovesOldAfterLateDeadlineOhiy(t *testing.T) {
	d := newOhiyDocker(t, container.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.cancelAfterNew = cancel

	id, _, err := ohiyRecreate(t, d, ctx)
	require.NoError(t, err)
	require.Error(t, ctx.Err(), "the fixture must have ended ctx before the removal")

	got := d.snapshot()
	require.Len(t, got, 1, "no leftover old container: %v", got)
	assert.Equal(t, id, got[ohiyOldName].id)
	assert.True(t, got[ohiyOldName].running)
}

// TestStandaloneUpdateLeftoverStaysStoppedOhiy: when the old container cannot
// be removed, its restart policy is set to "no", so a Docker daemon restart
// cannot start it beside its replacement; the warning says which happened.
func TestStandaloneUpdateLeftoverStaysStoppedOhiy(t *testing.T) {
	t.Run("restart policy cleared", func(t *testing.T) {
		d := newOhiyDocker(t, container.Config{})
		d.removeOldErr = errOhiyDaemon

		_, lines, err := ohiyRecreate(t, d, context.Background())
		require.NoError(t, err)

		got := d.snapshot()
		require.Contains(t, got, ohiyLeftover)
		assert.Equal(t, container.RestartPolicyDisabled, got[ohiyLeftover].restart)
		assert.Equal(t, container.RestartPolicyAlways, got[ohiyOldName].restart, "the new container keeps the user's policy")
		assert.True(t, slices.ContainsFunc(lines, func(l string) bool {
			return strings.Contains(l, ohiyLeftover) && strings.Contains(l, `restart policy is now "no"`)
		}), "%q", lines)
	})

	t.Run("restart policy could not be cleared", func(t *testing.T) {
		d := newOhiyDocker(t, container.Config{})
		d.removeOldErr = errOhiyDaemon
		d.updateErr = errOhiyDaemon

		_, lines, err := ohiyRecreate(t, d, context.Background())
		require.NoError(t, err, "the update still happened")

		assert.Equal(t, container.RestartPolicyAlways, d.snapshot()[ohiyLeftover].restart)
		assert.True(t, slices.ContainsFunc(lines, func(l string) bool {
			return strings.Contains(l, ohiyLeftover) && strings.Contains(l, "could not be cleared")
		}), "%q", lines)
	})
}

// TestStandaloneUpdateRollbackTriesEveryStepOhiy: a rollback step that fails
// does not stop the next one. The old container is started again even when
// the new one cannot be removed or the old name cannot be given back, and the
// error names every step that failed.
func TestStandaloneUpdateRollbackTriesEveryStepOhiy(t *testing.T) {
	t.Run("removing the new container fails", func(t *testing.T) {
		d := newOhiyDocker(t, container.Config{})
		d.startNewErr = errOhiyDaemon
		d.removeNewErr = errOhiyDaemon

		_, _, err := ohiyRecreate(t, d, context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rollback: removing the new container")
		assert.Contains(t, err.Error(), "rollback: renaming the old container back", "the new one still holds the name")

		got := d.snapshot()
		old, ok := got[ohiyLeftover]
		require.True(t, ok, "%v", got)
		assert.Equal(t, ohiyOldID, old.id)
		assert.True(t, old.running, "the old container serves again under its set-aside name")
	})

	t.Run("renaming the old container back fails", func(t *testing.T) {
		d := newOhiyDocker(t, container.Config{})
		d.startNewErr = errOhiyDaemon
		d.renameBackErr = errOhiyDaemon

		_, _, err := ohiyRecreate(t, d, context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rollback: renaming the old container back")
		assert.NotContains(t, err.Error(), "rollback: starting the old container")

		got := d.snapshot()
		require.Len(t, got, 1, "the new container was removed: %v", got)
		assert.True(t, got[ohiyLeftover].running)
	})
}

// TestStandaloneUpdateFailsBeforeTouchingOhiy: failures before the recreate
// leave the original as it was, running under its own name. Setting it aside
// failing restarts it (resumeOld); the old image being unreadable stops
// nothing at all.
func TestStandaloneUpdateFailsBeforeTouchingOhiy(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*ohiyDocker)
		wantErr string
	}{
		{"setting the old container aside fails", func(d *ohiyDocker) { d.renameAsideErr = errOhiyDaemon }, "renaming container"},
		{"the old image cannot be inspected", func(d *ohiyDocker) { d.oldImageErr = errOhiyDaemon }, "inspecting the current image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newOhiyDocker(t, container.Config{})
			tc.setup(d)

			_, _, err := ohiyRecreate(t, d, context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)

			got := d.snapshot()
			require.Len(t, got, 1, "%v", got)
			c := got[ohiyOldName]
			assert.Equal(t, ohiyOldID, c.id)
			assert.True(t, c.running, "the original runs again")
			assert.Empty(t, d.createdConfigs, "nothing was created")
		})
	}
}

// TestStandaloneUpdateKeepsPublishedPortsExposedOhiy: a port the container
// publishes stays in ExposedPorts even though the old image declared it and
// the new one does not; an exposed port nothing publishes follows the image.
func TestStandaloneUpdateKeepsPublishedPortsExposedOhiy(t *testing.T) {
	d := newOhiyDocker(t, container.Config{})
	var own container.Config
	require.NoError(t, json.Unmarshal([]byte(`{"ExposedPorts":{"80/tcp":{},"9000/tcp":{}}}`), &own))
	var host container.HostConfig
	require.NoError(t, json.Unmarshal([]byte(`{"PortBindings":{"80/tcp":[{"HostPort":"8080"}]}}`), &host))
	d.mu.Lock()
	d.containers[ohiyOldID].config.ExposedPorts = own.ExposedPorts
	d.containers[ohiyOldID].host = host
	d.mu.Unlock()

	_, _, err := ohiyRecreate(t, d, context.Background())
	require.NoError(t, err)

	require.Len(t, d.createdConfigs, 1)
	assert.Equal(t, []string{"80/tcp"}, ohiyKeys(d.createdConfigs[0].ExposedPorts),
		"80/tcp is published, so kept; 9000/tcp is the old image's and unpublished, so dropped")
}

// ohiyKeys lists a map's keys as strings, sorted; the port type lives in a
// module go.mod lists as indirect, so the test does not name it.
func ohiyKeys[K ~string, V any](m map[K]V) []string {
	var out []string
	for k := range m {
		out = append(out, string(k))
	}
	slices.Sort(out)
	return out
}

// TestStandaloneUpdateNeverRunsBothOhiy: the new container's start failed on
// the client side after the daemon had started it, and the rollback cannot
// remove it. The old container is NOT started beside it (two copies on the
// same volumes); the error says a person has to act. The other side, same
// fixture: when the new container is not running, the old one is started.
func TestStandaloneUpdateNeverRunsBothOhiy(t *testing.T) {
	t.Run("new container running and not removable", func(t *testing.T) {
		d := newOhiyDocker(t, container.Config{})
		d.startNewRuns = true
		d.removeNewErr = errOhiyDaemon

		_, _, err := ohiyRecreate(t, d, context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "manual action needed")

		got := d.snapshot()
		assert.True(t, got[ohiyOldName].running, "the new container still runs under the name")
		assert.False(t, got[ohiyLeftover].running, "the old container must not run beside it")
	})

	t.Run("new container stopped and not removable", func(t *testing.T) {
		d := newOhiyDocker(t, container.Config{})
		d.startNewErr = errOhiyDaemon
		d.removeNewErr = errOhiyDaemon

		_, _, err := ohiyRecreate(t, d, context.Background())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "manual action needed")
		assert.True(t, d.snapshot()[ohiyLeftover].running, "the old container serves again")
	})
}

// TestStandaloneUpdateOldAlreadyPrunedIsNotALeftoverOhiy: a prune removed the
// set-aside original before the final removal. That is success, not a
// leftover: no warning, no restart-policy change attempted.
func TestStandaloneUpdateOldAlreadyPrunedIsNotALeftoverOhiy(t *testing.T) {
	d := newOhiyDocker(t, container.Config{})
	d.pruneOldOnNew = true

	_, lines, err := ohiyRecreate(t, d, context.Background())
	require.NoError(t, err)

	require.Len(t, d.snapshot(), 1)
	assert.False(t, slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "could not be removed") }),
		"no leftover warning for a container that is gone: %q", lines)
	assert.Contains(t, lines, "Old container already removed")
}
