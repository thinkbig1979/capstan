package truth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/dockerenv"
)

// reDigestLine matches the top-level "Digest: sha256:<hex>" line that
// `docker buildx imagetools inspect` emits regardless of buildx version.
var reDigestLine = regexp.MustCompile(`(?m)^Digest:\s+(sha256:[0-9a-f]+)`)

// LocalRepoDigest returns the sha256 digest from the RepoDigest entry whose
// repository component matches the repository of imageRef.
//
// imageRef may carry a registry host, a tag, or both; they are stripped before
// comparison. For example, given imageRef "ghcr.io/bentopdf/app:latest" the
// function looks for a RepoDigest whose repository is "ghcr.io/bentopdf/app".
//
// If exactly one repoDigest is present and no repository prefix can be matched,
// the function falls back to that single entry (permissive for bare-name images
// such as "nginx:latest" whose RepoDigest is "nginx@sha256:…").
//
// Returns the bare "sha256:<hex>" string and true on success, or "", false if
// no matching entry is found or none carry a digest.
func LocalRepoDigest(imageRef string, repoDigests []string) (string, bool) {
	repo := imageRefRepository(imageRef)

	// First pass: look for a RepoDigest whose repo component matches.
	for _, rd := range repoDigests {
		at := strings.LastIndex(rd, "@")
		if at < 0 {
			continue
		}
		rdRepo := rd[:at]
		digest := rd[at+1:]
		if !strings.HasPrefix(digest, "sha256:") {
			continue
		}
		if rdRepo == repo {
			return digest, true
		}
	}

	// Single-entry fallback: if there is exactly one repoDigest and it carries a
	// valid digest, return it even though the repo prefix did not match. This
	// handles images pulled by bare name (e.g. "alpine") where the RepoDigest
	// is "docker.io/library/alpine@sha256:…" but imageRef is just "alpine".
	if len(repoDigests) == 1 {
		rd := repoDigests[0]
		at := strings.LastIndex(rd, "@")
		if at >= 0 {
			digest := rd[at+1:]
			if strings.HasPrefix(digest, "sha256:") {
				return digest, true
			}
		}
	}

	return "", false
}

// imageRefRepository strips a tag (and optional registry host's path) from an
// image reference, returning just the repository portion.
//
// Examples:
//
//	"nginx:latest"                      → "nginx"
//	"nginx"                             → "nginx"
//	"ghcr.io/foo/bar:1.2"              → "ghcr.io/foo/bar"
//	"registry.example.com:5000/a/b:v1" → "registry.example.com:5000/a/b"
func imageRefRepository(ref string) string {
	// Strip digest if present (ref@sha256:…)
	if idx := strings.LastIndex(ref, "@"); idx >= 0 {
		ref = ref[:idx]
	}
	// Strip tag: find the last colon, but only if what follows looks like a
	// plain tag (no slashes after the colon), so we don't strip a registry port.
	if idx := strings.LastIndex(ref, ":"); idx >= 0 {
		after := ref[idx+1:]
		if !strings.Contains(after, "/") {
			ref = ref[:idx]
		}
	}
	return ref
}

// buildImagetoolsRawCmd and buildImagetoolsVerboseCmd build (without
// starting) the two `docker buildx imagetools inspect` child processes
// RemoteRegistryDigest runs. Split out so tests can build and run each
// *exec.Cmd directly (see imagedigest_test.go) without a real docker binary.
//
// Unlike the docker/compose sites in package services, these two calls do
// not parse a compose file, so they carry no ${VAR} interpolation vector for
// an attacker-controlled compose file to exploit. cmd.Env is still scrubbed
// here as hygiene: a nil Env would otherwise still hand Capstan's own
// JWT_SECRET/STORAGE_KEY/GIT_HTTPS_TOKEN to a docker child process for no
// reason, which is the class of leak agent-os-iey and this bead both close
// (agent-os-3ux).
func buildImagetoolsRawCmd(ctx context.Context, ref string) *exec.Cmd {
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := exec.CommandContext(ctx, "docker", "buildx", "imagetools", "inspect", ref, "--raw")
	cmd.Env = dockerenv.Env()
	return cmd
}

func buildImagetoolsVerboseCmd(ctx context.Context, ref string) *exec.Cmd {
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := exec.CommandContext(ctx, "docker", "buildx", "imagetools", "inspect", ref)
	cmd.Env = dockerenv.Env()
	return cmd
}

// RemoteRegistryDigest fetches the registry's current manifest-list (index)
// digest for the given image reference. It is a package-level var so tests can
// stub it.
//
// Implementation strategy (version-independent):
//  1. Run `docker buildx imagetools inspect <ref> --raw` and hash the raw bytes
//     with SHA-256. This is robust across all buildx versions.
//  2. If that fails (buildx missing, non-zero exit), fall back to parsing the
//     top-level "Digest: sha256:<hex>" line from the verbose output of
//     `docker buildx imagetools inspect <ref>` (no --format, which is silently
//     ignored on buildx ≤ 0.23.0 — the bentopdf production bug).
//
// Never uses --format; never relies on --format's output.
var RemoteRegistryDigest = func(ctx context.Context, ref string) (string, error) {
	// Primary: hash the raw manifest bytes.
	rawOut, err := buildImagetoolsRawCmd(ctx, ref).Output()
	if err == nil && len(rawOut) > 0 {
		sum := sha256.Sum256(rawOut)
		return "sha256:" + hex.EncodeToString(sum[:]), nil
	}

	// Fallback: parse the top-level Digest line from verbose output.
	verboseOut, verboseErr := buildImagetoolsVerboseCmd(ctx, ref).Output()
	if verboseErr != nil {
		// Return the original error for better diagnostics.
		if err != nil {
			return "", fmt.Errorf("buildx imagetools inspect --raw: %w; fallback also failed: %v", err, verboseErr)
		}
		return "", fmt.Errorf("buildx imagetools inspect: %w", verboseErr)
	}

	m := reDigestLine.FindSubmatch(verboseOut)
	if m == nil {
		return "", fmt.Errorf("no Digest line found in buildx imagetools output for %s", ref)
	}
	return string(m[1]), nil
}

// ImageUpToDate compares the locally stored RepoDigest for imageRef with the
// remote registry's current index digest.
//
// Returns:
//   - upToDate: true when local == remote
//   - local: the digest found in repoDigests (empty string if not resolvable)
//   - remote: the digest fetched from the registry
//   - err: non-nil if either digest cannot be resolved; callers must not
//     silently treat an error as "up to date" or "update available"
func ImageUpToDate(ctx context.Context, imageRef string, repoDigests []string) (upToDate bool, local string, remote string, err error) {
	local, ok := LocalRepoDigest(imageRef, repoDigests)
	if !ok {
		return false, "", "", fmt.Errorf("could not resolve local repo digest for %s from %v", imageRef, repoDigests)
	}

	remote, err = RemoteRegistryDigest(ctx, imageRef)
	if err != nil {
		return false, local, "", fmt.Errorf("fetching remote digest for %s: %w", imageRef, err)
	}

	return local == remote, local, remote, nil
}
