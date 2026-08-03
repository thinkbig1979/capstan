package handlers

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// concurrentCreateRouter wires a Create-only router over a real database and a
// real scanner, with one OperationLock shared by every request — the production
// wiring (cmd/server/main.go passes a single lock to NewStacksHandler).
func concurrentCreateRouter(t *testing.T) (*gin.Engine, *database.DB, string) {
	t.Helper()

	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	cfg := &config.Config{StacksDir: tempDir}
	handler := NewStacksHandler(&fakeStackDocker{}, services.NewScannerService(cfg, db),
		services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	return router, db, tempDir
}

type concurrentCreate struct {
	name    string
	compose string
}

type concurrentCreateResult struct {
	code int
	body string
}

// raceCreates fires every create simultaneously and returns each caller's own
// outcome, positionally. Every per-request allocation (JSON body, request,
// recorder) happens before the barrier, so the only thing the released
// goroutines race on is ServeHTTP itself — that is what makes the overlap
// reliable rather than a matter of goroutine start-up luck.
func raceCreates(t *testing.T, router *gin.Engine, creates []concurrentCreate) []concurrentCreateResult {
	t.Helper()

	start := make(chan struct{})
	results := make([]concurrentCreateResult, len(creates))

	var wg sync.WaitGroup
	for i, cr := range creates {
		body, err := json.Marshal(map[string]any{
			"name":           cr.name,
			"composeContent": cr.compose,
			"deploy":         false,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		wg.Add(1)
		go func(i int, req *http.Request, w *httptest.ResponseRecorder) {
			defer wg.Done()
			<-start
			router.ServeHTTP(w, req)
			results[i] = concurrentCreateResult{code: w.Code, body: w.Body.String()}
		}(i, req, w)
	}

	close(start)
	wg.Wait()
	return results
}

// errorCodeOf pulls the models.AppError code out of an error response body.
func errorCodeOf(t *testing.T, body string) string {
	t.Helper()
	var appErr models.AppError
	require.NoError(t, json.Unmarshal([]byte(body), &appErr), "response is not an AppError: %s", body)
	return appErr.Code
}

// TestStacksHandler_Create_ConcurrentSameName_NoSilentLoss is the regression
// test for agent-os-0br: two simultaneous POST /stacks for the same name both
// returned 201 while only one caller's compose content reached disk — the other
// caller was told its stack was created and its content was silently discarded.
//
// Create took no operation lock at all, so both requests cleared the os.Stat
// duplicate guard before either had made the directory.
//
// The assertions are deliberately on OUTCOMES (status codes, surviving file
// content, row count), never on the race detector: this is a filesystem/logical
// race, not a memory race, and `-race` reports nothing for it — a gate built on
// -race goes green with the defect fully present.
//
// The result is exact rather than probabilistic in both directions. If the two
// requests overlap, the loser loses the operation lock; if they happen to
// serialise, the loser hits the os.Stat guard. Both paths answer 409
// DUPLICATE_STACK, so the post-fix expectation does not depend on scheduling.
func TestStacksHandler_Create_ConcurrentSameName_NoSilentLoss(t *testing.T) {
	router, db, tempDir := concurrentCreateRouter(t)

	const composeA = "services:\n  a:\n    image: nginx:1.21\n    restart: unless-stopped\n"
	const composeB = "services:\n  b:\n    image: redis:7\n    restart: unless-stopped\n"

	creates := []concurrentCreate{
		{name: "my-stack", compose: composeA},
		{name: "my-stack", compose: composeB},
	}
	results := raceCreates(t, router, creates)

	created := []int{}
	rejected := []int{}
	for i, r := range results {
		switch r.code {
		case http.StatusCreated:
			created = append(created, i)
		default:
			rejected = append(rejected, i)
		}
	}
	t.Logf("OBSERVED codes=[%d %d] created=%d", results[0].code, results[1].code, len(created))

	require.Len(t, created, 1,
		"exactly one create must succeed; got %d 201s (bodies: %q / %q)",
		len(created), results[0].body, results[1].body)

	// The loser must be told, with the same code the sequential duplicate path
	// already returns for the same user-visible situation.
	loser := results[rejected[0]]
	require.Equal(t, http.StatusConflict, loser.code, "the loser must get 409, body=%s", loser.body)
	require.Equal(t, models.ErrDuplicateStack, errorCodeOf(t, loser.body),
		"the loser must get DUPLICATE_STACK, body=%s", loser.body)

	// Nothing was silently discarded: the compose on disk belongs to the caller
	// that was told its stack was created.
	//nolint:gosec // the path is a test fixture under t.TempDir()
	onDisk, err := os.ReadFile(filepath.Join(tempDir, "my-stack", "compose.yaml"))
	require.NoError(t, err)
	require.Equal(t, creates[created[0]].compose, string(onDisk),
		"the surviving compose must be the winner's content, not the loser's")

	// One stack, one directory.
	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1, "exactly one stacks row must exist, got %+v", stacks)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly one stack directory must exist, got %+v", entries)
}

// TestStacksHandler_Create_ConcurrentDifferentNames_BothSucceed is the positive
// control for the test above. Without it, "one of them got a 409" would also be
// satisfied by serialising Create so hard that unrelated concurrent creates
// conflict — the operation lock is a try-lock that fails fast rather than
// queueing, so a lock keyed too broadly (the target directory, or a global key)
// would turn two unrelated creates into a spurious 409.
func TestStacksHandler_Create_ConcurrentDifferentNames_BothSucceed(t *testing.T) {
	router, db, tempDir := concurrentCreateRouter(t)

	const composeA = "services:\n  a:\n    image: nginx:1.21\n    restart: unless-stopped\n"
	const composeB = "services:\n  b:\n    image: redis:7\n    restart: unless-stopped\n"

	creates := []concurrentCreate{
		{name: "stack-a", compose: composeA},
		{name: "stack-b", compose: composeB},
	}
	results := raceCreates(t, router, creates)

	for i, r := range results {
		require.Equal(t, http.StatusCreated, r.code,
			"unrelated concurrent create %q must succeed, body=%s", creates[i].name, r.body)

		//nolint:gosec // the path is a test fixture under t.TempDir()
		onDisk, err := os.ReadFile(filepath.Join(tempDir, creates[i].name, "compose.yaml"))
		require.NoError(t, err)
		require.Equal(t, creates[i].compose, string(onDisk))
	}

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 2, "both stacks must be registered, got %+v", stacks)
}

// TestCreate_LockAcquiredBeforeDuplicateStat is a STRUCTURAL regression test
// for agent-os-0br's lock ordering (agent-os-zpg).
//
// It deliberately does NOT try to drive the race behaviourally. Two
// independent mutation sweeps established that with a try-lock the loser is
// rejected either way: at Acquire if the two requests are simultaneous, or at
// the os.Stat guard below if they happen to serialise. A barrier-based test
// (see raceCreates above) makes the two requests simultaneous, so it cannot
// tell "lock before stat" from "lock after stat" apart — both placements pass
// it. A symmetric delay inserted between stat and acquire does not help
// either: it still leaves both requests contending at the same instant, so
// one still loses the try-lock the same way. Separating the two placements
// behaviourally would need request B descheduled between ITS stat and ITS
// acquire for longer than request A's entire create (lint + file writes + DB
// insert + scan) — an asymmetric deschedule that cannot be driven from
// outside without a test-only seam in production code, which was considered
// and rejected as not worth the cost for this one property. See the comment
// above the Acquire call in Create (stack_crud.go) for the full account.
//
// What DOES differ, provably, between the two placements: which statement
// comes first in the source. This test parses stack_crud.go, locates the
// h.opLock.Acquire(...) call and the os.Stat(stackDir) duplicate-check call
// inside Create, and asserts Acquire's line comes before the stat's. Moving
// Acquire below the stat — the natural-looking refactor, since that is where
// the duplicate check lives — fails this test immediately and requires no
// interleaving to be reproduced.
//
// Verified against three mutations of Create, each reverted afterward
// (agent-os-zpg):
//   - Acquire and the stat swapped (the reordering this test exists to catch):
//     FAILS with an exact line-number message, e.g. "112 is not less than 103".
//   - The Acquire call deleted from Create entirely: package still compiles
//     (stackID stays referenced later in Create), and this test FAILS with
//     "did not find an h.opLock.Acquire(...) call inside Create" — not a
//     silent pass on a stale zero-value comparison.
//   - The os.Stat call deleted from Create entirely: package still compiles,
//     and this test FAILS with "did not find an os.Stat(...) call inside
//     Create", for the same reason.
//
// The explicit require.NotZero checks below exist specifically so the "call
// not found" case reports its own clear message instead of falling through
// to require.Less and comparing 0 against a real line (which would read as a
// passing or confusingly-failing ordering check rather than "I stopped
// finding what I was looking for").
//
// File resolution: srcPath is built from runtime.Caller(0), i.e. this test
// file's own path as recorded in the compiled binary — not the process's
// working directory, so it is unaffected by where `go test` is invoked from.
// If stack_crud.go is ever renamed, parser.ParseFile fails to open it and
// require.NoError below fails loudly with that filesystem error — it does
// not silently stop finding its subject. If Create is ever split so the
// Acquire/Stat calls move into a helper function, ast.Inspect only walks
// Create's own body, so this test would fail with the same "did not find"
// messages above and need a human to point it at the new location — a loud
// break on refactor, not a silent one, which is the failure mode this test
// is designed to avoid.
func TestCreate_LockAcquiredBeforeDuplicateStat(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must resolve this test file's own path")
	srcPath := filepath.Join(filepath.Dir(thisFile), "stack_crud.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, srcPath, nil, 0)
	require.NoError(t, err, "stack_crud.go must parse as valid Go")

	var createFn *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Create" && fn.Recv != nil {
			createFn = fn
			break
		}
	}
	require.NotNil(t, createFn, "method Create not found in stack_crud.go")

	var acquireLine, statLine int
	ast.Inspect(createFn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		switch sel.Sel.Name {
		// h.opLock.Acquire(...)
		case "Acquire":
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "opLock" {
				require.Zero(t, acquireLine,
					"Create must call opLock.Acquire exactly once; found a second call at %s",
					fset.Position(call.Pos()))
				acquireLine = fset.Position(call.Pos()).Line
			}
		// os.Stat(...)
		case "Stat":
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" {
				require.Zero(t, statLine,
					"Create must call os.Stat exactly once; found a second call at %s",
					fset.Position(call.Pos()))
				statLine = fset.Position(call.Pos()).Line
			}
		}
		return true
	})

	require.NotZero(t, acquireLine, "did not find an h.opLock.Acquire(...) call inside Create")
	require.NotZero(t, statLine, "did not find an os.Stat(...) call inside Create")
	require.Less(t, acquireLine, statLine,
		"h.opLock.Acquire (line %d) must be called before the os.Stat duplicate-check guard (line %d) — "+
			"see the comment above the Acquire call in Create for why this ordering is not, and cannot be, "+
			"enforced by a behavioural test (agent-os-zpg)",
		acquireLine, statLine)
}
