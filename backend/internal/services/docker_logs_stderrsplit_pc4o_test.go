package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-pc4o: DockerService.Logs read `docker compose logs --tail --timestamps`
// with cmd.CombinedOutput(), which points the child's stdout and stderr at ONE
// pipe. Anything the compose CLI, a CLI plugin or a `docker` wrapper wrote to
// stderr while still EXITING 0 therefore arrived inside the body that
// handlers.parseLogLines splits on newlines and turns into structured LogLine
// records keyed by container name (handlers/logs.go:86, :318, :338).
//
// The body is NOT free text for a human. parseLogLine does
// strings.SplitN(line, "|", 2) and returns nil when len(parts) < 2, so a merged
// stderr line either becomes a log entry ATTRIBUTED TO A CONTAINER THAT DOES NOT
// EXIST, or is SILENTLY DROPPED, or — written without a trailing newline —
// prefixes the next real stdout line and moves a REAL container's log entry
// under a fabricated name. handlers.TestParseLogLines_MergedStderrBody_*
// (handlers/logs_stderr_merge_pc4o_test.go) runs the real parser over a real
// OS-merged body and pins all three outcomes.
//
// These tests drive the real Logs() through the package's established
// execCommand indirection (exec_env.go) rather than a PATH wrapper, so the
// stdout/stderr contract of the actual call site is what is under test. They
// reuse stubComposeScript and stubbedStatusService from
// docker_status_stderrsplit_sl9z_test.go: the sibling bead built the same
// harness, and it is a DockerService with a temp-dir config plus a redirected
// execCommand, which is exactly what Logs() needs too.

// The two stdout rows stand in for the container's own output, in the shape
// `docker compose logs --timestamps` emits: "<container> | <RFC3339Nano> <msg>".
const (
	logRowWeb = `web-1  | 2026-09-20T10:00:00.000000000Z GET / 200`
	logRowDB  = `db-1   | 2026-09-20T10:00:01.000000000Z LOG:  checkpoint complete`
)

// bodyLines splits a Logs() body the way parseLogLines does and drops the empty
// trailing element, so a test can assert on the exact set of lines the parser
// will see.
func bodyLines(body string) []string {
	out := []string{}
	for _, l := range strings.Split(body, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestLogs_PositiveControl_NoStderr is the control: the same harness, the same
// two rows, nothing on stderr. It must pass both before and after the fix.
// Without it, a red in the three fault tests below could be the fixture's fault
// rather than the merge's.
func TestLogs_PositiveControl_NoStderr(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t, "printf '%s\\n' '"+logRowWeb+"'; printf '%s\\n' '"+logRowDB+"'; exit 0")

	body, err := svc.Logs(stack, 100)
	require.NoError(t, err)
	assert.Equal(t, []string{logRowWeb, logRowDB}, bodyLines(body))
}

// TestLogs_ZeroExitStderrWithPipe_DoesNotFabricateAContainerEntry covers the
// merge's worst outcome: a stderr diagnostic that happens to contain a pipe
// character. parseLogLine splits on the FIRST pipe and takes everything to its
// left as the container name, so such a line becomes a LogLine attributed to a
// container that does not exist — returned as JSON and filterable by that name.
//
// A pipe in a diagnostic needs no misconfiguration: logfmt and shell-pipeline
// advice both produce them routinely (`msg="use docker compose ps | jq"`), and
// exit code 0 throughout means nothing on this path has an error value to check.
func TestLogs_ZeroExitStderrWithPipe_DoesNotFabricateAContainerEntry(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t,
		"printf '%s\\n' '"+logRowWeb+"'; "+
			`printf '%s\n' 'time=2026-09-20T10:00:02Z level=warning msg="the attach|detach plugin is deprecated"' >&2; `+
			"printf '%s\\n' '"+logRowDB+"'; "+
			"exit 0")

	body, err := svc.Logs(stack, 100)
	require.NoError(t, err)

	// The defect, stated as the parser sees it: a third line whose pre-pipe
	// field becomes a container name nothing on this host is running.
	assert.Equal(t, []string{logRowWeb, logRowDB}, bodyLines(body),
		"a zero-exit stderr diagnostic containing a pipe must not reach the parser, "+
			"where its pre-pipe text becomes the Container of a fabricated log entry")
	assert.NotContains(t, body, "attach|detach")
}

// TestLogs_ZeroExitStderrUnterminated_DoesNotMisattributeARealEntry covers the
// sharper half: with one shared pipe a stderr write that does not end in a
// newline is CONCATENATED onto the front of the next stdout line. The joined
// line still contains the real row's pipe, so parseLogLine does not drop it —
// it takes the diagnostic text plus the real container name as the Container.
// A log entry the container really printed is therefore served under a name
// that does not exist, and disappears from its own container's filter.
func TestLogs_ZeroExitStderrUnterminated_DoesNotMisattributeARealEntry(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t,
		`printf '%s' 'level=warning msg="compose plugin is deprecated"' >&2; `+
			"printf '%s\\n' '"+logRowWeb+"'; "+
			"printf '%s\\n' '"+logRowDB+"'; "+
			"exit 0")

	body, err := svc.Logs(stack, 100)
	require.NoError(t, err)

	assert.Equal(t, []string{logRowWeb, logRowDB}, bodyLines(body),
		"an unterminated zero-exit stderr write must not be glued onto the next real "+
			"log line, which moves that line's entry under a fabricated container name")
}

// TestLogs_ZeroExitStderrWithoutPipe_IsNotSilentlyDroppedIntoTheBody covers the
// third outcome: a diagnostic with no pipe at all. parseLogLine's
// `len(parts) < 2 { return nil }` guard drops it without a trace, so merging it
// into the body is the worst of both worlds — it is not shown to the operator
// AND it is not recorded anywhere. The fix routes it to slog.Debug instead,
// which is the only place it can actually be read.
func TestLogs_ZeroExitStderrWithoutPipe_IsNotSilentlyDroppedIntoTheBody(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t,
		"printf '%s\\n' '"+logRowWeb+"'; "+
			`printf '%s\n' 'WARNING: a plugin failed to load' >&2; `+
			"printf '%s\\n' '"+logRowDB+"'; "+
			"exit 0")

	body, err := svc.Logs(stack, 100)
	require.NoError(t, err)

	assert.Equal(t, []string{logRowWeb, logRowDB}, bodyLines(body),
		"a pipe-less zero-exit diagnostic must not be merged into the body, where "+
			"parseLogLine's len(parts) < 2 guard discards it unseen and unlogged")
}

// TestLogs_NonZeroExit_ErrorUnchanged pins the exit-code-nonzero path across the
// fix: Logs must still return an empty body and the child's error VERBATIM,
// unwrapped, exactly as the CombinedOutput form did. It passes before and after;
// its job is to catch a regression in the half of the contract this bead does
// NOT change (handlers/logs.go:87 logs err and maps it through respondDockerErr).
func TestLogs_NonZeroExit_ErrorUnchanged(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t, `printf '%s\n' 'no configuration file provided' >&2; exit 1`)

	body, err := svc.Logs(stack, 100)
	require.Error(t, err)
	assert.Equal(t, "exit status 1", err.Error(),
		"the error must stay the child's own, unwrapped")
	assert.Empty(t, body)
}
