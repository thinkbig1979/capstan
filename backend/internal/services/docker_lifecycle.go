package services

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// StreamLine is a single line of streaming output from a compose command.
// The Outcome and Reason fields are only populated on the terminal "done" frame;
// they carry the verified lifecycle outcome so the frontend can treat the done
// frame as the definitive result (finding #18 fix).
type StreamLine struct {
	Type    string        `json:"type"`
	Line    string        `json:"line,omitempty"`
	Success bool          `json:"success,omitempty"`
	Error   string        `json:"error,omitempty"`
	Outcome truth.Outcome `json:"outcome,omitempty"`
	Reason  string        `json:"reason,omitempty"`
}

// lifecycleAction names the lifecycle operations that have a verifiable end state.
type lifecycleAction string

const (
	actionStart   lifecycleAction = "start"
	actionStop    lifecycleAction = "stop"
	actionRestart lifecycleAction = "restart"
	actionPull    lifecycleAction = "pull"
	actionDelete  lifecycleAction = "delete"
)

// classifyContainers is the pure classifier that maps a (status, containers,
// statusErr) triple to a truth.ActionResult for start/stop/restart actions.
// It has no I/O and no Docker calls — suitable for both production and unit tests.
//
// cmdErr is passed through to Failed() so callers can inspect the underlying
// compose exit error; it is nil for stop.
func classifyContainers(
	action lifecycleAction,
	cmdErr error,
	status string,
	containers []models.Container,
	statusErr error,
) truth.ActionResult {
	if statusErr != nil {
		return truth.Failed("could not verify stack state after "+string(action), statusErr)
	}

	switch action {
	case actionStop:
		if status == "stopped" || len(containers) == 0 {
			return truth.Success("stack stopped", truth.KV("status", status))
		}
		running := countRunning(containers)
		if running > 0 {
			return truth.Failed(
				fmt.Sprintf("stack not fully stopped: %d container(s) still running", running),
				nil,
				truth.KV("status", status),
			)
		}
		return truth.Success("stack stopped", truth.KV("status", status))

	case actionDelete:
		// Delete requires the containers to be gone entirely, not merely
		// non-running. `compose down` removes containers rather than just
		// stopping them, so unlike actionStop (where a present-but-exited
		// container is an acceptable "stopped" end state), ANY container still
		// listed by `compose ps` after a delete — running, paused, restarting,
		// or otherwise — means the delete did not fully succeed and the caller
		// must not proceed to remove the stack directory (see parseComposePSOutput:
		// its "stopped" aggregate is only ever produced when len(containers)==0,
		// so checking len(containers) here is equivalent to checking status but
		// makes the real invariant explicit).
		if len(containers) == 0 {
			return truth.Success("stack and volumes removed", truth.KV("status", status))
		}
		return truth.Failed(
			fmt.Sprintf("stack not fully removed: %d container(s) still present (%s)",
				len(containers), summarizeContainerStates(containers)),
			nil,
			truth.KV("status", status),
			truth.KV("containers", len(containers)),
		)

	case actionStart, actionRestart:
		if len(containers) == 0 {
			return truth.Failed("stack did not start: no containers found", cmdErr,
				truth.KV("status", status))
		}

		total := len(containers)
		running := countRunning(containers)
		unhealthy := countUnhealthy(containers)

		switch {
		case running == total && unhealthy == 0:
			return truth.Success("stack running",
				truth.KV("status", status),
				truth.KV("containers", total))
		case running == total && unhealthy > 0:
			return truth.Partial(
				fmt.Sprintf("%d container(s) unhealthy", unhealthy),
				truth.KV("status", status),
				truth.KV("unhealthy", unhealthy),
			)
		case running > 0 && running < total:
			return truth.Partial(
				fmt.Sprintf("%d of %d container(s) running", running, total),
				truth.KV("status", status),
				truth.KV("running", running),
				truth.KV("total", total),
			)
		default:
			return truth.Failed(
				fmt.Sprintf("stack did not start: 0 of %d container(s) running", total),
				cmdErr,
				truth.KV("status", status),
			)
		}
	}

	// Unreachable, but keeps the compiler happy.
	return truth.Failed("unknown lifecycle action "+string(action), nil)
}

