package services

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
)

// agent-os-fnch. A compose project with no Capstan stack row still shows on the
// dashboard, because the container list groups on compose's project label.
// These arms pin that GetAllContainersWithDetails carries compose's own
// working_dir and config_files labels, and that the no-row-no-error branch
// reports the unmanaged projects in ONE Info line, only when that set changes:
// the loop runs on every dashboard and stacks-list poll, so a line per call is
// log spam.
//
// Driven through qyg72ListFake (docker_update_listfault_test.go). Every
// container here is "exited" so the loop never reaches ContainerInspect, which
// that fake rejects.

const (
	fnchUnmanagedDir   = "/home/op/other/docker"
	fnchUnmanagedFiles = "/home/op/other/docker/docker-compose.a.yml,/home/op/other/docker/docker-compose.b.yml"
	fnchUnmanagedMsg   = "Compose projects are running without a Capstan stack"
)

func fnchCompose(id, project, workingDir, configFiles string) container.Summary {
	return container.Summary{
		ID:    id,
		Names: []string{"/" + id},
		State: "exited",
		Labels: map[string]string{
			"com.docker.compose.project":              project,
			"com.docker.compose.project.working_dir":  workingDir,
			"com.docker.compose.project.config_files": configFiles,
		},
	}
}

func fnchList(t *testing.T, svc *DockerService, fake *qyg72ListFake) map[string]fnchRow {
	t.Helper()
	got, err := svc.GetAllContainersWithDetails(context.Background(), g482HealthyDB(t))
	if err != nil {
		t.Fatalf("GetAllContainersWithDetails: %v", err)
	}
	if len(fake.beyond) != 0 {
		t.Fatalf("the loop called past ContainerList: %v", fake.beyond)
	}
	rows := make(map[string]fnchRow, len(got))
	for _, c := range got {
		rows[c.ID] = fnchRow{c.ComposeWorkingDir, c.ComposeConfigFiles, c.StackID}
	}
	return rows
}

type fnchRow struct{ workingDir, configFiles, stackID string }

func TestGetAllContainersWithDetails_CarriesComposeLocationLabels(t *testing.T) {
	fake := &qyg72ListFake{containers: []container.Summary{
		fnchCompose("unmanaged", g482ProjectUnknown, fnchUnmanagedDir, fnchUnmanagedFiles),
		fnchCompose("managed", g482ProjectKnown, "/opt/stacks/known", "/opt/stacks/known/compose.yaml"),
		{ID: "plain", Names: []string{"/plain"}, State: "exited", Labels: map[string]string{}},
	}}
	rows := fnchList(t, &DockerService{updateClient: fake}, fake)

	tests := []struct {
		id   string
		want fnchRow
	}{
		// config_files keeps its comma: displayed verbatim, never split.
		{"unmanaged", fnchRow{fnchUnmanagedDir, fnchUnmanagedFiles, ""}},
		{"managed", fnchRow{"/opt/stacks/known", "/opt/stacks/known/compose.yaml", g482StackID}},
		{"plain", fnchRow{"", "", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := rows[tt.id]; got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetAllContainersWithDetails_LogsUnmanagedProjectsOnlyWhenTheSetChanges(t *testing.T) {
	logs := g482CaptureLogs(t)
	fake := &qyg72ListFake{containers: []container.Summary{
		// Two containers of one project: still one entry, one line.
		fnchCompose("u1", g482ProjectUnknown, fnchUnmanagedDir, fnchUnmanagedFiles),
		fnchCompose("u2", g482ProjectUnknown, fnchUnmanagedDir, fnchUnmanagedFiles),
		fnchCompose("m1", g482ProjectKnown, "/opt/stacks/known", "/opt/stacks/known/compose.yaml"),
	}}
	svc := &DockerService{updateClient: fake}

	fnchList(t, svc, fake)
	if n := g482CountLines(logs, fnchUnmanagedMsg); n != 1 {
		t.Fatalf("first call: %d unmanaged lines, want 1\n%s", n, logs)
	}
	line := logs.String()
	for _, want := range []string{g482ProjectUnknown, fnchUnmanagedDir, "level=INFO"} {
		if !strings.Contains(line, want) {
			t.Errorf("unmanaged line does not name %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "/opt/stacks/known") {
		t.Errorf("the managed project's path reached the unmanaged line:\n%s", line)
	}

	fnchList(t, svc, fake)
	if n := g482CountLines(logs, fnchUnmanagedMsg); n != 1 {
		t.Errorf("identical second call: %d unmanaged lines in total, want still 1 — a line per poll is spam", n)
	}

	fake.containers = append(fake.containers, fnchCompose("u3", "fnch-other", "/srv/other", "/srv/other/compose.yml"))
	fnchList(t, svc, fake)
	if n := g482CountLines(logs, fnchUnmanagedMsg); n != 2 {
		t.Errorf("changed set: %d unmanaged lines in total, want 2", n)
	}
}

// The must-not arms. A stack that resolves, and a lookup that FAILED, are both
// not "unmanaged": the second keeps its existing Error line only, because "we
// could not tell" is not "this is unmanaged".
func TestGetAllContainersWithDetails_ManagedOrFailedLookupLogsNoUnmanagedLine(t *testing.T) {
	logs := g482CaptureLogs(t)
	managed := &qyg72ListFake{containers: []container.Summary{
		fnchCompose("m1", g482ProjectKnown, "/opt/stacks/known", "/opt/stacks/known/compose.yaml"),
	}}
	fnchList(t, &DockerService{updateClient: managed}, managed)

	failed := &qyg72ListFake{containers: []container.Summary{
		fnchCompose("f1", g482ProjectKnown, fnchUnmanagedDir, fnchUnmanagedFiles),
	}}
	got, err := (&DockerService{updateClient: failed}).GetAllContainersWithDetails(context.Background(), g482ClosedDB(t))
	if err != nil {
		t.Fatalf("GetAllContainersWithDetails: %v", err)
	}
	if len(got) != 1 || !got[0].StackLookupFailed {
		t.Fatalf("premise: the closed-db arm must reach the failed-lookup branch, got %+v", got)
	}

	if n := g482CountLines(logs, fnchUnmanagedMsg); n != 0 {
		t.Errorf("%d unmanaged lines, want 0:\n%s", n, logs)
	}
	if n := g482CountLines(logs, "Cannot resolve compose stacks"); n != 1 {
		t.Errorf("failed lookup: %d Error lines, want its existing 1", n)
	}
}
