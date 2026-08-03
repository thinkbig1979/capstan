package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
