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
// returns that directory (to be prepended to PATH). It answers the subcommands
// GitService.Pull -> pullCLI issues on the failure path:
//
//   - status --porcelain -> success, no output (clean working tree)
//   - rev-parse HEAD     -> success, a fixed fake hash
//   - pull --ff-only     -> exit 1, printing git's own "already up to date"
//     message, translated or not according to a faithful model of glibc
//     gettext's catalog selection (see the script), so the test observes
//     exactly what pullCLI's callers observe against a translated locale.
//   - anything else      -> success, no output. pullFailure's three probes
//     (ls-remote, rev-parse @{upstream}, merge-base --is-ancestor) land on
//     this catch-all, which puts every arm on the SAME classification path and
//     leaves the message text as the only thing that differs between them.
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
// That empty intersection retires exactly ONE of the two routes to a translated
// git message, and naming which one is the whole point. An earlier revision of
// this comment summarised it as a flat statement that translation could not be
// reached here at all, and that summary was then read back -- by its own author
// -- as a general fact about this host (agent-os-6cg8). It is not one:
//
//   - ROUTE 1, the messages locale itself selects the catalog (LANGUAGE unset).
//     Needs a GENERATED locale that ALSO has a git catalog. The intersection
//     measured above is empty, so route 1 is genuinely unreachable on this host.
//   - ROUTE 2, LANGUAGE selects the catalog and the messages locale only has to
//     be non-C. Needs any generated non-C locale plus a catalog for the language
//     LANGUAGE names; the two need not be related to each other. Route 2 IS
//     reachable here, by default, because the ambient LANG is en_US.UTF-8.
//
// OBSERVED, git 2.47.3, ambient LANG=en_US.UTF-8 with LC_ALL unset, running
// `git rev-parse --git-dir` outside a repository:
//
//	(unset LC_ALL; LANGUAGE=de)        -> "Schwerwiegend: Kein Git-Repository ..."  GERMAN
//	LANG=de_DE.UTF-8 LANGUAGE=de       -> "fatal: ..."  English
//	LC_ALL=nl_NL.utf8   LANGUAGE=de    -> "Schwerwiegend: Kein Git-Repository ..."  GERMAN
//	LC_ALL=nl_NL.utf8   (no LANGUAGE)  -> "fatal: ..."  English: locale, no catalog
//	LC_ALL=de_DE.UTF-8  (no LANGUAGE)  -> "fatal: ..."  English: catalog, no locale
//	LC_ALL=en_US.utf8   LANGUAGE=nl    -> "fatal: ..."  English: no nl catalog exists
//
// The first two arms are the pair that has to be read together: they differ
// only in LANG and they come out opposite. The first is route 2 firing on the
// host's own default environment, which is why translation is emphatically
// reachable here. The second reads English NOT because LANG outranked LANGUAGE
// -- it does not -- but because de_DE.UTF-8 is not generated on this box, so
// setlocale falls back to C, and a C messages locale disables translation
// outright and stops LANGUAGE being consulted at all. Pinning LANG to an
// ungenerated locale therefore destroys the very signal such an arm looks like
// it is testing; that reading is a host artifact, not gettext precedence.
//
// Arms three and four are route 1 failing in each direction, a locale with no
// catalog and a catalog with no locale, and arm three additionally shows the
// messages locale need only be NON-C and need not relate to the catalog at all.
// The last arm is the negative control that kills the lazy reading "LANGUAGE
// set means translated". The precise rule is therefore: a non-C messages locale
// AND an installed catalog for the language selected -- NOT a generated locale
// that itself carries a catalog.
//
// Two consequences, and the second is why this comment exists. First, the
// oracle models git-in-general rather than git-on-this-machine, because a
// properly configured German host DOES translate via route 1.
//
// Second, and this is what the fake git is for: a REAL-git arm cannot vary
// git's message text on this box at all, under ANY parent environment.
// gitCmdWithCreds clears LANGUAGE in the child, which shuts route 2 whatever
// the parent sets, and route 1 is unreachable here by the measurement above;
// with both routes shut, real git prints English every time and an arm built
// on it is a check that cannot fail. The fake git is the only instrument that
// can put a TRANSLATED git message in front of pullCLI on this host, which is
// exactly what a test guarding against prose classification has to do.
// Narrowing the oracle to this box's observed output would silently restore
// that cannot-fail state, arriving disguised as a cleanup.
//
// The branch this file was originally written for is GONE. pullCLI used to
// recognise an up-to-date pull by matching git's own prose, and that match is
// what let a translated message change the result. agent-os-fv2j deleted it:
// `git pull --ff-only` on a truly up-to-date repository EXITS 0 (OBSERVED, git
// 2.47.3, `git pull --ff-only; echo $?` prints "Already up to date." then "0"),
// so pullCLI's ordinary fall-through -- previousCommit == currentCommit after a
// successful, err-free pull -- already produced the no-change result, and the
// match only ever guarded a path today's git does not take. It survived from
// the earlier go-git implementation, where Pull() signalled the no-op with a
// non-nil NoErrAlreadyUpToDate sentinel instead of with success.
//
// So this file no longer pins LC_ALL=C behaviourally, and does not claim to:
// with no production code reading git's prose, removing that pin changes no
// pullCLI result. TestGitCmdWithCreds_PinsLocale below is what holds
// agent-os-vq3p, asserting the child env directly. What the fake git still
// buys is a FORWARD guard -- TestPullCLI_ClassificationIsLocaleIndependent --
// which goes red if anyone reintroduces a text match. See that test for which
// half of it does the catching, because it is not the half it looks like.
//
// Swapping only the external `git` process (never the production code under
// test: pullCLI, pullFailure, gitCmdWithCreds) is the same technique
// exec_env.go's execCommand indirection exists for elsewhere in this package,
// applied via PATH since GitService calls exec.Command directly rather than
// through an injectable var.
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

