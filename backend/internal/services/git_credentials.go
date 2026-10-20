package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
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

// Environment variable names GIT_SSH_COMMAND reads, set only on the git child.
const (
	sshEnvKey        = "CAPSTAN_GIT_SSH_KEY"
	sshEnvKnownHosts = "CAPSTAN_GIT_SSH_KNOWN_HOSTS"
)

// gitSSHCommand is GIT_SSH_COMMAND for a directory with an SSH key
// (agent-os-z0gl). git runs it through sh, so it holds only variable NAMES;
// the paths are in the child environment and never re-parsed by the shell.
// OBSERVED, OpenSSH 10.0p2, git 2.47.3:
//
//   - -o IdentityFile, never -i: a missing -i key only warns, then ssh offers
//     the default ~/.ssh/id_* identities even with IdentitiesOnly=yes. A
//     missing IdentityFile offers nothing.
//   - -F /dev/null ignores a mounted ~/.ssh/config (ProxyCommand, other
//     IdentityFiles); IdentityAgent=none ignores an inherited SSH_AUTH_SOCK;
//     BatchMode=yes fails a passphrase-protected key instead of prompting.
//   - Host keys are trusted on first use and pinned in Capstan's data dir
//     (D157): accept-new records a new host and refuses a changed key.
//     GlobalKnownHostsFile=/dev/null keeps that file the only source.
//   - The inner double quotes keep a path with spaces one value: ssh's -o
//     parser splits on whitespace. sshPathProblem refuses the characters
//     that would end or escape them.
//   - GIT_SSH_COMMAND overrides a repository's core.sshCommand, and
//     ssh.variant=ssh (argv and GIT_SSH_VARIANT) overrides its ssh.variant,
//     which changes the arguments git passes.
const gitSSHCommand = `ssh -F /dev/null -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes` +
	` -o StrictHostKeyChecking=accept-new -o GlobalKnownHostsFile=/dev/null` +
	` -o "IdentityFile=\"$` + sshEnvKey + `\""` +
	` -o "UserKnownHostsFile=\"$` + sshEnvKnownHosts + `\""`

// gitSSHRefused is GIT_SSH_COMMAND when a key applies but cannot be used:
// any ssh child fails instead of trying the container's default identities.
const gitSSHRefused = "false"

// defaultGitHTTPSUser is used when neither the stored setting nor the
// GIT_HTTPS_USER environment variable names one. Forges that authenticate by
// token ignore the username, but it must not be empty or git re-prompts.
const defaultGitHTTPSUser = "oauth2"

// gitCreds is everything a git child authenticates with, resolved once per
// operation by gitCredentials and threaded to every child (agent-os-9ha). It
// is a value, not a lookup key: on the write paths the child runs in the
// repository top, while the credential belongs to the requesting folder.
type gitCreds struct {
	user, token string

	// sshKey is the private key ssh uses for this directory's remote: its
	// own key for auth type "ssh", else the global key (D159, D164). ""
	// means no key applies and ssh runs as it always did.
	sshKey string
	// sshKeySource names where sshKey came from, for refusal messages.
	sshKeySource string
	// knownHosts is <DataDir>/known_hosts, absolute (D157).
	knownHosts string
	// sshErr is set when a key applies but cannot be used safely. Every ssh
	// child then fails (GIT_SSH_COMMAND=false) rather than falling back to
	// the container's default identities, and the network pre-checks
	// return it.
	sshErr error
	// httpsAuth is set when the directory's auth type is "https": no ssh key
	// applies by choice, so an ssh remote has no credential to use and is
	// refused (agent-os-rde7). Other directories without a key keep running
	// ssh as they always did.
	httpsAuth bool
}

// gitCredentials resolves dirPath's credentials from one read of its
// directory row: the HTTPS credential (httpsCredentials) and the SSH key
// (sshCredentials).
func (s *GitService) gitCredentials(dirPath string) gitCreds {
	var dir *models.Directory
	dirErr := errdefs.ErrNotFound
	if s.db != nil {
		dir, dirErr = s.db.GetDirectoryCredentials(dirPath)
	}
	var c gitCreds
	c.user, c.token = s.httpsCredentials(dirPath, dir, dirErr)
	s.sshCredentials(&c, dir, dirErr)
	return c
}

