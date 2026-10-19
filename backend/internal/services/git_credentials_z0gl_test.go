package services

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// httpsOf is the HTTPS half of c, for the tests written against the
// (user, token) pair httpsCredentials used to return.
func httpsOf(c gitCreds) (user, token string) {
	return c.user, c.token
}

// envValues returns every value env holds for key, in order.
func envValues(env []string, key string) []string {
	var out []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			out = append(out, v)
		}
	}
	return out
}

// lastEnv is the value a child sees for key: os/exec keeps the last one.
func lastEnv(env []string, key string) string {
	v := envValues(env, key)
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// requireSSHCode requires err to be a 409 AppError with code; it returns
// the message.
func requireSSHCode(t *testing.T, err error, code string) string {
	t.Helper()
	var ae *models.AppError
	require.ErrorAs(t, err, &ae, "err %v", err)
	assert.Equal(t, http.StatusConflict, ae.Status, "message %q", ae.Message)
	assert.Equal(t, code, ae.Code, "message %q", ae.Message)
	return ae.Message
}

// writeKey writes a stand-in private key file at path with mode perm.
func writeKey(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("not a real key\n"), 0o600))
	require.NoError(t, os.Chmod(path, perm))
}

func TestZ0gl_GitCmdWithCreds_SSHKeyEnv(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	c := gitCreds{sshKey: "/keys/a b/id%h", sshKeySource: "x", knownHosts: "/data dir/known_hosts"}
	cmd, _ := svc.gitCmdWithCreds(context.Background(), "/repo", c, "pull", "--ff-only")

	// ojdu's hardening prefix stays first, untouched (D158).
	wantPrefix := append([]string{"git", "-c", "safe.directory=/repo"}, gitHardeningArgs...)
	require.GreaterOrEqual(t, len(cmd.Args), len(wantPrefix))
	assert.Equal(t, wantPrefix, cmd.Args[:len(wantPrefix)])
	assert.Equal(t, []string{"-c", "ssh.variant=ssh", "pull", "--ff-only"}, cmd.Args[len(wantPrefix):])

	assert.Equal(t, `ssh -F /dev/null -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes`+
		` -o StrictHostKeyChecking=accept-new -o GlobalKnownHostsFile=/dev/null`+
		` -o "IdentityFile=\"$CAPSTAN_GIT_SSH_KEY\"" -o "UserKnownHostsFile=\"$CAPSTAN_GIT_SSH_KNOWN_HOSTS\""`,
		lastEnv(cmd.Env, "GIT_SSH_COMMAND"))
	assert.NotContains(t, gitSSHCommand, " -i ", "a missing -i key falls back to the default identities")
	assert.Equal(t, "ssh", lastEnv(cmd.Env, "GIT_SSH_VARIANT"))
	assert.Equal(t, "/keys/a b/id%%h", lastEnv(cmd.Env, sshEnvKey), "%% keeps ssh from expanding %h")
	assert.Equal(t, "/data dir/known_hosts", lastEnv(cmd.Env, sshEnvKnownHosts))
	for _, a := range cmd.Args {
		assert.NotContains(t, a, "/keys/", "the key path must not reach argv")
	}
}

func TestZ0gl_GitCmdWithCreds_SSHErrFailsClosed(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /inherited")
	svc := NewGitService(&config.Config{}, nil)
	c := gitCreds{sshKey: "/k", sshErr: errors.New("unusable")}
	cmd, _ := svc.gitCmdWithCreds(context.Background(), "/repo", c, "status")

	assert.Equal(t, gitSSHRefused, lastEnv(cmd.Env, "GIT_SSH_COMMAND"), "an inherited command must not win")
	assert.Empty(t, envValues(cmd.Env, sshEnvKey))
	assert.Contains(t, cmd.Args, "ssh.variant=ssh")
}

func TestZ0gl_GitCmdWithCreds_NoKeyNoSSHEnv(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	cmd, _ := svc.gitCmdWithCreds(context.Background(), "/repo", gitCreds{}, "status")
	assert.NotContains(t, cmd.Env, "GIT_SSH_COMMAND="+gitSSHCommand)
	assert.NotContains(t, cmd.Env, "GIT_SSH_COMMAND="+gitSSHRefused)
	assert.Empty(t, envValues(cmd.Env, sshEnvKey))
	assert.NotContains(t, cmd.Args, "ssh.variant=ssh")
}

