package truth

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// pullMessage is a minimal representation of the JSON message objects that the
// Docker daemon streams during a pull. We decode only the fields we need rather
// than importing github.com/docker/docker/pkg/jsonmessage directly, which would
// drag in terminal/display logic that has no place in a pure processing function.
//
// Docker wire format (relevant fields):
//
//	{ "status": "...", "progress": "...", "id": "...",
//	  "error": "...",            ← deprecated single-field error
//	  "errorDetail": { "message": "..." } }
type pullMessage struct {
	Status      string           `json:"status"`
	Progress    string           `json:"progress"`
	ID          string           `json:"id"`
	Stream      string           `json:"stream"`
	Error       string           `json:"error"`
	ErrorDetail *pullErrorDetail `json:"errorDetail"`
}

type pullErrorDetail struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// DrainPullStream reads the newline-delimited JSON message stream produced by
// a Docker image pull (e.g. the reader returned by client.ImagePull) and
// surfaces human-readable progress via emit. It returns a non-nil error if any
// message carries an error field or errorDetail, so callers can distinguish a
// failed pull from a successful one instead of discarding the stream.
//
// emit is called for every meaningful status line; it may be nil if the caller
// does not need progress events.
//
// The reader is fully consumed regardless of errors.
func DrainPullStream(reader io.Reader, emit func(line string)) error {
	var firstErr error

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}

		var msg pullMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			// Unparseable line: not a fatal error, skip and continue draining.
			continue
		}

		// Surface error conditions; capture the first one.
		if msg.ErrorDetail != nil && msg.ErrorDetail.Message != "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("docker pull error: %s", msg.ErrorDetail.Message)
			}
			continue
		}
		if msg.Error != "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("docker pull error: %s", msg.Error)
			}
			continue
		}

		if emit == nil {
			continue
		}

		// Compose a human-readable progress line from the available fields.
		line := buildProgressLine(msg)
		if line != "" {
			emit(line)
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return errors.Join(firstErr, fmt.Errorf("reading pull stream: %w", scanErr))
	}

	return firstErr
}

func buildProgressLine(msg pullMessage) string {
	if msg.Stream != "" {
		s := strings.TrimRight(msg.Stream, "\n")
		if s != "" {
			return s
		}
	}
	if msg.Status == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if msg.ID != "" {
		parts = append(parts, msg.ID+":")
	}
	parts = append(parts, msg.Status)
	if msg.Progress != "" {
		parts = append(parts, msg.Progress)
	}
	return strings.Join(parts, " ")
}
