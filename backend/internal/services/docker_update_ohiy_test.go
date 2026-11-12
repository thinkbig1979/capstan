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
	running bool
}

type ohiyDocker struct {
	mu         sync.Mutex
	containers map[string]*ohiyCtr
	images     map[string]image.InspectResponse
	tag        string // the image id ohiyRef points at
	created    int

	createErr    error              // ContainerCreate fails
	startNewErr  error              // ContainerStart fails for a container the update created
	cancelOnNew  context.CancelFunc // called when the new container is started, before failing
	removeOldErr error              // ContainerRemove fails for the original container
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
	return map[string]image.InspectResponse{
		ohiyOldImg: cfg("/old-entrypoint", "old-cmd", "old", "1", "/srv/old"),
		ohiyNewImg: cfg("/new-entrypoint", "new-cmd", "new", "2", "/srv/new"),
	}
}

// newOhiyDocker holds one running container created from the old image. own
// is the container's own Config, before the image's defaults are merged in.
func newOhiyDocker(t *testing.T, own container.Config) *ohiyDocker {
	t.Helper()
	d := &ohiyDocker{containers: map[string]*ohiyCtr{}, images: ohiyImages(), tag: ohiyOldImg}
	own.Image = ohiyRef
	d.containers[ohiyOldID] = &ohiyCtr{id: ohiyOldID, name: ohiyOldName, image: ohiyOldImg, config: d.merge(own, ohiyOldImg), running: true}
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
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID: c.id, Name: "/" + c.name, Image: c.image,
			State: &container.State{Running: c.running},
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
	if other := d.byName(name); other != nil && other != c {
		return fmt.Errorf("ohiy fake: name %s is in use", name)
	}
	c.name = name
	return nil
}

func (d *ohiyDocker) ContainerCreate(ctx context.Context, cfg *container.Config, _ *container.HostConfig, _ *dockernet.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	if err := ctx.Err(); err != nil {
		return container.CreateResponse{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
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
	d.containers[id] = &ohiyCtr{id: id, name: name, image: imageID, config: d.merge(*cfg, imageID)}
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
		if d.startNewErr != nil {
			return d.startNewErr
		}
	}
	c.running = true
	return nil
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