func TestZ0gl_SSHPathProblem(t *testing.T) {
	for p, bad := range map[string]bool{
		"/keys/id_ed25519":    false,
		"/keys/with space/id": false,
		"/keys/50%/id":        false,
		"/keys/$x/id":         false, // a bare $ stays literal (OBSERVED)
		"/keys/it's/id":       false,
		"keys/id":             true,
		"~/.ssh/id":           true,
		"/keys/a\"b":          true,
		"/keys/a\\b":          true,
		"/keys/${HOME}/id":    true,
		"/keys/a\nb":          true,
		"/keys/a\x00b":        true,
		"/keys/a\tb":          true,
		"":                    true,
	} {
		assert.Equal(t, bad, sshPathProblem(p) != "", "path %q: %q", p, sshPathProblem(p))
	}
	assert.Equal(t, "/a%%b%%%%c", sshEscape("/a%b%%c"))
}

// z0glService is a GitService over a real database with DataDir set and a
// home whose built-in default key path is under t.TempDir.
func z0glService(t *testing.T) (*GitService, *database.DB, *config.Config) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()
	db, err := database.NewWithMigrationsAndEncryptor(dataDir, NewTokenEncryptorOrDefault("", "test-secret-key-for-unit-tests-32c"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{DataDir: dataDir}
	return NewGitService(cfg, db), db, cfg
}

func z0glDir(t *testing.T, db *database.DB, authType, key string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: dir, Name: filepath.Base(dir)}))
	require.NoError(t, db.UpdateDirectoryCredentials(dir, authType, key, "", ""))
	return dir
}

