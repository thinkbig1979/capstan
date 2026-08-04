package services

import (
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

// Environment variable names the credential helper below reads. They are set
// only on the git child process, never exported into capstan's own environment.
const (
	credentialEnvUser  = "CAPSTAN_GIT_USERNAME"
	credentialEnvToken = "CAPSTAN_GIT_PASSWORD"
)

// gitCredentialHelper is a shell snippet installed as git's credential helper
// for the duration of a single invocation. It answers only the "get" operation
// and reads the secret out of the child environment.
//
// Everything else was rejected for leaking the token somewhere durable:
//   - rewriting origin to https://user:token@host persists the credential into
//     .git/config on disk, into that stack's backup snapshot, and into every log
//     line or API response that prints the remote (agent-os-qqw);
//   - -c http.<remote>.extraheader="Authorization: Basic ..." puts it in argv,
//     where any local process can read it from /proc/<pid>/cmdline;
//   - a GIT_ASKPASS script has to be written to disk and made executable.
//
// The snippet itself contains only variable *names*, so argv stays clean, and
// the -c flag means nothing is written to any config file.
const gitCredentialHelper = `!f() { test "$1" = get && printf 'username=%s\npassword=%s\n' ` +
	`"$` + credentialEnvUser + `" "$` + credentialEnvToken + `"; }; f`

// defaultGitHTTPSUser is used when neither the stored setting nor the
// GIT_HTTPS_USER environment variable names one. Forges that authenticate by
// token ignore the username, but it must not be empty or git re-prompts.
const defaultGitHTTPSUser = "oauth2"

// httpsCredentials resolves the git HTTPS credential for dirPath, in order:
//  1. directory authType "https" — that directory's own stored credential,
//     used as-is even if empty. Falling back to the global token here would
//     silently use a different credential than the one configured for this
//     directory, which is the exact failure mode being fixed (agent-os-qll):
//     a feature that reports "configured" while doing something else.
//  2. directory authType "ssh" — no HTTPS credential applies. SSH key auth
//     is a separate, currently unimplemented path (nothing in gitCmd consumes
//     GitSSHKeyPath); this only makes sure "ssh" never falls through to the
//     https credential below.
//  3. directory authType "" or "inherit" (or no directory row at all) — the
//     value stored (encrypted) in global settings, then GIT_HTTPS_TOKEN.
//
// A directory row that exists but cannot be READ is none of those three: it
// returns no credential at all rather than falling through (agent-os-2au). See
// the comment on the error branch below.
//
// The same applies one level up, to the global settings value in step 3: if
// git_https_token is stored but cannot be decrypted, that is not "no global
// credential configured" either, and it does not fall through to
// GIT_HTTPS_TOKEN (agent-os-oyj). See the comment on that error branch below.
//
// It returns an empty token when none is configured, in which case git runs
// exactly as before.
func (s *GitService) httpsCredentials(dirPath string) (user, token string) {
	if s.db != nil {
		cred, err := s.db.GetDirectoryCredentials(dirPath)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// No row for dirPath: this directory was never configured with a
			// credential of its own, so inheriting the global one below is the
			// intended behaviour.
		case err != nil:
			// The row exists but is unreadable — a decrypt failure from a rotated
			// STORAGE_KEY, or the database itself failing. Falling through here
			// would not mean "no credential", it would mean authenticating this
			// directory's remote with a DIFFERENT credential, silently: the exact
			// failure mode this function exists to prevent. It also discards
			// cred.GitAuthType, so an "ssh" directory would receive an HTTPS token
			// in violation of case 2 above.
			//
			// No error is propagated: httpsCredentials feeds gitCmd, which every
			// git invocation goes through, including purely local ones like
			// `git log` and `git diff` that never contact a remote. gitCmd attaches
			// a credential helper only for a non-empty token, so those keep working
			// and remote operations fail with git's own auth error instead.
			//
			// As with the warning below, the log line carries only the directory
			// path — never a token value, and never any other directory's state.
			slog.Error("cannot read the stored git credential for this directory; not falling back to a different (global) credential", "path", dirPath, "error", err)
			return "", ""
		default:
			switch strings.ToLower(cred.GitAuthType) {
			case "https":
				user, token = cred.GitHTTPSUser, cred.GitHTTPSToken
				if user == "" {
					user = defaultGitHTTPSUser
				}
				if token == "" {
					// Otherwise this state is indistinguishable from "everything is
					// fine" until the pull fails with a generic auth error: the UI
					// reports the directory as configured for its own HTTPS
					// credential, but no token was ever saved. The log line carries
					// only the directory path, never a token value or any other
					// directory's state, so nothing here reaches action-log details.
					slog.Warn("directory is configured for https git auth but has no stored credential; not falling back to a different (global) credential", "path", dirPath)
				}
				return user, token
			case "ssh":
				return "", ""
			}
		}
	}

	if s.db != nil {
		// A decrypt failure (wrong STORAGE_KEY) surfaces as an error here; treat
		// it as "no credential" and let git report the auth failure, rather than
		// failing every git command including the ones that need no remote. Unlike
		// the per-directory read above, no dirPath applies here — this setting is
		// global — so the log line names the setting instead of a path.
		//
		// sql.ErrNoRows is the healthy default state (no global credential ever
		// configured) and must stay silent; only a genuinely unreadable stored
		// value is worth an operator's attention (agent-os-2tt).
		v, err := s.db.GetSetting("git_https_token")
		switch {
		case err == nil:
			token = v
		case errors.Is(err, sql.ErrNoRows):
			// No global credential configured at all — nothing to report.
		case errors.Is(err, errdefs.ErrEncryptionUnavailable):
			// A row exists but there is no key to decrypt it with. Falling
			// through to the env/config token below (agent-os-oyj) would mean
			// authenticating with a DIFFERENT credential than the one an
			// operator believes is configured — the same failure mode 2au
			// fixed for the per-directory read above (see the comment on that
			// error branch). Returning "", "" here, rather than letting token
			// stay empty and fall through further down, is what makes this
			// fail closed instead of open.
			//
			// The git_https_user read and defaultGitHTTPSUser are skipped too:
			// a bare username with no token is a `false`-configured state that
			// doesn't correspond to anything the operator actually stored.
			slog.Error("cannot read the global git credential: no encryption key is configured", "setting", "git_https_token", "error", err)
			return "", ""
		default:
			// database/settings.go returns the decrypt error unwrapped, so this
			// covers both a genuine decrypt failure (rotated STORAGE_KEY) and any
			// other unreadable-database error; the message says "may have been
			// rotated" rather than asserting it, since a closed-database error
			// would otherwise be described inaccurately. Fails closed for the
			// same reason as the ErrEncryptionUnavailable branch above.
			slog.Error("cannot read the global git credential: the stored value could not be read; STORAGE_KEY may have been rotated", "setting", "git_https_token", "error", err)
			return "", ""
		}
		if v, err := s.db.GetSetting("git_https_user"); err == nil {
			user = v
		}
	}
	if s.config != nil {
		if token == "" {
			token = s.config.GitHTTPSToken
		}
		if user == "" {
			user = s.config.GitHTTPSUser
		}
	}
	if user == "" {
		user = defaultGitHTTPSUser
	}
	return user, token
}