// verifyLifecycle is the main entry point after a lifecycle command completes.
// For pull it classifies purely from cmdErr. For start/stop/restart it polls
// until the container states have settled (see pollUntilSettled) then calls
// classifyContainers on the final snapshot.
func (s *DockerService) verifyLifecycle(stack models.Stack, action lifecycleAction, cmdErr error, output string) truth.ActionResult {
	if action == actionPull {
		if cmdErr != nil {
			return truth.Failed("pull failed", cmdErr, truth.KV("output", trimOutput(output)))
		}
		return truth.Success("images pulled", truth.KV("output", trimOutput(output)))
	}

	// For start/stop/restart/delete we prove end state via Status().
	// Finding #10 fix: Status returns a real error on docker compose ps
	// failure instead of a swallowed sentinel.
	status, containers, statusErr := s.Status(stack)
	if statusErr != nil {
		return truth.Failed("could not verify stack state after "+string(action), statusErr)
	}

	if action == actionStop || action == actionDelete {
		// Stop/delete have no settling window — if compose down succeeded,
		// containers should already be absent.
		return classifyContainers(action, cmdErr, status, containers, nil)
	}

	// For start/restart: poll until the container states have settled. This
	// guards against slow-crash services (e.g. sleep 0.7; exit 1) that appear
	// "running" briefly then exit. pollUntilSettled returns the authoritative
	// final snapshot.
	finalStatus, finalContainers := s.pollUntilSettled(stack, status, containers)
	return classifyContainers(action, cmdErr, finalStatus, finalContainers, nil)
}

// countRunning returns the number of containers in the "running" state.
func countRunning(containers []models.Container) int {
	n := 0
	for _, c := range containers {
		if c.State == "running" {
			n++
		}
	}
	return n
}

// countUnhealthy returns the number of containers that have a healthcheck
// and are specifically in the "unhealthy" state. Containers without a healthcheck
// (empty Health field, or "none") are not counted.
func countUnhealthy(containers []models.Container) int {
	n := 0
	for _, c := range containers {
		if c.Health == "unhealthy" {
			n++
		}
	}
	return n
}

// summarizeContainerStates returns a compact "state:count" summary of the
// given containers' State field, e.g. "running:1, paused:2", for use in
// human-readable failure reasons. Order follows first occurrence.
func summarizeContainerStates(containers []models.Container) string {
	counts := make(map[string]int, len(containers))
	order := make([]string, 0, len(containers))
	for _, c := range containers {
		if _, seen := counts[c.State]; !seen {
			order = append(order, c.State)
		}
		counts[c.State]++
	}
	parts := make([]string, 0, len(order))
	for _, state := range order {
		parts = append(parts, fmt.Sprintf("%s:%d", state, counts[state]))
	}
	return strings.Join(parts, ", ")
}

// settleParams groups the tunable constants for pollUntilSettled so they can
// be overridden in tests via the functional options pattern without adding
// exported fields to DockerService.
type settleParams struct {
	pollInterval time.Duration
	dwellTarget  time.Duration // how long all-running must be sustained
	maxWait      time.Duration // outer deadline
}

// Contract boundary: "success" means the stack was continuously running (and
// not unhealthy) for at least dwellTarget. A container that runs longer than the
// dwell and then exits is a runtime failure, not a start failure, and is
// correctly reported as success here — no finite verification window can
// distinguish "started" from "will die at T+epsilon". 3 s closes the realistic
// crash-loop class (immediate / sub-second / few-second) without penalising
// genuinely slow-but-healthy starters.
var defaultSettleParams = settleParams{
	pollInterval: 500 * time.Millisecond,
	dwellTarget:  3 * time.Second, // must stay all-running for 3 s before success
	maxWait:      15 * time.Second,
}

// pollUntilSettled is the settling engine for start/restart verification.
//
// Algorithm:
//  1. Fail fast: at each poll, if ANY expected container is not "running" (or is
//     "unhealthy"), stop immediately — no need to wait for the full dwell window.
//  2. Dwell requirement: all containers must be continuously running (and none
//     unhealthy) for at least dwellTarget before we classify as success. This
//     prevents a slow-crash (e.g. "sleep 0.7; exit 1") from sneaking through as
//     success because it appeared stable for one interval.
//  3. Timeout: if maxWait expires while containers are still all-running (e.g.
//     a slow healthcheck), return the latest snapshot and let classifyContainers
//     decide — a healthy-but-slow-starting container is still success.
//
// The initial (status, containers) snapshot from the first Status call
// is included in the dwell accounting.
func (s *DockerService) pollUntilSettled(
	stack models.Stack,
	initialStatus string,
	initialContainers []models.Container,
) (string, []models.Container) {
	p := defaultSettleParams
	return s.pollUntilSettledWithParams(stack, initialStatus, initialContainers, p)
}

