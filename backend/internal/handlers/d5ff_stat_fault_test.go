package handlers

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-d5ff. (*LogsHandler).buildComposeArgs (logs.go:289, stat site :305)
// is a SECOND COPY of (*DockerService).buildComposeArgs (services/docker.go:284,
// stat site :312) — same
// name, same shape, the global.env path built from a different field
// (h.dataDir vs s.config.DataDir). Both dropped --env-file on any stat error,
// so fixing one and not the other would have left half the class live. These
// tests are the handlers-side half; services/d5ff_stat_fault_test.go carries
// the other half and the full rationale for the fixture.
//
// Fixture note, restated because it is the thing most likely to be "improved"
// back into a bug: this is ENOTDIR (a regular file used as a path component),
// NOT `chmod 000`. chmod is a no-op for root, so a chmod-based red arm does not
// arm wherever the suite runs as root — see
// integrationtest/compose_env_test.go:571-573, which has to skip itself for
// exactly that reason. ENOTDIR is structural and no uid can defeat it.

func d5ffCaptureSlogH(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func d5ffNotADirH(t *testing.T, parent, name string) string {
	t.Helper()
	p := filepath.Join(parent, name)
	if err := os.WriteFile(p, []byte("this is a regular file, not a directory"), 0o600); err != nil {
		t.Fatalf("seeding the ENOTDIR fixture: %v", err)
	}
	return p
}

func d5ffEnvFileValuesH(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--env-file" && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

func d5ffContainsH(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// d5ffLogsHandler builds a LogsHandler with a non-nil docker service, which is
// the condition logs.go:292 requires before it looks at global.env at all. The
// zero-value *services.DockerService is enough: buildComposeArgs only tests the
// pointer for nil and never calls through it.
func d5ffLogsHandler(dataDir string) *LogsHandler {
	return &LogsHandler{docker: &services.DockerService{}, dataDir: dataDir}
}

func d5ffStackH() models.Stack {
	return models.Stack{
		ID:          "root~test-stack:default",
		Directory:   "/srv/stacks/test-stack",
		ComposeFile: "compose.yaml",
		ProjectName: "test-stack-default",
	}
}

// TestLogsHandler_buildComposeArgs_GlobalEnvStatFault is the RED arm for the
// second copy. State is asserted before logs so the failure names the actual
// args slice rather than only "expected a log line".
func TestLogsHandler_buildComposeArgs_GlobalEnvStatFault(t *testing.T) {
	logs := d5ffCaptureSlogH(t)
	dataDir := d5ffNotADirH(t, t.TempDir(), "datadir")
	globalEnvPath := dataDir + "/global.env"

	args := d5ffLogsHandler(dataDir).buildComposeArgs(d5ffStackH(), "logs", []string{"-f"})

	if !d5ffContainsH(d5ffEnvFileValuesH(args), globalEnvPath) {
		t.Errorf("a global.env that could not be stat'd (ENOTDIR) was silently dropped from the compose command.\n"+
			"--env-file values = %q\nfull args = %q\nwant %q to be present so compose refuses instead of streaming logs from a differently-configured stack",
			d5ffEnvFileValuesH(args), args, globalEnvPath)
	}

	out := logs.String()
	if !strings.Contains(out, "ERROR") || !strings.Contains(out, globalEnvPath) || !strings.Contains(out, "not a directory") {
		t.Errorf("the stat fault was not logged at ERROR with its cause.\ncaptured logs = %q\nwant an ERROR line naming %q and \"not a directory\"", out, globalEnvPath)
	}
}

// TestLogsHandler_buildComposeArgs_GlobalEnvAbsent is the control: a plain
// ENOENT is the normal state of an install with no global.env and must stay
// byte-for-byte silent.
func TestLogsHandler_buildComposeArgs_GlobalEnvAbsent(t *testing.T) {
	logs := d5ffCaptureSlogH(t)
	dataDir := t.TempDir()
	globalEnvPath := filepath.Join(dataDir, "global.env")

	args := d5ffLogsHandler(dataDir).buildComposeArgs(d5ffStackH(), "logs", []string{"-f"})

	if d5ffContainsH(d5ffEnvFileValuesH(args), globalEnvPath) {
		t.Errorf("an absent global.env must not be passed to compose.\n--env-file values = %q\nfull args = %q", d5ffEnvFileValuesH(args), args)
	}
	if out := logs.String(); out != "" {
		t.Errorf("an absent global.env is the normal state and must log nothing.\ncaptured logs = %q", out)
	}
}

// TestLogsHandler_buildComposeArgs_GlobalEnvPresent is the second control: the
// healthy path is unchanged and silent.
func TestLogsHandler_buildComposeArgs_GlobalEnvPresent(t *testing.T) {
	logs := d5ffCaptureSlogH(t)
	dataDir := t.TempDir()
	globalEnvPath := filepath.Join(dataDir, "global.env")
	if err := os.WriteFile(globalEnvPath, []byte("TEST=value\n"), 0o600); err != nil {
		t.Fatalf("seeding global.env: %v", err)
	}

	args := d5ffLogsHandler(dataDir).buildComposeArgs(d5ffStackH(), "logs", []string{"-f"})

	if !d5ffContainsH(d5ffEnvFileValuesH(args), globalEnvPath) {
		t.Errorf("a readable global.env must still be passed to compose.\n--env-file values = %q\nfull args = %q", d5ffEnvFileValuesH(args), args)
	}
	if out := logs.String(); out != "" {
		t.Errorf("a readable global.env must log nothing.\ncaptured logs = %q", out)
	}
}

// TestLogsHandler_buildComposeArgs_NilDocker pins the surrounding guard at
// logs.go:292: with no docker service there is no env-file handling at all, and
// the fix must not start stat-ing global.env in that state.
func TestLogsHandler_buildComposeArgs_NilDocker(t *testing.T) {
	logs := d5ffCaptureSlogH(t)
	dataDir := d5ffNotADirH(t, t.TempDir(), "datadir")

	h := &LogsHandler{dataDir: dataDir} // docker is nil
	args := h.buildComposeArgs(d5ffStackH(), "logs", []string{"-f"})

	if vals := d5ffEnvFileValuesH(args); len(vals) != 0 {
		t.Errorf("with a nil docker service no --env-file may be added at all, got %q\nfull args = %q", vals, args)
	}
	if out := logs.String(); out != "" {
		t.Errorf("with a nil docker service global.env is never consulted, so nothing may be logged.\ncaptured logs = %q", out)
	}
}
