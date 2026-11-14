package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-hnys: the operations WebSocket persists the stack status the REST
// verbs persist, which is the verified end state in ar.Details["status"]. The
// done frame has to carry it out of RunStreaming, or a successful stop could
// only be stored as the Outcome fallback ("running").
func TestRunStreaming_DoneCarriesVerifiedStatus_Hnys(t *testing.T) {
	stubArgsChild00y3(t, `case "$*" in *" down"*) echo "Container p-web-1  Removed"; exit 0;; *) exit 0;; esac`)
	svc, stack := exitParity00y3Fixture(t)

	restAR, _ := svc.StopVerified(stack)
	require.Equal(t, truth.OutcomeSuccess, restAR.Outcome, "%+v", restAR)
	want, _ := restAR.Details["status"].(string)
	require.Equal(t, string(StackStatusStopped), want, "REST is the reference: %+v", restAR)

	done, all := lastStreamLine00y3(t, svc.RunStreaming(context.Background(), stack, "down", nil))
	require.Equal(t, "done", done.Type, "frames: %+v", all)
	assert.Equal(t, want, done.Status, "the WS done frame must carry the status REST persists")
}

// The status is for the server only: the done frame's wire shape is unchanged.
func TestStreamLine_StatusIsNotOnTheWire_Hnys(t *testing.T) {
	b, err := json.Marshal(StreamLine{Type: "done", Success: true, Outcome: truth.OutcomeSuccess, Status: "stopped"})
	require.NoError(t, err)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(b, &frame))
	assert.NotContains(t, frame, "status", "frame: %s", b)
	assert.Equal(t, "done", frame["type"], "control: the frame did marshal: %s", b)
}
