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
//     message, translated or not according to a faithful model of glibc
//     gettext's catalog selection (see the script), so the test observes
//     exactly what pullCLI's callers observe against a translated locale.
//
// The oracle models the REAL selection order, which is the whole point of the
// test: the messages locale is LC_ALL, else LC_MESSAGES, else LANG, else C;
// a C/POSIX messages locale disables translation outright and LANGUAGE is
// ignored; only for a non-C messages locale does LANGUAGE select the catalog.
// OBSERVED against git 2.47.3 (`git status --porcelain` outside a repo):
// LC_ALL=C LANGUAGE=de and LC_ALL=POSIX LANGUAGE=de both print English, while
// LC_ALL=en_US.utf8 LANGUAGE=de prints "Schwerwiegend: Kein Git-Repository".
// An earlier version of this script branched on $LANGUAGE alone, which models
// that precedence BACKWARDS: it made the test insensitive to LC_ALL=C, the
// load-bearing half of the fix, and sensitive only to the redundant half.
//
// DO NOT "FIX" THE ORACLE TO MATCH THIS MACHINE. It deliberately treats a
// de_* messages locale as translated even though real git ON THIS BOX prints
// English there. That divergence IS the guard, and the reason is measured, not
// assumed: no locale generated here has a git catalog, and no git catalog here
// has a generated locale. OBSERVED, git 2.47.3:
//
//	$ locale -a
//	C  C.utf8  en_US.utf8  nl_NL.utf8  POSIX
//
//	$ ls /usr/share/locale/*/LC_MESSAGES/git.mo          # 19 catalogs, no nl, no en
//	bg ca de el es fr id is it ko pl pt_PT ru sv tr uk vi zh_CN zh_TW
//
//	$ for l in $(locale -a); do base=${l%%.*}; base=${base%%_*}; \
//	    [ -f /usr/share/locale/$base/LC_MESSAGES/git.mo ] && echo "MATCH: $l"; done
//	(no output — the intersection over all five locales and all nineteen
//	 catalogs is EMPTY; this is the whole-set form, not two spot checks)
//
// So the messages-locale route to a translated git message is unreachable on
// this host, and de_DE.UTF-8 reads English here only because that locale is
// not generated — a host artifact, not gettext precedence:
//
//	LC_ALL=nl_NL.utf8   (no LANGUAGE)  -> "fatal: ..."  English: locale, no catalog
//	LC_ALL=de_DE.UTF-8  (no LANGUAGE)  -> "fatal: ..."  English: catalog, no locale
//	LC_ALL=nl_NL.utf8   LANGUAGE=de    -> "Schwerwiegend: Kein Git-Repository ..."  GERMAN
//	LC_ALL=en_US.utf8   LANGUAGE=nl    -> "fatal: ..."  English: no nl catalog exists
//
// The third arm shows the messages locale need only be NON-C and need not
// relate to the catalog at all. The fourth is the negative control that kills
// the lazy reading "LANGUAGE set means translated". The precise rule is
// therefore: a non-C messages locale AND an installed catalog for the selected
// language.
//
// Two consequences, and the second is why this comment exists. First, the
// oracle models git-in-general rather than git-on-this-machine, because a
// properly configured German host DOES translate via the messages locale.
// Second, because LANGUAGE= (the redundant half) is still cleared under the
// mutant that removes LC_ALL=C, and because the LANGUAGE route is the ONLY
// translation route available here, a REAL-git behavioural arm could not
// distinguish that mutant on this box at all. The fake git is not a
// convenience; it is the only instrument that can exercise the messages-locale
// route this host cannot. Narrowing the oracle to this box's observed output
// would silently restore the state where deleting LC_ALL=C keeps the test
// green — a check that cannot fail, arriving disguised as a cleanup.
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
# Resolve the messages locale the way glibc setlocale does, then pick a
# catalog the way gettext does. Empty is unset, per setlocale.
msgloc="$LC_ALL"
[ -n "$msgloc" ] || msgloc="$LC_MESSAGES"
[ -n "$msgloc" ] || msgloc="$LANG"
[ -n "$msgloc" ] || msgloc=C
catalog=en
case "$msgloc" in
  C|POSIX) ;;                      # translation off; LANGUAGE is not consulted
  *)
    sel="$msgloc"
    [ -z "$LANGUAGE" ] || sel="${LANGUAGE%%:*}"   # LANGUAGE wins, non-C only
    case "$sel" in de*) catalog=de ;; esac
    ;;
