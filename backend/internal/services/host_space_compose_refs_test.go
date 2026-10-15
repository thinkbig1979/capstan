package services

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-3rr3: an object a stack's compose file names is never a candidate,
// whether or not it carries the stack's compose project label.

// writeStack writes files into a fresh stack directory and returns the stack
// row the scanner would have stored for it.
func writeStack(t *testing.T, project string, files map[string]string) models.Stack {
	t.Helper()
	dir := filepath.Join(t.TempDir(), project)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	return models.Stack{ID: project, Directory: dir, ComposeFile: "compose.yaml", ProjectName: project, Status: "stopped"}
}

// referenceFixture holds, per kind, one object that a down stack names and one
// that nothing names. Every object is otherwise a candidate: unused, unlabelled,
// stopped, unattached.
func referenceFixture() *hostSpaceFakeDocker {
	return &hostSpaceFakeDocker{
		du: dockertypes.DiskUsage{
			Images: []*image.Summary{
				{ID: "sha256:pg", RepoTags: []string{"postgres:16"}, Size: 10},
				{ID: "sha256:built", RepoTags: []string{"shop-api:latest"}, Size: 10},
				{ID: "sha256:profiled", RepoTags: []string{"busybox:latest"}, Size: 10},
				{ID: "sha256:pinned", RepoDigests: []string{"ghcr.io/acme/worker@sha256:1111111111111111111111111111111111111111111111111111111111111111"}, Size: 10},
				{ID: "sha256:unrelated", RepoTags: []string{"unrelated:1"}, Size: 10},
			},
			Containers: []*container.Summary{
				{ID: "c-vpn", Names: []string{"/vpn"}, State: container.StateExited},
				{ID: "c-leftover", Names: []string{"/leftover"}, State: container.StateExited},
			},
			Volumes: []*volume.Volume{
				{Name: "pgdata", UsageData: usage(100, 0)},
				{Name: "shop-archive", UsageData: usage(100, 0)},
				{Name: "loose", UsageData: usage(100, 0)},
			},
			BuildCache: []*build.CacheRecord{{ID: "cache", Size: 5}},
		},
		nets: []network.Summary{
			{ID: "n-proxy", Name: "proxy"},
			{ID: "n-scratch", Name: "scratch"},
		},
		inspect: map[string]int{"n-proxy": 0, "n-scratch": 0},
	}
}

const shopCompose = `
services:
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data
      - archive:/archive
    networks: [proxy]
  api:
    build: .
  worker:
    image: ghcr.io/acme/worker@sha256:1111111111111111111111111111111111111111111111111111111111111111
    network_mode: "container:vpn"
  tools:
    image: docker.io/library/busybox
    profiles: [debug]
volumes:
  pgdata:
    external: true
  archive:
    name: shop-archive
networks:
  proxy:
    external: true
`

func newHostSpaceWithStacks(docker *hostSpaceFakeDocker, stacks ...models.Stack) *HostSpaceService {
	clock := &hostSpaceClock{}
	return newHostSpaceService(docker, &hostSpaceFakeStacks{stacks: stacks}, clock.now)
}

func TestHostSpaceComposeReferencesAreNotCandidates(t *testing.T) {
	shop := writeStack(t, "shop", map[string]string{"compose.yaml": shopCompose})
	svc := newHostSpaceWithStacks(referenceFixture(), shop)

	cand, err := svc.Candidates(context.Background())
	require.NoError(t, err)

	assert.Empty(t, cand.WithheldStacks)
	assert.Equal(t, []string{"sha256:unrelated"}, candidateIDs(cand.Images),
		"an image a down stack names (tag, build default name, profile-gated service, digest pin) was listed")
	assert.Equal(t, []string{"c-leftover"}, candidateIDs(cand.Containers),
		"a container a stack joins with network_mode: container: was listed")
	assert.Equal(t, []string{"loose"}, candidateIDs(cand.Volumes),
		"an external or explicitly named volume of a down stack was listed")
	assert.Equal(t, []string{"n-scratch"}, candidateIDs(cand.Networks),
		"an external network of a down stack was listed")

	sum, err := svc.Summary(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, sum.Volumes.ReclaimableCount, "the summary must agree with the candidates")
	assert.Equal(t, int64(100), sum.Volumes.ReclaimableBytes)
}

// Interpolation follows the stack's own .env, as compose does.
func TestHostSpaceComposeReferencesInterpolate(t *testing.T) {
	shop := writeStack(t, "shop", map[string]string{
		"compose.yaml": "services:\n  db:\n    image: postgres:${PG_VERSION}\n    volumes: [data:/d]\nvolumes:\n  data:\n    name: ${DATA_VOLUME:-pgdata}\n",
		".env":         "PG_VERSION=16\n",
	})
	cand, err := newHostSpaceWithStacks(referenceFixture(), shop).Candidates(context.Background())
	require.NoError(t, err)
	assert.Empty(t, cand.WithheldStacks)
	assert.NotContains(t, candidateIDs(cand.Images), "sha256:pg", "postgres:${PG_VERSION} with PG_VERSION=16 names postgres:16")
	assert.NotContains(t, candidateIDs(cand.Volumes), "pgdata", "the default in ${DATA_VOLUME:-pgdata} names pgdata")
	assert.Contains(t, candidateIDs(cand.Volumes), "loose")
}

