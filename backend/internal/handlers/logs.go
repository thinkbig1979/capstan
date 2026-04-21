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

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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
}

func NewLogsHandler(docker *services.DockerService, db *database.DB, jwtSecret string, authDisabled bool, dataDir string) *LogsHandler {
	return &LogsHandler{
		docker:       docker,
		db:           db,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
		dataDir:      dataDir,
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
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"LOGS_ERROR",
			"Failed to retrieve logs",
		))
		return
	}

	lines := parseLogLines(output, containerFilter)

	c.JSON(http.StatusOK, gin.H{
		"lines": lines,
	})
}

func (h *LogsHandler) StreamLogs(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		writeJSONError(c, http.StatusNotFound, "STACK_NOT_FOUND", "Stack not found")
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("Failed to upgrade WebSocket connection", "error", err)
		return
	}

	var userID string

	if h.authDisabled {
		userID = "anonymous"
	} else {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var authMsg struct {
			Type  string `json:"type"`
			Token string `json:"token"`
		}
		if err := conn.ReadJSON(&authMsg); err != nil {
			writeCloseMessage(conn, CloseCodeAuthFailure, "Auth timeout")
			conn.Close()
			return
		}

		if authMsg.Type != "auth" || authMsg.Token == "" {
			writeCloseMessage(conn, CloseCodeAuthFailure, "Invalid auth message")
			conn.Close()
			return
		}

		userID, err = authenticateToken(authMsg.Token, h.db, h.jwtSecret)
		if err != nil {
			writeCloseMessage(conn, CloseCodeAuthFailure, "Auth failed")
			conn.Close()
			return
		}
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	filterContainers := make(map[string]bool)
	filterMutex := sync.Mutex{}

	args := h.buildComposeArgs(*stack, "logs", []string{"-f", "--tail=100"})

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = stack.Directory

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

	var writeMu sync.Mutex

	go func() {
		ticker := time.NewTicker(DefaultPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				err := conn.WriteMessage(websocket.PingMessage, nil)
				writeMu.Unlock()
				if err != nil {
					slog.Debug("Failed to send ping", "error", err)
					return
				}
			}
		}
	}()

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
				writeMu.Lock()
				err := writeJSON(conn, logLine)
				writeMu.Unlock()
				if err != nil {
					slog.Debug("Failed to send log line", "error", err)
					goto cleanup
				}
			}
		}
	}

cleanup:
	cancel()
	if cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}

	slog.Debug("Log streaming connection closed", "stackID", id, "userID", userID)
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
		"2006-01-02 15:04:05",
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

	return &LogLine{
		Container: container,
		Timestamp: timestamp,
		Message:   message,
	}
}

func writeJSONError(c *gin.Context, status int, code, message string) {
	c.JSON(status, models.NewAppError(status, code, message))
}
