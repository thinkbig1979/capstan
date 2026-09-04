package services

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// userinfoPlaceholder is the marker left in place of a stripped credential. It
// matches redactToken's placeholder so the UI shows one convention.
const userinfoPlaceholder = "***"

// UserinfoRedactionMarker is the substring a caller checks to detect a value
// that has already been redacted, so it is never persisted back over the real
// one (agent-os-57xj).
const UserinfoRedactionMarker = userinfoPlaceholder + "@"

// schemeUserinfoRe matches a scheme-anchored userinfo section. Used ONLY when
// url.Parse rejects the input, to catch a credential in a URL too malformed to
// parse (e.g. a space inside the password).
//
// Anchored on "scheme://" on purpose: that is what keeps it off scp-like SSH
// remotes such as git@github.com:org/repo.git, which have no scheme and whose
// "git@" is not something to redact.
var schemeUserinfoRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@]*@`)

// RedactURLUserinfo removes the credential from a remote URL, keeping the parts
// an operator needs to recognise it: scheme, host, port and path.
//
// A remote URL reaches the API response body and the browser DOM, and an
// operator may have embedded a credential directly in .git/config
// (https://user:token@host/repo.git). That credential was served verbatim to
// every authenticated client (agent-os-57xj).
//
// This redacts by PATTERN, never by value. The existing redactToken replaces a
// specific known token, so it can only remove secrets Capstan itself issued —
// which is why it never caught this: the go-git path that serves most requests
// reads .git/config directly and never calls it at all, and even on the CLI path
// a credential Capstan did not issue passes straight through.
//
// The placeholder is kept rather than the userinfo dropped, deliberately: it
// tells the operator a credential is embedded, which is a misconfiguration they
// should fix by moving it into Capstan's credential store. Silently dropping it
// would make a broken config look clean.
//
// Four behaviours that are easy to get wrong and are pinned by tests:
//
//   - The whole userinfo goes, not just the password. The commonest GitHub PAT
//     form (https://ghp_xxx@host/) has no password field at all.
//   - A URL with no credential is returned BYTE-FOR-BYTE, never round-tripped
//     through url.String(), which re-encodes spaces and other legal characters.
//   - scp-like SSH (git@github.com:org/repo.git) makes url.Parse fail, so the
//     parse-error branch is load-bearing for the commonest SSH remote.
//   - ...but a malformed URL that DOES carry a credential takes that same
//     branch, so it cannot simply return the input. The regex above splits
//     those two cases, and both are tested.
//
// ssh://git@host/repo.git is redacted to ssh://***@host/repo.git even though
// "git" is not a secret. Preserving it would need an allowlist of usernames
// judged safe, which is precisely the value-keyed reasoning that produced this
// bug; the host and path still identify the remote.
func RedactURLUserinfo(raw string) string {
	if raw == "" {
		return raw
	}

	// Surrounding whitespace defeats both branches below: url.Parse reads a
	// leading space as part of the path so it finds no userinfo, and the regex
	// is anchored at the start of the string. Either way the credential
	// survived. Trim first, and preserve what was trimmed so a credential-free
	// value still round-trips byte-for-byte.
	trimmed := strings.TrimSpace(raw)
	if trimmed != raw {
		redacted := RedactURLUserinfo(trimmed)
		if redacted == trimmed {
			return raw
		}
		lead := raw[:strings.Index(raw, trimmed)]
		return lead + redacted + raw[len(lead)+len(trimmed):]
	}

	u, err := url.Parse(raw)
	if err != nil {
		// Unparseable. It may still carry a credential, so try the
		// scheme-anchored form rather than trusting the input.
		return schemeUserinfoRe.ReplaceAllString(raw, "${1}"+userinfoPlaceholder+"@")
	}
	// A restic-style repository nests a real URL inside an opaque part:
	// "rest:https://user:pass@host/" parses as scheme "rest" with the whole
	// URL as Opaque and NO userinfo, so the check below would pass it straight
	// through. Recurse into the nested URL and reattach the prefix.
	//
	// Gated on "://" so it fires only where a nested URL actually exists. Note
	// the gate is belt-and-braces, NOT the thing protecting "sftp:user@host:/path"
	// and "b2:bucket:path" — the "inner == u.Opaque" fallback below already
	// leaves those untouched on its own. An earlier version of this comment
	// claimed otherwise and a mutation proved it inert: removing the gate
	// changes no output across the whole corpus. Kept because it states the
	// intent at the point of the recursion, not because it is load-bearing.
	if u.User == nil && u.Opaque != "" && strings.Contains(u.Opaque, "://") {
		inner := RedactURLUserinfo(u.Opaque)
		if inner == u.Opaque {
			return raw
		}
		return u.Scheme + ":" + inner
	}

	if u.User == nil {
		// No credential: return the ORIGINAL, not u.String().
		return raw
	}

	// An EMPTY userinfo carries no secret, and marking it would tell the
	// operator a credential is embedded when none is — the marker's whole
	// purpose is that signal, so a false one is worse than none.
	if u.User.String() == "" {
		return raw
	}

	// Splice the marker in by hand. url.User("***") percent-encodes the
	// asterisks to %2A%2A%2A, which is neither readable nor the house marker.
	//
	// Fail CLOSED. u.String() only starts with scheme:// when there is both a
	// scheme and a host; for shapes like "http://host@" or "//user:pw@host" it
	// does not, and blindly concatenating produced garbage such as
	// "http://***@http:". A garbage remote is a worse outcome than a coarse
	// one, so those fall back to the scheme-anchored regex, which either
	// redacts correctly or leaves the value alone.
	prefix := u.Scheme + "://"
	full := u.String()
	if u.Scheme == "" || !strings.HasPrefix(full, prefix) {
		return schemeUserinfoRe.ReplaceAllString(raw, "${1}"+userinfoPlaceholder+"@")
	}
	u.User = nil
	return prefix + userinfoPlaceholder + "@" + strings.TrimPrefix(u.String(), prefix)
}

// redactErrPath rewrites any occurrence of path inside err's message with its
// redacted form.
//
// os.MkdirAll returns an *os.PathError that echoes the path it was given, so a
// remote restic repository URI carrying a credential ends up inside the error
// string — which agent-os-7z8c now logs in full for every 5xx. Wrapping with a
// redacted path is not enough on its own; the wrapped error still holds the
// raw one.
func redactErrPath(err error, path string) error {
	if err == nil || path == "" {
		return err
	}
	redacted := RedactURLUserinfo(path)
	if redacted == path {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), path, redacted))
}
