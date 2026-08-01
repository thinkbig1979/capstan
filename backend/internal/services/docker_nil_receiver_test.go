package services

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// nilReceiverExclusions lists exported *DockerService methods the reflection
// sweep below cannot call with zero-valued arguments. Every entry needs a
// reason: an empty list is the goal, and a silent skip would defeat the point
// of the sweep.
//
// There are currently none. Keep it that way if you can — a method that cannot
// be called with zero arguments usually cannot be called safely from a handler
// during an outage either.
var nilReceiverExclusions = map[string]string{}

// TestNilDockerService_NoExportedMethodPanics is the mechanical guarantee behind
// this task (agent-os-xay). Auditing "every method guards its nil receiver" by
// hand is only true on the day it is written; the next method someone adds
// reintroduces the crash — a nil dereference inside a streaming goroutine, which
// gin's RecoveryMiddleware cannot catch and which kills the process.
//
// So: enumerate the method set by reflection, call each one on a nil receiver
// with zero-valued arguments, and require that none panics. A new unguarded
// method fails here on the day it lands.
//
// Zero-valued arguments are safe to pass because a guarded method returns before
// touching them. A method that dereferences an argument before checking its
// receiver would fail this test — correctly, because that is the bug.
func TestNilDockerService_NoExportedMethodPanics(t *testing.T) {
	t.Parallel()

	nilSvc := reflect.ValueOf((*DockerService)(nil))
	svcType := nilSvc.Type()

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()

	var called int
	for i := 0; i < svcType.NumMethod(); i++ {
		method := svcType.Method(i)

		// NumMethod on a pointer type lists exported methods only, but be
		// explicit rather than relying on that.
		if !method.IsExported() {
			continue
		}

		if reason, skip := nilReceiverExclusions[method.Name]; skip {
			t.Logf("skipping %s: %s", method.Name, reason)
			continue
		}

		t.Run(method.Name, func(t *testing.T) {
			fn := nilSvc.MethodByName(method.Name)
			fnType := fn.Type()

			// Build zero values for every parameter. context.Context is an
			// interface whose zero value is nil, which a guarded method never
			// touches; pass a real one anyway so a method that reaches for a
			// deadline before its guard fails on the guard, not on the context.
			args := make([]reflect.Value, 0, fnType.NumIn())
			for p := 0; p < fnType.NumIn(); p++ {
				paramType := fnType.In(p)
				if fnType.IsVariadic() && p == fnType.NumIn()-1 {
					// Variadic tail: pass no elements at all.
					break
				}
				if paramType == ctxType {
					args = append(args, reflect.ValueOf(context.Background()))
					continue
				}
				args = append(args, reflect.Zero(paramType))
			}

			var results []reflect.Value
			require.NotPanics(t, func() {
				if fnType.IsVariadic() {
					results = fn.CallSlice(append(args, reflect.MakeSlice(fnType.In(fnType.NumIn()-1), 0, 0)))
					return
				}
				results = fn.Call(args)
			}, "%s must guard its nil receiver: main.go leaves dockerService nil when the daemon is unreachable", method.Name)

			// Returning without panicking is only half the claim. A method that
			// hands back a channel may have spawned the goroutine that fills it,
			// and that is where the process-killing panic lived — gin's
			// RecoveryMiddleware cannot catch it. Drain every channel result so
			// the goroutine, if any, runs to completion inside this test.
			for _, res := range results {
				if res.Kind() != reflect.Chan || res.IsNil() {
					continue
				}
				drainUntilClosed(t, method.Name, res)
			}
		})
		called++
	}

	// Guard against the sweep silently covering nothing (a rename, a build tag,
	// a reflection mistake) and reporting success.
	require.GreaterOrEqual(t, called, 30,
		"expected the sweep to cover the full DockerService surface; got %d methods", called)
	t.Logf("swept %d exported methods, %d excluded", called, len(nilReceiverExclusions))
}

