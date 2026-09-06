package services

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-d5ff. Three functions gated a decision on os.Stat and threw the
// error away, so "the file is absent" and "I could not find out whether the
// file is absent" produced the same answer:
//
//   - buildComposeArgs (docker.go) dropped --env-file, and the stack came up
//     without its global environment with nothing logged;
//   - stackDotEnv resolved the compose project name without the stack's .env
//     while the parse-error branch below it in the same function warned;
//   - determineEnvFile silently DEMOTED from .env.<stack> to .env, which is
//     worse than the other two: it substitutes a different file rather than
//     omitting one.
//
// THE FIXTURE IS NOT chmod. `chmod 000` is a no-op for root (CAP_DAC_OVERRIDE),
// so a red arm built on it does not arm wherever the suite runs as root — which
// is why integrationtest/compose_env_test.go:571-573 has to skip itself as root.
// ENOTDIR and ENAMETOOLONG are structural properties of the path, resolved
// before any permission check, so no uid and no capability can defeat them.
// Measured on this box (uid=1000), all three arms on one instrument:
//
//	ENOTDIR   err=stat …/datadir/global.env: not a directory       IsNotExist=false
//	ENAMETOOLONG err=stat …: file name too long                    IsNotExist=false
//	ENOENT    err=stat …/absent/global.env: no such file or directory  IsNotExist=true
//	PRESENT   err=<nil>                                            IsNotExist=false
//
// os.IsNotExist is therefore the exact discriminator the fix turns on, and it
// is the one ScanAll already uses (scanner.go:498-499,
// `hasGlobalEnv = !os.IsNotExist(err)`).
//
// Every test here asserts STATE BEFORE LOGS: the failure message names the
// actual args slice / resolved filename, which is simultaneously the assertion
// failure and the proof of the defect. A log assertion that ran first would
// report only "expected a log line" and never name the behaviour.

// d5ffCaptureSlog redirects the default logger into a buffer for one test.
//
// Deliberately not the captureSlog in git_credentials_test.go: that file was
// being edited concurrently (agent-os-xzoe) when this was written, and a test
// helper shared across a package boundary between two in-flight branches is a
// merge conflict waiting to happen.
func d5ffCaptureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// d5ffNotADir writes a REGULAR FILE at <parent>/name and returns its path, so
// that stat-ing anything BENEATH it fails with ENOTDIR rather than ENOENT.
func d5ffNotADir(t *testing.T, parent, name string) string {
	t.Helper()
	p := filepath.Join(parent, name)
	if err := os.WriteFile(p, []byte("this is a regular file, not a directory"), 0o600); err != nil {
		t.Fatalf("seeding the ENOTDIR fixture: %v", err)
	}
	return p
}

