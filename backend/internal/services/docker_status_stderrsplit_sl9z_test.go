package services

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-sl9z: DockerService.Status read `docker compose ps --format json`
// with cmd.CombinedOutput(), which points the child's stdout and stderr at ONE
// pipe. Anything the compose CLI, a CLI plugin or a `docker` wrapper wrote to
// stderr while still EXITING 0 therefore arrived inside the NDJSON that
// parseComposePSOutput splits on newlines and json.Unmarshals line by line.
//
// These tests drive the real Status() through the package's established
// execCommand indirection (exec_env.go) rather than a PATH wrapper, so the
// stdout/stderr contract of the actual call site is what is under test.

// stubComposeScript redirects execCommand at `sh -c <script>`, ignoring the
// real ("docker", "compose", ...) argv. The script stands in for the compose
// child: it writes the NDJSON rows the parser is supposed to see, plus whatever
// the fault puts on stderr.
func stubComposeScript(t *testing.T, script string) {
	t.Helper()
	orig := execCommand
	execCommand = func(name string, arg ...string) *exec.Cmd {
		//nolint:gosec // G204: `script` is a const-composed test fixture built in this file from literals, never attacker-controlled; a shell is the point — these tests need one child writing to both fds in a fixed order
		return exec.Command("sh", "-c", script)
	}
	t.Cleanup(func() { execCommand = orig })
}

func stubbedStatusService(t *testing.T) (*DockerService, models.Stack) {
	t.Helper()
	dir := t.TempDir()
	svc := &DockerService{config: &config.Config{StacksDir: dir, DataDir: dir}}
	stack := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "proj"}
	return svc, stack
}

func containerNames(containers []models.Container) []string {
	names := make([]string, 0, len(containers))
	for _, c := range containers {
		names = append(names, c.Name)
	}
	return names
}

const (
	rowWeb = `{"ID":"abc","Name":"proj-web-1","Service":"web","State":"running","Health":"","Image":"nginx:latest","Ports":""}`
	rowDB  = `{"ID":"def","Name":"proj-db-1","Service":"db","State":"running","Health":"healthy","Image":"postgres:15","Ports":""}`
)

// TestStatus_PositiveControl_NoStderr is the control: the same harness, the
// same two rows, nothing on stderr. It must pass both before and after the
// fix. Without it, a red in the two fault tests below could be the fixture's
// fault rather than the merge's.
func TestStatus_PositiveControl_NoStderr(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t, "printf '%s\\n' '"+rowWeb+"'; printf '%s\\n' '"+rowDB+"'; exit 0")

	status, containers, err := svc.Status(stack)
	require.NoError(t, err)
	assert.Equal(t, "running", status)
	assert.Equal(t, []string{"proj-web-1", "proj-db-1"}, containerNames(containers))
}

// TestStatus_ZeroExitStderrJSONLine_DoesNotFabricateAContainer covers the
// merge's worst outcome: a stderr diagnostic that is itself valid JSON.
// json.Unmarshal into the compose-ps struct IGNORES unknown fields, so such a
// line does not fail and is not skipped — it unmarshals cleanly into a record
// with every field empty. The result is a PHANTOM CONTAINER in the returned
// list and, because its State is "" rather than "running", an aggregate status
// of "partial" for a stack whose containers are all running.
//
// Structured JSON logging on stderr is the realistic vector: a credential
// helper, a BuildKit/compose plugin or a site `docker` wrapper emitting one
// JSON log line is enough, with no misconfiguration and exit code 0.
func TestStatus_ZeroExitStderrJSONLine_DoesNotFabricateAContainer(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t,
		"printf '%s\\n' '"+rowWeb+"'; "+
			"printf '%s\\n' '"+rowDB+"'; "+
			`printf '%s\n' '{"level":"warning","msg":"credential helper not found, falling back"}' >&2; `+
			"exit 0")

	status, containers, err := svc.Status(stack)
	require.NoError(t, err)

	// The defect: a third, empty container record built out of a log line.
	assert.Equal(t, []string{"proj-web-1", "proj-db-1"}, containerNames(containers),
		"a stderr JSON log line must not become a container row")
	assert.Equal(t, "running", status,
		"a stderr JSON log line must not drag the aggregate status off running")
}

// TestStatus_ZeroExitStderrPartialLine_DoesNotDestroyAContainerRow covers the
// other half of the merge: with one shared pipe, a stderr write that does not
// end in a newline is CONCATENATED onto the front of the next stdout line. That
// row then fails json.Unmarshal and parseComposePSOutput silently `continue`s
// past it, so a container that is running disappears from the list entirely.
func TestStatus_ZeroExitStderrPartialLine_DoesNotDestroyAContainerRow(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t,
		"printf '%s\\n' '"+rowWeb+"'; "+
			`printf '%s' 'level=warning msg="compose plugin is deprecated"' >&2; `+
			"printf '%s\\n' '"+rowDB+"'; "+
			"exit 0")

	status, containers, err := svc.Status(stack)
	require.NoError(t, err)

	assert.Equal(t, []string{"proj-web-1", "proj-db-1"}, containerNames(containers),
		"an unterminated stderr write must not swallow the next NDJSON row")
	assert.Equal(t, "running", status)
}

// TestStatus_NonZeroExit_ErrorUnchanged pins the exit-code-nonzero path across
// the fix: Status must still return the same wrapped error and no data. It
// passes before and after; its job is to catch a regression in the half of the
// contract this bead does NOT change.
func TestStatus_NonZeroExit_ErrorUnchanged(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposeScript(t, `printf '%s\n' 'no configuration file provided' >&2; exit 1`)

	status, containers, err := svc.Status(stack)
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "docker compose ps failed: "),
		"got %q", err.Error())
	assert.Empty(t, status)
	assert.Nil(t, containers)
}
