package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"github.com/thinkbig1979/capstan/backend/internal/dockerenv"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// unresolvedMarker stands in for a value whose interpolation failed (a
// required variable with no value). A name carrying it is unknown, so the
// stack is withheld; the same failure anywhere else in the file (a service's
// environment, say) cannot change a name and is ignored. Angle brackets
// cannot occur in a Docker object name, so it never matches a real one.
const unresolvedMarker = "<capstan:unresolved>"

// stackReferences is what the stacks' compose files name, by kind. A stack
// owns more than its compose project label says: a volume or network declared
// external (or with an explicit name:), an image a downed stack will pull or
// rebuild on its next up, and a one-off container another service joins.
// None of those carry the label, so the label rule alone would list them for
// removal (agent-os-3rr3).
type stackReferences struct {
	volumes    map[string]bool
	networks   map[string]bool
	images     map[string]bool
	containers map[string]bool
	// withheld names the stacks whose compose file could not be resolved.
	// Such a stack could name any object, so while it is non-empty no image,
	// container, volume or network is a candidate.
	withheld []string
}

func newStackReferences() stackReferences {
	return stackReferences{
		volumes:    map[string]bool{},
		networks:   map[string]bool{},
		images:     map[string]bool{},
		containers: map[string]bool{},
	}
}

// stackReferenceResolver reads what every stack's compose file names. It never
// fails as a whole: a stack it cannot read is withheld instead.
type stackReferenceResolver func(ctx context.Context, stacks []models.Stack) stackReferences

// composeStackReferences resolves each stack's compose file the way Capstan
// runs it: `-f <ComposeFile> -p <ProjectName>` with the same --env-file
// arguments buildComposeArgs passes and the same child environment
// (composeEnvironment), every profile active. It parses in
// process with compose-go rather than running `docker compose config`, because
// it runs on every host summary fetch (one per hostSpaceSnapshotTTL) and a
// process per stack per fetch is not worth what it would add.
func composeStackReferences(globalEnvPath string) stackReferenceResolver {
	return func(ctx context.Context, stacks []models.Stack) stackReferences {
		refs := newStackReferences()
		for _, st := range stacks {
			if err := addComposeReferences(ctx, &refs, st, globalEnvPath); err != nil {
				slog.Warn("Could not resolve a stack's compose file; no image, container, volume or network is offered for removal until it can be read",
					"stack", st.ProjectName, "directory", st.Directory, "error", err)
				refs.withheld = append(refs.withheld, st.ProjectName)
			}
		}
		sort.Strings(refs.withheld)
		return refs
	}
}

func addComposeReferences(ctx context.Context, refs *stackReferences, st models.Stack, globalEnvPath string) error {
	if st.ComposeFile == "" || st.ProjectName == "" {
		return errors.New("the stack has no compose file or project name recorded")
	}
	composePath := stackRelativePath(st.Directory, st.ComposeFile)

	env, err := composeEnvironment(st, globalEnvPath)
	if err != nil {
		return err
	}

	project, err := loader.LoadWithContext(ctx, types.ConfigDetails{
		WorkingDir:  filepath.Dir(composePath),
		ConfigFiles: []types.ConfigFile{{Filename: composePath}},
		Environment: env,
	}, func(o *loader.Options) {
		o.SetProjectName(st.ProjectName, true)
		// Only names are read here. A service's env_file or label_file is
		// not needed for any of them, so a missing one must not withhold
		// the stack.
		o.SkipResolveEnvironment = true
		o.SkipResolveLabels = true
		o.SkipConsistencyCheck = true
		o.Interpolate.Substitute = tolerantSubstitute
	})
	if err != nil {
		return err
	}

	var named []string
	add := func(set map[string]bool, names ...string) {
		for _, n := range names {
			if n != "" {
				set[n] = true
				named = append(named, n)
			}
		}
	}

	// A top-level volume or network is stored under the name compose gives
	// it (<project>_<key>, its explicit name:, or the key when external).
	// The key itself is added too: listing more names only keeps more.
	for key, vol := range project.Volumes {
		add(refs.volumes, key, vol.Name, st.ProjectName+"_"+key)
	}
	for key, nw := range project.Networks {
		add(refs.networks, key, nw.Name, st.ProjectName+"_"+key)
	}

	addWorkload := func(name, containerName string, spec types.ContainerSpec, build *types.BuildConfig) {
		if spec.Image != "" {
			add(refs.images, normalizedImageRefs(spec.Image)...)
		} else if build != nil {
			// compose's default image name for a built service, in both the
			// v2 (-) and v1 (_) spellings: a local-only image the next up
			// would otherwise have to rebuild.
			add(refs.images, normalizedImageRefs(st.ProjectName+"-"+name)...)
			add(refs.images, normalizedImageRefs(st.ProjectName+"_"+name)...)
		}
		add(refs.containers, containerName)
		for _, mode := range []string{spec.NetworkMode, spec.Pid, spec.Ipc} {
			if c, ok := strings.CutPrefix(mode, "container:"); ok {
				add(refs.containers, c)
			}
		}
		for _, from := range spec.VolumesFrom {
			if c, ok := strings.CutPrefix(from, "container:"); ok {
				c, _, _ = strings.Cut(c, ":")
				add(refs.containers, c)
			}
		}
		for _, v := range spec.Volumes {
			switch v.Type {
			case types.VolumeTypeVolume:
				add(refs.volumes, v.Source)
			case types.VolumeTypeImage:
				add(refs.images, normalizedImageRefs(v.Source)...)
			}
		}
	}
	// AllServices and AllJobs include the ones a profile disables: an
	// operator can start those with --profile at any time.
	for name, svc := range project.AllServices() {
		addWorkload(name, svc.ContainerName, svc.ContainerSpec, svc.Build)
	}
	for name, job := range project.AllJobs() {
		addWorkload(name, "", job.ContainerSpec, job.Build)
	}

	for _, n := range named {
		if strings.Contains(n, unresolvedMarker) {
			return fmt.Errorf("a name in %s needs a variable that has no value", composePath)
		}
	}
	return nil
}