// d5ffEnvFileValues returns the value following each --env-file flag, which is
// the state these tests are really about.
func d5ffEnvFileValues(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--env-file" && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

func d5ffContains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func d5ffStack() models.Stack {
	return models.Stack{
		ID:          "root~test-stack:default",
		Directory:   "/srv/stacks/test-stack",
		ComposeFile: "compose.yaml",
		ProjectName: "test-stack-default",
	}
}

// ---------------------------------------------------------------------------
// (*DockerService).buildComposeArgs — docker.go:284, stat site :312
// ---------------------------------------------------------------------------

// TestDockerService_buildComposeArgs_GlobalEnvStatFault is the RED arm. DataDir
// is a regular file, so stat(DataDir/global.env) is ENOTDIR: the global env
// file is NOT known to be absent. Pre-fix the flag was silently dropped and the
// stack came up without its global environment.
//
// The fix passes the operator's configured path to compose anyway. That is a
// refusal, not a shrug: `docker compose --env-file <ENOTDIR path>` exits 1 with
// "stat …/global.env: not a directory", so the command stops and names the real
// cause, without buildComposeArgs needing an error return it does not have.
//
// Measured on the compose that `docker compose version` reports as "Docker
// Compose version v5.5.1", on both `config` and `ps`. The command is quoted
// because the first version recorded here was wrong: it was the ENGINE version
// from `docker version --format '{{.Server.Version}}'` (26.1.5+dfsg1)
// mislabelled as compose's. A version in a comment outlives the session that
// wrote it, so it carries the command that re-derives it.
func TestDockerService_buildComposeArgs_GlobalEnvStatFault(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dataDir := d5ffNotADir(t, t.TempDir(), "datadir")
	globalEnvPath := dataDir + "/global.env"

	service := &DockerService{config: &config.Config{DataDir: dataDir}}

	args := service.buildComposeArgs(d5ffStack(), "up", []string{"-d"})

	if !d5ffContains(d5ffEnvFileValues(args), globalEnvPath) {
		t.Errorf("a global.env that could not be stat'd (ENOTDIR) was silently dropped from the compose command.\n"+
			"--env-file values = %q\nfull args = %q\nwant %q to be present so compose refuses instead of starting without the operator's environment",
			d5ffEnvFileValues(args), args, globalEnvPath)
	}

	out := logs.String()
	if !strings.Contains(out, "ERROR") || !strings.Contains(out, globalEnvPath) || !strings.Contains(out, "not a directory") {
		t.Errorf("the stat fault was not logged at ERROR with its cause.\ncaptured logs = %q\nwant an ERROR line naming %q and \"not a directory\"", out, globalEnvPath)
	}
}

// TestDockerService_buildComposeArgs_GlobalEnvAbsent is the control that stops
// the fix becoming a noise generator. A plain ENOENT is the normal state of
// every install with no global.env, and it must stay byte-for-byte silent.
//
// It is load-bearing rather than decorative: `docker compose --env-file` on a
// merely ABSENT path also exits 1 ("couldn't find env file: …"), so a fix that
// passed the flag unconditionally would break every healthy install.
func TestDockerService_buildComposeArgs_GlobalEnvAbsent(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dataDir := t.TempDir() // a real directory, with no global.env in it
	globalEnvPath := filepath.Join(dataDir, "global.env")

	service := &DockerService{config: &config.Config{DataDir: dataDir}}

	args := service.buildComposeArgs(d5ffStack(), "up", []string{"-d"})

	if d5ffContains(d5ffEnvFileValues(args), globalEnvPath) {
		t.Errorf("an absent global.env must not be passed to compose.\n--env-file values = %q\nfull args = %q", d5ffEnvFileValues(args), args)
	}
	if out := logs.String(); out != "" {
		t.Errorf("an absent global.env is the normal state and must log nothing.\ncaptured logs = %q", out)
	}
}

// TestDockerService_buildComposeArgs_GlobalEnvPresent is the second control:
// the healthy path is unchanged and still silent.
func TestDockerService_buildComposeArgs_GlobalEnvPresent(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dataDir := t.TempDir()
	globalEnvPath := filepath.Join(dataDir, "global.env")
	if err := os.WriteFile(globalEnvPath, []byte("TEST=value\n"), 0o600); err != nil {
		t.Fatalf("seeding global.env: %v", err)
	}

	service := &DockerService{config: &config.Config{DataDir: dataDir}}

	args := service.buildComposeArgs(d5ffStack(), "up", []string{"-d"})

	if !d5ffContains(d5ffEnvFileValues(args), globalEnvPath) {
		t.Errorf("a readable global.env must still be passed to compose.\n--env-file values = %q\nfull args = %q", d5ffEnvFileValues(args), args)
	}
	if out := logs.String(); out != "" {
		t.Errorf("a readable global.env must log nothing.\ncaptured logs = %q", out)
	}
}

// ---------------------------------------------------------------------------
// scanner.go:375 — stackDotEnv
// ---------------------------------------------------------------------------

// TestStackDotEnv_StatFault is the RED arm for the third shape, the one the
// standing scanner (scripts/check-getter-errors.sh) cannot see: it is
// `err != nil { return }`, not `err == nil` softening, so it is out of that
// instrument's class by construction rather than missed by it.
//
// The asymmetry IS the bug. stackDotEnv's parse-error branch (scanner.go:392,
// "Could not parse the stack's .env") warns and returns an empty map; the
// stat-error branch above it at :375 returned the same empty map in silence, so
// a fault on .env is indistinguishable from a stack that genuinely has none and
// the project name is reported as source "directory".
func TestStackDotEnv_StatFault(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dir := d5ffNotADir(t, t.TempDir(), "stackdir") // stat(dir/.env) -> ENOTDIR

	env := stackDotEnv(dir)

	if len(env) != 0 {
		t.Errorf("stackDotEnv must still degrade to an empty map, got %v", env)
	}
	out := logs.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, filepath.Join(dir, ".env")) || !strings.Contains(out, "not a directory") {
		t.Errorf("a .env that could not be stat'd was swallowed in silence, while stackDotEnv's own parse branch warns.\n"+
			"captured logs = %q\nwant a WARN line naming %q and \"not a directory\"", out, filepath.Join(dir, ".env"))
	}
}

// TestStackDotEnv_Absent is the control: most stacks have no .env at all, and
// that must stay silent.
func TestStackDotEnv_Absent(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dir := t.TempDir()

	env := stackDotEnv(dir)

	if len(env) != 0 {
		t.Errorf("no .env must yield an empty map, got %v", env)
	}
	if out := logs.String(); out != "" {
		t.Errorf("an absent .env is the normal state and must log nothing.\ncaptured logs = %q", out)
	}
}

// TestStackDotEnv_Present is the second control: a readable .env is still
// parsed, and still silently.
func TestStackDotEnv_Present(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("COMPOSE_PROJECT_NAME=fromdotenv\n"), 0o600); err != nil {
		t.Fatalf("seeding .env: %v", err)
	}

	env := stackDotEnv(dir)

	if env["COMPOSE_PROJECT_NAME"] != "fromdotenv" {
		t.Errorf("a readable .env must still be parsed, got %v", env)
	}
	if out := logs.String(); out != "" {
		t.Errorf("a readable .env must log nothing.\ncaptured logs = %q", out)
	}
}

