package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-89ut and agent-os-rb6f are two sites of ONE class: a read that could
// not answer is merged into a legitimate value state, so a fault and a normal
// condition produce the same branch and the fault is never reported.
//
// Every test here asserts on the CONSEQUENCE named in the log line, not merely
// on the presence of a line or on a prefix of it. A line that says a read
// failed without saying what that does to the caller satisfies "something was
// logged" while leaving an operator exactly as uninformed as silence did, and a
// prefix assertion is satisfied by a mutant that logs the wrong branch.

// ---------------------------------------------------------------- helpers --

// evCaptureLogs redirects the default slog logger at DEBUG into a buffer.
// DEBUG, not INFO: the pre-fix behaviour of both sites was to log at DEBUG (or
// not at all), so a capture that filtered DEBUG out could not tell "the fix
// raised the level" from "the fix added a line", and the must-stay-silent arms
// would pass against code that still logs the old DEBUG line.
func evCaptureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// ------------------------------------------------------- agent-os-89ut ------

// evRunStore is a backupRunStore whose GetBackupRunByID answer is chosen by the
// test. Only that method matters here; evictFinished calls nothing else.
type evRunStore struct {
	run *models.BackupRun
	err error
}

func (s evRunStore) CreateBackupRun(*models.BackupRun) error { return nil }
func (s evRunStore) UpdateBackupRun(*models.BackupRun) error { return nil }
func (s evRunStore) GetBackupRunByID(string) (*models.BackupRun, error) {
	return s.run, s.err
}

// evRegistry builds a registry holding exactly one FINISHED run (done closed,
// so the select in evictFinished takes the eviction branch) over the given
// store. It deliberately does NOT use NewBackupRunnerRegistry: that starts a
// gcLoop goroutine on a 5-minute ticker, which this test neither needs nor can
// join, and evictFinished is called directly instead.
func evRegistry(store backupRunStore) (*BackupRunnerRegistry, string) {
	const id = "89ut-run-id"
	done := make(chan struct{})
	close(done)
	return &BackupRunnerRegistry{
		runs:   map[string]*durableRun{id: {runID: id, kind: RunKindBackup, done: done}},
		db:     store,
		gcStop: make(chan struct{}),
	}, id
}

func evHas(reg *BackupRunnerRegistry, id string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	_, ok := reg.runs[id]
	return ok
}

// TestEvictFinished_ReadFaultIsReportedAndTheEntryIsKept is agent-os-89ut's
// primary arm. Before the fix the DB fault took the same silent `continue` as
// "this run has not finished yet", so a persistent fault leaked the entry
// forever with no trace at any level.
//
// The entry must be KEPT: a transient read fault must not evict a live run.
func TestEvictFinished_ReadFaultIsReportedAndTheEntryIsKept(t *testing.T) {
	buf := evCaptureLogs(t)
	reg, id := evRegistry(evRunStore{err: errors.New("disk I/O error reading backup_runs")})

	reg.evictFinished()

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("a read fault must be reported above DEBUG, which is off in production; got:\n%s", out)
	}
	// The CONSEQUENCE, not just the cause. A line naming only the failed read
	// leaves an operator unable to tell what it costs them.
	if !strings.Contains(out, "registry entry stays in memory") {
		t.Errorf("the log must name the consequence (the entry stays in the in-memory registry); got:\n%s", out)
	}
	if !strings.Contains(out, "retried on the next GC tick") {
		t.Errorf("the log must say the entry is retried, not abandoned; got:\n%s", out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("the log must name the run it is about; got:\n%s", out)
	}
	if !strings.Contains(out, "disk I/O error reading backup_runs") {
		t.Errorf("the log must carry the underlying cause; got:\n%s", out)
	}
	// Discriminates the fault branch from the missing-row branch: they must not
	// be able to satisfy each other's assertions.
	if strings.Contains(out, "no longer exists") {
		t.Errorf("a read fault must not be reported as a missing row; got:\n%s", out)
	}
	if !evHas(reg, id) {
		t.Errorf("a transient read fault must not evict a live run's entry")
	}
}

