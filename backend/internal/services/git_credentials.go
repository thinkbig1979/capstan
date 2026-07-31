package services

import (
	"os"
	"os/exec"
	"strings"
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

// httpsCredentials resolves the git HTTPS credential, preferring the value
// stored (encrypted) in settings over the GIT_HTTPS_TOKEN environment variable.
// It returns an empty token when none is configured, in which case git runs
// exactly as before.
func (s *GitService) httpsCredentials() (user, token string) {
	if s.db != nil {
		// A decrypt failure (wrong STORAGE_KEY) surfaces as an error here; treat
		// it as "no credential" and let git report the auth failure, rather than
		// failing every git command including the ones that need no remote.
		if v, err := s.db.GetSetting("git_https_token"); err == nil {
			token = v
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
	gitArgs := []string{"-c", "safe.directory=" + dirPath}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	user, token := s.httpsCredentials()
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
