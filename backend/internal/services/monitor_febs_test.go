package services

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-febs: one-off containers (`docker compose run`) are shown apart and
// do not count toward a stack's status (agent-os-dn1z), but the live paths
// still counted them. Producer: Docker's events and container lists for a
// compose project, which include oneoff=True containers. Consumers:
// stackEventFor's stack_status emit (written into the stacks list by
// useStackEvents) and GetContainersForStack (the stack metrics stream).

func projectEvent(id string, action events.Action, oneOff string) events.Message {
	return events.Message{
		Type:   events.ContainerEventType,
		Action: action,
		Actor: events.Actor{ID: id, Attributes: map[string]string{
			"com.docker.compose.project": "shop",
			"com.docker.compose.oneoff":  oneOff,
		}},
		Time: time.Now().Unix(),
	}
}

func eventsFor(t *testing.T, msgs ...events.Message) []string {
	t.Helper()
	src := &fakeEventsSource{steps: []fakeEventsStep{{msgs: msgs}}}
	svc := &MonitorService{events: src, wait: (&recordingWait{}).wait}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := svc.ListenEvents(ctx)
	require.NoError(t, err)
	got := make([]string, 0, len(msgs))
	for range msgs {
		ev := nextEvent(t, ch)
		got = append(got, ev.Type+"|"+ev.StackID+"|"+ev.Status)
	}
	return got
}

func TestMonitorFebs_OneOffLifecycleEventDoesNotSetTheStackStatus(t *testing.T) {
	got := eventsFor(t,
		projectEvent("aaaaaaaaaaaa0001", events.ActionDie, "True"),
		projectEvent("aaaaaaaaaaaa0002", events.ActionStart, "True"),
	)
	assert.Equal(t, []string{"container_event|shop|", "container_event|shop|"}, got,
		"a one-off's die/start must reach the stack as a container_event (a list refetch), never as a stack_status")
}

func TestMonitorFebs_ServiceContainerEventStillSetsTheStackStatus(t *testing.T) {
	got := eventsFor(t, projectEvent("aaaaaaaaaaaa0003", events.ActionDie, "False"))
	assert.Equal(t, []string{"stack_status|shop|stopped"}, got, "control: a service container's die still sets the status")
}

func TestMonitorFebs_GetContainersForStackLeavesOneOffsOut(t *testing.T) {
	d := fakeDaemonService(t, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, `[
			{"Id":"svc1","Names":["/shop-web-1"],"State":"running","Labels":{"com.docker.compose.project":"shop","com.docker.compose.oneoff":"False"}},
			{"Id":"run1","Names":["/shop-web-run-1"],"State":"running","Labels":{"com.docker.compose.project":"shop","com.docker.compose.oneoff":"True"}}
		]`)
	})
	m := &MonitorService{client: d.client}

	ids, err := m.GetContainersForStack(context.Background(), "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"svc1"}, ids, "the stack's metrics must not include a `compose run` container")
}

func TestDockerFebs_ServiceListLeavesOneOffsOutAndTheCountingListKeepsThem(t *testing.T) {
	d := fakeDaemonService(t, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, `[
			{"Id":"svc1","Names":["/shop-web-1"],"State":"running","Labels":{"com.docker.compose.project":"shop","com.docker.compose.oneoff":"False"}},
			{"Id":"run1","Names":["/shop-web-run-1"],"State":"running","Labels":{"com.docker.compose.project":"shop","com.docker.compose.oneoff":"True"}}
		]`)
	})

	services, err := d.GetServiceContainerList(context.Background(), "shop")
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, "shop-web-1", services[0].Name, "a stack-scoped list must not offer a `compose run` container")

	all, err := d.GetContainerListCtx(context.Background(), "shop")
	require.NoError(t, err)
	assert.Len(t, all, 2, "Delete and the scanner count one-offs: removing the row would hide them too")
}
