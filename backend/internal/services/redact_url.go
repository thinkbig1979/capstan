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

// authorityPrefixRe matches the "scheme://" (or bare "//") prefix that the
// parse-error fallback anchors on. Only a URL that announces an authority that
// way can have a userinfo section at all.
//
// The "//" is what keeps the fallback off scp-like SSH remotes such as
// git@github.com:org/repo.git, which have no authority marker and whose "git@"
// is not something to redact. url.Parse rejects those, so they arrive at the
// fallback and this anchor is the only thing between them and a mangled
// remote. The scheme is optional so protocol-relative "//user:pw@host/path" is
// covered too; the scheme name pattern cannot contain "@", so making it
// optional does not admit the scp-like form.
var authorityPrefixRe = regexp.MustCompile(`^(?:[a-zA-Z][a-zA-Z0-9+.\-]*:)?//`)

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
//     branch, so it cannot simply return the input. redactUnparseableUserinfo
//     splits those two cases, and both are tested.
//
// A fifth, added by agent-os-zzhs: a userinfo section holding no non-empty
// component ("@", ":@") is NOT a credential, and marking it is a live fault
// rather than a cosmetic one — the marker trips the save guard in the backup
// settings handler, so the operator cannot write to the field at all.
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
		return redactUnparseableUserinfo(raw)
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
	//
	// Tested component by component, NOT via u.User.String(). For ":@" — which
	// is what a compose template renders when both the user and the password
	// variable are unset — Go returns a password that is empty but SET, so
	// String() is ":" rather than "", the guard missed it, and the marker was
	// spliced into a URI carrying nothing (agent-os-zzhs). That is not
	// cosmetic: the served value then contains the marker, so the
	// UserinfoRedactionMarker guard in BackupHandler.updateSettings rejects every
	// write to the field and the operator cannot edit it at all.
	if password, _ := u.User.Password(); u.User.Username() == "" && password == "" {
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
	if u.Scheme == "" && u.Host != "" {
		// Protocol-relative ("//user:pw@host/path"). u.String() starts with
		// "//" here, so the splice below is well defined; without this the
		// fail-closed branch sent it to a fallback that cannot match a URL
		// with no scheme, and the credential was served in clear.
		prefix = "//"
	}
	full := u.String()
	if !strings.HasPrefix(full, prefix) {
		return redactUnparseableUserinfo(raw)
	}
	u.User = nil
	return prefix + userinfoPlaceholder + "@" + strings.TrimPrefix(u.String(), prefix)
}

// redactUnparseableUserinfo is the parse-error fallback: it locates the
// userinfo in a URL too malformed for url.Parse and replaces it with the
// marker, or returns raw byte-for-byte when it finds no credential.
//
// It replaced a single regexp (`^(scheme://)[^/@]*@`) whose character class
// could not cross a "/". A password containing "/" makes url.Parse read it as
// a port and reject the whole URL, so such a credential took this branch and
// then failed to match, and was served in clear. An AWS secret access key is
// 40 characters over [A-Za-z0-9/+], so it contains a "/" about 46% of the time
// (agent-os-zzhs).
func redactUnparseableUserinfo(raw string) string {
	loc := authorityPrefixRe.FindStringIndex(raw)
	if loc == nil {
		return raw
	}
	prefix, rest := raw[:loc[1]], raw[loc[1]:]

	// Where a conventional authority would end.
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}

	if at := strings.LastIndex(rest[:end], "@"); at >= 0 {
		// Conventional shape: the userinfo lies wholly before the first "/",
		// which is every case the old regexp handled.
		return spliceUserinfo(prefix, rest[:at], rest[at+1:])
	}

	// No "@" inside a conventional authority. Either the userinfo contains a
	// "/" and the authority runs past that first slash, or an "@" further
	// along belongs to the PATH and there is no credential at all.
	//
	// Those are told apart by the text before that first slash: if it is a
	// valid authority on its own then the authority really did end there, the
	// parse failed for some other reason (a bad percent-escape in the path),
	// and any later "@" is path content to leave alone. If it is NOT valid —
	// "bob:AB" out of "bob:AB/CD+SECRET@host", where url.Parse reported
	// `invalid port ":AB"` — then the authority extends past the slash.
	if isParseableAuthority(rest[:end]) {
		return raw
	}

	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return raw
	}
	host := rest[at+1:]
	if hostEnd := strings.IndexAny(host, "/?#"); hostEnd >= 0 {
		host = host[:hostEnd]
	}
	// Fail CLOSED: only splice when what follows the "@" is actually a host.
	// Otherwise the "@" is something else and rewriting would produce a
	// garbage remote, which is worse than a coarse one.
	if !isParseableAuthority(host) {
		return raw
	}
	return spliceUserinfo(prefix, rest[:at], rest[at+1:])
}

// spliceUserinfo replaces a userinfo section with the marker, leaving the
// value byte-for-byte alone when that section holds no non-empty component.
//
// "" ("http://@host") and ":" ("http://:@host") carry no secret, and this
// branch is reached for them whenever the URL is unparseable for an unrelated
// reason — a bad percent-escape in the path, say. Marking one would claim a
// credential is embedded where none is, and the marker then trips the
// UserinfoRedactionMarker guard in BackupHandler.updateSettings (agent-os-zzhs).
func spliceUserinfo(prefix, userinfo, after string) string {
	if user, password, _ := strings.Cut(userinfo, ":"); user == "" && password == "" {
		return prefix + userinfo + "@" + after
	}
	return prefix + userinfoPlaceholder + "@" + after
}

// isParseableAuthority reports whether s is a well-formed host[:port] on its
// own. Delegated to url.Parse rather than pattern-matched, so it agrees with
// the parser whose rejection put us on this branch in the first place.
func isParseableAuthority(s string) bool {
	if s == "" || strings.Contains(s, "@") {
		return false
	}
	u, err := url.Parse("http://" + s + "/")
	return err == nil && u.Host == s
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
