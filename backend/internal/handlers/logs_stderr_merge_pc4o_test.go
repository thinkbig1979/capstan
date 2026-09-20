package handlers

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-pc4o, the consequence half. These tests pin what the REAL
// parseLogLines does with a body in which a zero-exit stderr diagnostic has been
// merged into the container's own log lines, which is what services.Logs served
// it before that bead's fix.
//
// They do not model the merge, they perform it: one child process with both file
// descriptors pointed at one pipe via CombinedOutput, exactly the call
// services/docker.go:125 used to make. The interleaving below is therefore the
// OS's, not the test's.
//
// These pass BEFORE and AFTER the fix, deliberately. parseLogLine's tolerance is
// unchanged by it — a line with no pipe returning nil is pinned by
// TestParseLogLine's "line without pipe" case (logs_test.go:204) and is the same
// deliberate skip-a-bad-line behaviour parseComposePSOutput has. The fix splits
// the streams at the source instead. What these tests are for is to show that
// the source-side assertions in
// services/docker_logs_stderrsplit_pc4o_test.go are about fabricated and
// destroyed LOG ENTRIES, not merely about where some bytes ended up.

const (
	mergeRowWeb = `web-1  | 2026-09-20T10:00:00.000000000Z GET / 200`
	mergeRowDB  = `db-1   | 2026-09-20T10:00:01.000000000Z LOG:  checkpoint complete`
)

// mergedBody runs one child with stdout and stderr on a single pipe and returns
// the merged bytes, reproducing the pre-fix services.Logs read.
func mergedBody(t *testing.T, script string) string {
	t.Helper()
	//nolint:gosec // G204: `script` is a test fixture composed in this file from literals, never attacker-controlled; a shell is the point — these tests need one child writing to both fds in a fixed order
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	require.NoError(t, err)
	return string(out)
}

func lineContainers(lines []LogLine) []string {
	got := make([]string, 0, len(lines))
	for _, l := range lines {
		got = append(got, l.Container)
	}
	return got
}

// TestParseLogLines_MergedStderrBody_ControlIsClean is the control: the same
// harness with nothing on stderr yields exactly the two real containers. Without
// it, the fabrication below could be an artefact of the fixture.
func TestParseLogLines_MergedStderrBody_ControlIsClean(t *testing.T) {
	body := mergedBody(t, "printf '%s\\n' '"+mergeRowWeb+"'; printf '%s\\n' '"+mergeRowDB+"'; exit 0")

	assert.Equal(t, []string{"web-1", "db-1"}, lineContainers(parseLogLines(body, "")))
}

// TestParseLogLines_MergedStderrBody_FabricatesAContainer: a zero-exit stderr
// diagnostic containing a pipe becomes a third LogLine whose Container is the
// diagnostic's own text. It is returned to the browser as a log entry for a
// container that does not exist, and `?container=` can filter on it.
func TestParseLogLines_MergedStderrBody_FabricatesAContainer(t *testing.T) {
	body := mergedBody(t,
		"printf '%s\\n' '"+mergeRowWeb+"'; "+
			`printf '%s\n' 'time=2026-09-20T10:00:02Z level=warning msg="the attach|detach plugin is deprecated"' >&2; `+
			"printf '%s\\n' '"+mergeRowDB+"'; "+
			"exit 0")

	containers := lineContainers(parseLogLines(body, ""))
	require.Len(t, containers, 3, "merged body parses to three entries, not two")
	assert.Contains(t, containers,
		`time=2026-09-20T10:00:02Z level=warning msg="the attach`,
		"docker's own diagnostic is served as a container name")
}

// TestParseLogLines_MergedStderrBody_MisattributesARealEntry: an unterminated
// stderr write is glued onto the front of the next real row. The joined line
// still has the real row's pipe, so it is not dropped — the real entry is served
// under a fabricated container name, and vanishes from web-1's own filter.
func TestParseLogLines_MergedStderrBody_MisattributesARealEntry(t *testing.T) {
	body := mergedBody(t,
		`printf '%s' 'level=warning msg="compose plugin is deprecated"' >&2; `+
			"printf '%s\\n' '"+mergeRowWeb+"'; "+
			"printf '%s\\n' '"+mergeRowDB+"'; "+
			"exit 0")

	assert.Equal(t,
		[]string{`level=warning msg="compose plugin is deprecated"web-1`, "db-1"},
		lineContainers(parseLogLines(body, "")),
		"the real web-1 entry is re-keyed under a name that does not exist")
	assert.Empty(t, parseLogLines(body, "web-1"),
		"filtering on the real container now returns none of its lines")
}

// TestParseLogLines_MergedStderrBody_DropsAPipelessDiagnostic: a diagnostic with
// no pipe hits parseLogLine's len(parts) < 2 guard and is discarded. Merged into
// the body it is neither shown to the operator nor recorded anywhere.
func TestParseLogLines_MergedStderrBody_DropsAPipelessDiagnostic(t *testing.T) {
	body := mergedBody(t,
		"printf '%s\\n' '"+mergeRowWeb+"'; "+
			`printf '%s\n' 'WARNING: a plugin failed to load' >&2; `+
			"printf '%s\\n' '"+mergeRowDB+"'; "+
			"exit 0")

	assert.Contains(t, body, "WARNING: a plugin failed to load",
		"the diagnostic is in the body the parser is handed")
	assert.Equal(t, []string{"web-1", "db-1"}, lineContainers(parseLogLines(body, "")),
		"and the parser discards it without a trace")
}
