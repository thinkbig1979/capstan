package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// OperationStreamer is the operations handler's view of DockerService: stream a
// compose subcommand. Narrow enough to fake in a test, which is what lets the
// connection-cap behaviour be covered without a live daemon.
type OperationStreamer interface {
	RunStreaming(ctx context.Context, stack models.Stack, subcommand string, extraArgs []string) <-chan services.StreamLine
}

type OperationsHandler struct {
	// docker is nil when the daemon was unreachable at startup.
	docker OperationStreamer
	db     *database.DB
	opLock *services.OperationLock
	cm     *ConnectionManager
}

func NewOperationsHandler(docker OperationStreamer, db *database.DB, opLock *services.OperationLock, cm *ConnectionManager) *OperationsHandler {
	return &OperationsHandler{
		docker: docker,
		db:     db,
		opLock: opLock,
		cm:     cm,
	}
}

func (h *OperationsHandler) RegisterRoutes(group *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	group.GET("/ws/operations/:id/:action", h.handleOperation(jwtSecret, authDisabled))
}

func (h *OperationsHandler) handleOperation(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		stackID := c.Param("id")
		action := c.Param("action")

		// main leaves dockerService nil when the daemon was unreachable at
		// startup. RunStreaming dereferences it inside a goroutine, so the
		// resulting nil-pointer panic is not caught by RecoveryMiddleware and
		// takes the whole process down. Refuse before the upgrade instead, the
		// way the checks below already do. (Same shape as agent-os-ck4; the
		// wider audit of nil-docker paths is agent-os-xay.)
		if h.docker == nil {
			// agent-os-ua4y owner decision 2026-09-05: normalise this site's
			// ad-hoc gin.H body to the AppError {code,message} shape used
			// everywhere else. Status 503 is unchanged; DOCKER_UNAVAILABLE is a
			// new code (there was no machine-readable code here before).
			// NO-CAUSE: h.docker == nil is a boolean condition, not an error —
			// there is nothing in scope to attach, and services.ErrDockerUnavailable
			// is not minted here since that sentinel represents a different
			// condition (a wired but nil-receiver *DockerService, see dockerSvc's
			// doc comment in stacks.go) that did not occur on this path.
			// frontend consumer checked: hooks/useStreamingOperation.ts's
			// WSClient (lib/ws.ts) speaks raw WS frames only and never parses a
			// failed-upgrade JSON body (respond.go's renderDockerResult doc
			// comment: "a browser cannot read a failed handshake"), so this body
			// change is invisible to any current caller.
			handleError(c, models.NewAppError(http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE", DockerUnavailableMessage))
			return
		}

		// nil arm dropped, dead per GetStack's return shape (GetStack() in
		// internal/database/stacks.go always returns either &stack or a non-nil
		// err, never (nil, nil)).
		//
		// These refusals all happen before the upgrade, so a browser WebSocket
		// never reads their bodies (it sees only a failed connection). They are
		// AppErrors anyway so a non-browser client gets the same {code,message}
		// as the REST lifecycle routes for the same condition (agent-os-vupj).
		stack, err := h.db.GetStack(stackID)
		if err != nil {
			handleDBError(c, err, "Failed to load stack")
			return
		}

		// Validated before Acquire: action names the lock holder in the 409
		// another client reads, so it must be one of these four, never raw input.
		var subcommand string
		var extraArgs []string
		switch action {
		case "pull":
			subcommand = "pull"
		case "start":
			subcommand = "up"
			extraArgs = []string{"-d"}
		case "stop":
			subcommand = "down"
		case "restart":
			subcommand = "restart"
		default:
			handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Unknown action: "+action))
			return
		}

		lockToken, err := h.opLock.Acquire(stackID, action)
		if err != nil {
			handleError(c, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress, err.Error()))
			return
		}
		// The one release path for this acquisition (agent-os-a1ye.4). It is
		// called explicitly right after the streaming body below, and deferred
		// as the safety net for the early returns before it and for panics.
		// OnceFunc makes the second call a no-op, so the deferred call can never
		// release a lock that a newer operation on this stack acquired after the
		// explicit one freed it.
		releaseLock := sync.OnceFunc(func() { h.opLock.Release(stackID, lockToken) })
		defer releaseLock()

		// After authentication, so the cap keys on a real user ID. Operations
		// streams were the other endpoint missing from the ConnectionManager
		// every other WebSocket handler already uses (agent-os-a0y).
		conn, release, err := serveWS(c, h.db, jwtSecret, authDisabled, h.cm, wsRegistration{
			refuseCode:   CloseCodeRateLimit,
			refuseReason: "Too many open connections",
			onRefuse: func(conn *Connection) {
				slog.Warn("Operations connection refused: per-user limit reached",
					"user_id", conn.UserID, "stack_id", stackID, "action", action)
			},
		})
		if err != nil {
			return
		}
		// release() closes the connection and deregisters it, in that order.
		defer release()

		// The whole streaming body is wrapped in an IIFE so that every return
		// path below — not just the final fallthrough — releases the stack
		// lock (right after the closure call, below) before the outer
		// function's deferred conn.Conn.Close() unwinds. Defers run LIFO, so
		// without this the deferred releaseLock (the safety net for the early
		// returns between Acquire and here, and for panics) would be the LAST
		// thing to run on return — after the socket is already closed —
		// letting a client that observed the close redial the same stack and
		// hit a spurious 409 because this goroutine had not finished unwinding
		// yet (agent-os-o26). releaseLock is a sync.OnceFunc, so the deferred
		// call after this one does nothing.
		func() {
			// DESIGN CHOICE (agent-os-a1ye.3): the compose process does NOT run
			// under the socket's context. It used to, and the reader below
			// cancelled it on any read error, so closing the browser tab killed
			// a `compose up`/`down` part-way and could leave a restart stopped.
			// A detached compose up/down must finish rather than be killed by a
			// disconnect (or by shutdown: main.go hands this handler no server
			// context, so none cancels it either). RunStreaming bounds it with
			// the compose deadline, CAPSTAN_COMPOSE_TIMEOUT, which is what keeps
			// a hung child from holding the lock forever. There is no client
			// cancel frame to honour: the UI's cancel only closes the socket.
			opCtx := context.Background()

			// The socket's own lifetime: ends the ping loop once the client
			// is gone. It never reaches the process.
			wsCtx, wsCancel := context.WithCancel(context.Background())
			defer wsCancel()

			go safePingLoop(wsCtx, conn, DefaultPingInterval)

			go func() {
				for {
					if _, _, err := conn.Conn.ReadMessage(); err != nil {
						conn.logReadErr(err)
						wsCancel()
						return
					}
				}
			}()

			// send writes to the client until the first failure, then drops
			// everything after it. The caller keeps draining RunStreaming
			// either way: its goroutines block on a full channel, so an
			// abandoned channel would stall the process it streams.
			detached := false
			send := func(v any, what string) {
				if detached {
					return
				}
				if wsCtx.Err() != nil {
					detached = true
				} else if err := safeWriteJSON(conn, v); err != nil {
					slog.Debug("Failed to write "+what+"; operation continues without a client", "error", err)
					detached = true
				}
				if detached {
					slog.Info("Client left a streaming operation; it continues to completion",
						"stack_id", stackID, "action", action)
				}
			}

			send(gin.H{
				"type":   "start",
				"action": action,
				"stack":  stack.ProjectName,
			}, "start frame")

			if action == "restart" {
				// Two-phase restart: stream the down phase first, then the up phase.
				// Only the up-phase terminal done frame is the definitive result
				// (finding #18 fix: down-phase done is consumed and not forwarded as terminal).
				stopFailed := false
				for line := range h.docker.RunStreaming(opCtx, *stack, "down", nil) {
					if line.Type == "done" {
						if !line.Success {
							// The stop phase failed: forward its done frame
							// as the result and do not start.
							send(line, "stop failure frame")
							stopFailed = true
						}
						// Stop succeeded — do not forward the intermediate done frame;
						// the client will see the phase announcement instead.
						continue
					}
					send(line, "stop output")
				}
				if stopFailed {
					return
				}
				send(gin.H{
					"type":    "phase",
					"phase":   "starting",
					"message": "Stack stopped, starting...",
				}, "phase frame")
				subcommand = "up"
				extraArgs = []string{"-d"}
			}

			// Stream the main (or up-phase) command. The terminal done frame now
			// carries outcome+reason from the verified end state (finding #5 + #18 fix).
			for line := range h.docker.RunStreaming(opCtx, *stack, subcommand, extraArgs) {
				send(line, "operation output")
			}

			slog.Info("Streaming operation completed", "stack_id", stackID, "action", action)
		}()

		releaseLock()
	}
}
