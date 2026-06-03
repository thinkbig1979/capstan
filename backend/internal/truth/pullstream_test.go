package truth

import (
	"strings"
	"testing"
)

func TestDrainPullStream(t *testing.T) {
	t.Parallel()

	t.Run("clean stream returns nil and emits lines", func(t *testing.T) {
		t.Parallel()

		input := strings.Join([]string{
			`{"status":"Pulling from library/alpine","id":"3.20"}`,
			`{"status":"Pull complete","progressDetail":{},"id":"abc123"}`,
			`{"status":"Digest: sha256:1234567890abcdef","progressDetail":{}}`,
			`{"status":"Status: Downloaded newer image for alpine:3.20"}`,
		}, "\n")

		var lines []string
		err := DrainPullStream(strings.NewReader(input), func(line string) {
			lines = append(lines, line)
		})

		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
		if len(lines) == 0 {
			t.Error("expected at least one emitted line, got none")
		}
	})

	t.Run("errorDetail in stream returns error", func(t *testing.T) {
		t.Parallel()

		input := strings.Join([]string{
			`{"status":"Pulling from library/alpine"}`,
			`{"errorDetail":{"message":"manifest for alpine:nonexistent not found"},"error":"manifest for alpine:nonexistent not found"}`,
		}, "\n")

		err := DrainPullStream(strings.NewReader(input), nil)
		if err == nil {
			t.Fatal("expected non-nil error for errorDetail message")
		}
		if !strings.Contains(err.Error(), "manifest for alpine:nonexistent not found") {
			t.Errorf("error message should contain the detail text, got: %v", err)
		}
	})

	t.Run("deprecated error field also returns error", func(t *testing.T) {
		t.Parallel()

		input := `{"error":"pull access denied, repository does not exist or may require authentication"}`

		err := DrainPullStream(strings.NewReader(input), nil)
		if err == nil {
			t.Fatal("expected non-nil error for deprecated error field")
		}
		if !strings.Contains(err.Error(), "pull access denied") {
			t.Errorf("error message should contain pull access denied text, got: %v", err)
		}
	})

	t.Run("empty stream returns nil", func(t *testing.T) {
		t.Parallel()

		err := DrainPullStream(strings.NewReader(""), nil)
		if err != nil {
			t.Errorf("expected nil error for empty stream, got: %v", err)
		}
	})

	t.Run("stream with progress lines calls emit", func(t *testing.T) {
		t.Parallel()

		input := strings.Join([]string{
			`{"status":"Pulling fs layer","progressDetail":{"current":1024,"total":4096},"progress":"[=====>    ]  1.024kB/4.096kB","id":"layer1"}`,
			`{"status":"Download complete","progressDetail":{},"id":"layer1"}`,
		}, "\n")

		var emitted []string
		err := DrainPullStream(strings.NewReader(input), func(line string) {
			emitted = append(emitted, line)
		})

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(emitted) == 0 {
			t.Error("expected at least one emitted progress line")
		}
	})

	t.Run("nil emit does not panic on clean stream", func(t *testing.T) {
		t.Parallel()

		input := `{"status":"Pull complete","progressDetail":{},"id":"abc"}`

		// Should not panic.
		err := DrainPullStream(strings.NewReader(input), nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("stream with stream field emits trimmed content", func(t *testing.T) {
		t.Parallel()

		input := `{"stream":"Step 1/3 : FROM alpine\n"}`

		var emitted []string
		err := DrainPullStream(strings.NewReader(input), func(line string) {
			emitted = append(emitted, line)
		})

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(emitted) != 1 || emitted[0] != "Step 1/3 : FROM alpine" {
			t.Errorf("unexpected emitted lines: %v", emitted)
		}
	})
}