// TestEvictFinished_MissingRowIsEvictedAndReported. sql.ErrNoRows is NOT a read
// fault, and it is not "not finished yet" either: no future tick can ever read
// a FinishedAt for a row that is gone, so the entry is unreachable garbage.
// Keeping it would leak it AND re-log every five minutes forever.
func TestEvictFinished_MissingRowIsEvictedAndReported(t *testing.T) {
	buf := evCaptureLogs(t)
	reg, id := evRegistry(evRunStore{err: sql.ErrNoRows})

	reg.evictFinished()

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("a missing row must be reported above DEBUG; got:\n%s", out)
	}
	if !strings.Contains(out, "no longer exists") {
		t.Errorf("the log must name the missing row as the cause; got:\n%s", out)
	}
	if !strings.Contains(out, "can never be evicted by its finish time") {
		t.Errorf("the log must name the consequence that forces eviction; got:\n%s", out)
	}
	if strings.Contains(out, "retried on the next GC tick") {
		t.Errorf("a missing row must not be reported as a retryable fault; got:\n%s", out)
	}
	if evHas(reg, id) {
		t.Errorf("an entry whose row is gone must be evicted, not leaked")
	}
}

// TestEvictFinished_UnfinishedRunStaysSilent is the MUST-NOT-FIRE arm. Without
// it, a fix that logged on every pass would satisfy both arms above.
func TestEvictFinished_UnfinishedRunStaysSilent(t *testing.T) {
	buf := evCaptureLogs(t)
	reg, id := evRegistry(evRunStore{run: &models.BackupRun{ID: "89ut-run-id"}}) // FinishedAt nil

	reg.evictFinished()

	if out := buf.String(); strings.TrimSpace(out) != "" {
		t.Errorf("a run that has simply not finished yet is the NORMAL case and must log nothing; got:\n%s", out)
	}
	if !evHas(reg, id) {
		t.Errorf("an unfinished run must stay in the registry")
	}
}

// TestEvictFinished_UnparseableFinishedAtIsEvictedAndReported. A malformed
// finished_at is permanent, so retrying it forever is the same never-evictable
// leak as a missing row — and it was equally silent.
func TestEvictFinished_UnparseableFinishedAtIsEvictedAndReported(t *testing.T) {
	buf := evCaptureLogs(t)
	bad := "not-a-timestamp"
	reg, id := evRegistry(evRunStore{run: &models.BackupRun{ID: "89ut-run-id", FinishedAt: &bad}})

	reg.evictFinished()

	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "unparseable finished_at") {
		t.Errorf("an unparseable finished_at must be reported; got:\n%s", out)
	}
	if !strings.Contains(out, bad) {
		t.Errorf("the log must carry the value it could not parse; got:\n%s", out)
	}
	if evHas(reg, id) {
		t.Errorf("an entry with a permanently unparseable finish time must be evicted, not retried forever")
	}
}

// TestEvictFinished_RecentlyFinishedRunIsKeptSilently pins the retentionTTL
// behaviour the fix must not have disturbed: a run that finished a moment ago
// stays, without a word.
func TestEvictFinished_RecentlyFinishedRunIsKeptSilently(t *testing.T) {
	buf := evCaptureLogs(t)
	now := time.Now().UTC().Format(time.RFC3339)
	reg, id := evRegistry(evRunStore{run: &models.BackupRun{ID: "89ut-run-id", FinishedAt: &now}})

	reg.evictFinished()

	if out := buf.String(); strings.TrimSpace(out) != "" {
		t.Errorf("a recently finished run must log nothing; got:\n%s", out)
	}
	if !evHas(reg, id) {
		t.Errorf("a run that finished inside retentionTTL must not be evicted yet")
	}
}

