package services

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// Behavioural arms for agent-os-z0gl: real ssh against an unprivileged sshd
// on a loopback port. An argv assertion cannot tell -o IdentityFile from -i,
// and only -i falls back to the default identities (OBSERVED), so these run
// the real client.
//
// They skip when /usr/sbin/sshd or ssh-keygen is missing. The self-hosted CI
// runners were not checked for either, so on a runner without them these
// arms are skipped, not passed.

const sshdBinary = "/usr/sbin/sshd"

type testSSHD struct {
	dir, hostKey, authKeys, logPath string
	port                            int
	cmd                             *exec.Cmd
}

func requireSSHTools(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(sshdBinary); err != nil {
		t.Skipf("no %s on this host: the behavioural ssh arms did not run", sshdBinary)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen on this host: the behavioural ssh arms did not run")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh client on this host: the behavioural ssh arms did not run")
	}
}

// sshKeygen writes a fresh unencrypted ed25519 key pair at path.
func sshKeygen(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	//nolint:gosec // test helper, explicit argv
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "z0gl", "-f", path).CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// newTestSSHD prepares an sshd that authorizes the public keys in pubKeys.
func newTestSSHD(t *testing.T, pubKeys ...string) *testSSHD {
	t.Helper()
	d := &testSSHD{dir: t.TempDir(), port: freePort(t)}
	d.hostKey = filepath.Join(d.dir, "host_a")
	sshKeygen(t, d.hostKey)
	d.authKeys = filepath.Join(d.dir, "authorized_keys")
	var keys []string
	for _, p := range pubKeys {
		b, err := os.ReadFile(p) //nolint:gosec // test-owned key under t.TempDir()
		require.NoError(t, err)
		keys = append(keys, strings.TrimSpace(string(b)))
	}
	//nolint:gosec // test-owned path under t.TempDir()
	require.NoError(t, os.WriteFile(d.authKeys, []byte(strings.Join(keys, "\n")+"\n"), 0o600))
	return d
}

