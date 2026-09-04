package services

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// writeFakeGit installs a stand-in `git` executable in a fresh directory and
// returns that directory (to be prepended to PATH). It answers exactly the
// three subcommands GitService.Pull -> pullCLI issues:
//
//   - status --porcelain -> success, no output (clean working tree)
//   - rev-parse HEAD     -> success, a fixed fake hash
//   - pull --ff-only     -> exit 1, printing git's own "already up to date"
//     message -- in English, unless the child's LANGUAGE variable says
//     otherwise, so the test can observe exactly what pullCLI's callers
//     observe against a translated locale.
//
// A stand-in binary is necessary because a real, modern git CLI does not
// actually exercise this branch: `git pull --ff-only` on a truly up-to-date
// repository exits 0 (OBSERVED directly against git 2.47.3: `git pull
// --ff-only; echo $?` prints "Already up to date." then "0", in every locale
// tried), so pullCLI's own fall-through path (previousCommit == currentCommit
// after a successful, err-free pull) already produces the correct no-change
// result without ever reaching the string match. The string match survives
// from an earlier go-git-based implementation, where Pull() signals the
// no-op case via a non-nil git.NoErrAlreadyUpToDate sentinel error instead of
// success (see git.go's history around the CLI migration) -- so the branch
// this bead's locale bug lives in is real production code, reachable in
// principle (any future or non-standard git behaviour that fails non-zero
// while still saying "already up to date" hits it), but not reachable via an
// ordinary pull with today's git. Swapping only the external `git` process
// (never the production code under test: pullCLI, gitCmdWithCreds) is the
// same technique exec_env.go's execCommand indirection exists for elsewhere
// in this package, applied via PATH since GitService calls exec.Command
// directly rather than through an injectable var.
func writeFakeGit(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake git shell script requires a POSIX shell")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *'status --porcelain'*) exit 0 ;;
  *'rev-parse HEAD'*) echo 'deadbeefcafefeed0000000000000000000000'; exit 0 ;;
  *'pull --ff-only'*)
    if [ "$LANGUAGE" = "de" ]; then
      echo 'Bereits aktuell.' >&2
    else
      echo 'Already up to date.' >&2
    fi
    exit 1
    ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake git: %v", err)
	}
	return dir
}

// TestPullCLI_UpToDateIsLocaleIndependent pins agent-os-vq3p: pullCLI detects
// the "nothing to pull" case by string-matching git's own message ("Already
// up to date") in the child's combined output. git translates that message
// via gettext according to the child's LANGUAGE/LC_ALL/LANG (19 catalogs ship
// on a typical Linux box), and gitCmdWithCreds never pinned the child locale
// -- so on a non-English host this reachable-in-principle branch would
// silently misfire.
//
// The regression arm sets LANGUAGE to a translated locale exactly as an
// operator's shell/systemd unit would on a non-English host. The control arm
// proves the English path is unaffected by the fix, on the same instrument.
func TestPullCLI_UpToDateIsLocaleIndependent(t *testing.T) {
	fakeGitDir := writeFakeGit(t)
	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	svc := NewGitService(&config.Config{}, nil)

	t.Run("translated locale (regression arm)", func(t *testing.T) {
		t.Setenv("LANGUAGE", "de")

		result, err := svc.Pull(dir)
		if err != nil {
			t.Fatalf("Pull under LANGUAGE=de (git's own message would be 'Bereits aktuell.' unless "+
				"the child locale is pinned): expected the no-change result, got error: %v", err)
		}
		if result.PreviousCommit != result.CurrentCommit {
			t.Errorf("expected no-op pull (PreviousCommit == CurrentCommit), got %q != %q",
				result.PreviousCommit, result.CurrentCommit)
		}
	})

	t.Run("english locale (control)", func(t *testing.T) {
		t.Setenv("LANGUAGE", "en_US")

		result, err := svc.Pull(dir)
		if err != nil {
			t.Fatalf("Pull under LANGUAGE=en_US: expected the no-change result, got error: %v", err)
		}
		if result.PreviousCommit != result.CurrentCommit {
			t.Errorf("expected no-op pull (PreviousCommit == CurrentCommit), got %q != %q",
				result.PreviousCommit, result.CurrentCommit)
		}
	})
}

// TestGitCmdWithCreds_PinsLocale asserts the child env directly: LC_ALL=C is
// present and LANGUAGE is absent/empty, regardless of what Capstan's own
// process environment carries. This is the "seen failing first" unit for the
// env-building line itself (gitCmdWithCreds, driven directly per the bead's
// acceptance text), independent of any particular git message text.
func TestGitCmdWithCreds_PinsLocale(t *testing.T) {
	t.Setenv("LANGUAGE", "de")
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "en_US.utf8")

	svc := NewGitService(&config.Config{}, nil)
	cmd, _ := svc.gitCmdWithCreds(t.TempDir(), "", "", "status")

	var gotLCAll string
	lcAllSet := false
	langValue := ""
	langSet := false
	for _, kv := range cmd.Env {
		key, val, _ := strings.Cut(kv, "=")
		switch key {
		case "LC_ALL":
			gotLCAll, lcAllSet = val, true
		case "LANGUAGE":
			langValue, langSet = val, true
		}
	}

	if !lcAllSet || gotLCAll != "C" {
		t.Errorf("expected child env LC_ALL=C, got set=%v value=%q", lcAllSet, gotLCAll)
	}
	if langSet && langValue != "" {
		t.Errorf("expected child env LANGUAGE to be absent or empty, got %q", langValue)
	}
}