// settleStatus is the status provider used by the poll loop. Production uses
// the real Status; unit tests inject a scripted sequence via statusFn so
// pollUntilSettled runs against the real settling code rather than a copy.
func (s *DockerService) settleStatus(stack models.Stack) (string, []models.Container, error) {
	if s.statusFn != nil {
		return s.statusFn(stack)
	}
	return s.Status(stack)
}

func (s *DockerService) pollUntilSettledWithParams(
	stack models.Stack,
	initialStatus string,
	initialContainers []models.Container,
	p settleParams,
) (string, []models.Container) {
	// If the first snapshot already shows not-all-running, fail fast.
	if !allRunningNoUnhealthy(initialContainers) {
		return initialStatus, initialContainers
	}

	// Start the dwell clock from the moment we took the initial snapshot.
	dwellStart := time.Now()
	deadline := dwellStart.Add(p.maxWait)

	prevStatus := initialStatus
	prevContainers := initialContainers

	for time.Now().Before(deadline) {
		time.Sleep(p.pollInterval)

		newStatus, newContainers, err := s.settleStatus(stack)
		if err != nil {
			// Can't re-check; return the last known state.
			return prevStatus, prevContainers
		}

		// Fail fast: a container left the running state.
		if !allRunningNoUnhealthy(newContainers) {
			return newStatus, newContainers
		}

		// All containers still running and healthy — check dwell.
		if time.Since(dwellStart) >= p.dwellTarget {
			// Sustained dwell requirement met: declare stable.
			return newStatus, newContainers
		}

		prevStatus = newStatus
		prevContainers = newContainers
	}

	// Timed out while all containers were still running. Return the last snapshot
	// and let classifyContainers produce a success (the stack started but took
	// longer than maxWait to stabilise, which is acceptable for slow starters).
	return prevStatus, prevContainers
}

// allRunningNoUnhealthy returns true when every container in the slice is in
// state "running" and none is "unhealthy". An empty slice returns false (no
// containers = did not start).
func allRunningNoUnhealthy(containers []models.Container) bool {
	if len(containers) == 0 {
		return false
	}
	for _, c := range containers {
		if c.State != "running" {
			return false
		}
		if c.Health == "unhealthy" {
			return false
		}
	}
	return true
}

// trimOutput returns a truncated copy of output for use in ActionResult.Details.
func trimOutput(s string) string {
	const maxLen = 500
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}

// StartVerified runs `docker compose up -d` and returns a verified truth.ActionResult
// plus the raw combined output. Success is reported only when Status() confirms all
// containers have been continuously running for the dwell window (finding #5 / #10 fix).
func (s *DockerService) StartVerified(stack models.Stack) (truth.ActionResult, string) {
	if s == nil {
		return truth.Failed(dockerUnavailableReason, ErrDockerUnavailable), ""
	}

	args := s.buildComposeArgs(stack, "up", []string{"-d"})
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := execCommand("docker", args...)
	cmd.Dir = stack.Directory
	cmd.Env = dockerEnv()

	output, err := cmd.CombinedOutput()
	out := string(output)
	if err != nil {
		return truth.Failed("compose up failed", err,
			truth.KV("output", trimOutput(out))), out
	}

	return s.verifyLifecycle(stack, actionStart, nil, out), out
}

// StopVerified runs `docker compose down` and returns a verified truth.ActionResult
// plus the raw combined output. Success only when no containers remain running.
func (s *DockerService) StopVerified(stack models.Stack) (truth.ActionResult, string) {
	if s == nil {
		return truth.Failed(dockerUnavailableReason, ErrDockerUnavailable), ""
	}

	args := s.buildComposeArgs(stack, "down", nil)
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := execCommand("docker", args...)
	cmd.Dir = stack.Directory
	cmd.Env = dockerEnv()

	output, err := cmd.CombinedOutput()
	out := string(output)
	if err != nil {
		return truth.Failed("compose down failed", err,
			truth.KV("output", trimOutput(out))), out
	}

	return s.verifyLifecycle(stack, actionStop, nil, out), out
}

