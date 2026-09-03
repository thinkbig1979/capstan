package handlers

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type LogLine struct {
	Container string `json:"container"`
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

type LogFilterMessage struct {
	Type       string   `json:"type"`
	Containers []string `json:"containers"`
}

type LogsHandler struct {
	docker       *services.DockerService
	db           *database.DB
	jwtSecret    string
	authDisabled bool
	dataDir      string
	cm           *ConnectionManager
}

func NewLogsHandler(docker *services.DockerService, db *database.DB, jwtSecret string, authDisabled bool, dataDir string, cm *ConnectionManager) *LogsHandler {
	return &LogsHandler{
		docker:       docker,
		db:           db,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
		dataDir:      dataDir,
		cm:           cm,
	}
}

func (h *LogsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/stacks/:id/logs", h.GetLogs)
	group.GET("/ws/logs/:id", h.StreamLogs)
}

func (h *LogsHandler) GetLogs(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	tailStr := c.DefaultQuery("tail", "100")
	tail, err := strconv.Atoi(tailStr)
	if err != nil || tail < 1 {
		tail = 100
	}
	if tail > 5000 {
		tail = 5000
	}

	containerFilter := c.Query("container")

	output, err := h.docker.Logs(*stack, tail)
	if err != nil {
		slog.Error("Failed to get logs", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "LOGS_ERROR", "Failed to retrieve logs")
		return
	}

	lines := parseLogLines(output, containerFilter)

	c.JSON(http.StatusOK, gin.H{
		"lines": lines,
	})
}

func (h *LogsHandler) StreamLogs(c *gin.Context) {
	id := c.Param("id")

	// Refuse before the upgrade, not after: an upgraded socket that immediately
	// closes reads to the operator as a network fault, while a 503 names the
	// cause. h.docker is the concrete *services.DockerService here, so the nil
	// check means what it says (agent-os-xay).
	if h.docker == nil {
		writeJSONError(c, http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE", DockerUnavailableMessage)
		return
	}

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		writeJSONError(c, http.StatusNotFound, "STACK_NOT_FOUND", "Stack not found")
		return
	}

	wsConn, err := upgradeConnection(c, h.db, h.jwtSecret, h.authDisabled)
	if err != nil {
		return
	}

	if err := h.cm.Add(wsConn.ID, wsConn); err != nil {
		writeCloseMessage(wsConn.Conn, websocket.CloseNormalClosure, "Connection limit exceeded")
		wsConn.Conn.Close()
		return
	}
	defer h.cm.Remove(wsConn.ID)

	conn := wsConn.Conn
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	filterContainers := make(map[string]bool)
	filterMutex := sync.Mutex{}

	cmd := h.buildLogsCmd(ctx, *stack)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.Error("Failed to create stdout pipe", "error", err)
		writeCloseMessage(conn, websocket.CloseInternalServerErr, "Failed to create log stream")
		return
	}

	if err := cmd.Start(); err != nil {
		slog.Error("Failed to start docker compose logs", "error", err)
		writeCloseMessage(conn, websocket.CloseInternalServerErr, "Failed to start log stream")
		return
	}

	logChan := make(chan string, 100)

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			logChan <- scanner.Text()
		}
		close(logChan)
	}()

	// Through wsConn.WriteMutex via safePingLoop, not the private mutex this
	// used to declare locally: the ping writer here and the log-line writer
	// below are two independent data writers on the same connection, and
	// WriteMutex is what keeps them from racing each other (a raw write on
	// either side panics gorilla on a concurrent WriteMessage). The close
	// path (CloseForSession/CloseForUser) is safe against both regardless —
	// it sends its close frame via WriteControl, not through WriteMutex at
	// all (agent-os-teop; see safeWriteCloseMessage's doc comment).
	go safePingLoop(ctx, wsConn, DefaultPingInterval)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var msg LogFilterMessage
				if err := readJSON(conn, &msg); err != nil {
					slog.Debug("Failed to read WebSocket message", "error", err)
					cancel()
					return
				}

				if msg.Type == "filter" {
					filterMutex.Lock()
					filterContainers = make(map[string]bool)
					for _, c := range msg.Containers {
						filterContainers[c] = true
					}
					filterMutex.Unlock()
					slog.Debug("Updated log filter", "containers", msg.Containers)
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			goto cleanup
		case line, ok := <-logChan:
			if !ok {
				goto cleanup
			}

			logLine := parseLogLine(line)
			if logLine == nil {
				continue
			}

			filterMutex.Lock()
			shouldSend := len(filterContainers) == 0 || filterContainers[logLine.Container]
			filterMutex.Unlock()

			if shouldSend {
				if err := safeWriteJSON(wsConn, logLine); err != nil {
					slog.Debug("Failed to send log line", "error", err)
					goto cleanup
				}
			}
		}
	}

