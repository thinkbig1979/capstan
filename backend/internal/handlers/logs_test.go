package handlers

import (
	"testing"
)

func TestParseLogLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *LogLine
		wantNil bool
	}{
		{
			name:    "empty line",
			input:   "",
			wantNil: true,
		},
		{
			name:    "line without pipe",
			input:   "just a message",
			wantNil: true,
		},
		{
			name:  "simple log line",
			input: "web-1  | GET / 200",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "",
				Message:   "GET / 200",
			},
		},
		{
			name:  "log line with timestamp",
			input: "db-1  | 2026-02-13T10:00:00Z LOG: checkpoint complete",
			want: &LogLine{
				Container: "db-1",
				Timestamp: "2026-02-13T10:00:00Z",
				Message:   "LOG: checkpoint complete",
			},
		},
		{
			name:  "log line with RFC3339 timestamp",
			input: "web-1  | 2026-02-13T10:00:00.123Z [INFO] Starting server",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "2026-02-13T10:00:00.123Z",
				Message:   "[INFO] Starting server",
			},
		},
		{
			name:  "log line with datetime timestamp",
			input: "web-1  | 2026-02-13 10:00:00 Hello world",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "2026-02-13 10:00:00",
				Message:   "Hello world",
			},
		},
		{
			name:  "log line with just container and pipe",
			input: "web-1  |",
			want: &LogLine{
				Container: "web-1",
				Timestamp: "",
				Message:   "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLogLine(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("parseLogLine() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Errorf("parseLogLine() = nil, want %v", tt.want)
				return
			}
			if got.Container != tt.want.Container {
				t.Errorf("parseLogLine().Container = %v, want %v", got.Container, tt.want.Container)
			}
			if got.Timestamp != tt.want.Timestamp {
				t.Errorf("parseLogLine().Timestamp = %v, want %v", got.Timestamp, tt.want.Timestamp)
			}
			if got.Message != tt.want.Message {
				t.Errorf("parseLogLine().Message = %v, want %v", got.Message, tt.want.Message)
			}
		})
	}
}

func TestParseLogLines(t *testing.T) {
	input := `web-1  | 2026-02-13T10:00:00Z [INFO] Starting
db-1  | 2026-02-13T10:00:01Z LOG: ready
web-1  | GET / 200
`

	tests := []struct {
		name            string
		input           string
		containerFilter string
		wantCount       int
	}{
		{
			name:            "no filter",
			input:           input,
			containerFilter: "",
			wantCount:       3,
		},
		{
			name:            "filter by container",
			input:           input,
			containerFilter: "web-1",
			wantCount:       2,
		},
		{
			name:            "filter by different container",
			input:           input,
			containerFilter: "db-1",
			wantCount:       1,
		},
		{
			name:            "filter by non-existent container",
			input:           input,
			containerFilter: "redis-1",
			wantCount:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLogLines(tt.input, tt.containerFilter)
			if len(got) != tt.wantCount {
				t.Errorf("parseLogLines() returned %d lines, want %d", len(got), tt.wantCount)
			}
		})
	}
}