// RestartVerified stops then starts the stack and returns a verified truth.ActionResult
// plus the combined output of both phases. Success only when all containers are
// confirmed running after the start phase.
func (s *DockerService) RestartVerified(stack models.Stack) (truth.ActionResult, string) {
	if s == nil {
		return truth.Failed(dockerUnavailableReason, ErrDockerUnavailable), ""
	}

	stopAR, stopOut := s.StopVerified(stack)
	if stopAR.Outcome == truth.OutcomeFailed {
		return stopAR, stopOut
	}

	// Wait for the stack to be fully stopped before starting it again.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	backoff := 100 * time.Millisecond
waitLoop:
	for {
		select {
		case <-ctx.Done():
			break waitLoop
		default:
		}
		// Use Status so a real docker error breaks the loop (same as "stopped").
		status, _, sErr := s.Status(stack)
		if sErr != nil || status == "stopped" {
			break waitLoop
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > 2*time.Second {
			backoff = 2 * time.Second
		}
	}

	startAR, startOut := s.StartVerified(stack)
	return startAR, stopOut + startOut
}

// PullVerified runs `docker compose pull` and returns a verified truth.ActionResult
// plus the raw output. Success only when the pull command exits 0.
func (s *DockerService) PullVerified(stack models.Stack) (truth.ActionResult, string) {
	if s == nil {
		return truth.Failed(dockerUnavailableReason, ErrDockerUnavailable), ""
	}

	args := s.buildComposeArgs(stack, "pull", nil)
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := execCommand("docker", args...)
	cmd.Dir = stack.Directory
	cmd.Env = dockerEnv()

	output, err := cmd.CombinedOutput()
	out := string(output)
	return s.verifyLifecycle(stack, actionPull, err, out), out
}

// DeleteVerified runs `docker compose down -v` (removing named volumes, mirroring
// the legacy Delete behavior) and returns a verified truth.ActionResult plus the
// raw combined output. Success only when no containers remain running, proven via
// the same Status/verifyLifecycle machinery StopVerified uses — a compose
// exit code of 0 alone does not guarantee the containers actually stopped.
func (s *DockerService) DeleteVerified(stack models.Stack) (truth.ActionResult, string) {
	if s == nil {
		return truth.Failed(dockerUnavailableReason, ErrDockerUnavailable), ""
	}

	args := s.buildComposeArgs(stack, "down", []string{"-v"})
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := execCommand("docker", args...)
	cmd.Dir = stack.Directory
	cmd.Env = dockerEnv()

	output, err := cmd.CombinedOutput()
	out := string(output)
	if err != nil {
		return truth.Failed("compose down failed", err,
			truth.KV("output", trimOutput(out))), out
	}

	return s.verifyLifecycle(stack, actionDelete, nil, out), out
}

// Status returns the aggregate status string, the per-container list, and any
// error from `docker compose ps`. On failure it returns a real error (finding
// #10 fix) rather than swallowing it — this is the single exported status path
// used both internally by verifyLifecycle/pollUntilSettled and by callers
// outside this domain (e.g. BackupService).
func (s *DockerService) Status(stack models.Stack) (string, []models.Container, error) {
	if s == nil {
		return "", nil, ErrDockerUnavailable
	}

	args := s.buildComposeArgs(stack, "ps", []string{"--format", "json"})

	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := execCommand("docker", args...)
	cmd.Dir = stack.Directory
	cmd.Env = dockerEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker compose ps failed: %w", err)
	}

	return parseComposePSOutput(output)
}

// parseComposePSOutput parses NDJSON lines from `docker compose ps --format json`
// and returns the aggregate status and per-container list.
func parseComposePSOutput(output []byte) (string, []models.Container, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	containers := make([]models.Container, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		var composePS struct {
			ID      string `json:"ID"`
			Name    string `json:"Name"`
			Service string `json:"Service"`
			State   string `json:"State"`
			Health  string `json:"Health"`
			Image   string `json:"Image"`
			Ports   string `json:"Ports"`
		}

		if err := json.Unmarshal([]byte(line), &composePS); err != nil {
			continue
		}

		ports := parsePorts(composePS.Ports)

		c := models.Container{
			ID:     composePS.ID,
			Name:   composePS.Name,
			Image:  composePS.Image,
			State:  composePS.State,
			Status: composePS.State,
			Ports:  ports,
			Health: composePS.Health,
		}

		containers = append(containers, c)
	}

	status := "running"
	if len(containers) == 0 {
		status = "stopped"
	} else {
		for _, c := range containers {
			if c.State != "running" {
				status = "partial"
				break
			}
		}
	}

	return status, containers, nil
}

