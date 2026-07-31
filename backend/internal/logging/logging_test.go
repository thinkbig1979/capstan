package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// LOG_LEVEL was dead configuration: declared, defaulted, read from the
// environment and logged back out, with nothing consuming it. These tests pin
// down that it now actually gates output (agent-os-7li).

func TestConfigure_LevelGatesOutput(t *testing.T) {
	cases := []struct {
		level     string
		wantDebug bool
		wantInfo  bool
		wantWarn  bool
	}{
		{"debug", true, true, true},
		{"info", false, true, true},
		{"warn", false, false, true},
		{"error", false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Configure(&buf, tc.level, FormatText); err != nil {
				t.Fatalf("Configure(%q): %v", tc.level, err)
			}

			slog.Debug("debug-line")
			slog.Info("info-line")
			slog.Warn("warn-line")

			out := buf.String()
			for _, want := range []struct {
				marker  string
				present bool
			}{
				{"debug-line", tc.wantDebug},
				{"info-line", tc.wantInfo},
				{"warn-line", tc.wantWarn},
			} {
				if got := strings.Contains(out, want.marker); got != want.present {
					t.Errorf("LOG_LEVEL=%s: %s present = %v, want %v\noutput: %s",
						tc.level, want.marker, got, want.present, out)
				}
			}
		})
	}
}

func TestConfigure_JSONFormatParses(t *testing.T) {
	var buf bytes.Buffer
	if err := Configure(&buf, "info", FormatJSON); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	slog.Info("HTTP request", "status", 200, "path", "/api/v1/stacks")

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("JSON output does not parse: %v\nline: %s", err, buf.String())
	}
	if line["msg"] != "HTTP request" || line["path"] != "/api/v1/stacks" {
		t.Errorf("structured fields lost: %v", line)
	}
}

func TestConfigure_TextIsTheDefaultShape(t *testing.T) {
	var buf bytes.Buffer
	if err := Configure(&buf, "info", FormatText); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	slog.Info("plain", "k", "v")

	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("text format emitted JSON: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "k=v") {
		t.Errorf("expected key=value text output, got: %s", buf.String())
	}
}

// TestConfigure_RejectsUnknownValues is the "fails loudly" requirement: a typo
// in LOG_LEVEL must surface at startup rather than silently becoming info.
func TestConfigure_RejectsUnknownValues(t *testing.T) {
	var buf bytes.Buffer

	err := Configure(&buf, "bogus", FormatText)
	if err == nil {
		t.Fatal("expected an error for an unrecognised log level, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "debug, error, info, warn") {
		t.Errorf("error should name the bad value and the accepted set, got: %v", err)
	}

	if err := Configure(&buf, "info", "yaml"); err == nil {
		t.Fatal("expected an error for an unrecognised log format, got nil")
	}
}

func TestParseLevel_CaseAndSpaceTolerant(t *testing.T) {
	for _, in := range []string{"DEBUG", " debug ", "Debug"} {
		got, err := ParseLevel(in)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", in, err)
		}
		if got != slog.LevelDebug {
			t.Errorf("ParseLevel(%q) = %v, want debug", in, got)
		}
	}
}