func TestZ0gl_GitCredentials_Resolution(t *testing.T) {
	t.Run("ssh dir uses its own key, known_hosts in the data dir", func(t *testing.T) {
		svc, db, cfg := z0glService(t)
		require.NoError(t, db.SetSetting("git_ssh_key", "/global/key"))
		dir := z0glDir(t, db, "ssh", "/dir/key")
		c := svc.gitCredentials(dir)
		require.NoError(t, c.sshErr)
		assert.Equal(t, "/dir/key", c.sshKey)
		assert.Equal(t, filepath.Join(cfg.DataDir, "known_hosts"), c.knownHosts)
		assert.Empty(t, c.token)
	})
	t.Run("ssh dir without a key path refuses, never the global key", func(t *testing.T) {
		svc, db, _ := z0glService(t)
		require.NoError(t, db.SetSetting("git_ssh_key", "/global/key"))
		c := svc.gitCredentials(z0glDir(t, db, "ssh", ""))
		requireSSHCode(t, c.sshErr, models.ErrGitSSHKeyUnusable)
		assert.Empty(t, c.sshKey)
	})
	t.Run("https dir gets no ssh key even with a global one", func(t *testing.T) {
		svc, db, _ := z0glService(t)
		require.NoError(t, db.SetSetting("git_ssh_key", "/global/key"))
		c := svc.gitCredentials(z0glDir(t, db, "https", "/dir/key"))
		assert.NoError(t, c.sshErr)
		assert.Empty(t, c.sshKey)
	})
	for _, authType := range []string{"inherit", ""} {
		t.Run("inherit dir ("+authType+") uses the global setting", func(t *testing.T) {
			svc, db, cfg := z0glService(t)
			cfg.GitSSHKey = "/cfg/key"
			require.NoError(t, db.SetSetting("git_ssh_key", "/global/key"))
			c := svc.gitCredentials(z0glDir(t, db, authType, ""))
			require.NoError(t, c.sshErr)
			assert.Equal(t, "/global/key", c.sshKey)
			assert.Contains(t, c.sshKeySource, "global")
		})
	}
	t.Run("no row: cfg.GitSSHKey from GIT_SSH_KEY applies", func(t *testing.T) {
		svc, _, cfg := z0glService(t)
		cfg.GitSSHKey = "/cfg/key"
		c := svc.gitCredentials(t.TempDir())
		require.NoError(t, c.sshErr)
		assert.Equal(t, "/cfg/key", c.sshKey)
	})
	t.Run("D164: built-in default path, missing: no key applies", func(t *testing.T) {
		svc, _, cfg := z0glService(t)
		cfg.GitSSHKey = config.DefaultGitSSHKey()
		c := svc.gitCredentials(t.TempDir())
		assert.NoError(t, c.sshErr)
		assert.Empty(t, c.sshKey)
	})
	t.Run("D164: the settings page saving the default back is still the default", func(t *testing.T) {
		svc, db, _ := z0glService(t)
		require.NoError(t, db.SetSetting("git_ssh_key", config.DefaultGitSSHKey()))
		c := svc.gitCredentials(t.TempDir())
		assert.NoError(t, c.sshErr)
		assert.Empty(t, c.sshKey)
	})
	t.Run("D164: built-in default path, present: it applies", func(t *testing.T) {
		svc, _, cfg := z0glService(t)
		cfg.GitSSHKey = config.DefaultGitSSHKey()
		writeKey(t, cfg.GitSSHKey, 0o600)
		c := svc.gitCredentials(t.TempDir())
		require.NoError(t, c.sshErr)
		assert.Equal(t, config.DefaultGitSSHKey(), c.sshKey)
		assert.NoError(t, c.sshReady())
	})
	t.Run("D164: a custom global path, missing: refused", func(t *testing.T) {
		svc, db, _ := z0glService(t)
		require.NoError(t, db.SetSetting("git_ssh_key", filepath.Join(t.TempDir(), "gone")))
		c := svc.gitCredentials(t.TempDir())
		require.NoError(t, c.sshErr)
		msg := requireSSHCode(t, c.sshReady(), models.ErrGitSSHKeyUnusable)
		assert.Contains(t, msg, "global SSH key")
		assert.Contains(t, msg, "does not exist")
	})
	t.Run("empty DataDir refuses ssh, never /known_hosts", func(t *testing.T) {
		svc, db, cfg := z0glService(t)
		cfg.DataDir = ""
		c := svc.gitCredentials(z0glDir(t, db, "ssh", "/dir/key"))
		require.Error(t, c.sshErr)
		assert.Contains(t, c.sshErr.Error(), "no data directory")
		assert.Empty(t, c.knownHosts)
		cmd, _ := svc.gitCmdWithCreds(context.Background(), "/repo", c, "pull")
		assert.Equal(t, gitSSHRefused, lastEnv(cmd.Env, "GIT_SSH_COMMAND"))
	})
	t.Run("relative DataDir becomes absolute", func(t *testing.T) {
		svc, db, cfg := z0glService(t)
		cfg.DataDir = "rel-data"
		c := svc.gitCredentials(z0glDir(t, db, "ssh", "/dir/key"))
		require.NoError(t, c.sshErr)
		assert.True(t, filepath.IsAbs(c.knownHosts), c.knownHosts)
	})
	t.Run("a key path ssh would misread is refused", func(t *testing.T) {
		svc, db, _ := z0glService(t)
		for _, key := range []string{`/k/a"b`, "~/.ssh/id", "/k/${HOME}"} {
			c := svc.gitCredentials(z0glDir(t, db, "ssh", key))
			requireSSHCode(t, c.sshErr, models.ErrGitSSHKeyUnusable)
		}
	})
	t.Run("an unreadable global setting fails closed", func(t *testing.T) {
		db, hide, _ := hiddenSettingsDB(t)
		svc := NewGitService(&config.Config{DataDir: t.TempDir(), GitSSHKey: "/cfg/key"}, db)
		hide()
		c := svc.gitCredentials("/stacks/never-registered")
		require.Error(t, c.sshErr)
		assert.Contains(t, c.sshErr.Error(), "global git SSH key setting")
		assert.Empty(t, c.sshKey, "a fault must not fall back to cfg.GitSSHKey")
	})
}