esac

case "$*" in
  *'status --porcelain'*) exit 0 ;;
  *'rev-parse HEAD'*) echo 'deadbeefcafefeed0000000000000000000000'; exit 0 ;;
  *'pull --ff-only'*)
    if [ "$catalog" = de ]; then
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

// TestPullCLI_UpToDateIsLocaleIndependent pins agent-os-vq3p: gitCmdWithCreds
// pins the git child's locale, so pullCLI's string match on git's own message
// ("Already up to date") gives the same answer whatever locale Capstan itself
// is running under. That, not a particular HTTP status, is what this test
// pins: the bead's headline symptom (an up-to-date pull surfacing as a 500) is
// NOT reproducible on git 2.47.3, where `git pull --ff-only` on an up-to-date
// repository exits 0 and the string match is never reached -- see writeFakeGit
// for the evidence and for why the branch is nonetheless live production code.
// What generalises, and what the downstream beads depend on, is that the child
// environment is locale-pinned so no text match on git's output can drift with
// the host's language.
//
// The arms are chosen so each half of the pin is separately observable:
//
//   - "translated messages locale" sets LC_ALL to a German locale with no
//     LANGUAGE. Only LC_ALL=C in the child suppresses the translation, so this
//     arm goes RED if the LC_ALL=C pin is removed. It is the arm that protects
//     the load-bearing half of the fix.
//   - "LANGUAGE overrides a non-C locale" reproduces the environment that
//     actually produces German output from real git (OBSERVED: LC_ALL=en_US.utf8
//     LANGUAGE=de -> "Schwerwiegend: Kein Git-Repository"). It goes RED only if
//     BOTH halves are removed, i.e. against the pre-fix code.
//   - "english locale (control)" is the must-pass side: the fix makes the
//     behaviour locale-INDEPENDENT rather than merely moving which string is
//     matched.
func TestPullCLI_UpToDateIsLocaleIndependent(t *testing.T) {
	fakeGitDir := writeFakeGit(t)
	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	svc := NewGitService(&config.Config{}, nil)

	// assertNoChange drives Pull and asserts the up-to-date branch was taken.
	assertNoChange := func(t *testing.T, env string) {
		t.Helper()
		result, err := svc.Pull(dir)
		if err != nil {
			t.Fatalf("Pull under %s: expected the no-change result; got error: %v. Git's message "+
				"is only 'Already up to date.' while the child locale stays pinned.", env, err)
		}
		if result.PreviousCommit != result.CurrentCommit {
			t.Errorf("Pull under %s: expected no-op pull (PreviousCommit == CurrentCommit), got %q != %q",
				env, result.PreviousCommit, result.CurrentCommit)
		}
	}

	t.Run("translated messages locale (regression arm)", func(t *testing.T) {
		// No LANGUAGE: the translation is selected by the messages locale
		// alone, so clearing LANGUAGE cannot mask a missing LC_ALL=C.
		t.Setenv("LC_ALL", "de_DE.UTF-8")
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", "")
		t.Setenv("LANGUAGE", "")

		assertNoChange(t, "LC_ALL=de_DE.UTF-8")
	})

	t.Run("LANGUAGE overrides a non-C locale", func(t *testing.T) {
		t.Setenv("LC_ALL", "en_US.utf8")
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", "")
		t.Setenv("LANGUAGE", "de")

		assertNoChange(t, "LC_ALL=en_US.utf8 LANGUAGE=de")
	})

	t.Run("english locale (control)", func(t *testing.T) {
		t.Setenv("LC_ALL", "en_US.utf8")
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", "")
		t.Setenv("LANGUAGE", "")

		assertNoChange(t, "LC_ALL=en_US.utf8")
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
