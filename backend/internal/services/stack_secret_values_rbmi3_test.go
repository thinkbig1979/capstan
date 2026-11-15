package services

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-rbmi round-2 review F1, F3-F6: the stack's secret list keeps what
// it can read when one compose entry can't be resolved, never blocks on a
// file that is not a regular one, and warns once per path.

// One entry the walk cannot resolve (an interpolated env_file, a remote
// include, an include outside the stack) drops only that entry: every other
// env_file, and the inline values of the other compose files, stay.
func TestStackSecretValues_OneUnresolvableEntryKeepsTheOthers(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"interpolated env_file in another service": {
			"compose.yaml": "services:\n  a:\n    env_file: s.env\n  b:\n    env_file: ${STAGE:-prod}.env\n",
			"s.env":        "API_TOKEN=keptTok16qq\n",
		},
		"remote include": {
			"compose.yaml": "include:\n  - oci://x/y\nservices:\n  a:\n    env_file: s.env\n",
			"s.env":        "API_TOKEN=keptTok16qq\n",
		},
		"include with project_directory beside a readable include": {
			"compose.yaml":     "include:\n  - path: other.yaml\n    project_directory: ..\n  - sub/compose.yaml\n",
			"other.yaml":       "services:\n  o:\n    image: x\n",
			"sub/compose.yaml": "services:\n  b:\n    env_file: s.env\n    environment:\n      DB_PASSWORD: keptInline17qq\n",
			"sub/s.env":        "API_TOKEN=keptTok16qq\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeStackFiles(t, files)
			got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}, "")
			require.Contains(t, got, "keptTok16qq", "the readable env_file's value was dropped with the bad entry")
			if strings.Contains(files["sub/compose.yaml"], "keptInline17qq") {
				require.Contains(t, got, "keptInline17qq", "the included file's inline value was dropped with the bad entry")
			}
		})
	}
}

// An include outside the stack is not read, and the inline values of the
// files that are inside still count.
func TestStackSecretValues_IncludeOutsideKeepsTheInsideFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "out.yaml"),
		[]byte("services:\n  z:\n    environment:\n      DB_PASSWORD: outsideInc32qq\n"), 0o600))
	dir := filepath.Join(root, "stack")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("include:\n  - ../out.yaml\n  - sub/compose.yaml\nservices:\n  a:\n    env_file: in.env\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "compose.yaml"),
		[]byte("services:\n  b:\n    environment:\n      DB_PASSWORD: insideInc33qq\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.env"), []byte("API_TOKEN=insideTok33qq\n"), 0o600))

	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}, "")
	require.NotContains(t, got, "outsideInc32qq", "a compose file outside the stack is never read")
	require.Contains(t, got, "insideTok33qq")
	require.Contains(t, got, "insideInc33qq")
}

// A service env_file that is a FIFO is never opened: opening one for reading
// blocks until a writer appears, and the list is built on every log tick.
func TestStackSecretValues_FIFOEnvFileDoesNotBlock(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": "services:\n  a:\n    env_file: [fifo.env, s.env]\n",
		"s.env":        "API_TOKEN=besideFifo41qq\n",
	})
	fifo := filepath.Join(dir, "fifo.env")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	done := make(chan []string, 1)
	go func() {
		done <- StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}, "")
	}()
	select {
	case got := <-done:
		require.Contains(t, got, "besideFifo41qq", "CONTROL: the regular env_file beside it is read")
	case <-time.After(3 * time.Second):
		// The blocked reader is left behind: a writer opened here does not
		// release it on the base, which opens the FIFO a second time.
		t.Fatal("StackSecretValues blocked on a FIFO env_file for 3s")
	}
}

// An env_file over the compose read cap is not read whole on every tick.
func TestStackSecretValues_OversizedEnvFileIsSkipped(t *testing.T) {
	big := "API_TOKEN=bigFileTok51qq\n" + strings.Repeat("# pad\n", (maxComposeBytes/6)+1)
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": "services:\n  a:\n    env_file: [big.env, s.env]\n",
		"big.env":      big,
		"s.env":        "API_TOKEN=smallTok52qq\n",
	})
	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}, "")
	require.NotContains(t, got, "bigFileTok51qq")
	require.Contains(t, got, "smallTok52qq", "CONTROL: the small env_file is read")
}

// F5: a value written with spaces around it is printed trimmed by most apps.
func TestStackSecretValues_InlineValueTrimmedFormToo(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": "services:\n  a:\n    environment:\n      DB_PASSWORD: \"  trimPw14qq  \"\n",
	})
	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}, "")
	require.Contains(t, got, "trimPw14qq")
}

// F6: a top-level `secrets: file:` holds one raw value, not KEY=value lines.
func TestStackSecretValues_SecretsFileContent(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": "services:\n  a:\n    secrets: [db]\nsecrets:\n  db:\n    file: ./db.txt\n",
		"db.txt":       "secretFileRaw20qq\n",
	})
	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}, "")
	require.Contains(t, got, "secretFileRaw20qq")
}

// F4: the list is rebuilt every 5 s per open log stream; a file it skips is
// warned about once per stack, not on every rebuild.
func TestStackSecretValues_WarnsOncePerStackAndPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "out.env"), []byte("API_TOKEN=x\n"), 0o600))
	dir := filepath.Join(root, "stack")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  a:\n    env_file: ../out.env\n  b:\n    env_file: ${STAGE}.env\n"), 0o600))

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	stack := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "warnonce-" + filepath.Base(root)}
	for range 3 {
		StackSecretValues(stack, "")
	}
	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "out.env"), "the outside env_file is warned about once:\n%s", out)
	require.Equal(t, 1, strings.Count(out, "not a fixed path"), "the unresolvable entry is warned about once:\n%s", out)

	// CONTROL: another stack with the same files is a new warning.
	buf.Reset()
	stack.ProjectName += "-other"
	StackSecretValues(stack, "")
	require.Equal(t, 1, strings.Count(buf.String(), "out.env"))
}