// httpsCredentials resolves the git HTTPS credential for dirPath, in order:
//  1. directory authType "https" — that directory's own stored credential,
//     used as-is even if empty. Falling back to the global token here would
//     silently use a different credential than the one configured for this
//     directory, which is the exact failure mode being fixed (agent-os-qll):
//     a feature that reports "configured" while doing something else.
//  2. directory authType "ssh" — no HTTPS credential applies. The SSH key
//     is resolved by sshCredentials; this only makes sure "ssh" never falls
//     through to the https credential below.
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
//
// cred and err are gitCredentials' read of dirPath's row (err is
// errdefs.ErrNotFound when there is no database).
func (s *GitService) httpsCredentials(dirPath string, cred *models.Directory, err error) (user, token string) {
	switch {
	case errors.Is(err, errdefs.ErrNotFound):
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
		// No error is propagated: httpsCredentials feeds gitCmdWithCreds, which every
		// git invocation goes through, including purely local ones like
		// `git log` and `git diff` that never contact a remote. gitCmdWithCreds attaches
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

	if s.db != nil {
		// A decrypt failure (wrong STORAGE_KEY) surfaces as an error here; treat
		// it as "no credential" and let git report the auth failure, rather than
		// failing every git command including the ones that need no remote. Unlike
		// the per-directory read above, no dirPath applies here — this setting is
		// global — so the log line names the setting instead of a path.
		//
		// errdefs.ErrNotFound is the healthy default state (no global credential ever
		// configured) and must stay silent; only a genuinely unreadable stored
		// value is worth an operator's attention (agent-os-2tt).
		v, err := s.db.GetSetting("git_https_token")
		switch {
		case err == nil:
			token = v
		case errors.Is(err, errdefs.ErrNotFound):
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
		// git_https_user is NOT in sensitiveSettingKeys (database/settings.go:9-12
		// = {git_https_token, restic_password}), so unlike the token above it is
		// never decrypted and its only possible failure is a database-level one.
		//
		// This read used to be `if v, err := s.db.GetSetting("git_https_user");
		// err == nil { user = v }` — a fault and an absent row were the same
		// event, and a fault fell through to s.config.GitHTTPSUser below. That is
		// the "authenticate with a DIFFERENT credential than the one the operator
		// believes is configured" failure mode the token branch above exists to
		// prevent, applied to the other half of the same credential: two halves,
		// two standards, one function (agent-os-xzoe).
		//
		// NOT TESTED BY A FAULT ROUTE INTO THIS BRANCH, because no such route
		// exists: every branch of the token switch above that is neither nil nor
		// errdefs.ErrNotFound has already returned "", "", and a database-level fault
		// would have hit that read first. Verified by reading the switch at
		// :128-157, and OBSERVED: a `go test -overlay` mutant that swallows this
		// default branch entirely leaves ./internal/services GREEN, 0 `--- FAIL`,
		// with `go build ./...` exit 0. (`getter-errors reach` cannot answer this
		// one either -- its site shape is an `if err != nil` guard, so a
		// switch-based conversion is not in its site set at all. Its shell
		// driver check-getter-fault-reach.sh was deleted in agent-os-1hig; the
		// limitation named here is a property of the analyser, which survives.)
		// That unreachability is an accident of statement ORDER,
		// not a property of this site — move this read above the token read, or
		// weaken either `return "", ""`, and the old fall-through becomes live. So
		// the order is itself pinned, by
		// TestHTTPSCredentials_UnreadableSettings_TokenReadRefusesBeforeUserRead
		// in git_credentials_dbfault_test.go.
		storedUser, err := s.db.GetSetting("git_https_user")
		switch {
		case err == nil:
			user = storedUser
		case errors.Is(err, errdefs.ErrNotFound):
			// No username was ever stored — the healthy default. The config/env
			// value below, then defaultGitHTTPSUser, are the intended fallbacks
			// and this must stay silent (agent-os-2tt, same reasoning as the
			// token read's errdefs.ErrNotFound branch above).
		default:
			// Same fail-closed shape as the two token branches above, for the
			// same reason. The error is logged as "error", err following this
			// file's own convention: git_https_user is never encrypted, so this
			// error cannot carry ciphertext or key material, and neither can the
			// two token branches' (services/crypto.go:157-194 returns only fixed
			// literals and openWith swallows the AEAD error).
			slog.Error("cannot read the stored git https username: the stored value could not be read", "setting", "git_https_user", "error", err)
			return "", ""
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

// gitHardeningArgs go before the subcommand of every git child, so a stack
// repository's own config cannot make a plain read run a program it names
// (agent-os-ojdu). OBSERVED, git 2.47.3, with marker programs in repo config:
// GetStatus ran gpg.program four times (log.showSignature) and its
// `status --porcelain` ran core.fsmonitor and a post-index-change hook; GetLog
// and GetLogForFile ran gpg.program.
//
//   - core.fsmonitor=false: status and diff do not exec the fsmonitor hook.
//   - log.showSignature=false: log and show do not verify signatures, which
//     is what runs gpg.program.
//   - --no-optional-locks: status does not refresh and write the index, which
//     is what runs post-index-change. Writes still take the locks they need:
//     OBSERVED, add, commit and pull --ff-only under this flag still wrote the
//     index (post-index-change ran once each) and push succeeded.
//   - log.follow=false: a file log does not follow renames, which in a
//     blob:none partial clone lazy-fetches old blobs and so runs the repo's
//     remote.origin.uploadpack (OBSERVED: 1 run with log.follow=true, 0 with
//     false). Capstan never passes --follow, so this changes nothing unless
//     the repository set log.follow itself.
//
// core.hooksPath is deliberately NOT cleared here: pull's hooks are the
// repository's to run, and the pull fault tests inject faults through them.
//
// RESIDUAL, no git option closes these:
//   - Clean and process filters from .gitattributes still run on status and
//     diff. With --no-optional-locks a stat-dirty tracked file also stays
//     stat-dirty, so its filter runs on every status rather than once
//     (OBSERVED: 3 runs over 3 statuses, against 1 without the flag); the
//     dirty answer stays correct.
//   - In a partial clone, a read that needs a missing blob (diff HEAD on a
//     file never fetched) still lazy-fetches through the repo's upload-pack.
//     noLazyFetchEnv cannot go here: pull's checkout needs lazy fetch.
var gitHardeningArgs = []string{
	"-c", "core.fsmonitor=false",
	"-c", "log.showSignature=false",
	"-c", "log.follow=false",
	"--no-optional-locks",
}

// gitCmdWithCreds builds the git child process for dirPath with the HTTPS
// credential and the SSH key attached when configured. It also returns the token it
// used, so the caller can redact it from output without resolving (and
// decrypting) it twice.
//
// The credential is applied to every invocation rather than to a hand-picked
// list of remote-contacting subcommands: pull, fetch, clone, ls-remote and
// `status` with a configured upstream can all reach the network, and an
// omission from such a list is exactly the failure mode being fixed here. git
// only runs the helper when a remote actually challenges it.
//
// It takes already-resolved credentials instead of calling gitCredentials
// itself. A single logical git operation (status, pull, log,
// diff) issues several of these; resolving once at the top of that operation
// and passing the result to every call here — instead of letting each one
// re-resolve — is what turns N DB reads/decrypt attempts and N duplicate log
// lines per operation into one (agent-os-9ha). It intentionally carries no memoizing
// state of its own: see the doc comment on GitService for why a shared cache
// was rejected.
//
// The child runs under ctx, which the caller bounds (gitCommandWithCreds uses
// the git deadline, agent-os-a1ye.3): a git fetch or pull stalled on the
// network used to hang its request, and with it any operation lock held
// around it, until the server restarted.
func (s *GitService) gitCmdWithCreds(ctx context.Context, dirPath string, c gitCreds, args ...string) (*exec.Cmd, string) {
	gitArgs := append([]string{"-c", "safe.directory=" + dirPath}, gitHardeningArgs...)
	// stripCapstanSecrets removes Capstan's own secrets (JWT_SECRET,
	// STORAGE_KEY, GIT_HTTPS_TOKEN, RESTIC_PASSWORD) before the process
	// environment is forwarded to the git child; see exec_env.go's
	// stripCapstanSecrets for why this is a denylist rather than an
	// allowlist. Hygiene, consistent with this package's other child-process
	// call sites (exec_env.go, backup_restic.go).
	//
	// LC_ALL=C / LANGUAGE=<cleared> pin the child to English, untranslated
	// messages: git ships ~19 gettext catalogs, and callers of this command
	// (pullCLI's "Already up to date" match) parse its stdout/stderr as a
	// machine interface, not prose for a human. Without this, the same pull
	// that succeeds in English reports a false 500 on a non-English host
	// (agent-os-vq3p).
	//
	// LC_ALL=C is the load-bearing half; clearing LANGUAGE is defence in
	// depth, not the mechanism. glibc's gettext consults LANGUAGE only when
	// the MESSAGES locale resolves to something other than C or POSIX — a
	// C/POSIX messages locale disables translation outright and LANGUAGE is
	// then ignored entirely. The empty LANGUAGE costs nothing and keeps the
	// pin correct if anyone later moves LC_ALL off C, which is the one case
	// where LANGUAGE would win. OBSERVED, git version 2.47.3 on this host,
	// `git status --porcelain` run outside a repository in five environments:
	//
	//	LC_ALL=C           LANGUAGE=de           -> "fatal: not a git repository ..."       English
	//	LC_ALL=POSIX       LANGUAGE=de           -> "fatal: not a git repository ..."       English
	//	LANG=de_DE.UTF-8   LANGUAGE=de (no LC_ALL) -> "fatal: not a git repository ..."     English
	//	LC_ALL=de_DE.UTF-8 (no LANGUAGE)         -> "fatal: not a git repository ..."       English
	//	LC_ALL=en_US.utf8  LANGUAGE=de           -> "Schwerwiegend: Kein Git-Repository ..." German
	//
	// The last arm is the positive control: the same command, same git, so
	// the difference is the environment and the catalogs are demonstrably
	// installed. The fourth arm reads English only because de_DE.UTF-8 is not
	// generated on this box (`locale -a` lists en_US.utf8 and no de_*), so
	// setlocale falls back to C — a host artifact, not gettext precedence.
	//
	// Appending both here (rather than filtering them out of
	// stripCapstanSecrets' output first) is sufficient: os/exec's Cmd.Env is
	// documented to keep only the last value in the slice for a duplicate
	// key, so these two entries win over whatever stripCapstanSecrets
	// forwarded from Capstan's own environment.
	env := append(stripCapstanSecrets(os.Environ()), "GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C", "LANGUAGE=")

	switch {
	case c.sshErr != nil:
		gitArgs = append(gitArgs, "-c", "ssh.variant=ssh")
		env = append(env, "GIT_SSH_VARIANT=ssh", "GIT_SSH_COMMAND="+gitSSHRefused)
	case c.sshKey != "":
		// Every child, not only pull and push: a read in a partial clone can
		// lazy-fetch over ssh too. A child that never reaches ssh ignores it.
		gitArgs = append(gitArgs, "-c", "ssh.variant=ssh")
		env = append(env, "GIT_SSH_VARIANT=ssh", "GIT_SSH_COMMAND="+gitSSHCommand,
			sshEnvKey+"="+sshEscape(c.sshKey),
			sshEnvKnownHosts+"="+sshEscape(c.knownHosts),
		)
	}

	if token := c.token; token != "" {
		// The empty assignment first clears any helper inherited from system or
		// global config, so ours is the only one consulted.
		gitArgs = append(gitArgs,
			"-c", "credential.helper=",
			"-c", "credential.helper="+gitCredentialHelper,
		)
		env = append(env,
			credentialEnvUser+"="+c.user,
			credentialEnvToken+"="+token,
		)
	}

	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := boundCommand(ctx, "git", append(gitArgs, args...)...)
	cmd.Dir = dirPath
	cmd.Env = env
	return cmd, c.token
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

// sshCredentials sets c's SSH key from the directory row gitCredentials
// read (agent-os-z0gl):
//   - auth type "ssh": the directory's own key. No key path is an error, not
//     a fall-through: the directory says SSH, so another key would be a
//     different credential than the one configured.
//   - auth type "https": none.
//   - inherit, "" or no row: the global key (D159), git_ssh_key then
//     cfg.GitSSHKey. When that is the built-in default path and nothing is
//     there, no key applies and ssh runs as before (D164). The settings
//     page saves the default back as a row, so this compares the path, not
//     where it came from.
//
// An unreadable row or setting fails closed into sshErr, as httpsCredentials
// does for the token (agent-os-2au).
func (s *GitService) sshCredentials(c *gitCreds, dir *models.Directory, dirErr error) {
	switch {
	case errors.Is(dirErr, errdefs.ErrNotFound):
	case dirErr != nil:
		c.sshErr = fmt.Errorf("cannot read this directory's git credentials, so not connecting over SSH: %w", dirErr)
		return
	default:
		switch strings.ToLower(dir.GitAuthType) {
		case "https":
			c.httpsAuth = true
			return
		case "ssh":
			if dir.GitSSHKeyPath == "" {
				c.sshErr = models.NewAppError(http.StatusConflict, models.ErrGitSSHKeyUnusable,
					"This directory uses SSH git auth but has no SSH key path; set one in its git credentials")
				return
			}
			s.useSSHKey(c, dir.GitSSHKeyPath, "This directory's SSH key")
			return
		}
	}

	key := ""
	if s.config != nil {
		key = s.config.GitSSHKey
	}
	if s.db != nil {
		v, err := s.db.GetSetting("git_ssh_key")
		switch {
		case errors.Is(err, errdefs.ErrNotFound):
		case err != nil:
			c.sshErr = fmt.Errorf("cannot read the global git SSH key setting, so not connecting over SSH: %w", err)
			return
		case v != "":
			key = v
		}
	}
	if key == "" {
		return
	}
	if key == config.DefaultGitSSHKey() {
		if _, err := os.Stat(key); errors.Is(err, fs.ErrNotExist) {
			return
		}
	}
	s.useSSHKey(c, key, "The global SSH key (Settings > Git)")
}

// useSSHKey sets key as c's SSH key, or sshErr when ssh would misread its
// path or there is nowhere to keep host keys.
func (s *GitService) useSSHKey(c *gitCreds, key, source string) {
	c.sshKey, c.sshKeySource = key, source
	if problem := sshPathProblem(key); problem != "" {
		c.sshErr = models.NewAppError(http.StatusConflict, models.ErrGitSSHKeyUnusable,
			fmt.Sprintf("%s path %q %s", source, key, problem))
		return
	}
	dataDir := ""
	if s.config != nil {
		dataDir = s.config.DataDir
	}
	if dataDir == "" {
		// Fail closed: "" + "/known_hosts" would pin host keys at /known_hosts.
		c.sshErr = errors.New("no data directory is configured to keep SSH host keys in, so not connecting over SSH")
		return
	}
	// ssh runs in the repository, so a relative DATA_DIR must not reach it.
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		c.sshErr = fmt.Errorf("cannot resolve the data directory for SSH host keys: %w", err)
		return
	}
	c.knownHosts = filepath.Join(abs, "known_hosts")
	if problem := sshPathProblem(c.knownHosts); problem != "" {
		c.sshErr = fmt.Errorf("the SSH known_hosts path %q %s", c.knownHosts, problem)
		return
	}
	if err := knownHostsWritable(c.knownHosts); err != nil {
		c.sshErr = err
	}
}

// access(2) mode bits; package syscall does not name them.
const (
	accessX = 0x1
	accessW = 0x2
)

// knownHostsWritable refuses a known_hosts ssh could not record a host in.
// accept-new only warns when the write fails and connects anyway, so every
// connection is a first use and a CHANGED host key is accepted (OBSERVED,
// review r1-z0gl: read-only data dir and 0400 file both connected to a new
// host key). No ssh option turns that warning into a failure. The data
// directory is deployment state, so this is a plain error (a server fault),
// not a request-level refusal; the message names the fix for the operator.
func knownHostsWritable(kh string) error {
	fi, err := os.Stat(kh)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		dir := filepath.Dir(kh)
		if err := syscall.Access(dir, accessW|accessX); err != nil {
			return fmt.Errorf("SSH host keys cannot be recorded: the data directory %q is not writable by the user Capstan runs as (%w), so a changed host key would go unnoticed; make it writable by that user (PUID)", dir, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("cannot check the SSH known_hosts file %q: %w", kh, err)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("SSH host keys cannot be recorded: %q is not a regular file", kh)
	}
	if err := syscall.Access(kh, accessW); err != nil {
		return fmt.Errorf("SSH host keys cannot be recorded: %q is not writable by the user Capstan runs as (%w), so a changed host key would go unnoticed; chown it to that user (PUID) and chmod 600", kh, err)
	}
	return nil
}

// sshPathProblem says why ssh would not read p as the literal path, or "".
// OBSERVED, OpenSSH 10.0p2, in IdentityFile: `~` and `${HOME}` expand to the
// home directory, `%h` to the host, `\"` to a quote; a bare `$x` and `'`
// stay literal. `%` is escaped by sshEscape; the rest has no escape.
func sshPathProblem(p string) string {
	switch {
	case !filepath.IsAbs(p):
		return "is not an absolute path"
	case strings.ContainsAny(p, "\"\\"):
		return "contains a quote or backslash, which ssh would misread"
	case strings.Contains(p, "${"):
		return "contains ${, which ssh expands as an environment variable"
	case strings.IndexFunc(p, unicode.IsControl) >= 0:
		return "contains a control character"
	}
	return ""
}

// sshEscape escapes ssh's %-tokens in a path sshPathProblem accepted.
func sshEscape(p string) string {
	return strings.ReplaceAll(p, "%", "%%")
}

// sshReady checks, just before a network git child that will use ssh, that
// c's key can be used. OpenSSH refuses a key other users can read
// ("UNPROTECTED PRIVATE KEY FILE"), and a key mounted from the host is often
// 0644 or owned by root; each answer names the fix. nil when no key applies:
// the caller decides what that means.
func (c gitCreds) sshReady() error {
	if c.sshErr != nil {
		return c.sshErr
	}
	if c.sshKey == "" {
		return nil
	}
	unusable := func(format string, a ...any) error {
		return models.NewAppError(http.StatusConflict, models.ErrGitSSHKeyUnusable,
			fmt.Sprintf("%s %q ", c.sshKeySource, c.sshKey)+fmt.Sprintf(format, a...))
	}
	fi, err := os.Stat(c.sshKey)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return unusable("does not exist inside the container; mount the key file and set its path")
	case errors.Is(err, fs.ErrPermission):
		return unusable("cannot be reached by the user Capstan runs as; check the permissions of its directory")
	case err != nil:
		return fmt.Errorf("cannot check the SSH key %q: %w", c.sshKey, err)
	}
	if !fi.Mode().IsRegular() {
		return unusable("is not a regular file")
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return unusable("is readable by other users (mode %04o), which ssh refuses; run chmod 600 on it", perm)
	}
	f, err := os.Open(c.sshKey)
	if err != nil {
		return unusable("cannot be read by the user Capstan runs as; chown it to that user (PUID) and keep mode 600")
	}
	_ = f.Close() //nolint:errcheck // opened read-only to test access; nothing was written
	return nil
}

// isSSHRemoteURL reports whether git would reach u over ssh: ssh://,
// git+ssh://, ssh+git://, or scp-like [user@]host:path (git's own rule: with
// no scheme, a ':' before the first '/'). A <transport>:: helper is not.
func isSSHRemoteURL(u string) bool {
	if transportHelperRe.MatchString(u) {
		return false
	}
	if m := urlSchemeRe.FindStringSubmatch(u); m != nil {
		switch strings.ToLower(m[1]) {
		case "ssh", "git+ssh", "ssh+git":
			return true
		}
		return false
	}
	colon := strings.IndexByte(u, ':')
	slash := strings.IndexByte(u, '/')
	return colon >= 0 && (slash < 0 || colon < slash)
}

// sshRemoteRefusal answers a network operation that would go over ssh: with
// no key applying it is GIT_SSH_UNSUPPORTED, since ssh would use whatever
// default identity the container has (and the repository's core.sshCommand);
// a key that applies but cannot be used is GIT_SSH_KEY_UNUSABLE. lead names
// the operation, "Remote origin pushes" or "The remote pulls", so push and
// pull give one answer in one place (agent-os-rde7).
func sshRemoteRefusal(lead string, c gitCreds) error {
	if c.sshKey == "" && c.sshErr == nil {
		return models.NewAppError(http.StatusConflict, models.ErrGitSSHUnsupported,
			lead+" over SSH, but no SSH key applies to this directory; set its git auth to SSH with a key path, set the global SSH key in Settings > Git, or use an https remote")
	}
	return c.sshReady()
}

// sshPullPrecheck refuses a pull that would go over ssh with a key that
// cannot be used, before the pull runs. `ls-remote --get-url` reads the URL
// the pull would contact (default remote, insteadOf applied) without
// contacting it. A pull over https is never refused for a bad ssh key; when
// the URL cannot be read, the key is checked anyway. A directory set to https
// auth has no key by choice, so its ssh remote is refused too, as push
// refuses it (agent-os-rde7); a directory merely without a key (inherit, no
// global key) keeps running ssh as before.
func (s *GitService) sshPullPrecheck(dirPath string, c gitCreds) error {
	noKey := c.sshKey == "" && c.sshErr == nil
	if noKey && !c.httpsAuth {
		return nil
	}
	u, err := s.gitCommandWithCreds(dirPath, c, "ls-remote", "--get-url") //geterrors:ignore a failed read (no remote configured exits 128) means the URL is unknown, so the key is checked anyway: fail closed, and the pull reports its own failure
	if err != nil {
		return c.sshReady()
	}
	if !isSSHRemoteURL(u) {
		return nil
	}
	return sshRemoteRefusal("The remote pulls", c)
}