// What does NOT withhold a stack: anything that cannot change a name.
func TestHostSpaceComposeReferencesTolerateNameIrrelevantGaps(t *testing.T) {
	for name, compose := range map[string]string{
		"required variable outside a name": "services:\n  app:\n    image: unrelated:2\n    environment:\n      SECRET: ${SECRET:?set it}\n",
		"missing service env_file":         "services:\n  app:\n    image: unrelated:2\n    env_file: [missing.env]\n",
		"unset optional variable":          "services:\n  app:\n    image: unrelated:${NOT_SET}2\n",
		"obsolete version attribute":       "version: \"3.8\"\nservices:\n  app:\n    image: unrelated:2\n",
	} {
		t.Run(name, func(t *testing.T) {
			st := writeStack(t, "other", map[string]string{"compose.yaml": compose})
			cand, err := newHostSpaceWithStacks(referenceFixture(), st).Candidates(context.Background())
			require.NoError(t, err)
			assert.Empty(t, cand.WithheldStacks)
			assert.Contains(t, candidateIDs(cand.Volumes), "loose")
			assert.Contains(t, candidateIDs(cand.Images), "sha256:unrelated")
		})
	}
}

// A stack whose compose file cannot be resolved could name anything, so no
// image, container, volume or network is a candidate. Build cache is not
// something a compose file names and stays listed.
func TestHostSpaceComposeReferencesUnreadableWithholdsEverything(t *testing.T) {
	cases := map[string]func(t *testing.T) models.Stack{
		"missing compose file": func(t *testing.T) models.Stack {
			st := writeStack(t, "broken", nil)
			return st
		},
		"invalid YAML": func(t *testing.T) models.Stack {
			return writeStack(t, "broken", map[string]string{"compose.yaml": "services: [\n"})
		},
		"required variable in a volume name": func(t *testing.T) models.Stack {
			return writeStack(t, "broken", map[string]string{"compose.yaml": "services:\n  db:\n    image: x\nvolumes:\n  data:\n    name: ${VOL:?set it}\n"})
		},
		"required variable in an image": func(t *testing.T) models.Stack {
			return writeStack(t, "broken", map[string]string{"compose.yaml": "services:\n  db:\n    image: postgres:${TAG:?set it}\n"})
		},
		"env file compose would fail on": func(t *testing.T) models.Stack {
			st := writeStack(t, "broken", map[string]string{"compose.yaml": shopCompose})
			st.EnvFile = "missing.env"
			return st
		},
		"no compose file recorded": func(t *testing.T) models.Stack {
			return models.Stack{ID: "broken", ProjectName: "broken"}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			readable := writeStack(t, "shop", map[string]string{"compose.yaml": shopCompose})
			svc := newHostSpaceWithStacks(referenceFixture(), readable, mk(t))

			cand, err := svc.Candidates(context.Background())
			require.NoError(t, err, "an unreadable compose file withholds candidates, it does not fail the read")
			assert.Equal(t, []string{"broken"}, cand.WithheldStacks)
			assert.Empty(t, cand.Images)
			assert.Empty(t, cand.Containers)
			assert.Empty(t, cand.Volumes)
			assert.Empty(t, cand.Networks)
			assert.Equal(t, 1, cand.BuildCache.Count, "build cache is not named by compose files")

			sum, err := svc.Summary(context.Background())
			require.NoError(t, err)
			assert.Zero(t, sum.Volumes.ReclaimableCount)
			assert.Zero(t, sum.Images.ReclaimableCount)
		})
	}
}