func TestZ0gl_SSHReady(t *testing.T) {
	dir := t.TempDir()
	c := func(key string) gitCreds { return gitCreds{sshKey: key, sshKeySource: "This directory's SSH key"} }

	good := filepath.Join(dir, "good")
	writeKey(t, good, 0o600)
	assert.NoError(t, c(good).sshReady())

	msg := requireSSHCode(t, c(filepath.Join(dir, "missing")).sshReady(), models.ErrGitSSHKeyUnusable)
	assert.Contains(t, msg, "mount the key file")

	for _, perm := range []os.FileMode{0o644, 0o640, 0o604} {
		loose := filepath.Join(dir, "loose"+perm.String())
		writeKey(t, loose, perm)
		msg = requireSSHCode(t, c(loose).sshReady(), models.ErrGitSSHKeyUnusable)
		assert.Contains(t, msg, "chmod 600", "mode %v", perm)
	}

	msg = requireSSHCode(t, c(dir).sshReady(), models.ErrGitSSHKeyUnusable)
	assert.Contains(t, msg, "not a regular file")

	if os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		writeKey(t, locked, 0o000)
		msg = requireSSHCode(t, c(locked).sshReady(), models.ErrGitSSHKeyUnusable)
		assert.Contains(t, msg, "chown")
	}

	assert.NoError(t, gitCreds{}.sshReady(), "no key applies: nothing to check")
	err := errors.New("boom")
	assert.Equal(t, err, gitCreds{sshKey: good, sshErr: err}.sshReady())
}

func TestZ0gl_IsSSHRemoteURL(t *testing.T) {
	for u, want := range map[string]bool{
		"ssh://git@h/r.git": true, "SSH://h/r": true, "git+ssh://h/r": true, "ssh+git://h/r": true,
		"git@h:r.git": true, "h:r.git": true,
		"https://h/r.git": false, "file:///r": false, "/srv/r.git": false, "/srv/a:b/r.git": false,
		"../r.git": false, "ext::ssh -c x": false, "git://h/r": false, "origin": false,
	} {
		assert.Equal(t, want, isSSHRemoteURL(u), "url %q", u)
	}
}

// pullFix is a commitFix whose origin has one commit the work tree lacks.
func pullFix(t *testing.T) *commitFix {
	t.Helper()
	f := newCommitFix(t, false)
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(other), "clone", "-q", f.bare, other)
	writeFile(t, filepath.Join(other, "new.txt"), "n\n")
	runGit(t, other, "add", "new.txt")
	runGit(t, other, "-c", "user.name=o", "-c", "user.email=o@o.invalid", "commit", "-q", "-m", "ahead")
	runGit(t, other, "push", "-q", "origin", "main")
	f.svc.config.DataDir = f.dataDir
	return f
}

func TestZ0gl_PullPrecheck(t *testing.T) {
	t.Run("an ssh remote with a missing key is refused before the pull", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "ssh", filepath.Join(t.TempDir(), "missing"), "", ""))
		runGit(t, f.top, "remote", "set-url", "origin", "git@example.invalid:o/r.git")
		record := recordingGitOnPath(t)

		_, err := f.svc.pullCLI(f.dir)
		requireSSHCode(t, err, models.ErrGitSSHKeyUnusable)
		calls, rerr := os.ReadFile(record) //nolint:gosec // test-owned record file under t.TempDir()
		require.NoError(t, rerr)
		assert.Contains(t, string(calls), "ls-remote --get-url", "the instrument saw the probe")
		assert.NotContains(t, string(calls), "pull --ff-only")
	})
	t.Run("insteadOf turning an https url into ssh is still caught", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "ssh", filepath.Join(t.TempDir(), "missing"), "", ""))
		runGit(t, f.top, "remote", "set-url", "origin", "https://example.invalid/o/r.git")
		runGit(t, f.top, "config", "url.ssh://git@example.invalid/.insteadOf", "https://example.invalid/")
		_, err := f.svc.pullCLI(f.dir)
		requireSSHCode(t, err, models.ErrGitSSHKeyUnusable)
	})
	t.Run("a non-ssh remote is not refused for a bad ssh key", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "ssh", filepath.Join(t.TempDir(), "missing"), "", ""))
		res, err := f.svc.pullCLI(f.dir)
		require.NoError(t, err)
		assert.NotEqual(t, res.PreviousCommit, res.CurrentCommit, "%+v", res)
	})
	t.Run("local reads keep working with a broken key", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "ssh", `/bad"path`, "", ""))
		runGit(t, f.top, "remote", "set-url", "origin", "git@example.invalid:o/r.git")
		st, err := f.svc.GetStatus(f.dir)
		require.NoError(t, err)
		assert.Equal(t, "main", st.Branch)
		_, err = f.svc.GetLog(f.dir, 10, 0)
		require.NoError(t, err)
	})
}
