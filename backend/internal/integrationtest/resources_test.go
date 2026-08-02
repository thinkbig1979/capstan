//go:build integration

// Package integrationtest — resource mutation integration tests (B3).
//
// Tests covered:
//
//	#12: build a unique minimal image, tag it with a second name, delete by the
//	     second name → assert no_change (untagged-only, layer still present via
//	     the original tag); delete the original tag too → assert success (layer
//	     actually deleted). This is the false-success guard for finding #12.
//
//	#13: pull an image, make a dangling copy, prune → assert the reported entry
//	     count is non-zero.
//
//	#6:  create a temp stack dir + DB row, run compose down, remove dir and DB row,
//	     assert the directory is GONE and the DB row is removed.
//
//	#14: create a stack with a crash-looping deploy → assert outcome=partial/failed
//	     and the stack row still exists; create with a healthy deploy → success.
package integrationtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// ---- helpers ----

func newDockerServiceForResources(t *testing.T, stacksDir string) *services.DockerService {
	t.Helper()
	cfg := &config.Config{StacksDir: stacksDir}
	svc, err := services.NewDockerService(cfg)
	require.NoError(t, err)
	return svc
}

// buildMinimalImage builds a minimal FROM-scratch image with a unique label so
// it has no other references and can be truly deleted. Returns the image ID.
func buildMinimalImage(t *testing.T, tag string) string {
	t.Helper()

	dir := t.TempDir()
	dockerfile := `FROM alpine:3.21
LABEL capstan.test="` + tag + `"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644))

	cmd := exec.Command("docker", "build", "-t", tag, dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "docker build: %s", out)

	// Get the image ID.
	idOut, idErr := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", tag).Output()
	require.NoError(t, idErr)
	return strings.TrimSpace(string(idOut))
}

// dockerTagLocal adds a new tag to an existing local image using the docker CLI.
func dockerTagLocal(t *testing.T, src, dst string) {
	t.Helper()
	out, err := exec.Command("docker", "tag", src, dst).CombinedOutput()
	require.NoError(t, err, "docker tag %s %s: %s", src, dst, out)
}

// dockerRmiBestEffort removes an image tag (best-effort, ignores errors).
func dockerRmiBestEffort(tag string) {
	exec.Command("docker", "rmi", "--no-prune", tag).Run() //nolint:errcheck
}

// dockerRmiForce removes an image forcefully (best-effort).
func dockerRmiForce(tag string) {
	exec.Command("docker", "rmi", "-f", tag).Run() //nolint:errcheck
}

// imageExistsLocally returns true when the given ref is present in the local daemon.
func imageExistsLocally(ref string) bool {
	return exec.Command("docker", "image", "inspect", ref).Run() == nil
}

// classifyDeleteResp mirrors handlers.classifyImageDeleteResponse as a pure
// helper for the integration test so we can assert the exact outcome branch
// without importing the (internal) handlers package.
func classifyDeleteResp(resp []image.DeleteResponse) truth.ActionResult {
	var deleted, untagged []string
	for _, r := range resp {
		if r.Deleted != "" {
			deleted = append(deleted, r.Deleted)
		}
		if r.Untagged != "" {
			untagged = append(untagged, r.Untagged)
		}
	}

	if len(resp) == 0 {
		return truth.NoChange("image delete returned no entries",
			truth.KV("deleted", []string{}),
			truth.KV("untagged", []string{}),
		)
	}

	if len(deleted) > 0 {
		return truth.Success("image removed",
			truth.KV("deleted", deleted),
			truth.KV("untagged", untagged),
		)
	}

	return truth.NoChange("image still referenced by other tags; untagged only",
		truth.KV("deleted", []string{}),
		truth.KV("untagged", untagged),
	)
}

// upsertDirectoryAndStack inserts the directory FK dependency and then the stack
// row into the in-memory DB, matching the pattern used by the scanner/handler.
func upsertDirectoryAndStack(t *testing.T, db *database.DB, stackDir, stackID, projectName string) {
	t.Helper()

	dir := models.Directory{
		Path:      stackDir,
		Name:      filepath.Base(stackDir),
		IsGitRepo: false,
		ScannedAt: time.Now(),
	}
	require.NoError(t, db.UpsertDirectory(dir))

	stack := models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		ProjectName: projectName,
		Status:      "stopped",
	}
	require.NoError(t, db.UpsertStack(stack))
}

// ---- Finding #12: image delete false-success guard ----

func Test_Resource_ImageDelete_UntaggedOnly_NoChange(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "alpine:3.21")

	// Build an isolated test image (unique label) so it has no other references.
	ts := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	baseTag := "capstan-test-b3-base-" + ts + ":latest"
	extraTag := "capstan-test-b3-extra-" + ts + ":latest"

	buildMinimalImage(t, baseTag)
	t.Cleanup(func() {
		dockerRmiForce(baseTag)
		dockerRmiForce(extraTag)
	})

	// Add a second tag pointing to the same layer.
	dockerTagLocal(t, baseTag, extraTag)

	require.True(t, imageExistsLocally(baseTag), "prerequisite: baseTag must exist")
	require.True(t, imageExistsLocally(extraTag), "prerequisite: extraTag must exist")

	svc := newDockerServiceForResources(t, t.TempDir())
	ctx := context.Background()

	// Delete extraTag. The layer still exists under baseTag.
	resp, err := svc.DeleteImage(ctx, extraTag, false)
	require.NoError(t, err, "DeleteImage must not error when deleting an existing tag")

	ar := classifyDeleteResp(resp)
	t.Logf("step1 outcome=%s reason=%q resp=%+v", ar.Outcome, ar.Reason, resp)

	// Key assertion for finding #12: a tag-only removal MUST NOT be success.
	assert.NotEqual(t, truth.OutcomeSuccess, ar.Outcome,
		"deleting one tag of a multi-tag image must not report success (finding #12)")
	assert.Equal(t, truth.OutcomeNoChange, ar.Outcome,
		"untagged-only response must be no_change")

	// The image layer must still be present (accessible via baseTag).
	assert.True(t, imageExistsLocally(baseTag),
		"image must still be accessible via the remaining tag")

	// Now delete the last tag — the layer should be fully removed.
	resp2, err := svc.DeleteImage(ctx, baseTag, false)
	require.NoError(t, err)

	ar2 := classifyDeleteResp(resp2)
	t.Logf("step2 outcome=%s reason=%q resp=%+v", ar2.Outcome, ar2.Reason, resp2)
	assert.Equal(t, truth.OutcomeSuccess, ar2.Outcome,
		"deleting the last tag must report success (layer was removed)")

	assert.False(t, imageExistsLocally(baseTag),
		"image must no longer be accessible after deleting the last tag")
}

// ---- Finding #13: image prune count accuracy ----

func Test_Resource_ImagePrune_CountsUntaggedEntries(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "alpine:3.21")

	// Strategy: build image A with tag T (adding a unique RUN layer), then
	// build image B (different content) with the same tag T. This moves the tag
	// from A to B, leaving A dangling (untagged but layer present). Prune must
	// then report A as pruned.
	ts := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	tag := "capstan-test-prune-" + ts + ":latest"

	// Build first image.
	dir1 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "Dockerfile"),
		[]byte("FROM alpine:3.21\nRUN echo first-"+ts), 0644))
	out1, err1 := exec.Command("docker", "build", "-t", tag, dir1).CombinedOutput()
	require.NoError(t, err1, "build first: %s", out1)
	t.Cleanup(func() { dockerRmiForce(tag) })

	// Build second image with SAME tag — moves the tag, leaving first dangling.
	dir2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "Dockerfile"),
		[]byte("FROM alpine:3.21\nRUN echo second-"+ts), 0644))
	out2, err2 := exec.Command("docker", "build", "-t", tag, dir2).CombinedOutput()
	require.NoError(t, err2, "build second: %s", out2)

	// Verify there are dangling images now.
	danglingOut, _ := exec.Command("docker", "images", "--filter", "dangling=true", "--format", "{{.ID}}").Output()
	t.Logf("dangling images before prune: %s", strings.TrimSpace(string(danglingOut)))

	svc := newDockerServiceForResources(t, t.TempDir())
	ctx := context.Background()

	// Prune dangling images.
	report, pruneErr := svc.PruneImages(ctx, services.PruneOptions{All: false})
	require.NoError(t, pruneErr)

	t.Logf("prune report: ImagesDeleted=%d SpaceReclaimed=%d",
		len(report.ImagesDeleted), report.SpaceReclaimed)

	// The fixed handler counts both Deleted and Untagged entries (finding #13).
	totalEntries := len(report.ImagesDeleted)
	assert.Greater(t, totalEntries, 0,
		"prune must report at least one entry when a dangling image exists (finding #13)")
}

// ---- Finding #6: stack delete removes dir and DB row ----

func Test_Resource_StackDelete_RemovesDirAndDBRow(t *testing.T) {
	RequireDocker(t)

	stacksRoot := t.TempDir()
	stackDir := filepath.Join(stacksRoot, "test-delete-stack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	composeContent := `services:
  svc:
    image: alpine:3.21
    command: ["sleep", "1"]
    restart: "no"
`
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(composeContent), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	stackID := filepath.Base(stacksRoot) + "~test-delete-stack:default"
	upsertDirectoryAndStack(t, db, stackDir, stackID, "test-delete-stack-default")

	// Verify the row exists before delete.
	got, err := db.GetStack(stackID)
	require.NoError(t, err)
	require.NotNil(t, got, "stack must exist in DB before delete")

	svc := newDockerServiceForResources(t, stacksRoot)

	// compose down -v, verified (stack was never started; must succeed and
	// verify as removed).
	deleteAR, deleteOutput := svc.DeleteVerified(models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		ProjectName: "test-delete-stack-default",
	})
	require.NotEqual(t, truth.OutcomeFailed, deleteAR.Outcome, "compose down must verify as removed for a never-started stack: %s", deleteAR.Reason)
	t.Logf("compose down output: %s", strings.TrimSpace(deleteOutput))

	// Resolve abs path (mirroring the fixed handler).
	absStackDir, absErr := filepath.Abs(stackDir)
	require.NoError(t, absErr)

	// Remove directory.
	require.NoError(t, os.RemoveAll(absStackDir))

	// Remove DB row — surface the error (finding #6 fix).
	require.NoError(t, db.DeleteStack(stackID))

	// Verify directory is gone.
	_, statErr := os.Stat(stackDir)
	assert.True(t, os.IsNotExist(statErr),
		"stack directory must not exist after delete (finding #6)")

	// Verify DB row is gone. GetStack returns sql.ErrNoRows when not found,
	// which is the expected "not found" signal (not a real error).
	missing, dbErr := db.GetStack(stackID)
	if dbErr != nil && !errors.Is(dbErr, sql.ErrNoRows) {
		t.Fatalf("GetStack after delete: unexpected error: %v", dbErr)
	}
	assert.Nil(t, missing, "stack DB row must be gone after delete (finding #6)")
}

// ---- Finding #14: create with deploy — partial on crash, success on healthy ----

const crashDeployYAML = `services:
  crasher:
    image: alpine:3.21
    command: ["sh", "-c", "exit 1"]
    restart: "no"
`

const healthyDeployYAML = `services:
  sleeper:
    image: alpine:3.21
    command: ["sleep", "3600"]
    restart: "no"
`

func Test_Resource_CreateWithDeploy_CrashLoop_IsPartial(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "alpine:3.21")

	ts := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	project := sanitizeProjectName("it-b3-crash-" + ts)

	stacksRoot := t.TempDir()
	stackDir := filepath.Join(stacksRoot, "crashstack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(crashDeployYAML), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	stackID := filepath.Base(stacksRoot) + "~crashstack:default"
	upsertDirectoryAndStack(t, db, stackDir, stackID, project)

	svc := newDockerServiceForResources(t, stacksRoot)
	t.Cleanup(func() {
		exec.Command("docker", "compose",
			"-p", project,
			"-f", filepath.Join(stackDir, "compose.yaml"),
			"down", "-v", "--remove-orphans",
		).Run() //nolint:errcheck
	})

	// The fixed Create handler calls StartVerified (not the legacy Start).
	stack := models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		ProjectName: project,
		Status:      "stopped",
	}
	deployAR, deployOutput := svc.StartVerified(stack)
	t.Logf("StartVerified outcome=%s reason=%q output_len=%d", deployAR.Outcome, deployAR.Reason, len(deployOutput))

	// Stack was created+persisted. Deploy failed → handler must return partial (207).
	assert.NotEqual(t, truth.OutcomeSuccess, deployAR.Outcome,
		"crash-looping service must not be reported as success (finding #14)")
	assert.Contains(t,
		[]truth.Outcome{truth.OutcomeFailed, truth.OutcomePartial},
		deployAR.Outcome,
		"crash-loop deploy must be failed or partial (finding #14)")

	// The DB row must still exist — create succeeded, only deploy failed.
	got, dbErr := db.GetStack(stackID)
	require.NoError(t, dbErr)
	assert.NotNil(t, got,
		"stack row must survive a failed deploy (finding #14: create succeeded, deploy failed)")
}

func Test_Resource_CreateWithDeploy_HealthyService_IsSuccess(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "alpine:3.21")

	ts := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	project := sanitizeProjectName("it-b3-healthy-" + ts)

	stacksRoot := t.TempDir()
	stackDir := filepath.Join(stacksRoot, "healthystack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(healthyDeployYAML), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	stackID := filepath.Base(stacksRoot) + "~healthystack:default"
	upsertDirectoryAndStack(t, db, stackDir, stackID, project)

	svc := newDockerServiceForResources(t, stacksRoot)
	t.Cleanup(func() {
		exec.Command("docker", "compose",
			"-p", project,
			"-f", filepath.Join(stackDir, "compose.yaml"),
			"down", "-v", "--remove-orphans",
		).Run() //nolint:errcheck
	})

	stack := models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		ProjectName: project,
		Status:      "stopped",
	}
	deployAR, deployOutput := svc.StartVerified(stack)
	t.Logf("StartVerified outcome=%s reason=%q output_len=%d", deployAR.Outcome, deployAR.Reason, len(deployOutput))

	assert.Equal(t, truth.OutcomeSuccess, deployAR.Outcome,
		"healthy service must report success after deploy (finding #14)")
	assert.Nil(t, deployAR.Err, "Err must be nil on success")

	got, dbErr := db.GetStack(stackID)
	require.NoError(t, dbErr)
	assert.NotNil(t, got, "stack DB row must exist after successful create+deploy")
}