// tolerantSubstitute is compose's own substitution with two changes: a
// required variable with no value yields unresolvedMarker instead of failing
// the whole file, and unset variables are not logged on every fetch.
func tolerantSubstitute(value string, mapping template.Mapping) (string, error) {
	out, err := template.SubstituteWithOptions(value, mapping, template.WithoutLogging)
	if err != nil {
		return unresolvedMarker, nil
	}
	return out, nil
}

// composeEnvironment is the interpolation environment `docker compose` sees
// when Capstan runs it: the --env-file arguments buildComposeArgs passes
// (global.env when it exists, then the stack's EnvFile), or the project
// directory's .env when there are none, overridden by the compose child's
// own environment. That child gets dockerenv.Env() (exec_env.go dockerEnv),
// not Capstan's process environment, so only those variables override a
// file: a TAG set in Capstan's own env never reaches compose and must not
// change a name here either. A file compose would fail to read fails here
// too.
func composeEnvironment(st models.Stack, globalEnvPath string) (map[string]string, error) {
	var files []string
	if globalEnvPath != "" {
		if _, err := os.Stat(globalEnvPath); err == nil {
			files = append(files, globalEnvPath)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("checking the global env file: %w", err)
		}
	}
	if st.EnvFile != "" {
		files = append(files, stackRelativePath(st.Directory, st.EnvFile))
	}
	if len(files) == 0 && st.Directory != "" {
		dotEnv := filepath.Join(st.Directory, ".env")
		if _, err := os.Stat(dotEnv); err == nil {
			files = append(files, dotEnv)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("checking the stack's .env: %w", err)
		}
	}

	processEnv := map[string]string{}
	for _, kv := range dockerenv.Env() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			processEnv[k] = v
		}
	}
	env, err := dotenv.GetEnvFromFile(processEnv, files)
	if err != nil {
		return nil, err
	}
	for k, v := range processEnv {
		env[k] = v
	}
	return env, nil
}

func stackRelativePath(dir, p string) string {
	if filepath.IsAbs(p) || dir == "" {
		return p
	}
	return filepath.Join(dir, p)
}

// normalizedImageRefs returns the spellings under which Docker may list the
// image ref names: its familiar form (docker.io/library/ dropped, :latest
// added when untagged) and, for a digest-pinned ref, the name@digest form
// Docker keeps in RepoDigests.
func normalizedImageRefs(ref string) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	name, digest, hasDigest := strings.Cut(ref, "@")
	tag := ""
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name, tag = name[:i], name[i+1:]
	}
	for _, prefix := range []string{"docker.io/", "index.docker.io/", "registry-1.docker.io/"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			name = rest
			break
		}
	}
	if rest, ok := strings.CutPrefix(name, "library/"); ok && !strings.Contains(rest, "/") {
		name = rest
	}

	var out []string
	if hasDigest {
		out = append(out, name+"@"+digest)
	}
	if tag != "" || !hasDigest {
		if tag == "" {
			tag = "latest"
		}
		out = append(out, name+":"+tag)
	}
	return out
}

// The withholds* methods are the one gate every candidate kind passes: an
// object a stack's compose file names is not a candidate, and while any stack
// is withheld nothing of any of these kinds is. TestHostSpaceEveryCandidateKind
// IsFilteredByStackReferences fails for a HostCandidates list that skips it.

func (r stackReferences) withholdsImage(img *image.Summary) bool {
	return len(r.withheld) > 0 || r.referencesImage(img.RepoTags, img.RepoDigests)
}

func (r stackReferences) withholdsContainer(ctr *container.Summary) bool {
	return len(r.withheld) > 0 || r.referencesContainer(ctr.Names)
}

func (r stackReferences) withholdsVolume(vol *volume.Volume) bool {
	return len(r.withheld) > 0 || r.volumes[vol.Name]
}

func (r stackReferences) withholdsNetwork(n network.Summary) bool {
	return len(r.withheld) > 0 || r.networks[n.Name]
}

// referencesImage reports whether a stack names any tag or digest the image
// carries.
func (r stackReferences) referencesImage(repoTags, repoDigests []string) bool {
	for _, refs := range [][]string{repoTags, repoDigests} {
		for _, ref := range refs {
			for _, n := range normalizedImageRefs(ref) {
				if r.images[n] {
					return true
				}
			}
		}
	}
	return false
}

// referencesContainer matches Docker's "/name" spelling.
func (r stackReferences) referencesContainer(names []string) bool {
	for _, n := range names {
		if r.containers[strings.TrimPrefix(n, "/")] {
			return true
		}
	}
	return false
}