// drainUntilClosed reads ch to completion, failing if it does not close.
//
// Two things are being checked. First, that any goroutine behind the channel
// runs here rather than after the test returns — a panic in it would otherwise
// escape attribution (and, in production, the process). Second, that the
// channel closes at all: a method that returns a never-closing channel during
// an outage blocks its caller forever, which is a different way of hanging the
// server rather than a refusal.
func drainUntilClosed(t *testing.T, methodName string, ch reflect.Value) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, ok := ch.Recv(); !ok {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s returned a channel that never closed; a caller would block "+
			"forever during a Docker outage instead of being refused", methodName)
	}
}

// TestNilDockerService_FailsLoudly covers the dangerous half of the surface: a
// method that returns only values degrades SILENTLY on a nil receiver, handing
// the caller a zero value it reads as real data. A method returning an error
// degrades loudly by construction.
//
// Every exported method must therefore return, on a nil receiver, at least one
// of:
//   - a non-nil error wrapping ErrDockerUnavailable;
//   - a truth.ActionResult with Outcome == failed and the outage as its reason;
//   - a channel carrying a terminal error frame.
//
// Reflection enforces the rule for the whole method set, so a new method with a
// value-only signature cannot quietly return zeros during an outage.
func TestNilDockerService_FailsLoudly(t *testing.T) {
	t.Parallel()

	nilSvc := reflect.ValueOf((*DockerService)(nil))
	svcType := nilSvc.Type()

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errType := reflect.TypeOf((*error)(nil)).Elem()
	arType := reflect.TypeOf(truth.ActionResult{})
	streamLineType := reflect.TypeOf(StreamLine{})

	for i := 0; i < svcType.NumMethod(); i++ {
		method := svcType.Method(i)
		if _, skip := nilReceiverExclusions[method.Name]; skip {
			continue
		}
		// ValidateName is the one exported method that never touches the
		// receiver — it validates a string. Claiming a Docker outage from it
		// would be a lie, so it is exempt from the sentinel rule by design.
		if method.Name == "ValidateName" {
			continue
		}

		t.Run(method.Name, func(t *testing.T) {
			fn := nilSvc.MethodByName(method.Name)
			fnType := fn.Type()

			args := make([]reflect.Value, 0, fnType.NumIn())
			for p := 0; p < fnType.NumIn(); p++ {
				if fnType.IsVariadic() && p == fnType.NumIn()-1 {
					break
				}
				if fnType.In(p) == ctxType {
					args = append(args, reflect.ValueOf(context.Background()))
					continue
				}
				args = append(args, reflect.Zero(fnType.In(p)))
			}

			var results []reflect.Value
			if fnType.IsVariadic() {
				results = fn.CallSlice(append(args, reflect.MakeSlice(fnType.In(fnType.NumIn()-1), 0, 0)))
			} else {
				results = fn.Call(args)
			}

			for _, res := range results {
				switch {
				case res.Type() == errType:
					err, _ := res.Interface().(error)
					require.ErrorIs(t, err, ErrDockerUnavailable,
						"%s returns an error; it must be the outage sentinel", method.Name)
					return

				case res.Type() == arType:
					ar := res.Interface().(truth.ActionResult)
					require.Equal(t, truth.OutcomeFailed, ar.Outcome,
						"%s must report a failed outcome, not a zero-valued success", method.Name)
					require.ErrorIs(t, ar.Err, ErrDockerUnavailable)
					require.Contains(t, ar.Reason, "Docker daemon unreachable",
						"%s must give the operator an actionable reason", method.Name)
					return

				case res.Kind() == reflect.Chan && res.Type().Elem() == streamLineType:
					require.False(t, res.IsNil(), "%s must return a readable channel", method.Name)
					frame, ok := res.Recv()
					require.True(t, ok, "%s must emit a terminal error frame before closing", method.Name)
					line := frame.Interface().(StreamLine)
					require.Equal(t, "error", line.Type)
					require.Contains(t, line.Error, "Docker daemon unreachable")
					return
				}
			}

			t.Fatalf("%s returns only values with no way to signal the outage: "+
				"a caller would read its zero values as a real result. Give it an "+
				"error, a truth.ActionResult, or an error frame.", method.Name)
		})
	}
}
