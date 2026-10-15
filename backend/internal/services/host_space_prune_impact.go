package services

import (
	"context"
	"fmt"
	"sort"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The Resources-page prune routes keep Docker's own rule (Edwin, 2026-10-15,
// agent-os-8d8a): "unused" covers a stopped or removed stack's volumes and
// networks. These reads name the stacks that would lose something, so the
// confirm dialog can say so before the user prunes.

// VolumePruneImpact lists, per stack, the volumes `docker volume prune --all`
// would remove: those no container holds. A volume whose usage Docker did not
// count may be removed, so a stack's such volume is listed too. It always
// applies the --all rule; the dialog asks only when "all unused" is on.
func (s *HostSpaceService) VolumePruneImpact(ctx context.Context) (*PruneImpact, error) {
	b, err := s.newPruneImpactBuilder(ctx)
	if err != nil {
		return nil, err
	}
	du, err := s.docker.DiskUsage(ctx, dockertypes.DiskUsageOptions{
		Types: []dockertypes.DiskUsageObject{dockertypes.VolumeObject},
	})
	if err != nil {
		return nil, fmt.Errorf("reading Docker volume usage: %w", err)
	}
	for _, vol := range du.Volumes {
		if vol == nil {
			continue
		}
		if u := volumeUsageOf(vol); u.refCountKnown && u.refCount > 0 {
			continue
		}
		b.add(vol.Name, vol.Labels, func(r stackReferences) bool { return r.volumes[vol.Name] })
	}
	return b.result(), nil
}

// NetworkPruneImpact lists, per stack, the networks `docker network prune`
// would remove: those no running container is attached to. A container that
// is only created or stopped does not hold its network (OBSERVED on Docker
// API 1.45, agent-os-8d8a checkpoint), so a stopped stack loses its networks
// too. The prune's `until` filter is not applied: it can only spare more.
func (s *HostSpaceService) NetworkPruneImpact(ctx context.Context) (*PruneImpact, error) {
	b, err := s.newPruneImpactBuilder(ctx)
	if err != nil {
		return nil, err
	}
	nets, err := s.docker.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	for _, n := range nets {
		if builtInNetworks[n.Name] {
			continue
		}
		b.addIf(n.Name, n.Labels, func(r stackReferences) bool { return r.networks[n.Name] }, func() bool {
			// A failed inspect is -1: unknown attachments may be none.
			return networkContainerCount(ctx, s.docker, n.ID, -1) <= 0
		})
	}
	return b.result(), nil
}

// pruneImpactBuilder attributes unused objects to the stacks that own them by
// label or name them in their compose file.
type pruneImpactBuilder struct {
	owned     map[string]bool
	refs      map[string]stackReferences
	unchecked []string
	items     map[string][]string
}

func (s *HostSpaceService) newPruneImpactBuilder(ctx context.Context) (*pruneImpactBuilder, error) {
	stacks, err := s.stacks.ListStacks()
	if err != nil {
		return nil, fmt.Errorf("listing stacks: %w", err)
	}
	b := &pruneImpactBuilder{
		owned:     map[string]bool{},
		refs:      map[string]stackReferences{},
		unchecked: []string{},
		items:     map[string][]string{},
	}
	for _, st := range stacks {
		if st.ProjectName == "" {
			continue
		}
		b.owned[st.ProjectName] = true
		// One stack per resolver call, so each name is attributed to the
		// stack that names it.
		refs := s.refs(ctx, []models.Stack{st})
		if len(refs.withheld) > 0 {
			b.unchecked = append(b.unchecked, st.ProjectName)
			continue
		}
		b.refs[st.ProjectName] = refs
	}
	sort.Strings(b.unchecked)
	return b, nil
}

func (b *pruneImpactBuilder) add(name string, labels map[string]string, named func(stackReferences) bool) {
	b.addIf(name, labels, named, func() bool { return true })
}

// addIf attributes name to its stacks, calling removable only for an object
// some stack owns or names, since it may cost a Docker read.
func (b *pruneImpactBuilder) addIf(name string, labels map[string]string, named func(stackReferences) bool, removable func() bool) {
	stacks := map[string]bool{}
	if project := labels[composeProjectLabel]; b.owned[project] {
		stacks[project] = true
	}
	for project, refs := range b.refs {
		if named(refs) {
			stacks[project] = true
		}
	}
	if len(stacks) == 0 || !removable() {
		return
	}
	for project := range stacks {
		b.items[project] = append(b.items[project], name)
	}
}

func (b *pruneImpactBuilder) result() *PruneImpact {
	out := &PruneImpact{Stacks: []PruneImpactStack{}, UncheckedStacks: b.unchecked}
	for project, items := range b.items {
		sort.Strings(items)
		out.Stacks = append(out.Stacks, PruneImpactStack{Stack: project, Items: items})
	}
	sort.Slice(out.Stacks, func(i, j int) bool { return out.Stacks[i].Stack < out.Stacks[j].Stack })
	return out
}
