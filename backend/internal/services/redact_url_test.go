package services

import (
	"errors"
	"testing"
)

// TestRedactURLUserinfo pins agent-os-57xj at the unit level.
//
// The failing arms assert a secret is gone. The control arms assert EXACT
// EQUALITY, which is what stops the cheapest wrong fix: blanking the field
// removes the secret and passes any test that only checks for absence.
func TestRedactURLUserinfo(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		why  string
	}{
		// --- credentials present: must be removed, everything else kept ---
		{"user and password", "https://bob:SECRETPW123@github.com/org/repo.git",
			"https://***@github.com/org/repo.git",
			"the filed defect"},
		{"username-only PAT", "https://ghp_SENTINELTOKEN123@github.com/org/repo.git",
			"https://***@github.com/org/repo.git",
			"the most common GitHub PAT form has NO password field; stripping only the password leaks it"},
		{"empty username, password set", "https://:justpassword@host/repo.git",
			"https://***@host/repo.git",
			"a password with no username is still a password"},
		{"ssh scheme with username", "ssh://git@github.com/org/repo.git",
			"ssh://***@github.com/org/repo.git",
			"'git' is not a secret, but distinguishing it from ghp_xxx needs an allowlist, which is the value-keyed thinking that caused this bug"},
		{"malformed but credentialed", "https://user:pa ss@host/repo.git",
			"https://***@host/repo.git",
			"url.Parse REJECTS this; it must not fall through the parse-error branch with the credential intact"},

		// --- restic-style repository URIs: a real URL nested in an opaque part ---
		{"restic rest backend", "rest:https://bob:SECRET@host/repo/",
			"rest:https://***@host/repo/",
			"restic nests the URL, so url.Parse sees scheme=rest with NO userinfo and passes it through"},
		{"restic s3 backend", "s3:https://AKIAKEY:SECRETKEY@s3.amazonaws.com/bucket",
			"s3:https://***@s3.amazonaws.com/bucket",
			"an S3 access key and secret in the repository URI"},

		// --- no credential: must come back byte-for-byte unchanged ---
		{"restic sftp login name", "sftp:user@host:/srv/backups", "sftp:user@host:/srv/backups",
			"CONTROL: the opaque part is not a URL and 'user' is a login name, like scp-style git@host"},
		{"restic b2 bucket", "b2:bucketname:path/to/repo", "b2:bucketname:path/to/repo", "CONTROL"},
		{"restic local path", "/srv/restic", "/srv/restic", "CONTROL"},
		{"rclone remote", "rclone:remote:path", "rclone:remote:path", "CONTROL"},
		{"clean https", "https://github.com/org/repo.git", "https://github.com/org/repo.git",
			"CONTROL: exact equality kills 'blank the field'"},
		{"scp-like ssh", "git@github.com:org/repo.git", "git@github.com:org/repo.git",
			"CONTROL: url.Parse ERRORS on this, so 'return raw on parse error' is load-bearing for the commonest SSH form"},
		{"local absolute path", "/srv/stacks/repo.git", "/srv/stacks/repo.git",
			"CONTROL: a local remote is a path, not a URL"},
		{"relative path", "../other-repo", "../other-repo", "CONTROL"},
		{"empty", "", "", "CONTROL: no remote configured"},
		{"at-sign in the PATH not userinfo", "https://host/~user@example/repo.git",
			"https://host/~user@example/repo.git",
			"CONTROL: a regex-only design mangles this"},
		{"space in path, no credential", "https://host/path with space/repo.git",
			"https://host/path with space/repo.git",
			"CONTROL: never round-trip through url.String(), it re-encodes the space to %20"},
		{"port preserved", "http://host:8080/a/b.git", "http://host:8080/a/b.git", "CONTROL"},
		{"file scheme", "file:///srv/repo.git", "file:///srv/repo.git", "CONTROL"},
	}

	for _, tc := range cases {
		got := RedactURLUserinfo(tc.in)
		if got != tc.want {
			t.Errorf("%s:\n  in   = %q\n  got  = %q\n  want = %q\n  why  = %s", tc.name, tc.in, got, tc.want, tc.why)
		}
	}
}