cleanup:
	cancel()
	if cmd.Process != nil {
		// Best-effort teardown: Kill fails harmlessly if the process already
		// exited, and Wait's error (an expected non-zero exit from the kill
		// signal) isn't actionable here.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	slog.Debug("Log streaming connection closed", "stackID", id, "userID", wsConn.UserID)
}

// buildLogsCmd builds the `docker compose logs` child process for
// StreamLogs, without starting it. Split out from StreamLogs so tests can
// build and run the constructed *exec.Cmd directly (see logs_test.go) rather
// than only through the full websocket flow (agent-os-3ux).
func (h *LogsHandler) buildLogsCmd(ctx context.Context, stack models.Stack) *exec.Cmd {
	args := h.buildComposeArgs(stack, "logs", []string{"-f", "--tail=100", "--timestamps"})

	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = stack.Directory
	// Scrub Capstan's own secrets (JWT_SECRET, STORAGE_KEY, GIT_HTTPS_TOKEN)
	// out of the child's environment. A nil Env here would let `docker
	// compose logs` inherit Capstan's full os.Environ(), and the compose file
	// it reads is user-authored content (any authenticated user can write it
	// verbatim — handlers/compose.go, handlers/stack_crud.go) that `docker
	// compose` interpolates ${VAR} from the process environment into
	// (agent-os-3ux, following agent-os-iey's fix for the sibling sites in
	// package services).
	cmd.Env = services.DockerEnv()
	return cmd
}

func (h *LogsHandler) buildComposeArgs(stack models.Stack, subcommand string, extraArgs []string) []string {
	args := []string{"compose"}

	if h.docker != nil {
		globalEnvPath := h.dataDir + "/global.env"
		if _, err := os.Stat(globalEnvPath); err == nil {
			args = append(args, "--env-file", globalEnvPath)
		}
		if stack.EnvFile != "" {
			args = append(args, "--env-file", stack.EnvFile)
		}
	}

	args = append(args, "-f", stack.ComposeFile)
	args = append(args, "-p", stack.ProjectName)
	args = append(args, subcommand)
	args = append(args, extraArgs...)

	return args
}

func parseLogLines(output string, containerFilter string) []LogLine {
	lines := strings.Split(output, "\n")
	result := make([]LogLine, 0, len(lines))

	for _, line := range lines {
		logLine := parseLogLine(line)
		if logLine == nil {
			continue
		}

		if containerFilter != "" && logLine.Container != containerFilter {
			continue
		}

		result = append(result, *logLine)
	}

	return result
}

func parseLogLine(line string) *LogLine {
	if line == "" {
		return nil
	}

	parts := strings.SplitN(line, "|", 2)
	if len(parts) < 2 {
		return nil
	}

	container := strings.TrimSpace(parts[0])
	rest := parts[1]

	rest = strings.TrimSpace(rest)
	if rest == "" {
		return &LogLine{
			Container: container,
			Timestamp: "",
			Message:   "",
		}
	}

	timestamp := ""
	message := rest

	timeFormats := []string{
		"2006-01-02T15:04:05.000000000Z",
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05.000Z",
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05.000Z",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		time.RFC3339Nano,
	}

	for _, format := range timeFormats {
		if len(rest) >= len(format) {
			potentialTime := rest[:len(format)]
			if _, err := time.Parse(format, potentialTime); err == nil {
				timestamp = potentialTime
				message = strings.TrimSpace(rest[len(format):])
				break
			}
		}
	}

	for _, format := range timeFormats {
		if len(message) >= len(format) {
			potentialTime := message[:len(format)]
			if _, err := time.Parse(format, potentialTime); err == nil {
				message = strings.TrimSpace(message[len(format):])
				break
			}
		}
	}

	return &LogLine{
		Container: container,
		Timestamp: timestamp,
		Message:   message,
	}
}

func writeJSONError(c *gin.Context, status int, code, message string) {
	c.JSON(status, models.NewAppError(status, code, message))
}