// ---------------------------------------------------------------------------
// determineEnvFile — three stat sites on 5ec87b4 (:1128/:1135/:1140), now one
// helper, envFileIfPresent at scanner.go:1162
// ---------------------------------------------------------------------------

// d5ffLongStackName is a stack name long enough that ".env." + it exceeds the
// 255-byte limit on a single path component, so stat fails with ENAMETOOLONG.
// Chosen over ENOTDIR here because this test needs the DIRECTORY to be real and
// to hold a readable .env — that is the only way to show the DEMOTION.
var d5ffLongStackName = strings.Repeat("n", 260)

// TestDetermineEnvFile_StatFault_DoesNotDemote is the RED arm for the fallback
// chain, and the harm here differs in kind from the other two sites. The other
// two OMIT an env file; this one SUBSTITUTES a different one. Pre-fix, a fault
// on .env.<stack> fell straight through to the .env branch below and returned
// ".env", so the stack was recorded as using another file entirely — wrong
// configuration, which fails less visibly than missing configuration.
func TestDetermineEnvFile_StatFault_DoesNotDemote(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dir := t.TempDir()
	// A real, readable .env sits here: it is the file the pre-fix code demoted
	// to. Without it the test could not tell "did not demote" from "found
	// nothing".
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("A=b\n"), 0o600); err != nil {
		t.Fatalf("seeding .env: %v", err)
	}

	got := determineEnvFile(dir, d5ffLongStackName)

	want := ".env." + d5ffLongStackName
	if got != want {
		t.Errorf("a stat fault on the stack's own env file silently demoted to a DIFFERENT file.\n"+
			"determineEnvFile returned %q\nwant %q (the configured file, so compose refuses on it rather than loading another stack's environment)", got, want)
	}
	out := logs.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "file name too long") {
		t.Errorf("the stat fault behind the demotion was not logged with its cause.\ncaptured logs = %q\nwant a WARN line carrying \"file name too long\"", out)
	}
}

// TestDetermineEnvFile_StatFault_Default covers the stackName == "default"
// branch, which has its own single stat and no fallback to demote to.
func TestDetermineEnvFile_StatFault_Default(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dir := d5ffNotADir(t, t.TempDir(), "stackdir") // stat(dir/.env) -> ENOTDIR

	got := determineEnvFile(dir, "default")

	if got != ".env" {
		t.Errorf("a .env that could not be stat'd was reported as no env file at all.\ndetermineEnvFile returned %q, want %q", got, ".env")
	}
	out := logs.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "not a directory") {
		t.Errorf("the stat fault was not logged with its cause.\ncaptured logs = %q", out)
	}
}

// TestDetermineEnvFile_Absent is the control across BOTH branches: a directory
// with no env files at all yields "" in silence, exactly as before.
func TestDetermineEnvFile_Absent(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dir := t.TempDir()

	if got := determineEnvFile(dir, "default"); got != "" {
		t.Errorf("default branch: want \"\", got %q", got)
	}
	if got := determineEnvFile(dir, "mystack"); got != "" {
		t.Errorf("named branch: want \"\", got %q", got)
	}
	if out := logs.String(); out != "" {
		t.Errorf("absent env files are the normal state and must log nothing.\ncaptured logs = %q", out)
	}
}

// TestDetermineEnvFile_Present is the second control, including the legitimate
// ENOENT fallback: .env.<stack> genuinely missing must still fall back to .env.
// This is the behaviour the fix must NOT break while removing the fault-driven
// fallback above it.
func TestDetermineEnvFile_Present(t *testing.T) {
	logs := d5ffCaptureSlog(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("A=b\n"), 0o600); err != nil {
		t.Fatalf("seeding .env: %v", err)
	}

	if got := determineEnvFile(dir, "default"); got != ".env" {
		t.Errorf("default branch with a readable .env: want \".env\", got %q", got)
	}
	// .env.mystack is genuinely absent (ENOENT), so falling back to .env is
	// correct and must survive the fix.
	if got := determineEnvFile(dir, "mystack"); got != ".env" {
		t.Errorf("named branch must still fall back to .env when .env.mystack is genuinely absent: got %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, ".env.mystack"), []byte("A=c\n"), 0o600); err != nil {
		t.Fatalf("seeding .env.mystack: %v", err)
	}
	if got := determineEnvFile(dir, "mystack"); got != ".env.mystack" {
		t.Errorf("named branch must prefer .env.mystack when it is readable: got %q", got)
	}

	if out := logs.String(); out != "" {
		t.Errorf("readable env files must log nothing.\ncaptured logs = %q", out)
	}
}
