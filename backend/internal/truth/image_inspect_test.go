package truth

import (
	"context"
	"errors"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
)

// fakeImageInspector is a hand-rolled stub that satisfies ImageInspector
// without requiring a real Docker daemon.
type fakeImageInspector struct {
	containerJSON dockertypes.ContainerJSON
	containerErr  error
	imageInspect  dockertypes.ImageInspect
	imageErr      error
}

func (f *fakeImageInspector) ContainerInspect(_ context.Context, _ string) (dockertypes.ContainerJSON, error) {
	return f.containerJSON, f.containerErr
}

func (f *fakeImageInspector) ImageInspectWithRaw(_ context.Context, _ string) (dockertypes.ImageInspect, []byte, error) {
	return f.imageInspect, nil, f.imageErr
}

// --- ResolveContainerImage ---

func TestResolveContainerImage(t *testing.T) {
	const (
		containerID = "abc123"
		imageID     = "sha256:deadbeef"
		configRef   = "nginx:latest"
	)

	tests := []struct {
		name            string
		cli             *fakeImageInspector
		wantImageRef    string
		wantRepoDigests []string
		wantImageID     string
		wantErrContains string
	}{
		{
			name: "picks first valid RepoTag",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: imageID,
					},
					Config: &containertypes.Config{Image: configRef},
				},
				imageInspect: dockertypes.ImageInspect{
					ID:          imageID,
					RepoTags:    []string{"nginx:1.25", "nginx:latest"},
					RepoDigests: []string{"nginx@sha256:aabbccdd"},
				},
			},
			wantImageRef:    "nginx:1.25",
			wantRepoDigests: []string{"nginx@sha256:aabbccdd"},
			wantImageID:     imageID,
		},
		{
			name: "skips <none>:<none> tags and falls back to next valid tag",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: imageID,
					},
					Config: &containertypes.Config{Image: configRef},
				},
				imageInspect: dockertypes.ImageInspect{
					ID:          imageID,
					RepoTags:    []string{"<none>:<none>", "nginx:stable"},
					RepoDigests: []string{"nginx@sha256:ffff0000"},
				},
			},
			wantImageRef:    "nginx:stable",
			wantRepoDigests: []string{"nginx@sha256:ffff0000"},
			wantImageID:     imageID,
		},
		{
			name: "falls back to Config.Image when all RepoTags are <none>",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: imageID,
					},
					Config: &containertypes.Config{Image: configRef},
				},
				imageInspect: dockertypes.ImageInspect{
					ID:          imageID,
					RepoTags:    []string{"<none>:<none>"},
					RepoDigests: []string{"ghcr.io/foo/bar@sha256:cafecafe"},
				},
			},
			wantImageRef:    configRef,
			wantRepoDigests: []string{"ghcr.io/foo/bar@sha256:cafecafe"},
			wantImageID:     imageID,
		},
		{
			name: "falls back to Config.Image when RepoTags is empty",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: imageID,
					},
					Config: &containertypes.Config{Image: configRef},
				},
				imageInspect: dockertypes.ImageInspect{
					ID:          imageID,
					RepoTags:    nil,
					RepoDigests: []string{"nginx@sha256:00001111"},
				},
			},
			wantImageRef:    configRef,
			wantRepoDigests: []string{"nginx@sha256:00001111"},
			wantImageID:     imageID,
		},
		{
			name: "returns RepoDigests and imageID even when Config is nil",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: imageID,
					},
					Config: nil,
				},
				imageInspect: dockertypes.ImageInspect{
					ID:          imageID,
					RepoTags:    []string{"alpine:3.20"},
					RepoDigests: []string{"alpine@sha256:12345678"},
				},
			},
			wantImageRef:    "alpine:3.20",
			wantRepoDigests: []string{"alpine@sha256:12345678"},
			wantImageID:     imageID,
		},
		{
			name: "propagates container inspect error",
			cli: &fakeImageInspector{
				containerErr: errors.New("no such container"),
			},
			wantErrContains: "inspecting container",
		},
		{
			name: "propagates image inspect error",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: imageID,
					},
					Config: &containertypes.Config{Image: configRef},
				},
				imageErr: errors.New("image not found"),
			},
			wantErrContains: "inspecting image",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// No t.Parallel(): no shared global state in these tests, but keeping
			// consistent with the package convention of not parallelising tests
			// that live alongside global-mutating tests (see imagedigest_test.go).
			gotRef, gotDigests, gotID, err := ResolveContainerImage(context.Background(), tc.cli, containerID)

			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrContains)
				}
				if !containsString(err.Error(), tc.wantErrContains) {
					t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.wantErrContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotRef != tc.wantImageRef {
				t.Errorf("imageRef = %q, want %q", gotRef, tc.wantImageRef)
			}
			if !stringSliceEqual(gotDigests, tc.wantRepoDigests) {
				t.Errorf("repoDigests = %v, want %v", gotDigests, tc.wantRepoDigests)
			}
			if gotID != tc.wantImageID {
				t.Errorf("imageID = %q, want %q", gotID, tc.wantImageID)
			}
		})
	}
}

