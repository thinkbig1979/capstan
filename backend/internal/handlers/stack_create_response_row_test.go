package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// ---------------------------------------------------------------------------
// agent-os-hgtb — POST /api/v1/stacks must report the row it persisted
// ---------------------------------------------------------------------------
//
// Create builds a models.Stack literal, upserts it, then runs a SYNCHRONOUS
// ScanDirectoryWithRoot that upserts OVER that row with the scanner's own
// values — and then serialised the LOCAL literal, not the repaired row. So the
// API answered a create with a stack description the same handler's own
// database already contradicted.
//
// THE ASSERTION IS ROW-EQUALITY, NOT A NAMED FIELD, deliberately. The bead was
// filed about IsGitRepo/GitBranch, and those two turn out NOT to be the field
// that diverges here: resolveGitState (services/scanner.go) looks for `.git`
// inside the stack's OWN directory, and Create always makes a brand-new
// subdirectory — an existing one is a 409 — so at scan time there is never a
// `.git` in it and BOTH the literal and the row carry false/"". They agree, for
// a reason that has nothing to do with the handler being correct. What diverged
// on this tree was Status: the literal said "stopped" and the scanner wrote
// "unknown" (services/scanner.go, the models.Stack it upserts). Since
// agent-os-qags.23 UpsertStack keeps an existing row's status, so that
// divergence is gone; see
// TestStacksHandler_Create_RowKeepsTheStatusCreateWrote. Pinning the whole row
// rather than one field catches a divergence the day one appears, including
// the git fields the day a git-backed create becomes reachable, and
// does not have to be rewritten in between.
//
// ALL THREE RESPONSE PATHS ARE COVERED. The stale local was rendered at the
// created path AND in the shared `details` map both deploy branches build, so a
// fix verified only on the first would leave two paths defective.
//
// WHAT THE TWO DEPLOY ARMS CAN AND CANNOT PROVE, stated because a reader will
// otherwise assume more of them than is true. Only the no-deploy arm is
// fail-first: it FAILS against the unfixed handler on `status` ("stopped" vs
// "unknown"). The two deploy arms PASS against the unfixed handler, because the
// deploy path writes UpdateStackStatus(verifiedStatus) to the row and assigns
// the same value to the local, so status agrees there — and every other field
// agrees by coincidence, per the git-fields note above. They are therefore
// regression guards, not discriminators of this defect, and they arm the moment
// any scanner-written field starts to diverge. That they are wired to their own
// response path rather than vacuous was proved separately with a mutant that
// renders a different struct into the shared `details` map: it kills exactly
// these two and neither of the others.

// hgtbFixture wires a Create handler over a real scanner and an in-memory DB,
// and registers the target directory. docker decides which of the three
// response paths the request takes.
func hgtbFixture(t *testing.T, docker stackDocker, stackName string) (*database.DB, *gin.Engine) {
	t.Helper()
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: tempDir}
	handler := NewStacksHandler(docker, services.NewScannerService(cfg, db),
		services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)

	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))
	createTestDirectory(t, db, filepath.Join(tempDir, stackName))
	return db, router
}

// hgtbCreate posts a create and returns the recorder.
func hgtbCreate(t *testing.T, router *gin.Engine, stackName string, deploy bool) *httptest.ResponseRecorder {
	t.Helper()
	reqBytes, err := json.Marshal(map[string]any{
		"name":           stackName,
		"composeContent": "services:\n  web:\n    image: nginx:1.21\n",
		"deploy":         deploy,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// hgtbAssertResponseMatchesRow is the whole point of this file: whatever the
// response says about the stack, the stored row says the same. It compares the
// row re-encoded through the SAME json tags, so a field added to models.Stack
// is covered without touching this helper.
func hgtbAssertResponseMatchesRow(t *testing.T, db *database.DB, body []byte, path string) {
	t.Helper()

	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))
	details, ok := resp["details"].(map[string]any)
	require.True(t, ok, "response has no details object: %s", body)
	respStack, ok := details["stack"].(map[string]any)
	require.True(t, ok, "response details carry no stack: %s", body)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1, "exactly one stack should have been created")

	rowJSON, err := json.Marshal(stacks[0])
	require.NoError(t, err)
	var rowStack map[string]any
	require.NoError(t, json.Unmarshal(rowJSON, &rowStack))

	// Containers are a live-Docker decoration the row never stores, so they are
	// not part of what "the persisted row" means.
	delete(respStack, "containers")
	delete(rowStack, "containers")

	assert.Equal(t, rowStack, respStack,
		"the %s response describes a different stack than the row this same handler stored. "+
			"The scan inside Create upserts over the row it wrote, so the response must be re-read "+
			"from the database rather than serialised from the struct built before the scan "+
			"(agent-os-hgtb)", path)
}

// TestStacksHandler_Create_ResponseMatchesPersistedRow covers the no-deploy
// path (the 201 "stack created" response).
func TestStacksHandler_Create_ResponseMatchesPersistedRow(t *testing.T) {
	const name = "hgtb-plain"
	db, router := hgtbFixture(t, &recordingStackDocker{}, name)

	w := hgtbCreate(t, router, name, false)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	hgtbAssertResponseMatchesRow(t, db, w.Body.Bytes(), "created")
}

// TestStacksHandler_CreateWithDeploy_ResponseMatchesPersistedRow covers the
// deploy-succeeded path. It shares the `details` map with the 207 path below,
// so the two together pin both halves of the switch.
func TestStacksHandler_CreateWithDeploy_ResponseMatchesPersistedRow(t *testing.T) {
	const name = "hgtb-deployed"
	db, router := hgtbFixture(t, &recordingStackDocker{}, name)

	w := hgtbCreate(t, router, name, true)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	hgtbAssertResponseMatchesRow(t, db, w.Body.Bytes(), "created and deployed")
}

// TestStacksHandler_CreateDeployFails_ResponseMatchesPersistedRow covers the
// 207 created-but-not-deployed path — the one a user is most likely to be
// looking at, since it is the one that leaves them on a stack detail page with
// something to fix.
func TestStacksHandler_CreateDeployFails_ResponseMatchesPersistedRow(t *testing.T) {
	const name = "hgtb-partial"
	db, router := hgtbFixture(t, failingStartStackDocker{}, name)

	w := hgtbCreate(t, router, name, true)
	require.Equal(t, http.StatusMultiStatus, w.Code, w.Body.String())

	hgtbAssertResponseMatchesRow(t, db, w.Body.Bytes(), "created but not deployed")
}

// TestStacksHandler_Create_RowKeepsTheStatusCreateWrote is the premise control
// for the three tests above, rewritten by agent-os-qags.23. Until then the
// scanner's upsert replaced the whole row, so Status was the one field that
// differed between Create's literal ("stopped") and the scanned row
// ("unknown"), and this test pinned that divergence. UpsertStack now keeps the
// stored status of an existing row, so the scan leaves "stopped" in place and
// the literal and the row agree on every field on this tree. The three tests
// above are regression guards from here on, not discriminators of agent-os-hgtb:
// they arm again the day a scanner-written field diverges (a git-backed create).
// What this asserts instead is the qags.23 behaviour at the Create seam.
func TestStacksHandler_Create_RowKeepsTheStatusCreateWrote(t *testing.T) {
	const name = "hgtb-premise"
	db, router := hgtbFixture(t, &recordingStackDocker{}, name)

	w := hgtbCreate(t, router, name, false)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)

	assert.Equal(t, "stopped", stacks[0].Status,
		"Create writes \"stopped\" and the synchronous scan after it must not reset it to \"unknown\" (agent-os-qags.23)")
}
