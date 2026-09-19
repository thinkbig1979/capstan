package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// A nil Go slice marshals to JSON null; a zero-length non-nil slice marshals to
// []. Every slice field below carries no omitempty, so the key is always
// present and the two shapes are distinguishable on the wire — and a generated
// TypeScript `T[]` is a lie about any field that can arrive as null.
//
// This test PINS what the production construction paths actually produce. It is
// deliberately not a statement about what they SHOULD produce: four of the
// eleven fields are pinned as null because that is the observed wire, and each
// of those is a defect recorded for follow-up rather than repaired here.
//
// Each row names the construction site it mirrors. The site is the evidence;
// this table is the assertion that the site's initialisation expression yields
// the JSON shape claimed for it.
func TestWireSliceNullability(t *testing.T) {
	cases := []struct {
		name string
		// site is the production construction path this row mirrors.
		site string
		// value is the struct in the state that site leaves it in.
		value any
		// key is the JSON field under test.
		key string
		// wantNull is true when the production path can emit JSON null there.
		wantNull bool
	}{
		{
			name:     "Stack.containers on the Docker-outage path",
			site:     "handlers/stacks.go List/Get leave applyLiveStatus uncalled when GetStackStatuses errors; database/stacks.go ListStacks/GetStack never scan a Containers column, so the field keeps its nil zero value",
			value:    Stack{},
			key:      "containers",
			wantNull: true,
		},
		{
			name:     "Stack.containers on the live-status path",
			site:     "handlers/stacks.go applyLiveStatus assigns []models.Container{} for a project absent from the snapshot",
			value:    Stack{Containers: []Container{}},
			key:      "containers",
			wantNull: false,
		},
		{
			name:     "Container.ports",
			site:     "services/docker.go ListContainers builds ports with make([]models.PortBinding, 0); services/docker_lifecycle.go takes it from parsePorts, which also opens with make(...,0)",
			value:    Container{Ports: []PortBinding{}},
			key:      "ports",
			wantNull: false,
		},
		{
			name:     "PullResult.changedFiles",
			site:     "services/git.go PullVerified opens changedFiles := []string{} before any branch assigns to it",
			value:    PullResult{ChangedFiles: []string{}},
			key:      "changedFiles",
			wantNull: false,
		},
		{
			name:     "LogResult.commits",
			site:     "services/git.go GetLog and GetLogForFile both open commits := []models.GitCommit{}",
			value:    LogResult{Commits: []GitCommit{}},
			key:      "commits",
			wantNull: false,
		},
		{
			name:     "DiffResult.files on a commit that touched nothing",
			site:     "services/git.go getDiffCLI declares var files []string and only assigns when diff-tree printed something; an empty commit leaves it nil",
			value:    DiffResult{},
			key:      "files",
			wantNull: true,
		},
		{
			name:     "DashboardContainerInfo.ports",
			site:     "services/docker.go GetAllContainersWithDetails builds ports with make([]models.PortBinding, 0)",
			value:    DashboardContainerInfo{Ports: []PortBinding{}},
			key:      "ports",
			wantNull: false,
		},
		{
			name:     "DockerImage.repoTags",
			site:     "services/docker_resources.go ListImages substitutes []string{\"<none>\"} when the daemon reports nil RepoTags",
			value:    DockerImage{RepoTags: []string{"<none>"}},
			key:      "repoTags",
			wantNull: false,
		},
		{
			name:     "DockerNetwork.labels on a network with no labels",
			site:     "services/docker_resources.go ListNetworks declares var labelStrs []string and appends only inside `if net.Labels != nil`; the default bridge/host/none networks carry no compose labels, so this fires on every Docker host",
			value:    DockerNetwork{},
			key:      "labels",
			wantNull: true,
		},
		{
			name:     "UpdateSettingsResponse.applyDays",
			site:     "handlers/settings.go initialises ApplyDays: []int{} in the struct literal, before the parse that may fail — the field's own doc comment requires it",
			value:    UpdateSettingsResponse{ApplyDays: []int{}},
			key:      "applyDays",
			wantNull: false,
		},
		{
			name:     "BackupSnapshot.tags on an untagged snapshot",
			site:     "services/backup_restic.go ListSnapshots copies resticSnapshot.Tags straight through; restic omits the \"tags\" key for an untagged snapshot, leaving the field nil",
			value:    BackupSnapshot{Paths: []string{"/srv"}},
			key:      "tags",
			wantNull: true,
		},
		{
			name:     "BackupSnapshot.paths when restic omits the key",
			site:     "services/backup_restic.go ListSnapshots copies resticSnapshot.Paths straight through with no guard; restic emits the key for every snapshot it writes, so this is the unguarded shape rather than an observed one",
			value:    BackupSnapshot{},
			key:      "paths",
			wantNull: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got, present := decoded[tc.key]
			if !present {
				t.Fatalf("key %q absent from %s\nsite: %s", tc.key, raw, tc.site)
			}
			isNull := string(got) == "null"
			if isNull != tc.wantNull {
				t.Fatalf("%s = %s; wantNull=%v\nsite: %s", tc.key, got, tc.wantNull, tc.site)
			}
			if !isNull && !strings.HasPrefix(string(got), "[") {
				t.Fatalf("%s = %s; expected a JSON array\nsite: %s", tc.key, got, tc.site)
			}
		})
	}
}
