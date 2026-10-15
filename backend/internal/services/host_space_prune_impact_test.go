package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-8d8a: Docker's volume and network prune remove what is unused by
// Docker's rule, which includes a stopped or removed stack's objects. The
// prune impact names those stacks so the confirm dialog can warn.

func shopLabel() map[string]string { return map[string]string{composeProjectLabel: "shop"} }

func TestPruneImpact_VolumesListsStackOwnedAndNamed(t *testing.T) {
	shop := writeStack(t, "shop", map[string]string{"compose.yaml": shopCompose})
	docker := &hostSpaceFakeDocker{du: dockertypes.DiskUsage{Volumes: []*volume.Volume{
		{Name: "pgdata", UsageData: usage(100, 0)},                          // external in shop's compose
		{Name: "shop-archive", UsageData: usage(100, 0)},                    // explicit name: in shop's compose
		{Name: "shop_cache", Labels: shopLabel(), UsageData: usage(100, 0)}, // label only
		{Name: "shop_held", Labels: shopLabel(), UsageData: usage(100, 1)},  // a container holds it: kept
		{Name: "loose", UsageData: usage(100, 0)},                           // nothing names it
		{Name: "other_db", Labels: map[string]string{composeProjectLabel: "other"}, UsageData: usage(100, 0)},
	}}}
	svc := newHostSpaceWithStacks(docker, shop)

	impact, err := svc.VolumePruneImpact(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []PruneImpactStack{{Stack: "shop", Items: []string{"pgdata", "shop-archive", "shop_cache"}}}, impact.Stacks,
		"a stack's unused named, external and labelled volumes must be listed; a held one, an unrelated one and a non-Capstan project's must not")
	assert.Empty(t, impact.UncheckedStacks)
}

func TestPruneImpact_NetworksListsStackOwnedAndExternal(t *testing.T) {
	shop := writeStack(t, "shop", map[string]string{"compose.yaml": shopCompose})
	docker := &hostSpaceFakeDocker{
		nets: []network.Summary{
			{ID: "n-proxy", Name: "proxy"},                               // external in shop's compose
			{ID: "n-default", Name: "shop_default", Labels: shopLabel()}, // label only
			{ID: "n-busy", Name: "shop_busy", Labels: shopLabel()},       // a running container is attached
			{ID: "n-scratch", Name: "scratch"},                           // nothing names it
			{ID: "n-bridge", Name: "bridge"},
		},
		inspect: map[string]int{"n-proxy": 0, "n-default": 0, "n-busy": 1, "n-scratch": 0, "n-bridge": 0},
	}
	svc := newHostSpaceWithStacks(docker, shop)

	impact, err := svc.NetworkPruneImpact(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []PruneImpactStack{{Stack: "shop", Items: []string{"proxy", "shop_default"}}}, impact.Stacks,
		"a stack's unattached external and labelled networks must be listed; an attached one, an unrelated one and a built-in must not")
	assert.Empty(t, impact.UncheckedStacks)
}

func TestPruneImpact_UnreadableComposeIsUnchecked(t *testing.T) {
	shop := writeStack(t, "shop", map[string]string{"compose.yaml": shopCompose})
	broken := writeStack(t, "broken", nil) // no compose file on disk
	docker := &hostSpaceFakeDocker{
		du: dockertypes.DiskUsage{Volumes: []*volume.Volume{
			{Name: "pgdata", UsageData: usage(100, 0)},
			{Name: "broken_data", Labels: map[string]string{composeProjectLabel: "broken"}, UsageData: usage(100, 0)},
		}},
		nets:    []network.Summary{{ID: "n-proxy", Name: "proxy"}},
		inspect: map[string]int{"n-proxy": 0},
	}
	svc := newHostSpaceWithStacks(docker, shop, broken)

	vols, err := svc.VolumePruneImpact(context.Background())
	require.NoError(t, err, "an unreadable compose file is reported, it does not fail the read")
	assert.Equal(t, []string{"broken"}, vols.UncheckedStacks)
	assert.Equal(t, []PruneImpactStack{
		{Stack: "broken", Items: []string{"broken_data"}},
		{Stack: "shop", Items: []string{"pgdata"}},
	}, vols.Stacks, "an unchecked stack's labelled volume is still attributed by its label")

	nets, err := svc.NetworkPruneImpact(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"broken"}, nets.UncheckedStacks)
	assert.Equal(t, []PruneImpactStack{{Stack: "shop", Items: []string{"proxy"}}}, nets.Stacks)
}

// Docker's "not counted" is not "unused": a stack's object whose usage is
// unknown may be removed, so it is listed.
func TestPruneImpact_UnknownUsageIsListed(t *testing.T) {
	docker := &hostSpaceFakeDocker{
		du: dockertypes.DiskUsage{Volumes: []*volume.Volume{
			{Name: "shop_nousage", Labels: shopLabel()},
			{Name: "shop_uncounted", Labels: shopLabel(), UsageData: usage(-1, -1)},
			{Name: "stray", UsageData: usage(-1, -1)},
		}},
		nets: []network.Summary{
			{ID: "n-shop", Name: "shop_default", Labels: shopLabel()},
			{ID: "n-stray", Name: "stray"},
		},
		inspect: map[string]int{}, // every inspect fails
	}
	svc := newHostSpaceWithStacks(docker, models.Stack{ID: "s1", ProjectName: "shop"})
	svc.refs = noStackReferences

	vols, err := svc.VolumePruneImpact(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []PruneImpactStack{{Stack: "shop", Items: []string{"shop_nousage", "shop_uncounted"}}}, vols.Stacks)

	nets, err := svc.NetworkPruneImpact(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []PruneImpactStack{{Stack: "shop", Items: []string{"shop_default"}}}, nets.Stacks)
}

func TestPruneImpact_ReadFailureIsAnError(t *testing.T) {
	boom := errors.New("daemon gone")
	cases := map[string]struct {
		docker *hostSpaceFakeDocker
		stacks *hostSpaceFakeStacks
		read   func(*HostSpaceService, context.Context) (*PruneImpact, error)
	}{
		"volumes: disk usage": {&hostSpaceFakeDocker{duErr: boom}, &hostSpaceFakeStacks{},
			(*HostSpaceService).VolumePruneImpact},
		"volumes: stacks": {&hostSpaceFakeDocker{}, &hostSpaceFakeStacks{err: boom},
			(*HostSpaceService).VolumePruneImpact},
		"networks: network list": {&hostSpaceFakeDocker{netErr: boom}, &hostSpaceFakeStacks{},
			(*HostSpaceService).NetworkPruneImpact},
		"networks: stacks": {&hostSpaceFakeDocker{}, &hostSpaceFakeStacks{err: boom},
			(*HostSpaceService).NetworkPruneImpact},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := newHostSpaceService(tc.docker, tc.stacks, (&hostSpaceClock{}).now)
			svc.refs = noStackReferences
			impact, err := tc.read(svc, context.Background())
			assert.ErrorIs(t, err, boom, "a failed read must not be served as \"no stack affected\"")
			assert.Nil(t, impact)
		})
	}
}

func TestPruneImpact_EmptySendsArrays(t *testing.T) {
	svc, _ := newHostSpaceForTest(&hostSpaceFakeDocker{})
	for _, read := range []func(context.Context) (*PruneImpact, error){svc.VolumePruneImpact, svc.NetworkPruneImpact} {
		impact, err := read(context.Background())
		require.NoError(t, err)
		body, err := json.Marshal(impact)
		require.NoError(t, err)
		assert.JSONEq(t, `{"stacks":[],"uncheckedStacks":[]}`, string(body))
	}
}
