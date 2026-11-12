package services

import (
	"context"
	"strings"
)

// RcloneRedactionMarker is what RedactRcloneRemote leaves in place of each
// parameter value. A remote still carrying it is a redacted one, never a
// value to store (agent-os-qnso).
const RcloneRedactionMarker = "=" + userinfoPlaceholder

// RedactRcloneRemote masks the credential parts of an rclone remote so it can
// be logged, streamed or served (agent-os-qnso).
//
// rclone_remote is a sensitive setting because rclone accepts a connection
// string as the remote: ":s3,access_key_id=AK,secret_access_key=SK" or
// "name,token=...". Every parameter after the first comma is "key=value", and
// the value is masked whatever the key, by pattern, never by value: which
// keys are secret differs per backend. The backend or remote name before the
// first comma is kept, with any URL userinfo in it masked, so an operator
// still recognises the remote. A parameter with no "=" (a bare flag) is kept.
//
// Values may be quoted with ' or " (rclone's own grammar), so a comma inside
// quotes does not end the value. An unterminated quote fails closed: the rest
// of the string is masked.
func RedactRcloneRemote(remote string) string {
	head, params, found := strings.Cut(remote, ",")
	head = RedactURLUserinfo(head)
	if !found {
		return head
	}
	var b strings.Builder
	b.WriteString(head)
	for params != "" {
		b.WriteByte(',')
		param, rest, ok := cutRcloneParam(params)
		if !ok {
			b.WriteString(userinfoPlaceholder)
			break
		}
		key, _, hasValue := strings.Cut(param, "=")
		if hasValue {
			b.WriteString(key + RcloneRedactionMarker)
		} else {
			b.WriteString(param)
		}
		params = rest
	}
	return b.String()
}

// rcloneTargetForDisplay is "remote:path" with the remote's credentials
// masked, for logs, errors and stream lines. The argv keeps the raw value.
func rcloneTargetForDisplay(remote, path string) string {
	return RedactRcloneRemote(remote) + ":" + path
}

// cutRcloneParam splits off the first comma-separated parameter, honouring '
// and " quotes. ok is false for an unterminated quote.
func cutRcloneParam(s string) (param, rest string, ok bool) {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ',':
			return s[:i], s[i+1:], true
		}
	}
	if quote != 0 {
		return "", "", false
	}
	return s, "", true
}

// rcloneSecrets returns the strings a scrubber must remove from rclone's
// output for remote: the remote as written, and each parameter value. rclone
// echoes the connection string in its own error lines (OBSERVED, rclone
// v1.60.1: `Failed to create file system for ":sftp,...,pass=PW:x"`).
func rcloneSecrets(remote string) []string {
	if RedactRcloneRemote(remote) == remote {
		return nil
	}
	secrets := []string{remote}
	_, params, _ := strings.Cut(remote, ",")
	for params != "" {
		param, rest, ok := cutRcloneParam(params)
		if !ok {
			// Unterminated quote: everything after it is secret.
			secrets = append(secrets, params)
			break
		}
		if _, v, hasValue := strings.Cut(param, "="); hasValue {
			for _, s := range []string{v, strings.Trim(v, `'"`)} {
				// A one- or two-character value would scrub ordinary text;
				// such a value is not a usable credential anyway.
				if len(s) >= 3 {
					secrets = append(secrets, s)
				}
			}
		}
		params = rest
	}
	return secrets
}

// scrubRclone masks remote's credential parts in s.
func scrubRclone(s string, secrets []string, masked string) string {
	if len(secrets) == 0 {
		return s
	}
	s = strings.ReplaceAll(s, secrets[0], masked)
	for _, v := range secrets[1:] {
		s = strings.ReplaceAll(s, v, userinfoPlaceholder)
	}
	return s
}

// scrubbingRunner is a commandRunner whose output lines have an rclone
// remote's credentials masked before they reach the caller's channel, which
// feeds the live stream and the stored run log.
type scrubbingRunner struct {
	inner   commandRunner
	secrets []string
	masked  string
}

// scrubRcloneRunner wraps runner for remote, or returns it as it is when
// the remote carries nothing to mask.
func scrubRcloneRunner(runner commandRunner, remote string) commandRunner {
	secrets := rcloneSecrets(remote)
	if len(secrets) == 0 {
		return runner
	}
	return scrubbingRunner{inner: runner, secrets: secrets, masked: RedactRcloneRemote(remote)}
}

func (r scrubbingRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	if out == nil {
		return r.inner.Run(ctx, name, args, env, out)
	}
	mid := make(chan StreamLine, 32)
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for l := range mid {
			l.Line = scrubRclone(l.Line, r.secrets, r.masked)
			select {
			case out <- l:
			case <-ctx.Done():
			}
		}
	}()
	// Closed by defer so a panicking Run cannot strand the forwarder.
	err := func() error {
		defer close(mid)
		return r.inner.Run(ctx, name, args, env, mid)
	}()
	<-forwarded
	return err
}

func (r scrubbingRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	return r.inner.Output(ctx, name, args, env)
}
