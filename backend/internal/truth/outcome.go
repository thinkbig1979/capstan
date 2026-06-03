// Package truth provides the Action Truth Contract primitives: typed outcomes,
// consistent HTTP rendering, and helpers that every domain action uses to prove
// its effects rather than trusting proxy signals such as exit codes.
package truth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Outcome is the canonical result class for every mutating action.
type Outcome string

const (
	// OutcomeSuccess means the action completed and its effect was verified.
	OutcomeSuccess Outcome = "success"

	// OutcomeNoChange means the resource was already in the desired state;
	// the action was a verified no-op (e.g. image already up to date).
	OutcomeNoChange Outcome = "no_change"

	// OutcomePartial means the action succeeded for a subset of targets;
	// at least one sub-operation failed. HTTP 207.
	OutcomePartial Outcome = "partial"

	// OutcomeFailed means the action failed; the resource is unchanged or
	// left in an indeterminate state.
	OutcomeFailed Outcome = "failed"
)

// ActionResult is the return type for every mutating action.
// Err is intentionally excluded from JSON so the wire format stays clean;
// callers use Err for internal error propagation.
type ActionResult struct {
	Outcome Outcome        `json:"outcome"`
	Reason  string         `json:"reason"`
	Details map[string]any `json:"details,omitempty"`
	Err     error          `json:"-"`
}

// HTTPStatus returns the appropriate HTTP status code for the result.
// success and no_change → 200
// partial → 207 (Multi-Status)
// failed → 500
func (r ActionResult) HTTPStatus() int {
	switch r.Outcome {
	case OutcomeSuccess, OutcomeNoChange:
		return http.StatusOK
	case OutcomePartial:
		return http.StatusMultiStatus
	case OutcomeFailed:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// Render writes the ActionResult as JSON to a gin context using the
// appropriate HTTP status code. All action endpoints should use this
// to ensure consistent wire format across domains.
func Render(c *gin.Context, r ActionResult) {
	c.JSON(r.HTTPStatus(), r)
}

// kv is a key-value pair used by the optional details variadic arguments.
type kv struct {
	key string
	val any
}

// KV constructs a detail entry for use with Success, NoChange, Partial, and Failed.
// Example: Success("image pulled", KV("digest", "sha256:abc..."))
func KV(key string, val any) kv {
	return kv{key: key, val: val}
}

func buildDetails(pairs []kv) map[string]any {
	if len(pairs) == 0 {
		return nil
	}
	m := make(map[string]any, len(pairs))
	for _, p := range pairs {
		m[p.key] = p.val
	}
	return m
}

// Success returns an ActionResult indicating the action completed and its
// effect was verified.
func Success(reason string, details ...kv) ActionResult {
	return ActionResult{
		Outcome: OutcomeSuccess,
		Reason:  reason,
		Details: buildDetails(details),
	}
}

// NoChange returns an ActionResult indicating the resource was already in the
// desired state and no mutation occurred.
func NoChange(reason string, details ...kv) ActionResult {
	return ActionResult{
		Outcome: OutcomeNoChange,
		Reason:  reason,
		Details: buildDetails(details),
	}
}

// Partial returns an ActionResult indicating partial success across a set of
// targets. Use details to report which targets succeeded and which failed.
func Partial(reason string, details ...kv) ActionResult {
	return ActionResult{
		Outcome: OutcomePartial,
		Reason:  reason,
		Details: buildDetails(details),
	}
}

// Failed returns an ActionResult indicating the action failed. err may be nil
// if no underlying error is available; reason should always describe what went wrong.
func Failed(reason string, err error, details ...kv) ActionResult {
	return ActionResult{
		Outcome: OutcomeFailed,
		Reason:  reason,
		Details: buildDetails(details),
		Err:     err,
	}
}
