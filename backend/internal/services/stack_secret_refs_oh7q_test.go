package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-oh7q: a secret in a container label was not in the list the
// locked logs and inspect mask with. Labels are read as Env is.
func TestContainerSecretValues_oh7q_Labels(t *testing.T) {
	inspect := container.InspectResponse{Config: &container.Config{Labels: map[string]string{
		"DB_PASSWORD":            "lAbel99xq",
		"traefik.http.api_key":   "lAbel88xq",
		"backup.target":          "s3:https://KEY:lAbel77xq@minio/b",
		"com.docker.compose.svc": "web",
	}}}
	values := containerSecretValues(inspect)
	for _, s := range []string{"lAbel99xq", "lAbel88xq", "lAbel77xq"} {
		out, _ := MaskSecrets("app printed "+s+" here", "", values)
		require.NotContains(t, out, s, "%q", values)
	}
	require.NotContains(t, values, "web")
}

// A build arg leaves no trace in the container, so a sensitive one written
// as a reference (`DB_PASSWORD: ${PW}`, PW under an ordinary name in .env)
// was masked nowhere: the reference's resolved value is now in the list.
// The literal forms were already in it (pinned here).
func TestStackSecretValues_oh7q_ReferencedSensitiveValues(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("PW=bArgRef99x\nTOK=bArgRef88x\nLBL=lAbRef77x\nSHORT=ab\nPLAIN=plainVis99\n"), 0o600))
	compose := `services:
  a:
    image: x
    build:
      context: .
      args:
        DB_PASSWORD: ${PW}
        PLAIN_ARG: ${PLAIN}
        LIT_PASSWORD: bArgLit66x
    labels:
      - "x.api_token=${TOK}"
      - "y.secret=$LBL"
      - "z.password=${SHORT:?set it}"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o600))
	values := StackSecretValues(models.Stack{Directory: dir, ComposeFile: filepath.Join(dir, "docker-compose.yml"), ProjectName: "p"}, "")
	for _, s := range []string{"bArgRef99x", "bArgRef88x", "lAbRef77x", "bArgLit66x"} {
		require.Contains(t, values, s)
	}
	require.NotContains(t, values, "plainVis99", "a reference under an ordinary key stays out")
	require.NotContains(t, values, "ab", "the short-value filter still applies")
}
