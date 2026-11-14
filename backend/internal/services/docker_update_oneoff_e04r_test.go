package services

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-e04r: when the compose service was not running before the update,
// both compose apply paths stop what `compose up` started. The filter that
// found it also matched a `docker compose run --rm` one-off of the same
// service, and stopping that makes Docker remove it and its anonymous volumes
// (OBSERVED on Docker 26.1.5 / Compose v5.7.1). Only the service container may
// be stopped.

// oneOffE04rFake answers ContainerList like the daemon does: a container is
// listed only when it carries every label=k=v the filter asks for. The status
// filter is ignored; both containers run.
type oneOffE04rFake struct {
	*qags20Fake
	containers []container.Summary
	stopped    []string
}

func (f *oneOffE04rFake) ContainerList(_ context.Context, opts container.ListOptions) ([]container.Summary, error) {
	var out []container.Summary
	for _, c := range f.containers {
		match := true
		for _, l := range opts.Filters.Get("label") {
			k, v, _ := strings.Cut(l, "=")
			if c.Labels[k] != v {
				match = false
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *oneOffE04rFake) ContainerStop(_ context.Context, id string, _ container.StopOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

func newOneOffE04rFake(project string) *oneOffE04rFake {
	labels := func(oneoff string) map[string]string {
		return map[string]string{
			"com.docker.compose.project": project,
			"com.docker.compose.service": "web",
			"com.docker.compose.oneoff":  oneoff,
		}
	}
	return &oneOffE04rFake{qags20Fake: &qags20Fake{}, containers: []container.Summary{
		{ID: "service-container", Labels: labels("False")},
		{ID: "one-off-container", Labels: labels("True")},
	}}
}

// Not parallel: stubComposeBySubcommand swaps the package's exec seam.
func TestComposeUpdate_e04r_DoesNotStopOneOff(t *testing.T) {
	paths := map[string]func(*DockerService, models.Stack) error{
		"updateComposeContainer": func(s *DockerService, st models.Stack) error {
			return s.updateComposeContainer(context.Background(), st, "web", false)
		},
		"updateComposeContainerStreaming": func(s *DockerService, st models.Stack) error {
			return s.updateComposeContainerStreaming(context.Background(), st, "web", false,
				func(LogLine) {}, func(Status) {})
		},
	}
	for name, apply := range paths {
		t.Run(name, func(t *testing.T) {
			stubComposeBySubcommand(t, "true", "true")
			svc, stack := stubbedStatusService(t)
			fake := newOneOffE04rFake(stack.ProjectName)
			svc.updateClient = fake

			require.NoError(t, apply(svc, stack))
			assert.Equal(t, []string{"service-container"}, fake.stopped,
				"only the service container is stopped back; the one-off is left running")
		})
	}
}
