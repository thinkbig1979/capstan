package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestSelectUpdates_SkipsIfRemoteMissing ensures that a candidate whose
// imageRef is absent from the remoteDigests map is never reported as an update.
// This closes the phantom-update path where a registry timeout would previously
// leave an empty/unexpected string in remoteDigests that then didn't match,
// producing a false positive.
func TestSelectUpdates_SkipsIfRemoteMissing(t *testing.T) {
	t.Parallel()

	candidates := []updateCandidate{
		{
			localDigest: "sha256:aaa",
			info: models.ContainerUpdateInfo{
				ContainerID:   "c1",
				ContainerName: "app1",
				ImageRef:      "ghcr.io/example/app:latest",
			},
		},
	}
	// Remote fetch failed — entry absent from map.
	remoteDigests := map[string]string{}

	result := selectUpdates(candidates, remoteDigests)
	assert.Empty(t, result, "candidate with no remote digest entry must be skipped")
}

// TestSelectUpdates_SkipsIfRemoteEmpty verifies that an empty remote digest
// string is treated as a failed fetch and the candidate is skipped.
func TestSelectUpdates_SkipsIfRemoteEmpty(t *testing.T) {
	t.Parallel()

	candidates := []updateCandidate{
		{
			localDigest: "sha256:aaa",
			info: models.ContainerUpdateInfo{
				ContainerID:   "c1",
				ContainerName: "app1",
				ImageRef:      "ghcr.io/example/app:latest",
			},
		},
	}
	remoteDigests := map[string]string{
		"ghcr.io/example/app:latest": "",
	}

	result := selectUpdates(candidates, remoteDigests)
	assert.Empty(t, result, "candidate with empty remote digest must be skipped")
}

// TestSelectUpdates_ReportsWhenDigestsDiffer confirms that a candidate whose
// local digest differs from the remote is included in the result.
func TestSelectUpdates_ReportsWhenDigestsDiffer(t *testing.T) {
	t.Parallel()

	imageRef := "ghcr.io/example/app:latest"
	candidates := []updateCandidate{
		{
			localDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			info: models.ContainerUpdateInfo{
				ContainerID:   "c1",
				ContainerName: "app1",
				ImageRef:      imageRef,
			},
		},
	}
	remoteDigests := map[string]string{
		imageRef: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}

	result := selectUpdates(candidates, remoteDigests)
	assert.Len(t, result, 1, "candidate with differing digest must be reported")
	assert.Equal(t, "c1", result[0].ContainerID)
}

// TestSelectUpdates_SkipsWhenDigestsMatch verifies that a candidate whose local
// digest equals the remote is NOT reported as needing an update. This is the
// core bentopdf fix: an up-to-date image must NOT appear in the result.
func TestSelectUpdates_SkipsWhenDigestsMatch(t *testing.T) {
	t.Parallel()

	imageRef := "ghcr.io/bentopdf/app:latest"
	digest := "sha256:268f3e4a00000000000000000000000000000000000000000000000000000e1f"
	candidates := []updateCandidate{
		{
			localDigest: digest,
			info: models.ContainerUpdateInfo{
				ContainerID:   "bentopdf-c1",
				ContainerName: "bentopdf",
				ImageRef:      imageRef,
			},
		},
	}
	remoteDigests := map[string]string{
		imageRef: digest,
	}

	result := selectUpdates(candidates, remoteDigests)
	assert.Empty(t, result,
		"image whose local digest equals remote must NOT be reported as needing update "+
			"(bentopdf phantom-update regression test)")
}

// TestSelectUpdates_MultipleImages tests the mixed case: some images match,
// some differ, some have no remote entry.
func TestSelectUpdates_MultipleImages(t *testing.T) {
	t.Parallel()

	candidates := []updateCandidate{
		{
			localDigest: "sha256:aaa",
			info: models.ContainerUpdateInfo{
				ContainerID: "c-uptodate",
				ImageRef:    "image-uptodate:latest",
			},
		},
		{
			localDigest: "sha256:old",
			info: models.ContainerUpdateInfo{
				ContainerID: "c-stale",
				ImageRef:    "image-stale:latest",
			},
		},
		{
			localDigest: "sha256:any",
			info: models.ContainerUpdateInfo{
				ContainerID: "c-nofetch",
				ImageRef:    "image-nofetch:latest",
			},
		},
	}
	remoteDigests := map[string]string{
		"image-uptodate:latest": "sha256:aaa", // same → skip
		"image-stale:latest":    "sha256:new", // different → report
		// "image-nofetch:latest" absent → skip
	}

	result := selectUpdates(candidates, remoteDigests)
	assert.Len(t, result, 1, "only the stale image should be returned")
	assert.Equal(t, "c-stale", result[0].ContainerID)
}