// RunStreaming executes a compose subcommand and emits each output line on the
// returned channel. The terminal "done" frame now carries a verified Outcome and
// Reason for start/stop/restart actions so the frontend has an unambiguous signal
// (finding #5 + #18 fix). Pull success is still determined by exit code.
func (s *DockerService) RunStreaming(ctx context.Context, stack models.Stack, subcommand string, extraArgs []string) <-chan StreamLine {
	// The nil receiver is checked before the goroutine starts. buildComposeArgs
	// dereferences s.config inside it, and a panic in a goroutine is not caught
	// by gin's RecoveryMiddleware — it kills the whole process (agent-os-xay).
	if s == nil {
		out := make(chan StreamLine, 1)
		out <- StreamLine{Type: "error", Error: dockerUnavailableReason}
		close(out)
		return out
	}

	out := make(chan StreamLine, 100)

	// Derive the lifecycle action from the subcommand so we can run verification
	// after the compose command completes.
	action := streamingAction(subcommand)

	go func() {
		defer close(out)

		args := s.buildComposeArgs(stack, subcommand, extraArgs)
		//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
		cmd := execCommandContext(ctx, "docker", args...)
		cmd.Dir = stack.Directory
		cmd.Env = dockerEnv()

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			out <- StreamLine{Type: "error", Error: fmt.Sprintf("Failed to create pipe: %v", err)}
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			out <- StreamLine{Type: "error", Error: fmt.Sprintf("Failed to create stderr pipe: %v", err)}
			return
		}

		if err := cmd.Start(); err != nil {
			out <- StreamLine{Type: "error", Error: fmt.Sprintf("Failed to start command: %v", err)}
			return
		}

		scanDone := make(chan struct{}, 2)
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) != "" {
					out <- StreamLine{Type: "data", Line: line}
				}
			}
			if err := scanner.Err(); err != nil {
				out <- StreamLine{Type: "error", Error: err.Error()}
			}
			scanDone <- struct{}{}
		}()
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) != "" {
					out <- StreamLine{Type: "data", Line: line}
				}
			}
			if err := scanner.Err(); err != nil {
				out <- StreamLine{Type: "error", Error: err.Error()}
			}
			scanDone <- struct{}{}
		}()

		<-scanDone
		<-scanDone

		cmdErr := cmd.Wait()

		// Verify end state before emitting the terminal done frame.
		// For pull: classify from exit code.
		// For start/stop/restart: call verifyLifecycle which runs Status
		// and (for start/restart) pollUntilSettled to prove real end state.
		var ar truth.ActionResult
		if action != "" {
			ar = s.verifyLifecycle(stack, action, cmdErr, "")
		} else {
			// Unknown subcommand — fall back to exit-code semantics.
			if cmdErr != nil {
				ar = truth.Failed("command failed", cmdErr)
			} else {
				ar = truth.Success("command completed")
			}
		}

		out <- StreamLine{
			Type:    "done",
			Success: ar.Outcome == truth.OutcomeSuccess || ar.Outcome == truth.OutcomeNoChange,
			Error:   streamError(ar),
			Outcome: ar.Outcome,
			Reason:  ar.Reason,
		}
	}()

	return out
}

// streamingAction maps a compose subcommand to the lifecycle action used for
// post-run verification. Returns "" for subcommands without verifiable lifecycle
// state (e.g. "logs").
func streamingAction(subcommand string) lifecycleAction {
	switch subcommand {
	case "up":
		return actionStart
	case "down":
		return actionStop
	case "restart":
		return actionRestart
	case "pull":
		return actionPull
	}
	return ""
}

// streamError extracts a human-readable error string from an ActionResult for
// use in the done frame's Error field. Returns empty string on success.
func streamError(ar truth.ActionResult) string {
	if ar.Outcome == truth.OutcomeSuccess || ar.Outcome == truth.OutcomeNoChange {
		return ""
	}
	if ar.Err != nil {
		return ar.Err.Error()
	}
	return ar.Reason
}