// FreeSpace rebuilds the candidates through the same filter, so a referenced
// ID asked for by name is skipped, never removed.
func TestHostSpaceFreeSpace_SkipsComposeReferences(t *testing.T) {
	shop := writeStack(t, "shop", map[string]string{"compose.yaml": shopCompose})
	docker := referenceFixture()
	svc := newHostSpaceWithStacks(docker, shop)

	res, err := svc.FreeSpace(context.Background(), HostFreeSpaceRequest{
		Volumes:  []string{"pgdata", "loose"},
		Networks: []string{"n-proxy"},
		Images:   []string{"sha256:pg"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"loose"}, res.Volumes.Removed)
	assert.Len(t, res.Volumes.Skipped, 1)
	assert.Empty(t, res.Networks.Removed)
	assert.Empty(t, res.Images.Removed)
	for _, r := range docker.removals {
		assert.NotEqual(t, "pgdata", r.id)
	}
}

func TestNormalizedImageRefs(t *testing.T) {
	for ref, want := range map[string][]string{
		"postgres":                       {"postgres:latest"},
		"postgres:16":                    {"postgres:16"},
		"docker.io/library/postgres:16":  {"postgres:16"},
		"library/postgres":               {"postgres:latest"},
		"docker.io/acme/app":             {"acme/app:latest"},
		"localhost:5000/app":             {"localhost:5000/app:latest"},
		"ghcr.io/acme/app:1.2":           {"ghcr.io/acme/app:1.2"},
		"ghcr.io/acme/app@sha256:ab":     {"ghcr.io/acme/app@sha256:ab"},
		"ghcr.io/acme/app:1.2@sha256:ab": {"ghcr.io/acme/app@sha256:ab", "ghcr.io/acme/app:1.2"},
		"  ":                             nil,
	} {
		assert.Equal(t, want, normalizedImageRefs(ref), ref)
	}
}

// TestHostSpaceEveryCandidateKindIsFilteredByStackReferences is the guard for
// the class: a HostCandidates list that does not pass through the stack
// reference gate lists an object a stack names. Each []HostCandidateItem field
// needs a fixture here; a new candidate kind fails until it has one, and the
// fixture fails until fetch routes that kind through stackReferences.
func TestHostSpaceEveryCandidateKindIsFilteredByStackReferences(t *testing.T) {
	// One otherwise-removable object per kind, all named "named".
	docker := &hostSpaceFakeDocker{
		du: dockertypes.DiskUsage{
			Images:     []*image.Summary{{ID: "named", RepoTags: []string{"named:latest"}}},
			Containers: []*container.Summary{{ID: "named", Names: []string{"/named"}, State: container.StateExited}},
			Volumes:    []*volume.Volume{{Name: "named", UsageData: usage(1, 0)}},
		},
		nets:    []network.Summary{{ID: "named", Name: "named"}},
		inspect: map[string]int{"named": 0},
	}
	kinds := map[string]func(*stackReferences){
		"Images":     func(r *stackReferences) { r.images["named:latest"] = true },
		"Containers": func(r *stackReferences) { r.containers["named"] = true },
		"Volumes":    func(r *stackReferences) { r.volumes["named"] = true },
		"Networks":   func(r *stackReferences) { r.networks["named"] = true },
	}

	listed := func(refs stackReferences, field string) []HostCandidateItem {
		svc, _ := newHostSpaceForTest(docker)
		svc.refs = func(context.Context, []models.Stack) stackReferences { return refs }
		cand, err := svc.Candidates(context.Background())
		require.NoError(t, err)
		return reflect.ValueOf(*cand).FieldByName(field).Interface().([]HostCandidateItem)
	}

	itemsType := reflect.TypeOf([]HostCandidateItem{})
	ct := reflect.TypeOf(HostCandidates{})
	seen := 0
	for i := 0; i < ct.NumField(); i++ {
		f := ct.Field(i)
		if f.Type != itemsType {
			continue
		}
		seen++
		reference, ok := kinds[f.Name]
		if !ok {
			t.Errorf("HostCandidates.%s has no stack-reference fixture: route it through stackReferences and add one here", f.Name)
			continue
		}
		require.Len(t, listed(newStackReferences(), f.Name), 1, "%s: the fixture object must be a candidate when nothing names it", f.Name)

		refs := newStackReferences()
		reference(&refs)
		assert.Empty(t, listed(refs, f.Name), "%s: an object a stack names was listed", f.Name)

		withheld := newStackReferences()
		withheld.withheld = []string{"broken"}
		assert.Empty(t, listed(withheld, f.Name), "%s: listed while a stack's compose file is unreadable", f.Name)
	}
	assert.Equal(t, len(kinds), seen, "a fixture names a HostCandidates field that no longer exists")
}

// global.env takes part in interpolation, as the --env-file buildComposeArgs
// passes.
func TestHostSpaceComposeReferencesReadGlobalEnv(t *testing.T) {
	globalEnv := filepath.Join(t.TempDir(), "global.env")
	require.NoError(t, os.WriteFile(globalEnv, []byte("DATA_VOLUME=loose\n"), 0o600))
	shop := writeStack(t, "shop", map[string]string{
		"compose.yaml": "services:\n  db:\n    image: x\nvolumes:\n  data:\n    name: ${DATA_VOLUME:?set it}\n",
	})

	refs := composeStackReferences(globalEnv)(context.Background(), []models.Stack{shop})
	assert.Empty(t, refs.withheld)
	assert.True(t, refs.volumes["loose"], "the volume name comes from global.env")

	refs = composeStackReferences("")(context.Background(), []models.Stack{shop})
	assert.Equal(t, []string{"shop"}, refs.withheld, "without global.env the required variable has no value")
}
