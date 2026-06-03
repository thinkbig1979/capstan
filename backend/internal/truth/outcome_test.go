package truth

import (
	"errors"
	"net/http"
	"testing"
)

func TestOutcomeConstructors(t *testing.T) {
	t.Parallel()

	t.Run("Success carries reason and details", func(t *testing.T) {
		t.Parallel()
		r := Success("image pulled", KV("digest", "sha256:abc"))
		if r.Outcome != OutcomeSuccess {
			t.Errorf("got outcome %q, want %q", r.Outcome, OutcomeSuccess)
		}
		if r.Reason != "image pulled" {
			t.Errorf("got reason %q, want %q", r.Reason, "image pulled")
		}
		if r.Details["digest"] != "sha256:abc" {
			t.Errorf("got detail digest %v, want sha256:abc", r.Details["digest"])
		}
		if r.Err != nil {
			t.Errorf("Success Err should be nil, got %v", r.Err)
		}
	})

	t.Run("NoChange carries reason, no error", func(t *testing.T) {
		t.Parallel()
		r := NoChange("already up to date")
		if r.Outcome != OutcomeNoChange {
			t.Errorf("got outcome %q, want %q", r.Outcome, OutcomeNoChange)
		}
		if r.Details != nil {
			t.Errorf("expected nil details, got %v", r.Details)
		}
		if r.Err != nil {
			t.Errorf("NoChange Err should be nil, got %v", r.Err)
		}
	})

	t.Run("Partial carries reason and details", func(t *testing.T) {
		t.Parallel()
		r := Partial("2 of 3 services restarted", KV("failed", "svc-c"))
		if r.Outcome != OutcomePartial {
			t.Errorf("got outcome %q, want %q", r.Outcome, OutcomePartial)
		}
		if r.Details["failed"] != "svc-c" {
			t.Errorf("got detail failed %v, want svc-c", r.Details["failed"])
		}
	})

	t.Run("Failed carries reason and wraps err", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("pull timeout")
		r := Failed("pull failed", sentinel)
		if r.Outcome != OutcomeFailed {
			t.Errorf("got outcome %q, want %q", r.Outcome, OutcomeFailed)
		}
		if !errors.Is(r.Err, sentinel) {
			t.Errorf("expected sentinel error, got %v", r.Err)
		}
	})

	t.Run("Failed with nil err is valid", func(t *testing.T) {
		t.Parallel()
		r := Failed("unknown failure", nil)
		if r.Err != nil {
			t.Errorf("expected nil Err, got %v", r.Err)
		}
	})

	t.Run("No details when no KV pairs given", func(t *testing.T) {
		t.Parallel()
		r := Success("done")
		if r.Details != nil {
			t.Errorf("expected nil details map when no KV pairs provided, got %v", r.Details)
		}
	})
}

func TestHTTPStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		result   ActionResult
		wantHTTP int
	}{
		{"success → 200", Success("ok"), http.StatusOK},
		{"no_change → 200", NoChange("already up to date"), http.StatusOK},
		{"partial → 207", Partial("2/3 done"), http.StatusMultiStatus},
		{"failed → 500", Failed("boom", nil), http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.result.HTTPStatus()
			if got != tc.wantHTTP {
				t.Errorf("HTTPStatus() = %d, want %d", got, tc.wantHTTP)
			}
		})
	}
}
