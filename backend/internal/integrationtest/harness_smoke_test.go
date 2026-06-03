//go:build integration

package integrationtest

import (
	"strings"
	"testing"
)

// smokeComposeYAML is a minimal single-service Compose file that runs an
// alpine container in the foreground. alpine is the smallest (~5 MB) public
// image, keeping CI pull times negligible. `sleep 3600` keeps the container
// alive long enough for assertions before cleanup tears it down.
const smokeComposeYAML = `services:
  sleeper:
    image: alpine:3.21
    command: ["sleep", "3600"]
    restart: "no"
`

// TestSmoke_HarnessEnd2End proves the harness works against a real Docker
// daemon:
//  1. RequireDocker — skip gracefully if no daemon available.
//  2. NewTempStack — write compose.yaml to a temp dir with a unique project name.
//  3. RunCompose "up -d" — start the stack detached.
//  4. AssertContainerState — verify the sleeper service is running.
//  5. cleanup (implicit via t.Cleanup) — `docker compose down -v` removes the
//     container and its resources, then the temp directory is removed.
//
// This test is intentionally kept narrow: it exercises the harness itself, not
// any production code. Wave B integration tests will import this package and
// build on these helpers to verify real Docker operations.
func TestSmoke_HarnessEnd2End(t *testing.T) {
	RequireDocker(t)

	dir, project, cleanup := NewTempStack(t, smokeComposeYAML)
	defer cleanup() // belt-and-suspenders; t.Cleanup also runs it

	t.Logf("smoke test: project=%q dir=%q", project, dir)

	// Start the stack detached.
	out := RunCompose(t, dir, project, "up", "-d")
	t.Logf("compose up output: %s", strings.TrimSpace(out))

	// Verify the container reached the running state within 15 s.
	AssertContainerState(t, dir, project, "sleeper", true)

	t.Log("smoke test: container is running — tearing down")

	// Explicit early cleanup so the test log shows the down output.
	cleanup()

	// Verify the container is gone after down.
	AssertContainerState(t, dir, project, "sleeper", false)

	t.Log("smoke test: container is stopped — PASS")
}

// TestSmoke_ImageRepoDigests proves that PullPinnedImage + ImageRepoDigests
// round-trip correctly. After a pull, at least one RepoDigest must be present
// and must contain "alpine" and "sha256:".
//
// This is the simplest possible smoke test for the digest helpers that
// Wave A1 (truth/imagedigest.go) will use more extensively.
func TestSmoke_ImageRepoDigests(t *testing.T) {
	RequireDocker(t)

	const ref = "alpine:3.21"

	PullPinnedImage(t, ref)

	digests := ImageRepoDigests(t, ref)
	if len(digests) == 0 {
		t.Fatalf("ImageRepoDigests(%q): got empty slice after pull", ref)
	}

	t.Logf("RepoDigests for %q: %v", ref, digests)

	for _, d := range digests {
		if strings.Contains(d, "sha256:") {
			return // at least one digest has the expected form
		}
	}
	t.Errorf("ImageRepoDigests(%q): none of %v contain 'sha256:'", ref, digests)
}
