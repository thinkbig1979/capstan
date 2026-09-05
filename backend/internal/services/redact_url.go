package services

import (
	"errors"
	"fmt"
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

	// An inline credential in an OPAQUE part, with no "//" anywhere
	// (agent-os-qonw): "sftp:user:PW@host:/path". Neither branch below can
	// see it — url.Parse reports no userinfo for an opaque part, and the
	// parse-error fallback anchors on "//" — so it is decided first, by the
	// per-scheme grammar table both this function and ValidateRepositoryForm
	// read.
	if redacted, ok := redactOpaqueCredential(raw); ok {
		return redacted
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

// ───────────────────────────────────────────
// Opaque repository forms (agent-os-qonw)
// ───────────────────────────────────────────
//
// restic repository strings that are NOT URLs: "sftp:user@host:/path",
// "s3:host/bucket", "rclone:remote:path", "b2:bucket:path". They parse as
// scheme + Opaque with no userinfo, so RedactURLUserinfo's two branches
// (url.Parse, and a "//"-anchored fallback) both pass them through. An operator
// who types "sftp:user:PW@host:/path" therefore had the password served in
// clear, with an ORDINARY password and no "/" at all — a different defect from
// agent-os-zzhs.
//
// The layer decision, made in the bead after checking each backend's own
// grammar (restic docs, restic source, and restic 0.18.0 run locally):
//
//   - "sftp:user:PW@host:/path": restic cuts at the FIRST ":" and reads
//     "user" as the host (OBSERVED: `ssh: Could not resolve hostname user`).
//     Its URL form never calls url.User.Password(). SFTP is key-auth.
//   - "s3:KEY:PW@host/bucket": restic reads everything before the first "/"
//     as the endpoint and never reads a URL userinfo; credentials come from
//     AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY in the environment.
//   - "rclone:user:PW@remote:path": rclone looks for a remote named "user"
//     (OBSERVED: `didn't find section in config file`).
//   - "rclone::sftp,host=h,user=u,pass=OBSCURED:path": a documented rclone
//     connection string, which restic passes through verbatim and rclone DOES
//     consume (OBSERVED: `input too short when revealing password`). The only
//     corpus shape that is a real form with an inline credential.
//
// So the first three are misconfigurations, and starring them out on read
// would make a backup that cannot work look handled. ValidateRepositoryForm
// refuses all four at save with a message naming the supported form; the
// connection string is refused too because rclone.conf is where rclone puts
// that secret and where Capstan's credential store argues it belongs.
// redactOpaqueCredential covers rows persisted BEFORE the validator existed:
// they are served starred, their round-trip is refused (by the marker guard
// for "***@", by the validator for "pass=***"), and the operator's way out is
// a form the validator accepts — exactly what a legacy rest:https://user:pw@
// row gets today.
//
// THE HARD PART IS DISCRIMINATING, not redacting more. Over-starring a value
// that carries no secret is a LOCKOUT: the marker trips the save guard in
// BackupHandler.updateSettings and the field becomes unsavable (agent-os-zzhs
// arm 2). So the rules below are BOUNDED — three schemes, one shape each —
// rather than a general "colon before at-sign" rule, which would star
// "rclone:gdrive:edwin@example.com" (a folder named after an email address)
// and "sftp:host:/path/with@sign". Both are pinned as negative arms.
//
// Known ambiguity, stated rather than discovered later: a path that itself
// reads "...@word:..." after the first ":" ("sftp:host:/a@b:c",
// "rclone:gdrive:edwin@example.com:sub") is indistinguishable from a
// credential by shape alone and is over-starred. Pathological, untested,
// documented — the same posture agent-os-zzhs took for "/" in a password
// combined with a later "@" in the path. Over-redaction is the safe side.

// opaqueSchemeRe matches "scheme:" at the start of a value. Deliberately NOT
// url.Parse: the fallback needs to work on values url.Parse rejects too, and
// the scheme is kept byte-for-byte (url.Parse lower-cases it).
var opaqueSchemeRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.\-]*):`)

// splitOpaque returns the scheme and the opaque part of a "scheme:opaque"
// value, or ok=false when the value is not that shape or the opaque part
// starts with "//" (an authority, which the URL branches own).
func splitOpaque(raw string) (scheme, opaque string, ok bool) {
	m := opaqueSchemeRe.FindStringSubmatch(raw)
	if m == nil {
		return "", "", false
	}
	opaque = raw[len(m[0]):]
	if strings.HasPrefix(opaque, "//") {
		return "", "", false
	}
	return m[1], opaque, true
}

// opaqueCredentialBoundary reports the index of the "@" that ends an inline
// "user:pw@" credential in an opaque part, per the bounded per-scheme grammar,
// or ok=false when the part carries none.
//
//   - sftp, rclone: the LAST "@" whose right side reads "host:" (`[^/:@]+:`)
//     and whose left side contains a ":". The right-side test is the
//     discriminator: in "sftp:user@host:/path" the left side has no ":", in
//     "rclone:gdrive:edwin@example.com" the right side has no ":", and in
//     "sftp:host:/path/with@sign" neither holds. Taking the LAST qualifying
//     "@" lets a password contain "@"; not excluding "/" from the left side
//     lets it contain "/" (an AWS secret does, ~46% of the time).
//   - s3: the endpoint is everything before the first "/", and a
//     well-formed endpoint (host[:port]) never contains "@". So the last "@"
//     inside that segment ends a userinfo, and a ":" before it means
//     "KEY:SECRET". A "/" in the secret is NOT covered here (the documented
//     "s3:https://..." form, which is, takes the URL branch).
//
// A userinfo with no non-empty component (":@") is not a credential, for the
// reason spliceUserinfo gives.
func opaqueCredentialBoundary(scheme, opaque string) (at int, ok bool) {
	switch strings.ToLower(scheme) {
	case "sftp", "rclone":
		for i := strings.LastIndex(opaque, "@"); i >= 0; i = strings.LastIndex(opaque[:i], "@") {
			right := opaque[i+1:]
			colon := strings.IndexAny(right, "/:@")
			if colon <= 0 || right[colon] != ':' {
				continue
			}
			if strings.Contains(opaque[:i], ":") {
				at = i
				ok = true
				break
			}
		}
	case "s3":
		seg := opaque
		if slash := strings.Index(seg, "/"); slash >= 0 {
			seg = seg[:slash]
		}
		i := strings.LastIndex(seg, "@")
		if i < 0 || !strings.Contains(seg[:i], ":") || i == len(seg)-1 {
			return 0, false
		}
		at, ok = i, true
	}
	if !ok {
		return 0, false
	}
	if user, password, _ := strings.Cut(opaque[:at], ":"); user == "" && password == "" {
		return 0, false
	}
	return at, true
}

// redactOpaqueCredential stars an inline credential in an opaque repository
// form, or reports ok=false and leaves the decision to the URL branches.
func redactOpaqueCredential(raw string) (string, bool) {
	scheme, opaque, ok := splitOpaque(raw)
	if !ok {
		return raw, false
	}
	prefix := raw[:len(scheme)+1]
	if at, ok := opaqueCredentialBoundary(scheme, opaque); ok {
		return prefix + userinfoPlaceholder + "@" + opaque[at+1:], true
	}
	if strings.EqualFold(scheme, "rclone") {
		if redacted, changed := redactRcloneConnectionString(opaque); changed {
			return prefix + redacted, true
		}
	}
	return raw, false
}

// rcloneParam is one "name=value" parameter of an rclone connection string,
// with the byte span of its raw value (quotes included) inside the spec.
type rcloneParam struct {
	name       string
	value      string // unquoted
	start, end int    // raw value span, [start, end)
}

// parseRcloneConnectionString parses the remote spec of an rclone connection
// string — "remote,param=value,...:path" or ":backend,param=value,...:path"
// (rclone docs, "Connection strings") — and returns its parameters, or
// ok=false when the value has no parameters at all (a plain "remote:path").
//
// Quoting follows the documented rule: a value containing ":" or "," is
// wrapped in " or ', and a quote inside is doubled. Quotes are only special
// at the START of a value, as in rclone's own parser. A bare "name" with no
// "=" is a flag (rclone reads it as "=true") and carries nothing.
func parseRcloneConnectionString(opaque string) (params []rcloneParam, ok bool) {
	i := 0
	if strings.HasPrefix(opaque, ":") {
		i = 1
	}
	// The remote or backend name, up to the first "," or ":".
	nameEnd := strings.IndexAny(opaque[i:], ",:")
	if nameEnd < 0 || opaque[i+nameEnd] != ',' {
		return nil, false
	}
	i += nameEnd + 1

	for i < len(opaque) {
		eq := strings.IndexAny(opaque[i:], "=,:")
		if eq < 0 {
			break
		}
		if opaque[i+eq] != '=' {
			// A bare flag. "," moves on; ":" ends the spec.
			if opaque[i+eq] == ':' {
				break
			}
			i += eq + 1
			continue
		}
		name := opaque[i : i+eq]
		i += eq + 1
		start := i
		var value string
		if i < len(opaque) && (opaque[i] == '"' || opaque[i] == '\'') {
			q := opaque[i]
			i++
			var b strings.Builder
			for i < len(opaque) {
				if opaque[i] == q {
					if i+1 < len(opaque) && opaque[i+1] == q {
						b.WriteByte(q)
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(opaque[i])
				i++
			}
			value = b.String()
		} else {
			end := strings.IndexAny(opaque[i:], ",:")
			if end < 0 {
				end = len(opaque) - i
			}
			value = opaque[i : i+end]
			i += end
		}
		params = append(params, rcloneParam{name: name, value: value, start: start, end: i})
		if i >= len(opaque) || opaque[i] == ':' {
			break
		}
		i++ // the ","
	}
	return params, true
}

// rcloneSecretParamWords are the last "_"-separated words of an rclone option
// name that hold a secret. Enumerated from `rclone config providers` (rclone
// v1.60.1): every IsPassword option (pass, password, password2, secret,
// api_password, file_password, folder_password, key_file_pass, library_key,
// plex_password) plus the names that are plainly secrets without that flag
// (token, access_token, refresh_token, permanent_token, plex_token,
// auth_token, bearer_token, session_token, client_secret,
// application_credential_secret, secret_access_key, private_access_key, key,
// key_pem, sse_customer_key, service_account_credentials). Names ending in
// _id, _file, _url, _command, _expiry, _md5 and _agent fall outside by
// construction; the two exact names below do not fit the word rule.
var rcloneSecretParamWords = map[string]struct{}{
	"pass": {}, "password": {}, "password2": {}, "passphrase": {},
	"token": {}, "secret": {}, "key": {}, "pem": {}, "credentials": {},
}

var rcloneSecretParamExact = map[string]struct{}{
	"sas_url": {}, "sse_customer_key_base64": {},
}

func isRcloneSecretParam(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if _, ok := rcloneSecretParamExact[name]; ok {
		return true
	}
	last := name
	if i := strings.LastIndex(name, "_"); i >= 0 {
		last = name[i+1:]
	}
	_, ok := rcloneSecretParamWords[last]
	return ok
}

// rcloneInlineSecret returns the first connection-string parameter carrying a
// non-empty secret, or "" when there is none. An EMPTY value hides nothing
// ("pass=" means ssh-agent, per rclone's sftp docs) and marking it would be
// the false signal the zzhs empty-userinfo rule exists to avoid.
func rcloneInlineSecret(opaque string) string {
	params, ok := parseRcloneConnectionString(opaque)
	if !ok {
		return ""
	}
	for _, p := range params {
		if p.value != "" && isRcloneSecretParam(p.name) {
			return p.name
		}
	}
	return ""
}

// redactRcloneConnectionString stars the value of every secret-bearing
// parameter, keeping every other byte, or reports changed=false.
func redactRcloneConnectionString(opaque string) (string, bool) {
	params, ok := parseRcloneConnectionString(opaque)
	if !ok {
		return opaque, false
	}
	var b strings.Builder
	last, changed := 0, false
	for _, p := range params {
		if p.value == "" || !isRcloneSecretParam(p.name) {
			continue
		}
		b.WriteString(opaque[last:p.start])
		b.WriteString(userinfoPlaceholder)
		last = p.end
		changed = true
	}
	if !changed {
		return opaque, false
	}
	b.WriteString(opaque[last:])
	return b.String(), true
}

// ValidateRepositoryForm refuses a restic repository value that carries an
// inline credential in a place the backend does not read it, with a message
// naming the form it does support. It never echoes the value.
//
// It reads the same grammar table as redactOpaqueCredential, so for the opaque
// shapes "refused at save" and "starred on read" are one decision — which is
// what makes serving a legacy row starred safe (see the section comment). A
// nil error means "not a form this table knows to be wrong", not "valid": the
// URL forms are left to the backend, except that an sftp:// password is
// refused because restic's sftp parser never reads one.
func ValidateRepositoryForm(raw string) error {
	s := strings.TrimSpace(raw)
	if scheme, opaque, ok := splitOpaque(s); ok {
		switch strings.ToLower(scheme) {
		case "sftp":
			if _, found := opaqueCredentialBoundary(scheme, opaque); found {
				return errors.New(sftpPasswordRefused)
			}
		case "s3":
			if _, found := opaqueCredentialBoundary(scheme, opaque); found {
				return errors.New("s3: credentials are not accepted in the repository field; use s3:host/bucket or s3:https://host/bucket and supply AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY in the backup environment")
			}
		case "rclone":
			if _, found := opaqueCredentialBoundary(scheme, opaque); found {
				return errors.New("rclone: credentials are not accepted in the repository field; define the remote in rclone.conf and use rclone:remote:path")
			}
			if name := rcloneInlineSecret(opaque); name != "" {
				return fmt.Errorf("rclone: connection-string parameter %q carries a credential, which is not accepted in the repository field; define the remote in rclone.conf and use rclone:remote:path", name)
			}
		}
		return nil
	}
	if u, err := url.Parse(s); err == nil && strings.EqualFold(u.Scheme, "sftp") && u.User != nil {
		if password, _ := u.User.Password(); password != "" {
			return errors.New(sftpPasswordRefused)
		}
	}
	return nil
}

const sftpPasswordRefused = "sftp: a password is not accepted in the repository field; use sftp:user@host:/path or sftp://user@host:port/path with key authentication"

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