// TestEvictFinished_ExpiredRunIsEvictedSilently is the other half of the
// retentionTTL pin: the normal eviction path still works and is still quiet.
func TestEvictFinished_ExpiredRunIsEvictedSilently(t *testing.T) {
	buf := evCaptureLogs(t)
	old := time.Now().UTC().Add(-2 * retentionTTL).Format(time.RFC3339)
	reg, id := evRegistry(evRunStore{run: &models.BackupRun{ID: "89ut-run-id", FinishedAt: &old}})

	reg.evictFinished()

	if out := buf.String(); strings.TrimSpace(out) != "" {
		t.Errorf("the normal expiry eviction must log nothing; got:\n%s", out)
	}
	if evHas(reg, id) {
		t.Errorf("a run finished longer ago than retentionTTL must be evicted")
	}
}

// ------------------------------------------------------- agent-os-rb6f ------

// evStubDigest swaps truth.RemoteRegistryDigest (already a package var) for the
// duration of a test. This is the seam that makes fetchRemoteDigests testable
// without a daemon; DockerService.client is a concrete *client.Client
// (docker.go:55), so CheckForUpdates itself is not.
func evStubDigest(t *testing.T, fn func(context.Context, string) (string, error)) {
	t.Helper()
	prev := truth.RemoteRegistryDigest
	truth.RemoteRegistryDigest = fn
	t.Cleanup(func() { truth.RemoteRegistryDigest = prev })
}

func evRefs(refs ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		m[r] = struct{}{}
	}
	return m
}

// TestFetchRemoteDigests_FetchFaultIsReportedWithItsConsequence is
// agent-os-rb6f's primary arm. Before the fix a fetch fault shared one branch
// with "the registry reports no digest", logged at DEBUG only, so a persistent
// registry fault was indistinguishable from "you are up to date".
func TestFetchRemoteDigests_FetchFaultIsReportedWithItsConsequence(t *testing.T) {
	buf := evCaptureLogs(t)
	evStubDigest(t, func(_ context.Context, ref string) (string, error) {
		if ref == "good:1" {
			return "sha256:good", nil
		}
		return "", fmt.Errorf("unauthorized: authentication required for %s", ref)
	})

	got := fetchRemoteDigests(context.Background(), evRefs("good:1", "bad:1", "bad:2"))

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("a registry fetch fault must be reported above DEBUG, which is off in production; got:\n%s", out)
	}
	if !strings.Contains(out, "Could not determine whether an update exists") {
		t.Errorf("the log must say the update status is UNKNOWN, not that a fetch failed; got:\n%s", out)
	}
	if !strings.Contains(out, "reported as having no update available") {
		t.Errorf("the log must name the consequence — the operator is shown 'no update'; got:\n%s", out)
	}
	// Real counts, not a boolean dressed as one. unreadable is 2 of 3 checked;
	// a mutant that reported len(uniqueRefs) for both, or 1 for either, fails.
	if !strings.Contains(out, "unreadable=2") {
		t.Errorf("the aggregate must count the refs that actually failed (2); got:\n%s", out)
	}
	if !strings.Contains(out, "checked=3") {
		t.Errorf("the aggregate must count every ref it checked (3); got:\n%s", out)
	}
	// Sorted, so the assertion is not order-dependent across map iteration.
	if !strings.Contains(out, `images="bad:1, bad:2"`) {
		t.Errorf("the aggregate must name the affected images; got:\n%s", out)
	}
	if strings.Contains(out, "images=good:1") || strings.Contains(out, "bad:1, bad:2, good:1") {
		t.Errorf("a ref that resolved must not be reported as unreadable; got:\n%s", out)
	}
	// Exactly one aggregate line per scan, not one per failing image.
	if n := strings.Count(out, "Could not determine whether an update exists"); n != 1 {
		t.Errorf("the fault report must be ONE line per scan, got %d; a per-image warning fires forever for every locally built image:\n%s", n, out)
	}
	if len(got) != 1 || got["good:1"] != "sha256:good" {
		t.Errorf("the refs that resolved must still be returned; got %v", got)
	}
}

