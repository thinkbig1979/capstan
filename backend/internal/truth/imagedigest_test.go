package truth

import (
	"context"
	"errors"
	"testing"
)

// --- LocalRepoDigest ---

func TestLocalRepoDigest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		imageRef    string
		repoDigests []string
		wantDigest  string
		wantOK      bool
	}{
		{
			name:     "single repoDigest fallback — bare name image",
			imageRef: "alpine:3.20",
			repoDigests: []string{
				"alpine@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			},
			wantDigest: "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			wantOK:     true,
		},
		{
			name:     "single repoDigest fallback — library image with docker.io prefix",
			imageRef: "nginx:latest",
			repoDigests: []string{
				"docker.io/library/nginx@sha256:aabbccdd1234567890aabbccdd1234567890aabbccdd1234567890aabbccdd12",
			},
			wantDigest: "sha256:aabbccdd1234567890aabbccdd1234567890aabbccdd1234567890aabbccdd12",
			wantOK:     true,
		},
		{
			name:     "exact repo match from multiple repoDigests",
			imageRef: "ghcr.io/bentopdf/app:latest",
			repoDigests: []string{
				"docker.io/library/nginx@sha256:aabbccdd1234567890aabbccdd1234567890aabbccdd1234567890aabbccdd12",
				"ghcr.io/bentopdf/app@sha256:268f3e4a000000000000000000000000000000000000000000000000000000e1f0",
				"quay.io/other/image@sha256:ffffffff0000000000000000000000000000000000000000000000000000ffff",
			},
			wantDigest: "sha256:268f3e4a000000000000000000000000000000000000000000000000000000e1f0",
			wantOK:     true,
		},
		{
			name:     "multiple repoDigests, no match and no single-entry fallback",
			imageRef: "ghcr.io/bentopdf/app:latest",
			repoDigests: []string{
				"docker.io/library/nginx@sha256:aabbccdd1234567890aabbccdd1234567890aabbccdd1234567890aabbccdd12",
				"quay.io/other/image@sha256:ffffffff0000000000000000000000000000000000000000000000000000ffff",
			},
			wantDigest: "",
			wantOK:     false,
		},
		{
			name:        "empty repoDigests list",
			imageRef:    "alpine:latest",
			repoDigests: []string{},
			wantDigest:  "",
			wantOK:      false,
		},
		{
			name:        "nil repoDigests list",
			imageRef:    "alpine:latest",
			repoDigests: nil,
			wantDigest:  "",
			wantOK:      false,
		},
		{
			name:     "imageRef with registry host and port",
			imageRef: "registry.example.com:5000/myapp/service:v2.3",
			repoDigests: []string{
				"registry.example.com:5000/myapp/service@sha256:deadbeef1234567890deadbeef1234567890deadbeef1234567890deadbeef12",
			},
			wantDigest: "sha256:deadbeef1234567890deadbeef1234567890deadbeef1234567890deadbeef12",
			wantOK:     true,
		},
		{
			name:     "imageRef without tag still matches",
			imageRef: "ghcr.io/foo/bar",
			repoDigests: []string{
				"ghcr.io/foo/bar@sha256:cafe1234567890cafe1234567890cafe1234567890cafe1234567890cafe1234",
			},
			wantDigest: "sha256:cafe1234567890cafe1234567890cafe1234567890cafe1234567890cafe1234",
			wantOK:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDigest, gotOK := LocalRepoDigest(tc.imageRef, tc.repoDigests)
			if gotOK != tc.wantOK {
				t.Errorf("LocalRepoDigest(%q, ...) ok = %v, want %v", tc.imageRef, gotOK, tc.wantOK)
			}
			if gotDigest != tc.wantDigest {
				t.Errorf("LocalRepoDigest(%q, ...) digest = %q, want %q", tc.imageRef, gotDigest, tc.wantDigest)
			}
		})
	}
}

// --- imageRefRepository (internal, tested through behaviour) ---

func TestImageRefRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref  string
		want string
	}{
		{"nginx:latest", "nginx"},
		{"nginx", "nginx"},
		{"ghcr.io/foo/bar:1.2", "ghcr.io/foo/bar"},
		{"registry.example.com:5000/a/b:v1", "registry.example.com:5000/a/b"},
		{"alpine@sha256:abc123", "alpine"},
		{"ghcr.io/foo/bar@sha256:abc123", "ghcr.io/foo/bar"},
		{"ghcr.io/foo/bar:latest@sha256:abc123", "ghcr.io/foo/bar"},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			t.Parallel()
			got := imageRefRepository(tc.ref)
			if got != tc.want {
				t.Errorf("imageRefRepository(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// --- ImageUpToDate (stubbed RemoteRegistryDigest) ---

// TestImageUpToDate must NOT call t.Parallel() at the top level or in any
// subtest: every subtest assigns the package-global RemoteRegistryDigest and
// restores it via t.Cleanup. Running them in parallel would cause a data race
// on that shared variable.
func TestImageUpToDate(t *testing.T) {
	const localDigest = "sha256:268f3e4a000000000000000000000000000000000000000000000000000000e1f0"
	const otherDigest = "sha256:ffffffff0000000000000000000000000000000000000000000000000000ffff"

	repoDigests := []string{
		"ghcr.io/bentopdf/app@" + localDigest,
	}

	t.Run("up to date when local == remote", func(t *testing.T) {
		orig := RemoteRegistryDigest
		RemoteRegistryDigest = func(_ context.Context, _ string) (string, error) {
			return localDigest, nil
		}
		t.Cleanup(func() { RemoteRegistryDigest = orig })

		upToDate, local, remote, err := ImageUpToDate(context.Background(), "ghcr.io/bentopdf/app:latest", repoDigests)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !upToDate {
			t.Errorf("expected upToDate=true, got false (local=%s remote=%s)", local, remote)
		}
	})

	t.Run("stale when local != remote", func(t *testing.T) {
		orig := RemoteRegistryDigest
		RemoteRegistryDigest = func(_ context.Context, _ string) (string, error) {
			return otherDigest, nil
		}
		t.Cleanup(func() { RemoteRegistryDigest = orig })

		upToDate, local, remote, err := ImageUpToDate(context.Background(), "ghcr.io/bentopdf/app:latest", repoDigests)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if upToDate {
			t.Errorf("expected upToDate=false, got true (local=%s remote=%s)", local, remote)
		}
		if local != localDigest {
			t.Errorf("local digest = %q, want %q", local, localDigest)
		}
		if remote != otherDigest {
			t.Errorf("remote digest = %q, want %q", remote, otherDigest)
		}
	})

	t.Run("error when remote fetch fails", func(t *testing.T) {
		sentinel := errors.New("registry unreachable")
		orig := RemoteRegistryDigest
		RemoteRegistryDigest = func(_ context.Context, _ string) (string, error) {
			return "", sentinel
		}
		t.Cleanup(func() { RemoteRegistryDigest = orig })

		_, _, _, err := ImageUpToDate(context.Background(), "ghcr.io/bentopdf/app:latest", repoDigests)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("error chain should contain sentinel, got: %v", err)
		}
	})

	t.Run("error when no local repo digest resolved", func(t *testing.T) {
		orig := RemoteRegistryDigest
		RemoteRegistryDigest = func(_ context.Context, _ string) (string, error) {
			return localDigest, nil
		}
		t.Cleanup(func() { RemoteRegistryDigest = orig })

		_, _, _, err := ImageUpToDate(context.Background(), "ghcr.io/bentopdf/app:latest", nil)
		if err == nil {
			t.Fatal("expected error for nil repoDigests, got nil")
		}
	})
}
