package services

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- agent-os-qnso: an rclone remote can be a connection string carrying a
// credential, and it reached the INFO log, error messages, the DR stream
// line and rclone's own echoed output in clear. ---

const (
	qnsoRemote = ":s3,provider=Minio,access_key_id=AKQNSO1234,secret_access_key='SKQNSO,5678'"
	qnsoAK     = "AKQNSO1234"
	qnsoSK     = "SKQNSO"
)

func assertNoQnsoSecret(t *testing.T, s, where string) {
	t.Helper()
	assert.NotContains(t, s, qnsoAK, "%s carries the access key: %s", where, s)
	assert.NotContains(t, s, qnsoSK, "%s carries the secret key: %s", where, s)
}

func TestQnso_RedactRcloneRemote(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		qnsoRemote:                          ":s3,provider=***,access_key_id=***,secret_access_key=***",
		"myremote":                          "myremote",
		"myremote,env_auth,token=\"a,b,c\"": "myremote,env_auth,token=***",
		":sftp,host=h,pass='unterminated,more=SECRET": ":sftp,host=***,***",
		":webdav,url=https://u:pw@host/dav":           ":webdav,url=***",
		"":                                            "",
	}
	for in, want := range cases {
		assert.Equal(t, want, RedactRcloneRemote(in), "in %q", in)
	}
	assert.NotContains(t, RedactRcloneRemote(":sftp,host=h,pass='unterminated,more=SECRET"), "SECRET", "an unterminated quote fails closed")
}

// qnsoRunner fails or echoes like rclone does: every Run writes a line naming
// the remote as rclone prints it, and every call fails when fail is set.
type qnsoRunner struct {
	mu     sync.Mutex
	fail   bool
	output []byte
}

func (r *qnsoRunner) Run(_ context.Context, name string, args []string, _ []string, out chan<- StreamLine) error {
	if name == "rclone" && out != nil {
		for _, a := range args[slices.Index(args, "--")+1:] {
			if strings.Contains(a, ":") {
				out <- StreamLine{Type: "stderr", Line: `Failed to create file system for "` + a + `": denied`}
			}
		}
	}
	if r.fail {
		return errors.New("rclone exited: exit status 1")
	}
	return nil
}

func (r *qnsoRunner) Output(context.Context, string, []string, []string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return nil, errors.New("rclone: exit status 1")
	}
	return r.output, nil
}

func qnsoManager(r commandRunner) (*RcloneManager, *bytes.Buffer) {
	var logs bytes.Buffer
	cfg := testBackupConfig()
	cfg.RcloneRemote = qnsoRemote
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return newRcloneManagerWithRunner(cfg, r, logger), &logs
}

// Every error an rclone call returns, on every path that formats the remote.
func TestQnso_ErrorsNeverCarryTheRemoteCredential(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, logs := qnsoManager(&qnsoRunner{fail: true})

	err := m.TestConnectivity(ctx, "")
	require.Error(t, err)
	assertNoQnsoSecret(t, err.Error(), "TestConnectivity error")

	_, err = m.PreflightSync(ctx, t.TempDir(), "", "")
	require.Error(t, err)
	assertNoQnsoSecret(t, err.Error(), "preflight error")

	_, err = m.remoteHasSnapshots(ctx, "", "")
	require.Error(t, err)
	assertNoQnsoSecret(t, err.Error(), "remoteHasSnapshots error")

	err = m.RestoreRepo(ctx, "", "", t.TempDir(), "", 1, nil)
	require.Error(t, err)
	assertNoQnsoSecret(t, err.Error(), "RestoreRepo probe error")
	assertNoQnsoSecret(t, logs.String(), "the log")
	assert.Contains(t, err.Error(), ":s3,", "the error still names the backend")
}

// The INFO log lines and rclone's own echoed output, for sync and restore.
func TestQnso_LogsAndStreamNeverCarryTheRemoteCredential(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := &qnsoRunner{output: []byte("config\n")}
	m, logs := qnsoManager(r)

	var lines []string
	collect := func() (chan StreamLine, func()) {
		out := make(chan StreamLine, 64)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for l := range out {
				lines = append(lines, l.Line)
			}
		}()
		return out, func() { close(out); <-done }
	}

	out, stop := collect()
	require.NoError(t, m.Sync(ctx, t.TempDir(), "", "bucket", 1, 1, 0, out))
	stop()
	out, stop = collect()
	require.NoError(t, m.RestoreRepo(ctx, "", "bucket", t.TempDir(), "", 1, out))
	stop()

	require.Len(t, lines, 2, "both echoes reached the stream: %v", lines)
	for _, l := range lines {
		assertNoQnsoSecret(t, l, "stream line")
		assert.Contains(t, l, "secret_access_key=***", "the echoed remote is masked, not dropped: %s", l)
	}
	require.Contains(t, logs.String(), "Starting rclone sync")
	require.Contains(t, logs.String(), "Starting rclone restore")
	assertNoQnsoSecret(t, logs.String(), "the INFO log")
}

// Control: a named remote is not a credential and passes through untouched,
// in the log and in rclone's output.
func TestQnso_NamedRemoteIsNotMasked(t *testing.T) {
	t.Parallel()
	r := &qnsoRunner{output: []byte("config\n")}
	var logs bytes.Buffer
	cfg := testBackupConfig()
	cfg.RcloneRemote = "myremote"
	m := newRcloneManagerWithRunner(cfg, r, slog.New(slog.NewTextHandler(&logs, nil)))
	out := make(chan StreamLine, 8)
	require.NoError(t, m.Sync(context.Background(), t.TempDir(), "", "bucket", 1, 1, 0, out))
	close(out)
	l := <-out
	assert.Contains(t, l.Line, `"myremote:bucket"`)
	assert.Contains(t, logs.String(), "myremote:bucket")
}

// The DR restore's opening stream line names the source.
func TestQnso_DRStreamLineNeverCarriesTheRemoteCredential(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", qnsoRemote))
	r := &qnsoRunner{output: []byte("config\n")}
	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, r)

	out := make(chan StreamLine, 128)
	_ = svc.RunDRRestore(context.Background(), out)
	close(out)
	var all []string
	for l := range out {
		all = append(all, l.Line)
	}
	joined := strings.Join(all, "\n")
	require.Contains(t, joined, "Starting DR restore from")
	assertNoQnsoSecret(t, joined, "DR stream")
}
