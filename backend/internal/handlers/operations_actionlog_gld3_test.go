package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// scriptedStreamer replays a fixed done frame per compose subcommand, so a test
// picks which phase of a run fails.
type scriptedStreamer struct {
	done map[string]services.StreamLine
}

func (s *scriptedStreamer) RunStreaming(_ context.Context, _ models.Stack, subcommand string, _ []string) <-chan services.StreamLine {
	out := make(chan services.StreamLine, 2)
	out <- services.StreamLine{Type: "output", Line: subcommand + " running"}
	out <- s.done[subcommand]
	close(out)
	return out
}

var (
	doneOK     = services.StreamLine{Type: "done", Success: true, Outcome: truth.OutcomeSuccess}
	doneFailed = services.StreamLine{Type: "done", Success: false, Outcome: truth.OutcomeFailed, Reason: "compose exited 1"}
)

// TestOperationsActionLog is agent-os-gld3's acceptance: a lifecycle action run
// over the operations WebSocket writes exactly one action-log row for its stack,
// naming the action and its outcome, as the REST lifecycle routes already do.
func TestOperationsActionLog(t *testing.T) {
	cases := []struct {
		name        string
		action      string
		done        map[string]services.StreamLine
		wantOutcome truth.Outcome
	}{
		{"pull succeeds", "pull", map[string]services.StreamLine{"pull": doneOK}, truth.OutcomeSuccess},
		{"start fails", "start", map[string]services.StreamLine{"up": doneFailed}, truth.OutcomeFailed},
		// The early return when the stop phase fails is its own exit path.
		{"restart fails at stop", "restart", map[string]services.StreamLine{"down": doneFailed, "up": doneOK}, truth.OutcomeFailed},
		{"restart succeeds", "restart", map[string]services.StreamLine{"down": doneOK, "up": doneOK}, truth.OutcomeSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newOperationsFixtureWith(t, NewConnectionManager(5), &scriptedStreamer{done: tc.done})

			dialOperations(t, srv, "stack-a", tc.action)

			rows, err := db.GetActionsByStack("stack-a", 10)
			require.NoError(t, err)
			require.Len(t, rows, 1, "one run writes exactly one action-log row")
			require.Equal(t, tc.action, rows[0].Action)

			var detail struct {
				Outcome truth.Outcome `json:"outcome"`
			}
			require.NoError(t, json.Unmarshal([]byte(rows[0].Detail), &detail), "detail: %s", rows[0].Detail)
			require.Equal(t, tc.wantOutcome, detail.Outcome, "detail: %s", rows[0].Detail)
		})
	}
}