// start runs sshd with the given host key until stop or the test ends.
func (d *testSSHD) start(t *testing.T, hostKey string) {
	t.Helper()
	cfg := filepath.Join(d.dir, "sshd_config")
	body := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nAuthorizedKeysFile %s\nPidFile %s\n"+
		"StrictModes no\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nLogLevel DEBUG1\n",
		d.port, hostKey, d.authKeys, filepath.Join(d.dir, "sshd.pid"))
	require.NoError(t, os.WriteFile(cfg, []byte(body), 0o600))
	d.logPath = filepath.Join(d.dir, "sshd-"+filepath.Base(hostKey)+".log")
	logFile, err := os.Create(d.logPath) //nolint:gosec // test-owned log under t.TempDir()
	require.NoError(t, err)
	//nolint:gosec // test helper, explicit argv
	d.cmd = exec.Command(sshdBinary, "-D", "-e", "-f", cfg)
	d.cmd.Stderr = logFile
	require.NoError(t, d.cmd.Start())
	t.Cleanup(func() {
		d.stop(t)
		require.NoError(t, logFile.Close())
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(d.port), 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		if time.Now().After(deadline) {
			log, rerr := os.ReadFile(d.logPath) //nolint:gosec // test-owned log
			t.Fatalf("sshd did not listen on %d: %v\n%s (log read: %v)", d.port, err, log, rerr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (d *testSSHD) stop(t *testing.T) {
	t.Helper()
	if d.cmd == nil {
		return
	}
	if err := d.cmd.Process.Kill(); err != nil {
		t.Logf("killing sshd: %v", err)
	}
	// Wait reports the kill; reaping the process is the point.
	if err := d.cmd.Wait(); err != nil {
		t.Logf("sshd exit: %v", err)
	}
	d.cmd = nil
}

// log returns sshd's log once the connection it served has been cleaned up.
func (d *testSSHD) log(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, err := os.ReadFile(d.logPath)
		require.NoError(t, err)
		s := string(b)
		if strings.Contains(s, "do_cleanup") || strings.Contains(s, "Connection closed") || time.Now().After(deadline) {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// keyOffers counts the public keys the client offered (one sshd debug
// line per key it was asked about).
func keyOffers(log string) int {
	return strings.Count(log, "userauth_pubkey: publickey test pkalg")
}

func (d *testSSHD) url(t *testing.T, repo string) string {
	t.Helper()
	u, err := user.Current()
	require.NoError(t, err)
	return fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", u.Username, d.port, repo)
}

// defaultIdentities lists the passwd home's default keys: what a -i
// fallback would offer. ssh reads the passwd home, not $HOME (OBSERVED).
func defaultIdentities(t *testing.T) []string {
	t.Helper()
	u, err := user.Current()
	require.NoError(t, err)
	var found []string
	for _, n := range []string{"id_rsa", "id_ecdsa", "id_ecdsa_sk", "id_ed25519", "id_ed25519_sk"} {
		if _, err := os.Stat(filepath.Join(u.HomeDir, ".ssh", n)); err == nil {
			found = append(found, n)
		}
	}
	return found
}

// Paths with a space and a % exercise the three quoting layers end to end.
func z0glSSHPaths(t *testing.T) (keyPath, dataDir string) {
	t.Helper()
	base := t.TempDir()
	keyPath = filepath.Join(base, "key dir %h", "id ed25519")
	sshKeygen(t, keyPath)
	dataDir = filepath.Join(base, "data dir %d")
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	return keyPath, dataDir
}

func TestZ0gl_SSH_MissingKeyOffersNoDefaultIdentity(t *testing.T) {
	requireSSHTools(t)
	keyPath, dataDir := z0glSSHPaths(t)
	d := newTestSSHD(t, keyPath+".pub")
	d.start(t, d.hostKey)
	repo := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, filepath.Dir(repo), "init", "-q", "--bare", repo)
	svc, _, cfg := z0glService(t)
	cfg.DataDir = dataDir
	work := t.TempDir()

	if ids := defaultIdentities(t); len(ids) == 0 {
		t.Log("NOTE: no default identity in the passwd home, so a -i fallback would offer nothing here: " +
			"the missing-key arm below cannot tell -i from -o IdentityFile on this host")
	} else {
		t.Logf("default identities a -i fallback would offer: %v", ids)
	}

	present := gitCreds{sshKey: keyPath, sshKeySource: "k", knownHosts: filepath.Join(dataDir, "known_hosts")}
	missing := present
	missing.sshKey = filepath.Join(filepath.Dir(keyPath), "deleted")

	// The key vanished after any pre-check: ssh must offer nothing at all.
	_, err := svc.gitCommandWithCreds(work, missing, "ls-remote", d.url(t, repo))
	assert.Error(t, err, "a connection with no key must not authenticate")
	log := d.log(t)
	assert.Equal(t, 0, keyOffers(log), "a missing key fell back to other identities:\n%s", log)

	// Positive control, same instrument: the key is present, offered and
	// accepted, and the host key is recorded in the data dir.
	_, err = svc.gitCommandWithCreds(work, present, "ls-remote", d.url(t, repo))
	require.NoError(t, err)
	log2 := d.log(t)
	assert.Equal(t, 1, keyOffers(log2)-keyOffers(log), "the present key is the one key offered")
	kh, err := os.ReadFile(present.knownHosts)
	require.NoError(t, err, "accept-new did not write known_hosts in the data dir")
	assert.Contains(t, string(kh), "[127.0.0.1]:"+strconv.Itoa(d.port))
}

func TestZ0gl_SSH_ChangedHostKeyRefused(t *testing.T) {
	requireSSHTools(t)
	keyPath, dataDir := z0glSSHPaths(t)
	d := newTestSSHD(t, keyPath+".pub")
	repo := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, filepath.Dir(repo), "init", "-q", "--bare", repo)
	svc, _, cfg := z0glService(t)
	cfg.DataDir = dataDir
	c := gitCreds{sshKey: keyPath, sshKeySource: "k", knownHosts: filepath.Join(dataDir, "known_hosts")}
	work := t.TempDir()

	d.start(t, d.hostKey)
	_, err := svc.gitCommandWithCreds(work, c, "ls-remote", d.url(t, repo))
	require.NoError(t, err, "first contact must be accepted (accept-new)")
	pinned, err := os.ReadFile(c.knownHosts)
	require.NoError(t, err)
	_, err = svc.gitCommandWithCreds(work, c, "ls-remote", d.url(t, repo))
	require.NoError(t, err, "the same host key must keep working")

	d.stop(t)
	other := filepath.Join(d.dir, "host_b")
	sshKeygen(t, other)
	d.start(t, other)
	_, err = svc.gitCommandWithCreds(work, c, "ls-remote", d.url(t, repo))
	require.Error(t, err, "a changed host key was accepted")
	assert.Contains(t, err.Error(), "Host key verification failed")
	after, rerr := os.ReadFile(c.knownHosts)
	require.NoError(t, rerr)
	assert.Equal(t, string(pinned), string(after), "the pinned host key was replaced")
}

// TestZ0gl_SSH_PushAndPullThroughTheRoutes: an ssh-auth directory pulls and
// pushes over ssh through PushVerified and pullCLI, with its own key.
func TestZ0gl_SSH_PushAndPullThroughTheRoutes(t *testing.T) {
	requireSSHTools(t)
	keyPath, dataDir := z0glSSHPaths(t)
	f := pullFix(t)
	f.svc.config.DataDir = dataDir
	d := newTestSSHD(t, keyPath+".pub")
	d.start(t, d.hostKey)
	require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "ssh", keyPath, "", ""))
	runGit(t, f.top, "remote", "set-url", "origin", d.url(t, f.bare))

	res, err := f.svc.pullCLI(f.dir)
	require.NoError(t, err)
	assert.NotEqual(t, res.PreviousCommit, res.CurrentCommit, "the pull over ssh fetched nothing")

	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {b: {}}\n")
	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "over ssh", Files: []string{"compose.yaml"}, Push: true})
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, f.head(t), f.remoteMain(t), "the push over ssh did not reach the remote")

	// The key loosened to 0644: refused before the push, naming the fix.
	require.NoError(t, os.Chmod(keyPath, 0o644)) //nolint:gosec // the loose mode is the fault under test
	ar, _ = f.svc.PushVerified(f.dir)
	requireRefusal(t, ar, http.StatusConflict, models.ErrGitSSHKeyUnusable)
	assert.Contains(t, ar.Err.Error(), "chmod 600")
}