// --- ContainerImageAdvanced ---

func TestContainerImageAdvanced(t *testing.T) {
	const containerID = "ctr-xyz"

	tests := []struct {
		name            string
		cli             *fakeImageInspector
		oldImageID      string
		wantAdvanced    bool
		wantNewImageID  string
		wantErrContains string
	}{
		{
			name: "advanced when image ID changed",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: "sha256:newimage",
					},
				},
			},
			oldImageID:     "sha256:oldimage",
			wantAdvanced:   true,
			wantNewImageID: "sha256:newimage",
		},
		{
			name: "not advanced when image ID unchanged",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: "sha256:sameimage",
					},
				},
			},
			oldImageID:     "sha256:sameimage",
			wantAdvanced:   false,
			wantNewImageID: "sha256:sameimage",
		},
		{
			name: "reports new image ID even when not advanced",
			cli: &fakeImageInspector{
				containerJSON: dockertypes.ContainerJSON{
					ContainerJSONBase: &dockertypes.ContainerJSONBase{
						Image: "sha256:stableimage",
					},
				},
			},
			oldImageID:     "sha256:stableimage",
			wantAdvanced:   false,
			wantNewImageID: "sha256:stableimage",
		},
		{
			name: "propagates inspect error",
			cli: &fakeImageInspector{
				containerErr: errors.New("daemon not responding"),
			},
			oldImageID:      "sha256:any",
			wantErrContains: "inspecting container",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			advanced, newID, err := ContainerImageAdvanced(context.Background(), tc.cli, containerID, tc.oldImageID)

			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrContains)
				}
				if !containsString(err.Error(), tc.wantErrContains) {
					t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.wantErrContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if advanced != tc.wantAdvanced {
				t.Errorf("advanced = %v, want %v", advanced, tc.wantAdvanced)
			}
			if newID != tc.wantNewImageID {
				t.Errorf("newImageID = %q, want %q", newID, tc.wantNewImageID)
			}
		})
	}
}

// --- pickImageRef ---

func TestPickImageRef(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		fallback string
		want     string
	}{
		{
			name:     "nil tags returns fallback",
			tags:     nil,
			fallback: "nginx:latest",
			want:     "nginx:latest",
		},
		{
			name:     "empty tags returns fallback",
			tags:     []string{},
			fallback: "alpine:3.20",
			want:     "alpine:3.20",
		},
		{
			name:     "skips none tags and returns first real tag",
			tags:     []string{"<none>:<none>", "myapp:v1.0"},
			fallback: "ignored",
			want:     "myapp:v1.0",
		},
		{
			name:     "returns first tag when no none tags",
			tags:     []string{"redis:7", "redis:latest"},
			fallback: "ignored",
			want:     "redis:7",
		},
		{
			name:     "all none tags returns fallback",
			tags:     []string{"<none>:<none>", "<none>"},
			fallback: "config-ref:1.2",
			want:     "config-ref:1.2",
		},
		{
			name:     "skips bare digest-style tag",
			tags:     []string{"@sha256:abcdef", "ubuntu:22.04"},
			fallback: "ignored",
			want:     "ubuntu:22.04",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pickImageRef(tc.tags, tc.fallback)
			if got != tc.want {
				t.Errorf("pickImageRef(%v, %q) = %q, want %q", tc.tags, tc.fallback, got, tc.want)
			}
		})
	}
}

// helpers

func containsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
