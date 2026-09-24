package handlers

import (
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// This file is the handler-side half of agent-os-ymyc's boundary.
//
// Before it, 18 routes each hand-built their own 404 from a raw
// sql.ErrNoRows check. The wire shape of every one of them was therefore
// pinned only by whichever per-site test happened to exist, and about a dozen
// sites in this class had already answered 404 for a database FAULT
// (7lg1, 3h9x, 1gqn, 8tqd, g482, r1by, xzoe, obgr, l42o, uacg, koy9, 89ut, rltu).
//
// Two things are pinned here, and they are different claims:
//
//  1. handleError's mapping — a Kind becomes a status, a code and a message.
//  2. That each of the 23 collapsed routes (18 from ymyc, 4 from symj, 1 from vupj) asks for the mapping it used to
//     produce by hand. The behavioural table cannot see that on its own,
//     because every route reaches the same function; the source census at the
//     bottom is what makes each row's "route" column true.
//
// The source files are read through go:embed rather than os.ReadFile on
// purpose: a mutation applied with `go test -overlay` is compiled in but never
// written to disk, so an os.ReadFile census would read the UNMUTATED file and
// pass against the very mutant it exists to catch.

//go:embed backup.go compose.go directories.go env.go git.go logs.go monitoring.go operations.go stack_crud.go stack_lifecycle.go stacks.go updates.go
var collapsedRouteSources embed.FS

// notFoundRoute is one collapsed route: the getter it calls, the Kind that
// getter mints, and the exact wire it answered BEFORE the collapse. Every
// expected value below was read off the pre-change tree at 6166396, so a
// change to any of them is a wire-contract break, not a test to update.
type notFoundRoute struct {
	File     string
	Function string
	Getter   string
	Kind     string
	FaultMsg string
	WantCode string
	WantMsg  string
}

var collapsedRoutes = []notFoundRoute{
	{"stack_lifecycle.go", "Start", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"stack_lifecycle.go", "Stop", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"stack_lifecycle.go", "Restart", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"stack_lifecycle.go", "Pull", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"stack_crud.go", "Delete", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"stacks.go", "Get", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"env.go", "Get", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"env.go", "Put", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"env.go", "Create", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"compose.go", "Get", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"compose.go", "Put", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"compose.go", "PutComposeAndEnv", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"logs.go", "GetLogs", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"backup.go", "upsertPolicy", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"backup.go", "runRestore", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"backup.go", "getRunDetail", "GetBackupRunByID", "backup run", "Failed to load backup run", models.ErrNotFound, "Backup run not found"},
	{"directories.go", "UpdateCredentials", "GetDirectory", "directory", "Failed to load directory", models.ErrNotFound, "Directory not found"},
	{"directories.go", "CredentialStatus", "GetDirectory", "directory", "Failed to load directory", models.ErrNotFound, "Directory not found"},
	// agent-os-symj: these four answered NOT_FOUND for an absent stack while the
	// fifteen stack rows above answered STACK_NOT_FOUND for the identical
	// condition from the identical getter. They converge on STACK_NOT_FOUND, so
	// here, unlike the rows above, the expected code is a deliberate change.
	{"updates.go", "updateStack", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"git.go", "resolvePathFromStack", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"monitoring.go", "getStackContainers", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	{"monitoring.go", "handleMetricsWebSocket", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
	// agent-os-vupj: answered an absent stack with a bare {"error"} body and no
	// code at all. Like the symj rows, the expected code is a deliberate change.
	{"operations.go", "handleOperation", "GetStack", "stack", "Failed to load stack", models.ErrStackNotFound, "Stack not found"},
}

func runHandleDBError(t *testing.T, err error, faultMsg string) (int, models.AppError) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	handleDBError(c, err, faultMsg)
	var body models.AppError
	// Not a discard: a body that does not parse as an AppError is itself a
	// wire-contract break, and silently unmarshalling into a zero value would
	// make every field assertion below compare "" against "" and pass.
	if uerr := json.Unmarshal(w.Body.Bytes(), &body); uerr != nil {
		t.Fatalf("response body is not an AppError: %v (body %q)", uerr, w.Body.String())
	}
	return w.Code, body
}

// TestNotFoundWire_CollapsedRoutes is the per-route wire table. Each row runs
// BOTH arms on one instrument, because a one-armed check cannot tell a correct
// mapping from one that answers 404 for everything — which is the defect this
// whole class is made of.
func TestNotFoundWire_CollapsedRoutes(t *testing.T) {
	t.Parallel()
	if len(collapsedRoutes) != 23 {
		t.Fatalf("table has %d rows, want 23 — one per collapsed route", len(collapsedRoutes))
	}

	fault := errors.New("sql: database is closed")

	for _, r := range collapsedRoutes {
		t.Run(r.File+"/"+r.Function, func(t *testing.T) {
			absent := &errdefs.NotFoundError{Kind: r.Kind, Key: "whatever"}
			status, body := runHandleDBError(t, absent, r.FaultMsg)
			if status != http.StatusNotFound {
				t.Errorf("absent %s: status = %d, want 404", r.Getter, status)
			}
			if body.Code != r.WantCode {
				t.Errorf("absent %s: code = %q, want %q (this is the wire contract, not a test to update)", r.Getter, body.Code, r.WantCode)
			}
			if body.Message != r.WantMsg {
				t.Errorf("absent %s: message = %q, want %q", r.Getter, body.Message, r.WantMsg)
			}

			// The other arm: a fault must NOT borrow the 404.
			status, body = runHandleDBError(t, fault, r.FaultMsg)
			if status != http.StatusInternalServerError {
				t.Errorf("faulted %s: status = %d, want 500 — a database that could not answer is not an absent row", r.Getter, status)
			}
			if body.Code != "INTERNAL_ERROR" {
				t.Errorf("faulted %s: code = %q, want INTERNAL_ERROR", r.Getter, body.Code)
			}
			if body.Message != r.FaultMsg {
				t.Errorf("faulted %s: message = %q, want %q — the route's own diagnostic message is what names the failing call", r.Getter, body.Message, r.FaultMsg)
			}
		})
	}
}

// TestNotFoundWire_RouteCensus makes the table's "route" column a claim about
// the source rather than a label. Without it, every row could be satisfied by a
// single correct handleDBError while a route quietly kept its own hand-built
// 404 — which is exactly the pre-change state.
func TestNotFoundWire_RouteCensus(t *testing.T) {
	t.Parallel()

	type site struct{ fn, msg string }
	found := map[string][]site{}
	funcRe := regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?([A-Za-z0-9_]+)\(`)
	// Two call shapes route an absence through notFoundWire: handleDBError in a
	// handler, and dbError in a helper that returns its error to a caller which
	// hands it to handleError (git.go's resolvePathFromStack).
	callRe := regexp.MustCompile(`(?:handleDBError\(c, err, |dbError\(err, )"([^"]*)"\)`)

	files, err := collapsedRouteSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded sources: %v", err)
	}
	total := 0
	for _, f := range files {
		b, err := collapsedRouteSources.ReadFile(f.Name())
		if err != nil {
			t.Fatalf("read %s: %v", f.Name(), err)
		}
		cur := ""
		for _, line := range strings.Split(string(b), "\n") {
			if m := funcRe.FindStringSubmatch(line); m != nil {
				cur = m[1]
			}
			if m := callRe.FindStringSubmatch(line); m != nil {
				found[f.Name()] = append(found[f.Name()], site{cur, m[1]})
				total++
			}
		}
	}

	// Positive control: the census must actually FIND things. A regex that
	// matched nothing would make every "expected site is present" check below
	// fail loudly, but this states the corpus size so a silent narrowing of the
	// embed list is visible too.
	if total != 23 {
		t.Fatalf("census found %d handleDBError/dbError call sites across %d embedded files, want 23 — the table and the source disagree", total, len(files))
	}

	for _, r := range collapsedRoutes {
		hit := false
		for _, s := range found[r.File] {
			if s.fn == r.Function {
				hit = true
				if s.msg != r.FaultMsg {
					t.Errorf("%s/%s calls handleDBError with %q, table says %q", r.File, r.Function, s.msg, r.FaultMsg)
				}
			}
		}
		if !hit {
			t.Errorf("%s/%s is in the table but does not call handleDBError — it either kept a hand-built 404 or was renamed", r.File, r.Function)
		}
	}

	// And the reverse direction, which is the one that catches a NEW site added
	// later without a row: every call site found must be in the table.
	for file, sites := range found {
		for _, s := range sites {
			in := false
			for _, r := range collapsedRoutes {
				if r.File == file && r.Function == s.fn {
					in = true
				}
			}
			if !in {
				t.Errorf("%s/%s calls handleDBError but has no row in collapsedRoutes — add one naming the wire code it must answer", file, s.fn)
			}
		}
	}
}

// TestHandleError_NotFoundMapsTo404 pins the mapping itself, including the
// ordering that makes it safe: an explicit AppError minted by a handler is a
// decision the absence branch must not override.
func TestHandleError_NotFoundMapsTo404(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, err error) (int, models.AppError) {
		t.Helper()
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
		handleError(c, err)
		var body models.AppError
		if uerr := json.Unmarshal(w.Body.Bytes(), &body); uerr != nil {
			t.Fatalf("response body is not an AppError: %v (body %q)", uerr, w.Body.String())
		}
		return w.Code, body
	}

	for kind, want := range notFoundWire {
		t.Run("kind="+kind, func(t *testing.T) {
			status, body := run(t, &errdefs.NotFoundError{Kind: kind, Key: "k"})
			if status != http.StatusNotFound || body.Code != want.Code || body.Message != want.Message {
				t.Fatalf("kind %q -> %d/%s/%q, want 404/%s/%q", kind, status, body.Code, body.Message, want.Code, want.Message)
			}
		})
	}

	t.Run("a plain fault is still 500", func(t *testing.T) {
		status, body := run(t, errors.New("sql: database is closed"))
		if status != http.StatusInternalServerError || body.Code != "INTERNAL_ERROR" {
			t.Fatalf("fault -> %d/%s, want 500/INTERNAL_ERROR", status, body.Code)
		}
	})

	t.Run("an explicit AppError wins over the absence branch", func(t *testing.T) {
		// A handler that deliberately wraps an absence in its own AppError has
		// made a decision. If the absence branch ran first it would silently
		// rewrite every such route's status and code.
		wrapped := models.NewAppErrorWithCause(http.StatusConflict, "OPERATION_IN_PROGRESS", "busy", &errdefs.NotFoundError{Kind: "stack", Key: "s"})
		status, body := run(t, wrapped)
		if status != http.StatusConflict || body.Code != "OPERATION_IN_PROGRESS" {
			t.Fatalf("wrapped absence -> %d/%s, want 409/OPERATION_IN_PROGRESS", status, body.Code)
		}
	})

	t.Run("an unlisted Kind still answers 404, not 500", func(t *testing.T) {
		status, body := run(t, &errdefs.NotFoundError{Kind: "something new", Key: "k"})
		if status != http.StatusNotFound || body.Code != models.ErrNotFound {
			t.Fatalf("unlisted kind -> %d/%s, want 404/%s", status, body.Code, models.ErrNotFound)
		}
	})
}
