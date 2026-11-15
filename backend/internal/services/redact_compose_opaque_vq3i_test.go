package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-vq3i: an opaque URL credential (sftp:user:pw@host:/p) under a key
// that is not sensitive was in no secret list, and the free-text URL scan
// needs "://", so compose output showed it.

func TestRedactComposeOutput_vq3i_OpaqueCredentialFromEnvFileHidden(t *testing.T) {
	// The value, and the env file that sets it. user:pw@host has no scheme,
	// so only the secret list can hide it.
	for value, env := range map[string]string{
		"sftp:user:SEKRITvq3i@host:/p": "BACKUP=\"sftp:user:SEKRITvq3i@host:/p\"\n",
		"sftp:user:SEKRITvq3i@host:/q": "BACKUP=sftp:user:SEKRITvq3i@host:/q\n",
		"user:SEKRITvq3i@host":         "BACKUP=user:SEKRITvq3i@host\n",
		"user:SEKRITvq3i@nas:/p":       "TZ=UTC\nBAD KEY=1\nBACKUP=user:SEKRITvq3i@nas:/p\n",
	} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600))
		secrets := StackSecretValues(models.Stack{Directory: dir, ProjectName: "p"}, "")
		// compose echoes the interpolated value, in a command line say.
		out := redactComposeOutput("rclone copy "+value+" failed", secrets)
		assert.NotContains(t, out, "SEKRITvq3i", "env %q, secrets %q", env, secrets)
	}
}

// An opaque credential named by no env file (a literal in the compose file,
// or a message quoting one) is starred by its sftp/s3 grammar.
func TestRedactURLUserinfoInText_vq3i_OpaqueCredentialInFreeText(t *testing.T) {
	for in, want := range map[string]string{
		"ssh: sftp:user:SEKRITvq3i@host:/p failed": "ssh: sftp:***@host:/p failed",
		"repo=s3:KEY:SEKRITvq3i@minio/bucket ok":   "repo=s3:***@minio/bucket ok",
		`"SFTP:user:SEKRITvq3i@host:/p"`:           `"SFTP:***@host:/p"`,
		// Left alone, as RedactURLUserinfo leaves them (agent-os-qonw).
		"sftp:user@host:/backups/nginx@sha256:abc": "sftp:user@host:/backups/nginx@sha256:abc",
		"rclone:gdrive:edwin@example.com":          "rclone:gdrive:edwin@example.com",
		"xsftp:user:pw@host:/p":                    "xsftp:user:pw@host:/p",
	} {
		assert.Equal(t, want, RedactURLUserinfoInText(in), "%q", in)
		masked, _ := MaskSecrets(in, "", nil)
		if want != in {
			assert.NotContains(t, masked, "SEKRITvq3i", "MaskSecrets %q", in)
			assert.True(t, HasURLUserinfo(in), "HasURLUserinfo %q", in)
		}
	}
}
