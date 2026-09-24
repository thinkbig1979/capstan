package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// TestPullVerified_RedeployedStacksIsNeverNull pins agent-os-io23's one real
// site. `redeployed` was a nil slice, so a pull with redeploy requested whose
// changed files matched no stack sent `"redeployedStacks": null` in the
// "pulled and redeployed" result, where every other list in that body is an
// array and the TypeScript type is string[].
//
// The fixture reaches that branch for real: the upstream changes a file, a
// redeploy is requested with a non-nil DockerService, and no stack lives in the
// directory, so the loop redeploys nothing. The DockerService is never called.
func TestPullVerified_RedeployedStacksIsNeverNull(t *testing.T) {
	work, seed, root := pullFixture(t)
	if err := os.WriteFile(filepath.Join(seed, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	mustGit(t, seed, "add", "notes.txt")
	advanceUpstream(t, seed, root)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := NewGitService(&config.Config{}, db)
	ar, pr := svc.PullVerified(work, true, &DockerService{})
	if pr == nil || pr.PreviousCommit == pr.CurrentCommit || len(pr.ChangedFiles) == 0 {
		t.Fatalf("fixture did not pull a file change: outcome=%s reason=%q result=%+v", ar.Outcome, ar.Reason, pr)
	}
	if ar.Outcome != truth.OutcomeSuccess || ar.Reason != "pulled and redeployed" {
		t.Fatalf("outcome=%s reason=%q, want success \"pulled and redeployed\"", ar.Outcome, ar.Reason)
	}

	body, err := json.Marshal(ar)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"redeployedStacks":[]`) {
		t.Errorf("body = %s, want \"redeployedStacks\":[]", body)
	}
}
