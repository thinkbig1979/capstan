package services

import (
	"context"
	"encoding/json"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-rnba: composePSOneOff split `compose ps`'s Labels string on
// commas, so a user label VALUE holding ",com.docker.compose.oneoff=True" made
// a running service container read as a one-off and drop out of the status.
// Producer: the comma split of Labels. Consumer: parseComposePSOutput's
// container list and composeStackStatus.

// composePSLine renders one `compose ps` row the way compose v5 does for the
// --format value in argv: "json" prints the row with Labels as compose's
// comma-joined k=v string (sorted, values unescaped); a Go template that reads
// .Label is evaluated against the label map. Any other format fails the test,
// so a fixture never answers an argv real compose was not probed with.
func composePSLine(t *testing.T, argv []string, row map[string]any, labels map[string]string) string {
	t.Helper()
	format := ""
	if i := slices.Index(argv, "--format"); i >= 0 && i+1 < len(argv) {
		format = argv[i+1]
	}
	pairs := make([]string, 0, len(labels))
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		pairs = append(pairs, k+"="+labels[k])
	}
	full := maps.Clone(row)
	full["Labels"] = strings.Join(pairs, ",")
	rowJSON, err := json.Marshal(full)
	require.NoError(t, err)

	switch format {
	case "json":
		return string(rowJSON)
	case composePSFormat:
		oneOff, err := json.Marshal(labels["com.docker.compose.oneoff"])
		require.NoError(t, err)
		return `{"row":` + string(rowJSON) + `,"oneoff":` + string(oneOff) + `}`
	}
	t.Fatalf("compose ps fixture got --format %q, which no probe of real compose covers", format)
	return ""
}

// stubComposePS stands in for the docker binary: for `ps` it prints the lines
// rows returns for the argv it got; every other subcommand exits 0 silently.
func stubComposePS(t *testing.T, rows func(argv []string) []string) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		if !slices.Contains(arg, "ps") {
			return exec.CommandContext(ctx, "true")
		}
		out := strings.Join(rows(arg), "\n") + "\n"
		//nolint:gosec // G204: a test fixture's own output, passed as an argument, not a shell string
		return exec.CommandContext(ctx, "printf", "%s", out)
	}
	t.Cleanup(func() { execCommandContext = orig })
}

// The row rv-status captured with real compose v5.7.1: a running service
// container whose user label value holds the one-off fragment.
func TestStatus_UserLabelValueHoldingTheOneOffFragmentIsAServiceContainer_Rnba(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposePS(t, func(argv []string) []string {
		return []string{composePSLine(t, argv,
			map[string]any{"ID": "abc", "Name": "rvstatusprobe7c-web-1", "Service": "web", "State": "running", "Image": "nginx"},
			map[string]string{
				"com.docker.compose.project": "rvstatusprobe7c",
				"com.docker.compose.service": "web",
				"com.docker.compose.oneoff":  "False",
				"note":                       "x,com.docker.compose.oneoff=True",
			})}
	})

	status, containers, err := svc.Status(stack)
	require.NoError(t, err)
	assert.Equal(t, []string{"rvstatusprobe7c-web-1"}, containerNames(containers), "a service container must be listed whatever its label values hold")
	assert.Equal(t, "running", status)
}

// Control: a real one-off is still left out.
func TestStatus_RealOneOffIsStillLeftOut_Rnba(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	stubComposePS(t, func(argv []string) []string {
		return []string{
			composePSLine(t, argv,
				map[string]any{"ID": "a", "Name": "p-web-1", "Service": "web", "State": "exited", "Image": "nginx"},
				map[string]string{"com.docker.compose.project": "p", "com.docker.compose.oneoff": "False"}),
			composePSLine(t, argv,
				map[string]any{"ID": "b", "Name": "p-web-run-1", "Service": "web", "State": "running", "Image": "nginx"},
				map[string]string{"com.docker.compose.project": "p", "com.docker.compose.oneoff": "True"}),
		}
	})

	status, containers, err := svc.Status(stack)
	require.NoError(t, err)
	assert.Equal(t, []string{"p-web-1"}, containerNames(containers))
	assert.Equal(t, "stopped", status, "a running one-off does not make a stopped stack partial")
}

// asComposePSFormat turns `--format json` rows into composePSFormat rows of a
// service container, for parser fixtures written in the old shape. A line
// that is not a JSON object is passed through, so a junk-line fixture stays junk.
func asComposePSFormat(ndjson string) string {
	lines := strings.Split(ndjson, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "{") {
			lines[i] = `{"row":` + l + `,"oneoff":"False"}`
		}
	}
	return strings.Join(lines, "\n")
}
