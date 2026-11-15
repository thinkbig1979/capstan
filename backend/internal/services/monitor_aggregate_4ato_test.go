package services

import (
	"testing"

	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/assert"
)

// agent-os-4ato: one container's lifecycle event set the WHOLE stack's status.
// stackEventFor mapped a single container's die to stack_status "stopped" (and
// start to "running"), and useStackEvents wrote that into the stacks-list row,
// so a 3-service stack with one dead service read "stopped" (truth: partial)
// and one service starting on a stopped stack read "running".
//
// An event names one container; the aggregate needs every container of the
// project, which only the stack list computes (BuildStackStatuses). So every
// lifecycle action now reaches the client as a container_event, which makes it
// refetch the list and the stack, as one-offs already did (agent-os-febs).
//
// This drives ListenEvents with events from two different services of one
// project; a real multi-container Docker probe is not possible at this level.
func TestMonitor4ato_OneServiceEventDoesNotSetTheStackStatus(t *testing.T) {
	got := eventsFor(t,
		projectEvent("aaaaaaaaaaaa4a01", events.ActionDie, "False"),   // web dies, db and cache still run
		projectEvent("aaaaaaaaaaaa4a02", events.ActionStart, "False"), // a stopped stack: one service starts
		projectEvent("aaaaaaaaaaaa4a03", events.ActionPause, "False"), // one of three paused
		projectEvent("aaaaaaaaaaaa4a04", events.ActionKill, "False"),
		projectEvent("aaaaaaaaaaaa4a05", events.ActionStop, "False"),
		projectEvent("aaaaaaaaaaaa4a06", events.ActionRestart, "False"),
		projectEvent("aaaaaaaaaaaa4a07", events.ActionUnPause, "False"),
	)
	want := make([]string, 7)
	for i := range want {
		want[i] = "container_event|shop|"
	}
	assert.Equal(t, want, got,
		"one container's action must reach the client as a container_event (a refetch of the aggregate), never as the whole stack's status")
}
