package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestTerminalWS_MissingStackClosesWithNotFoundNotRetryableCode is the
// PERMANENT half of agent-os-vi0o's terminal.go:86 split. Before the fix,
// `if err != nil || stack == nil` collapsed "stack genuinely does not exist"
// (sql.ErrNoRows) with "the database itself faulted" into one
// writeCloseMessage(websocket.CloseNormalClosure, ...) — a code frontend/src/
// lib/ws.ts's shouldReconnectAfter does NOT suppress, so a deleted stack
// redials forever at ~1/second (agent-os-jj8u's mechanism: serveWS upgrades
// before this check runs, so onopen has already fired and zeroed
// reconnectAttempts). This is agent-os-7lg1's db.GetStack collapse in its
// WS-shaped form.
//
// Asserts on the CLOSE CODE itself (dialTerminal, terminal_scope_test.go),
// never on a duration — the instrument note both vi0o and jj8u insist on.
func TestTerminalWS_MissingStackClosesWithNotFoundNotRetryableCode(t *testing.T) {
	cm := NewConnectionManager(10)
	f := newTerminalFixture(t, cm, &fakeContainerLister{})

	code, text := dialTerminal(t, f, "no-such-stack", "irrelevant-container")

	if code != CloseCodeNotFound {
		t.Fatalf("close code = %d (%q), want %d (CloseCodeNotFound) — a missing stack must close "+
			"with a code frontend/src/lib/ws.ts's shouldReconnectAfter suppresses, or the client "+
			"redials a stack that will never exist at ~1/second (agent-os-vi0o)", code, text, CloseCodeNotFound)
	}
	if text != "Stack not found" {
		t.Errorf("close reason = %q, want %q", text, "Stack not found")
	}
}

// TestTerminalWS_DatabaseFaultClosesWithRetryableCode is the NEGATIVE control
// vi0o's acceptance criteria require: a TRANSIENT failure (the database
// itself faulted, not a missing row) must still close with a code the
// frontend DOES reconnect after. Without this arm, a fix that suppressed
// retry for every db.GetStack error (not just ErrNoRows) would look
// identical to the correct, narrower fix — and would break recovery from an
// ordinary transient DB hiccup (a locked sqlite file, a decrypt fault).
//
// faultyDB (faulty_db_test.go) is a *database.DB whose underlying connection
// is already closed: every query fails with "sql: database is closed",
// proven NOT to be sql.ErrNoRows by
// TestFaultyDB_FailsDifferentlyFromHealthyNotFound in that file.
//
// Asserts the POSITIVE shape the transient branch writes (1000 + "Failed to
// load stack"), not "code is outside the suppressed set". The complement was
// satisfiable by a crash: with the branch deleted (go test -overlay,
// agent-os-hd91) the handler fell through to stack.ProjectName on a nil stack,
// net/http recovered the panic, the client saw 1006, and 1006 is not
// suppressed either — a crashed handler passed. 1006 is exactly what this
// assertion now rejects.
func TestTerminalWS_DatabaseFaultClosesWithRetryableCode(t *testing.T) {
	cm := NewConnectionManager(10)
	f := newTerminalFaultyDBFixture(t, cm)

	code, text := dialTerminal(t, f, "any-stack-id", "irrelevant-container")

	if code != websocket.CloseNormalClosure {
		t.Fatalf("close code = %d (%q), want %d (CloseNormalClosure) — the transient branch's own "+
			"code, which frontend/src/lib/ws.ts's shouldReconnectAfter does not suppress; anything "+
			"else (a suppressed code, or 1006 from a crashed handler) is not that branch (agent-os-vi0o, agent-os-hd91)",
			code, text, websocket.CloseNormalClosure)
	}
	if text != "Failed to load stack" {
		t.Errorf("close reason = %q, want %q", text, "Failed to load stack")
	}
}

// newTerminalFaultyDBFixture is the transient-arm counterpart to
// newTerminalFixture (terminal_scope_test.go): identical wiring, but db is a
// faultyDB so h.db.GetStack fails with something other than sql.ErrNoRows.
//
// authDisabled=true means upgradeConnection never reads db during
// authentication (ws.go: userID = "anon:"+c.ClientIP() on that path), so the
// only query the closed connection sees is the GetStack call this test is
// about. actions is nil: assertContainerInStack's ActionLogger call is
// unreached here, since GetStack fails and returns before that check ever
// runs.
func newTerminalFaultyDBFixture(t *testing.T, cm *ConnectionManager) *terminalFixture {
	t.Helper()

	db := faultyDB(t)
	terminal := services.NewTerminalService(&config.Config{})
	lister := &fakeContainerLister{}
	handler := NewTerminalHandler(terminal, lister, db, cm, nil)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &terminalFixture{server: srv, db: db, terminal: terminal, cm: cm, lister: lister}
}