// TestZ0gl_SSH_UnwritableKnownHostsNeverAcceptsAChangedKey is the review's
// probe through the real resolver: with a known_hosts ssh cannot write, a
// changed host key must not connect. The writable arm is the positive
// control on the same instrument: first contact connects, the changed key
// is refused.
func TestZ0gl_SSH_UnwritableKnownHostsNeverAcceptsAChangedKey(t *testing.T) {
	requireSSHTools(t)
	if os.Geteuid() == 0 {
		t.Skip("root writes regardless of mode: the read-only arms cannot be built")
	}
	keyPath, _ := z0glSSHPaths(t)
	d := newTestSSHD(t, keyPath+".pub")
	repo := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, filepath.Dir(repo), "init", "-q", "--bare", repo)
	svc, db, cfg := z0glService(t)
	dir := z0glDir(t, db, "ssh", keyPath)

	for _, mode := range []string{"writable", "ro-dir", "ro-file"} {
		t.Run(mode, func(t *testing.T) {
			dd := filepath.Join(t.TempDir(), "data")
			require.NoError(t, os.Mkdir(dd, 0o700))
			t.Cleanup(func() { require.NoError(t, os.Chmod(dd, 0o700)) }) //nolint:gosec // test: directory modes are the fault under test
			switch mode {
			case "ro-dir":
				require.NoError(t, os.Chmod(dd, 0o500)) //nolint:gosec // test: directory modes are the fault under test
			case "ro-file":
				require.NoError(t, os.WriteFile(filepath.Join(dd, "known_hosts"), nil, 0o400))
			}
			cfg.DataDir = dd
			c := svc.gitCredentials(dir)

			d.start(t, d.hostKey)
			_, first := svc.gitCommandWithCreds(t.TempDir(), c, "ls-remote", d.url(t, repo))
			d.stop(t)
			other := filepath.Join(d.dir, "host_"+mode)
			sshKeygen(t, other)
			d.start(t, other)
			_, changed := svc.gitCommandWithCreds(t.TempDir(), c, "ls-remote", d.url(t, repo))
			d.stop(t)

			assert.Error(t, changed, "a CHANGED host key connected")
			if mode == "writable" {
				assert.NoError(t, first, "first contact must be accepted (accept-new)")
				if changed != nil {
					assert.Contains(t, changed.Error(), "Host key verification failed")
				}
				return
			}
			assert.Error(t, first, "an unwritable known_hosts must refuse before any trust is recorded")
			err := c.sshReady()
			require.Error(t, err, "the network pre-check must refuse")
			assert.Contains(t, err.Error(), "host keys cannot be recorded")
		})
	}
}
