package services

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-fjd6: the two compose lookups that pick ONE container of a service,
// findComposeContainer and the pre-update lookup in
// UpdateComposeServiceStreaming, took the first container carrying the
// project and service labels. A `docker compose run` one-off carries the same
// labels, and the daemon lists newest first, so a one-off started after the
// service container was picked (OBSERVED on Docker 26.1.5 / Compose v5.7.1).
// The update then judged, or took its old image from, the wrong container.

// oneOffFjd6Fake lists the one-off FIRST, as the daemon does for a one-off
// newer than the service container, and records which container was
// inspected.
type oneOffFjd6Fake struct {
	*oneOffE04rFake
	inspected []string
}

func (f *oneOffFjd6Fake) ContainerInspect(_ context.Context, id string) (container.InspectResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspected = append(f.inspected, id)
	return container.InspectResponse{}, errors.New("fjd6 fake: inspect ends the update here")
}

func newOneOffFjd6Fake(project string) *oneOffFjd6Fake {
	base := newOneOffE04rFake(project)
	// Newest first: the one-off ahead of the service container.
	base.containers[0], base.containers[1] = base.containers[1], base.containers[0]
	return &oneOffFjd6Fake{oneOffE04rFake: base}
}

func TestComposeLookups_fjd6_PickServiceNotOneOff(t *testing.T) {
	t.Run("findComposeContainer", func(t *testing.T) {
		svc, stack := stubbedStatusService(t)
		fake := newOneOffFjd6Fake(stack.ProjectName)
		svc.updateClient = fake

		got := svc.findComposeContainer(context.Background(), stack.ProjectName, "web", "fallback")
		assert.Equal(t, "service-container", got, "the post-update container is the service's, not a one-off")
	})

	t.Run("UpdateComposeServiceStreaming pre-update lookup", func(t *testing.T) {
		svc, stack := stubbedStatusService(t)
		fake := newOneOffFjd6Fake(stack.ProjectName)
		svc.updateClient = fake

		_, _, _, _ = svc.UpdateComposeServiceStreaming(context.Background(), stack, "web",
			func(LogLine) {}, func(Status) {})
		require.NotEmpty(t, fake.inspected, "the update inspects the pre-update container for its old image")
		assert.Equal(t, "service-container", fake.inspected[0],
			"the old image is read from the service container, not a one-off")
	})
}