// TestPullCLI_ClassificationIsLocaleIndependent is the forward guard on
// agent-os-fv2j's rule: pullCLI and pullFailure classify a failed pull by
// asking git questions and reading EXIT CODES, never by matching its prose.
//
// There are two assertions here, and MEASURED it is the first that does the
// catching -- which is worth stating, because it is not the one the test's
// name points at.
//
// FIRST, each arm's precondition: Pull must FAIL. The fake git exits 1, so a
// success means pullCLI manufactured a no-change result out of a failed
// command, which is exactly what the deleted "Already up to date" match did.
// Reintroducing that match turns all three arms red here, on this function's
// own precondition Fatalf. OBSERVED, that mutation applied to git.go under
// `go test -overlay`, go build exit 0 first so the red is an assertion:
//
//	under translated messages locale: Pull reported success (&{...})
//	although the fake git exited 1
//
// SECOND, the three arms' (HTTP status, error code) pairs must be IDENTICAL.
// This one is belt-and-braces, and honestly so: while gitCmdWithCreds pins the
// child to LC_ALL=C, the fake git resolves the C catalog and prints English in
// every arm, so a text match alone cannot make the arms DIFFER -- it makes all
// three wrong the same way, and the precondition is what fires. The comparison
// covers the composite fault this family actually fears: prose classification
// arriving together with some future call path whose child env is not pinned.
//
// It does NOT pin agent-os-vq3p's LC_ALL=C, and must not be read as doing so:
// with no text match left in pullCLI, deleting that pin changes no result
// here. TestGitCmdWithCreds_PinsLocale is vq3p's pin and asserts the child
// environment directly.
func TestPullCLI_ClassificationIsLocaleIndependent(t *testing.T) {
	fakeGitDir := writeFakeGit(t)
	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	svc := NewGitService(&config.Config{}, nil)

	type outcome struct {
		status int
		code   string
	}

	arms := []struct {
		name string
		env  [][2]string
	}{
		// The messages locale alone selects a German catalog: route 1, the
		// route real git cannot take on this host (see writeFakeGit).
		{"translated messages locale", [][2]string{
			{"LC_ALL", "de_DE.UTF-8"}, {"LC_MESSAGES", ""}, {"LANG", ""}, {"LANGUAGE", ""}}},
		// LANGUAGE selects it over a non-C messages locale: route 2.
		{"LANGUAGE over a non-C locale", [][2]string{
			{"LC_ALL", "en_US.utf8"}, {"LC_MESSAGES", ""}, {"LANG", ""}, {"LANGUAGE", "de"}}},
		// Untranslated control.
		{"english locale (control)", [][2]string{
			{"LC_ALL", "en_US.utf8"}, {"LC_MESSAGES", ""}, {"LANG", ""}, {"LANGUAGE", ""}}},
	}

	got := make([]outcome, 0, len(arms))
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			for _, kv := range arm.env {
				t.Setenv(kv[0], kv[1])
			}
			result, err := svc.Pull(dir)
			if err == nil {
				t.Fatalf("under %s: Pull reported success (%+v) although the fake git exited 1; "+
					"a failed pull must never be turned into a no-change result", arm.name, result)
			}
			status, code := statusFor(err)
			got = append(got, outcome{status: status, code: code})
		})
	}

	if len(got) != len(arms) {
		t.Fatalf("precondition: %d arms recorded an outcome, want %d", len(got), len(arms))
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[0] {
			t.Errorf("classification varies with the locale: %q gave HTTP %d (%s) but %q gave HTTP %d (%s) "+
				"-- only code that reads git's message text can do that",
				arms[0].name, got[0].status, got[0].code, arms[i].name, got[i].status, got[i].code)
		}
	}
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