// TestRedactURLUserinfo_KeepsWhatTheOperatorNeeds is the positive half, stated
// separately because it is the property a "blank it" fix destroys and an
// absence-only assertion cannot see.
//
// The frontend classifies the remote by scheme prefix to render an SSH-vs-HTTPS
// badge (frontend/src/components/git/GitSettingsSection.tsx), so dropping the
// scheme is a UI regression, not a cosmetic choice. And keeping "***@" rather
// than removing the userinfo entirely is deliberate: it tells the operator a
// credential is embedded, which is a real misconfiguration they should fix by
// moving it into Capstan's credential store. Silently dropping it makes a
// broken config look clean.
func TestRedactURLUserinfo_KeepsWhatTheOperatorNeeds(t *testing.T) {
	got := RedactURLUserinfo("https://bob:SECRETPW123@github.com:8443/org/repo.git")

	for _, must := range []string{"https://", "github.com:8443", "/org/repo.git", "***@"} {
		if !contains(got, must) {
			t.Errorf("redacted URL %q lost %q — the operator must still recognise their own remote, and the scheme drives the frontend's SSH/HTTPS badge", got, must)
		}
	}
	if contains(got, "SECRETPW123") {
		t.Errorf("redacted URL %q still contains the secret", got)
	}
	if contains(got, "%2A") {
		t.Errorf("redacted URL %q percent-encoded the placeholder — url.User(\"***\") does this; build the marker by hand", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestGetStatus_DoesNotServeAnEmbeddedCredential is the end-to-end arm.
//
// GET /api/v1/git has TWO paths: go-git (which serves most requests and reads
// .git/config directly) and a CLI fallback. The bead as filed named only the
// CLI one, so the most likely wrong fix patches that path alone and leaves the
// leak untouched. This drives the public entry point, and asserts
// TrackingBranch is set — which only the go-git path does — so the test cannot
// pass by accidentally exercising the fallback.
//
// It builds the service with &GitService{}: no config, no db, so the resolved
// token is empty and redactToken is a no-op. A fix that merely extends
// redactToken fails here.
func TestGetStatus_DoesNotServeAnEmbeddedCredential(t *testing.T) {
	const secret = "SECRETPW123"
	dir := t.TempDir()
	run := func(args ...string) { mustGit(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	run("remote", "add", "origin", "https://bob:"+secret+"@127.0.0.1:1/repo.git")
	// A tracking ref is required or getDivergence returns "" and the test
	// passes vacuously against an empty RemoteURL.
	run("update-ref", "refs/remotes/origin/main", "HEAD")

	svc := &GitService{}
	status, err := svc.GetStatus(dir)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.TrackingBranch == "" {
		t.Fatal("precondition: no tracking branch, so the go-git path did not run and this test proves nothing")
	}
	if contains(status.RemoteURL, secret) {
		t.Errorf("GetStatus served the embedded credential: RemoteURL = %q", status.RemoteURL)
	}
	if !contains(status.RemoteURL, "127.0.0.1:1") || !contains(status.RemoteURL, "/repo.git") {
		t.Errorf("RemoteURL = %q lost the host or path; the operator must still recognise their remote", status.RemoteURL)
	}
}

// TestGetStatusCLI_DoesNotServeAnEmbeddedCredential covers the CLI fallback.
//
// It exists because a mutation proved the go-git end-to-end test does NOT
// cover this path: deleting the CLI redaction left the whole suite green. Two
// population sites, two tests — a suite that exercises one call site is not
// evidence about the other, however thoroughly it tests the function itself.
func TestGetStatusCLI_DoesNotServeAnEmbeddedCredential(t *testing.T) {
	const secret = "SECRETPW123"
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	mustGit(t, dir, "remote", "add", "origin", "https://bob:"+secret+"@127.0.0.1:1/repo.git")

	svc := &GitService{}
	status, err := svc.getStatusCLI(dir)
	if err != nil {
		t.Fatalf("getStatusCLI: %v", err)
	}
	if contains(status.RemoteURL, secret) {
		t.Errorf("the CLI fallback served the embedded credential: RemoteURL = %q", status.RemoteURL)
	}
	if !contains(status.RemoteURL, "127.0.0.1:1") {
		t.Errorf("RemoteURL = %q lost the host", status.RemoteURL)
	}
}

// TestRedactErrPath_ScrubsThePathOutOfTheWrappedError covers the sink created
// by agent-os-7z8c: os.MkdirAll returns an *os.PathError echoing its argument,
// and every 5xx now logs the full error chain, so a remote restic repository
// URI carrying a credential would reach the log through an error message.
//
// Wrapping with a redacted path is NOT enough on its own — the wrapped error
// still holds the raw one — which is why this asserts on the whole chain.
func TestRedactErrPath_ScrubsThePathOutOfTheWrappedError(t *testing.T) {
	const secret = "SECRETPW123"
	raw := "rest:https://bob:" + secret + "@host/repo/"
	inner := errors.New("mkdir " + raw + ": invalid argument")

	got := redactErrPath(inner, raw)
	if contains(got.Error(), secret) {
		t.Errorf("the credential survived in the error chain: %q", got.Error())
	}
	if !contains(got.Error(), "host/repo/") {
		t.Errorf("error %q lost the host and path an operator needs", got.Error())
	}

	// CONTROL: an error with no credential in it is returned untouched, so the
	// helper is not just blanking every error it sees.
	clean := errors.New("mkdir /srv/restic: permission denied")
	if redactErrPath(clean, "/srv/restic").Error() != clean.Error() {
		t.Errorf("a credential-free error was modified: %q", redactErrPath(clean, "/srv/restic").Error())
	}
}

// TestRedactURLUserinfo_SlashInPassword pins agent-os-zzhs arm 1.
//
// url.Parse truncates the authority at the first "/", so a password containing
// one makes the host "user:PASS" and the parse fails with `invalid port`. The
// parse-error fallback then had to catch it and could not: its character class
// was [^/@]*, which cannot cross a "/". The credential was returned
// BYTE-FOR-BYTE and served to every authenticated client.
//
// Not a corner case. An AWS secret access key is 40 characters over
// [A-Za-z0-9/+], so P(contains "/") = 1-(63/64)^40 ~= 46%.
//
// Asserted as EXACT EQUALITY, not absence: absence alone is satisfied by
// blanking the field, and the operator still has to recognise their own remote.
func TestRedactURLUserinfo_SlashInPassword(t *testing.T) {
	const secret = "AB/CD+SECRET999"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"restic rest backend", "rest:https://bob:" + secret + "@backup.example.com/repo/",
			"rest:https://***@backup.example.com/repo/"},
		{"restic rest backend with port", "rest:https://bob:" + secret + "@backup.example.com:8000/repo/",
			"rest:https://***@backup.example.com:8000/repo/"},
		{"plain https", "https://bob:" + secret + "@backup.example.com/repo/",
			"https://***@backup.example.com/repo/"},
		{"restic s3 backend, real AWS key shape", "s3:https://AKIAIOSFODNN7:wJalrXUtnFEMI/K7MDENG@s3.example.com/bucket",
			"s3:https://***@s3.example.com/bucket"},
		{"several slashes", "https://bob:A/B/C/SECRET999@host/repo.git",
			"https://***@host/repo.git"},
		{"empty username, slashed password", "https://:" + secret + "@host/repo.git",
			"https://***@host/repo.git"},
		{"protocol-relative", "//bob:" + secret + "@host/path",
			"//***@host/path"},
	}

	for _, tc := range cases {
		got := RedactURLUserinfo(tc.in)
		if got != tc.want {
			t.Errorf("%s:\n  in   = %q\n  got  = %q\n  want = %q", tc.name, tc.in, got, tc.want)
		}
	}

	// POSITIVE CONTROL: the same secret with the "/" removed already redacted
	// correctly before this fix. It is what proves the arms above are armed —
	// that they exercise the redactor rather than matching nothing.
	const in = "rest:https://bob:ABCDSECRET999@backup.example.com/repo/"
	if got, want := RedactURLUserinfo(in), "rest:https://***@backup.example.com/repo/"; got != want {
		t.Errorf("positive control broke: in = %q, got = %q, want = %q", in, got, want)
	}
}

// TestRedactURLUserinfo_EmptyUserinfoIsNotACredential pins agent-os-zzhs arm 2,
// which points the OPPOSITE way: a marker spliced into a URI that carries no
// secret at all.
//
// "http://:@host/" is what a compose template renders when both the user and
// the password variable are unset. Go parses that userinfo as an empty username
// plus a password that is empty but SET, so u.User.String() returns ":" and the
// old "== \"\"" guard never fired.
//
// Not cosmetic: the served value then contains UserinfoRedactionMarker, so the
// save guard at handlers/backup.go rejects EVERY write to the field. The
// operator is told a credential is embedded in a repository that has none, and
// cannot save an edit to it. A lockout, with no attacker.
func TestRedactURLUserinfo_EmptyUserinfoIsNotACredential(t *testing.T) {
	unchanged := []string{
		"rest:http://:@host:8000/repo/",
		"http://:@host/path",
		"http://@host/path",
		// Unparseable for an unrelated reason (a bad percent-escape in the
		// path), so it reaches the fallback rather than the parse-success
		// guard. Both paths need the rule.
		"http://@host/100%discount",
		"http://:@host/100%discount",
	}
	for _, in := range unchanged {
		if got := RedactURLUserinfo(in); got != in {
			t.Errorf("%q carries no secret but was marked as if it did:\n  got = %q", in, got)
		}
	}

	// CONTROL, the converse direction: a userinfo with ANY non-empty component
	// is still a credential and must still be redacted. Without this the test
	// above is satisfied by never redacting anything.
	redacted := map[string]string{
		"rest:http://user:@host:8000/":     "rest:http://***@host:8000/",
		"http://user:pw@host/100%discount": "http://***@host/100%discount",
		"http://user@host/100%discount":    "http://***@host/100%discount",
	}
	for in, want := range redacted {
		if got := RedactURLUserinfo(in); got != want {
			t.Errorf("%q carries a credential:\n  got  = %q\n  want = %q", in, got, want)
		}
	}
}

// TestRedactURLUserinfo_LeavesAnAtSignInThePathAlone is the discrimination arm
// for the widened fallback.
//
// Widening the fallback so it can cross a "/" puts every "@" in a PATH at risk
// of being mistaken for a userinfo boundary. These are all unparseable, so they
// reach the fallback, and each carries an "@" that must survive untouched.
func TestRedactURLUserinfo_LeavesAnAtSignInThePathAlone(t *testing.T) {
	for _, in := range []string{
		// A bad percent-escape in the path, plus an "@" further along.
		"http://host/pa%th/~user@example/repo.git",
		"https://host:99/deep/path/with@sign/and%zz",
		// The commonest SSH remote. It has no "//" at all, which is the
		// anchor keeping the fallback off it.
		"git@github.com:org/repo.git",
	} {
		if got := RedactURLUserinfo(in); got != in {
			t.Errorf("the @ here is not a credential boundary:\n  in  = %q\n  got = %q", in, got)
		}
	}
}
