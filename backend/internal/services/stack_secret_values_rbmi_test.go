package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-rbmi review F1: the stack's secret list must hold what the locked
// compose view hides, so the views that mask by value (container logs,
// inspect) hide it too.

func writeStackFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	return dir
}

func TestStackSecretValues_ComposeInlineValuesAndServiceEnvFiles(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": `services:
  app:
    image: app:1
    environment:
      DB_PASSWORD: inlinePw77zz
      TZ: Europe/Amsterdam
    env_file: secrets.env
  worker:
    image: app:1
    environment:
      - SMTP_PASSWORD=listPw33kk
      - LOG_LEVEL=debug
    env_file:
      - ./more.env
`,
		"secrets.env": "API_TOKEN=svcEnvTok99qq\n",
		"more.env":    "SECRET_KEY=moreEnv88jj\n",
	})
	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml"}, "")
	for source, v := range map[string]string{
		"inline environment map":  "inlinePw77zz",
		"inline environment list": "listPw33kk",
		"service env_file string": "svcEnvTok99qq",
		"service env_file list":   "moreEnv88jj",
	} {
		require.Contains(t, got, v, "the %s value is missing from the list", source)
	}
	require.NotContains(t, got, "Europe/Amsterdam", "a value under a non-sensitive key is not a secret")
	require.NotContains(t, got, "debug")
}

// A reference holds no secret: the list keeps the value it points at, from
// the env files, not the "${...}" text.
func TestStackSecretValues_ComposeReferenceIsNotAValue(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    environment:\n      DB_PASSWORD: ${DB_PASSWORD}\n",
		".env":         "DB_PASSWORD=fromDotEnv44gg\n",
	})
	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml"}, "")
	require.Contains(t, got, "fromDotEnv44gg")
	require.NotContains(t, got, "${DB_PASSWORD}")
}

// The compose view masks a file that does not parse line by line; the list
// still learns its KEY: value lines.
func TestStackSecretValues_UnparseableComposeStillYieldsItsValues(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    environment:\n      DB_PASSWORD: brokenYaml55\n    bad: [unclosed\n",
	})
	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml"}, "")
	require.Contains(t, got, "brokenYaml55")
}

// An env_file outside the stack folder is not read: a compose file must not
// make Capstan open any file the server can.
func TestStackSecretValues_EnvFileOutsideTheStackIsNotRead(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.env")
	require.NoError(t, os.WriteFile(outside, []byte("API_TOKEN=outsideTok66hh\n"), 0o600))
	dir := filepath.Join(root, "stack")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  app:\n    env_file:\n      - ../outside.env\n      - inside.env\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inside.env"), []byte("API_TOKEN=insideTok22ss\n"), 0o600))

	got := StackSecretValues(models.Stack{Directory: dir, ComposeFile: "compose.yaml"}, "")
	require.NotContains(t, got, "outsideTok66hh")
	require.Contains(t, got, "insideTok22ss", "CONTROL: the inside env_file is read")
}