// TestFetchRemoteDigests_EmptyDigestIsNotAFault is the discrimination the class
// is about, from the other side: the registry ANSWERED, and answered with no
// digest. That is not a fault and must not be counted or warned about.
func TestFetchRemoteDigests_EmptyDigestIsNotAFault(t *testing.T) {
	buf := evCaptureLogs(t)
	evStubDigest(t, func(context.Context, string) (string, error) { return "", nil })

	got := fetchRemoteDigests(context.Background(), evRefs("quiet:1", "quiet:2"))

	out := buf.String()
	if strings.Contains(out, "level=WARN") {
		t.Errorf("a registry that answered with no digest is not a fault and must not warn; got:\n%s", out)
	}
	if strings.Contains(out, "Could not determine whether an update exists") {
		t.Errorf("the empty-digest case must not be aggregated as unreadable; got:\n%s", out)
	}
	// The OTHER branch must be the one that fired, or this arm is satisfied by
	// a mutant that dropped both cases on the floor.
	if !strings.Contains(out, "Registry reported no remote digest for image") {
		t.Errorf("the empty-digest case must still be traceable at DEBUG; got:\n%s", out)
	}
	if len(got) != 0 {
		t.Errorf("an empty digest must not enter the result map; got %v", got)
	}
}

// TestFetchRemoteDigests_AllResolvedStaysSilent is the MUST-NOT-FIRE arm.
// Without it a fix that warned unconditionally would satisfy the arm above.
func TestFetchRemoteDigests_AllResolvedStaysSilent(t *testing.T) {
	buf := evCaptureLogs(t)
	evStubDigest(t, func(_ context.Context, ref string) (string, error) {
		return "sha256:" + ref, nil
	})

	got := fetchRemoteDigests(context.Background(), evRefs("a:1", "b:1"))

	if out := buf.String(); strings.Contains(out, "level=WARN") {
		t.Errorf("a scan in which every digest resolved must warn about nothing; got:\n%s", out)
	}
	if len(got) != 2 {
		t.Errorf("every resolved ref must be returned; got %v", got)
	}
}

// TestFindComposeContainer_ListFaultIsReported covers the sibling site at
// docker_update.go's findComposeContainer, which the bead text did not name and
// which the orchestrator's sparring partner mis-located.
//
// The fault is forced by pointing a real *client.Client at a closed port rather
// than by stubbing: the field is a concrete *client.Client, so there is nothing
// to stub. 127.0.0.1:1 refuses immediately, so this neither reaches the network
// nor depends on a daemon being installed.
func TestFindComposeContainer_ListFaultIsReported(t *testing.T) {
	buf := evCaptureLogs(t)
	c, err := client.NewClientWithOpts(client.WithHost("tcp://127.0.0.1:1"), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("build docker client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	s := &DockerService{client: c}

	got := s.findComposeContainer(context.Background(), "proj", "svc", "pre-update-id")

	if got != "pre-update-id" {
		t.Fatalf("the fallback id must still be returned; got %q", got)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("a container-list fault must be reported; before agent-os-rb6f it was silent. got:\n%s", out)
	}
	if !strings.Contains(out, "falling back to the pre-update id") {
		t.Errorf("the log must name the consequence, not only the failed call; got:\n%s", out)
	}
	if !strings.Contains(out, "outcome may be reported wrongly") {
		t.Errorf("the log must say what the fallback costs — the update's outcome is misreported; got:\n%s", out)
	}
	if !strings.Contains(out, "project=proj") || !strings.Contains(out, "service=svc") {
		t.Errorf("the log must identify which service it is about; got:\n%s", out)
	}
}
