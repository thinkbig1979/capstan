//go:build integration

package truth

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRemoteRegistryDigestMatchesLocal is the integration test that would have
// caught the buildx --format bug. It:
//
//  1. Pulls alpine:3.20 via docker pull.
//  2. Fetches its RepoDigests via docker image inspect.
//  3. Calls the real RemoteRegistryDigest implementation.
//  4. Asserts that the remote digest matches the local RepoDigest.
//
// Run with: go test -tags=integration ./internal/truth/ -run TestRemoteRegistryDigest
func TestRemoteRegistryDigestMatchesLocal(t *testing.T) {
	const ref = "alpine:3.20"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Step 1: Pull the image so we have a local RepoDigest.
	t.Logf("Pulling %s ...", ref)
	pullOut, err := exec.CommandContext(ctx, "docker", "pull", ref).CombinedOutput()
	if err != nil {
		t.Fatalf("docker pull %s: %v\n%s", ref, err, pullOut)
	}

	// Step 2: Inspect for RepoDigests.
	inspectOut, err := exec.CommandContext(ctx, "docker", "image", "inspect", ref).Output()
	if err != nil {
		t.Fatalf("docker image inspect %s: %v", ref, err)
	}

	var inspectResults []struct {
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := json.Unmarshal(inspectOut, &inspectResults); err != nil {
		t.Fatalf("parsing inspect output: %v", err)
	}
	if len(inspectResults) == 0 || len(inspectResults[0].RepoDigests) == 0 {
		t.Fatal("no RepoDigests found after pull — cannot compare")
	}

	repoDigests := inspectResults[0].RepoDigests

	// Step 3: Resolve local digest.
	localDigest, ok := LocalRepoDigest(ref, repoDigests)
	if !ok {
		t.Fatalf("LocalRepoDigest could not match %q against %v", ref, repoDigests)
	}
	t.Logf("Local RepoDigest: %s", localDigest)

	// Step 4: Fetch remote digest using the real (non-stubbed) implementation.
	remoteDigest, err := RemoteRegistryDigest(ctx, ref)
	if err != nil {
		t.Fatalf("RemoteRegistryDigest(%s): %v", ref, err)
	}
	t.Logf("Remote digest:    %s", remoteDigest)

	// Step 5: The digests must match (image is just pulled so it should be current).
	if !strings.HasPrefix(remoteDigest, "sha256:") {
		t.Errorf("remote digest %q does not look like a sha256 digest", remoteDigest)
	}
	if localDigest != remoteDigest {
		t.Errorf("digest mismatch: local=%s remote=%s\n"+
			"This is the bentopdf symptom: if local != remote for a freshly-pulled image, "+
			"RemoteRegistryDigest is broken (check buildx --format / --raw handling).",
			localDigest, remoteDigest)
	}
}
