package services

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-sdbr, agent-os-zrm2, agent-os-jbnx: follow-ups of agent-os-fvk3.
// The streaming compose paths (RunStreaming, streamComposeCmd), the
// updateComposeContainer errors and Logs' zero-exit debug line all carried
// compose output past redactComposeOutput. These reuse fvk3's planted secrets
// (fvk3Service, fvk3Script, assertFvk3Redacted), which write every secret to
// stdout and the diagnosis to stderr. The zrm2 and jbnx tests live in their
// own files and share the helpers below.

// stubComposeContextScript redirects execCommandContext at `sh -c <script>`
// and passes the real compose argv through as "$@", so a script can fail only
// the pull or only the up of a two-step update.
func stubComposeContextScript(t *testing.T, script string) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		//nolint:gosec // G204: `script` is a test fixture composed from literals in this package; a shell is the point, one child writing to both fds
		return exec.CommandContext(ctx, "sh", append([]string{"-c", script, "sh"}, arg...)...)
	}
	t.Cleanup(func() { execCommandContext = orig })
}

// sdbrStepScript, when the compose subcommand is verb, writes fvk3's secret
// line to stderr AND stdout, then the diagnosis to stderr, and exits with code.
// Any other subcommand exits 0 silently. The subshell's copy is the stderr one:
// fvk3Script alone puts the secrets on stdout only, which cannot tell a fix
// that covers both pipes from one that covers stdout.
func sdbrStepScript(verb, code string) string {
	return `case " $* " in *" ` + verb + ` "*) ( ` + fvk3Script("0") + ` ) 1>&2 2>/dev/null; ` +
		fvk3Script(code) + ` ;; esac; exit 0`
}

// sdbrRedactedURL is what the stderr copy of the secret line must carry once
// redacted: proof that line was emitted, not dropped.
const sdbrRedactedURL = "https://***@registry.example.com/app"

func TestRunStreaming_RedactsEveryLine(t *testing.T) {
	svc, stack := fvk3Service(t, "")
	stubComposeContextScript(t, sdbrStepScript("pull", "1"))

	var lines []string
	for l := range svc.RunStreaming(context.Background(), stack, "pull", nil) {
		if l.Type == "data" {
			lines = append(lines, l.Line)
		}
	}
	require.Len(t, lines, 3, "expected the secret line from each pipe and the diagnosis: %q", lines)
	withURL := 0
	for _, l := range lines {
		if strings.Contains(l, sdbrRedactedURL) {
			withURL++
		}
	}
	assert.Equal(t, 2, withURL, "the secret line must arrive, redacted, from both pipes: %q", lines)
	for _, l := range lines {
		for _, secret := range []string{fvk3StackSecret, fvk3GlobalSecret, fvk3URLPassword} {
			assert.NotContains(t, l, secret, "streamed line leaks a planted secret")
		}
	}
	assertFvk3Redacted(t, "streamed lines", strings.Join(lines, "\n"))
}

func TestStreamComposeCmd_RedactsEveryLine(t *testing.T) {
	for _, verb := range []string{"pull", "up"} {
		t.Run(verb, func(t *testing.T) {
			svc, stack := fvk3Service(t, "")
			stubComposeContextScript(t, sdbrStepScript(verb, "1"))

			var lines []LogLine
			emit := func(l LogLine) { lines = append(lines, l) }
			err := svc.updateComposeContainerStreaming(context.Background(), stack, "web", true, emit, func(Status) {})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "compose "+verb+" failed")

			var compose []string
			for _, l := range lines {
				if l.Stream == StreamStatus {
					continue
				}
				for _, secret := range []string{fvk3StackSecret, fvk3GlobalSecret, fvk3URLPassword} {
					assert.NotContains(t, l.Text, secret, "%s line leaks a planted secret", l.Stream)
				}
				compose = append(compose, l.Text)
			}
			require.Len(t, compose, 3, "expected the secret line from each pipe and the diagnosis: %q", compose)
			for _, st := range []LogLineStream{StreamStdout, StreamStderr} {
				found := false
				for _, l := range lines {
					found = found || (l.Stream == st && strings.Contains(l.Text, sdbrRedactedURL))
				}
				assert.True(t, found, "no redacted secret line on %s: %+v", st, lines)
			}
			assertFvk3Redacted(t, "emitted lines", strings.Join(compose, "\n"))
		})
	}
}