// gitCmd builds the git child process for dirPath with the HTTPS credential
// attached when one is configured. It also returns the token it used, so the
// caller can redact it from output without resolving (and decrypting) it twice.
//
// The credential is applied to every invocation rather than to a hand-picked
// list of remote-contacting subcommands: pull, fetch, clone, ls-remote and
// `status` with a configured upstream can all reach the network, and an
// omission from such a list is exactly the failure mode being fixed here. git
// only runs the helper when a remote actually challenges it.
func (s *GitService) gitCmd(dirPath string, args ...string) (*exec.Cmd, string) {
	user, token := s.httpsCredentials(dirPath)
	return s.gitCmdWithCreds(dirPath, user, token, args...)
}

// gitCmdWithCreds is gitCmd with credential resolution factored out: it takes
// an already-resolved (user, token) pair instead of calling httpsCredentials
// itself. A single logical git operation (status, pull, log, diff) issues
// several of these; resolving once at the top of that operation and passing
// the result to every call here — instead of letting each one re-resolve via
// gitCmd — is what turns N DB reads/decrypt attempts and N duplicate log lines
// per operation into one (agent-os-9ha). It intentionally carries no memoizing
// state of its own: see the doc comment on GitService for why a shared cache
// was rejected.
func (s *GitService) gitCmdWithCreds(dirPath, user, token string, args ...string) (*exec.Cmd, string) {
	gitArgs := []string{"-c", "safe.directory=" + dirPath}
	// stripCapstanSecrets removes Capstan's own secrets (JWT_SECRET,
	// STORAGE_KEY, GIT_HTTPS_TOKEN, RESTIC_PASSWORD) before the process
	// environment is forwarded to the git child; see exec_env.go's
	// stripCapstanSecrets for why this is a denylist rather than an
	// allowlist. Hygiene, consistent with this package's other child-process
	// call sites (exec_env.go, backup_restic.go).
	env := append(stripCapstanSecrets(os.Environ()), "GIT_TERMINAL_PROMPT=0")

	if token != "" {
		// The empty assignment first clears any helper inherited from system or
		// global config, so ours is the only one consulted.
		gitArgs = append(gitArgs,
			"-c", "credential.helper=",
			"-c", "credential.helper="+gitCredentialHelper,
		)
		env = append(env,
			credentialEnvUser+"="+user,
			credentialEnvToken+"="+token,
		)
	}

	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := exec.Command("git", append(gitArgs, args...)...)
	cmd.Dir = dirPath
	cmd.Env = env
	return cmd, token
}

// redactToken replaces every occurrence of token in s with a placeholder.
//
// git never prints the password it received, so this is defence in depth: it
// also covers the case where the operator embedded the same token in the remote
// URL, which git *does* echo back in error messages. Those messages travel into
// AppError.Details, the action log and the API response.
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}
