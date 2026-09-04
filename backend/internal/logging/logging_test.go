package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
)

// LOG_LEVEL was dead configuration: declared, defaulted, read from the
// environment and logged back out, with nothing consuming it. These tests pin
// down that it now actually gates output (agent-os-7li).

// restoreLoggingDefaults snapshots the process-wide logging state and puts it
// back when the test ends. Every test below that calls Configure must call it
// first, because Configure installs a handler PERMANENTLY — that is correct
// production behaviour (cmd/server/main.go calls it once at startup and never
// unwinds it), and it is precisely why a test that calls it must unwind it
// itself.
//
// Three pieces of state, not one. slog.SetDefault also calls
// log.SetOutput(handlerWriter{...}) and log.SetFlags(0) on the STANDARD log
// package whenever the new handler is not slog's internal defaultHandler, and
// nothing puts those back for you (agent-os-ac0o). OBSERVED, by calling
// Configure(&buf, "info", FormatText) and diffing the state either side:
// writerChanged=true flagsChanged=true slogChanged=true.
//
// Reading log.Writer() and log.Flags() BEFORE the swap is load-bearing: after
// Configure runs, the flags are already 0 and the writer already points at the
// test's buffer, so a snapshot taken late restores the leak instead of the
// original.
func restoreLoggingDefaults(t *testing.T) {
	t.Helper()
	prevSlog := slog.Default()
	prevWriter, prevFlags := log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
}

// TestRestoreLoggingDefaults_RestoresStdlibLog guards the helper above, not
// Configure. It is the ratchet for the whole class: a cleanup simplified back
// to slog.SetDefault(prev) alone leaks the stdlib log redirect for the rest of
// the test binary, and every later log.Print lands in an abandoned buffer
// rather than stderr. Nothing fails when that happens — the diagnostics simply
// stop arriving — so a comment would not survive and a test has to.
//
// This package reaches slog.SetDefault INDIRECTLY, through Configure, which is
// why a class sweep pinned to the literal string "slog.SetDefault" does not
// see this file at all. The test is the durable record of that.
func TestRestoreLoggingDefaults_RestoresStdlibLog(t *testing.T) {
	slogBefore := slog.Default()
	writerBefore, flagsBefore := log.Writer(), log.Flags()

	t.Run("configure", func(t *testing.T) {
		restoreLoggingDefaults(t)
		var buf bytes.Buffer
		if err := Configure(&buf, "info", FormatText); err != nil {
			t.Fatalf("Configure: %v", err)
		}
	})

	if slog.Default() != slogBefore {
		t.Errorf("slog default not restored")
	}
	if log.Writer() != writerBefore {
		t.Errorf("stdlib log writer not restored: the capture leaked its redirect, so later log output in this binary is swallowed")
	}
	if log.Flags() != flagsBefore {
		t.Errorf("stdlib log flags not restored: got %d, want %d", log.Flags(), flagsBefore)
	}
}

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
			restoreLoggingDefaults(t)
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
	restoreLoggingDefaults(t)
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
	restoreLoggingDefaults(t)
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
	// Both calls below error out inside Configure before it reaches
	// slog.SetDefault, so this test cannot currently leak — OBSERVED:
	// writerChanged=false flagsChanged=false slogChanged=false for a bad level
	// and for a bad format. The guard is here anyway so that whether a test in
	// this file is safe does not depend on the reader knowing the order of
	// Configure's internal validation.
	restoreLoggingDefaults(t)
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
