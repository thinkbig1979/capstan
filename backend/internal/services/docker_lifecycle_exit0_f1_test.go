package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-rpbx review F1, Edwin's ruling (2026-11-15). Status lists exited
// containers since rpbx (compose ps -a), so a one-shot init service that ran
// and exited 0 was counted as "not running" and every start or restart of its
// stack verified as partial. In start and restart verification such a
// container is done, not failed. A non-zero exit still counts as failed, and an
// exit code compose did not report counts as failed (fail closed). The stack
// status (composeStackStatus, BuildStackStatuses) is unchanged.
//
// compose v5.7.1 ps -a JSON, probed 2026-11-15 (project wkdmstatusprobe2,
// removed by name): {'Name': 'wkdmstatusprobe2-init-1', 'State': 'exited',
// 'ExitCode': 0, ...}, {'Name': 'wkdmstatusprobe2-bad-1', 'State': 'exited',
// 'ExitCode': 1, ...}.

func f1Exit(code int) *int { return &code }

func TestClassifyStart_InitExitedZeroIsDone_F1(t *testing.T) {
	for _, action := range []lifecycleAction{actionStart, actionRestart} {
		ar := classifyContainers(action, nil, "partial", []models.Container{
			{Name: "init", State: "exited", ExitCode: f1Exit(0)},
			{Name: "web", State: "running"},
		}, nil)
		assert.Equal(t, truth.OutcomeSuccess, ar.Outcome, "%s: an init container that exited 0 is done: %q", action, ar.Reason)
	}
}

func TestClassifyStart_NonZeroOrUnknownExitStillCounts_F1(t *testing.T) {
	cases := map[string]*int{"exit 1": f1Exit(1), "exit code not reported": nil}
	for name, code := range cases {
		ar := classifyContainers(actionStart, nil, "partial", []models.Container{
			{Name: "init", State: "exited", ExitCode: code},
			{Name: "web", State: "running"},
		}, nil)
		assert.Equal(t, truth.OutcomePartial, ar.Outcome, "%s: %q", name, ar.Reason)
	}
}

func TestClassifyStart_EveryContainerExitedZero_F1(t *testing.T) {
	ar := classifyContainers(actionStart, nil, "stopped", []models.Container{
		{Name: "job-a", State: "exited", ExitCode: f1Exit(0)},
		{Name: "job-b", State: "exited", ExitCode: f1Exit(0)},
	}, nil)
	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome, ar.Reason)
}

// The dwell still runs for the long-running service: an init container that
// exited 0 must not fail the first snapshot fast, and a service that crashes
// (exit 1) inside the dwell window is still caught.
func TestPollUntilSettled_DwellStillCatchesCrashBesideInit_F1(t *testing.T) {
	initDone := models.Container{Name: "init", State: "exited", ExitCode: f1Exit(0)}
	svc := &DockerService{statusFn: scriptedStatus([]statusSnap{
		{"partial", []models.Container{initDone, {Name: "web", State: "running"}}},                     // poll 1
		{"stopped", []models.Container{initDone, {Name: "web", State: "exited", ExitCode: f1Exit(1)}}}, // poll 2: crashed
	})}
	p := settleParams{pollInterval: 20 * time.Millisecond, dwellTarget: 5 * time.Second, maxWait: 10 * time.Second}

	status, final := svc.pollUntilSettledWithParams(models.Stack{}, "partial",
		[]models.Container{initDone, {Name: "web", State: "running"}}, p)
	ar := classifyContainers(actionStart, nil, status, final, nil)

	assert.Equal(t, truth.OutcomeFailed, ar.Outcome, "the crashed service must be caught, not the init container's first snapshot returned: %q", ar.Reason)
	require.Len(t, final, 2)
	assert.Equal(t, "exited", final[1].State, "the loop ran past the first snapshot")
}

func TestPollUntilSettled_InitExitedZeroThenStableIsSuccess_F1(t *testing.T) {
	snap := []models.Container{{Name: "init", State: "exited", ExitCode: f1Exit(0)}, {Name: "web", State: "running"}}
	svc := &DockerService{statusFn: scriptedStatus([]statusSnap{{"partial", snap}})}
	p := settleParams{pollInterval: 20 * time.Millisecond, dwellTarget: 100 * time.Millisecond, maxWait: 5 * time.Second}

	status, final := svc.pollUntilSettledWithParams(models.Stack{}, "partial", snap, p)
	ar := classifyContainers(actionStart, nil, status, final, nil)
	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome, ar.Reason)
}

func TestParseComposePS_CarriesExitCode_F1(t *testing.T) {
	_, containers, err := parseComposePSOutput([]byte(
		`{"Name":"p-init-1","State":"exited","ExitCode":0}` + "\n" +
			`{"Name":"p-bad-1","State":"exited","ExitCode":1}` + "\n" +
			`{"Name":"p-old-1","State":"exited"}` + "\n"))
	require.NoError(t, err)
	require.Len(t, containers, 3)
	require.NotNil(t, containers[0].ExitCode)
	assert.Equal(t, 0, *containers[0].ExitCode)
	require.NotNil(t, containers[1].ExitCode)
	assert.Equal(t, 1, *containers[1].ExitCode)
	assert.Nil(t, containers[2].ExitCode, "no ExitCode in the row means unknown, not 0")
}
