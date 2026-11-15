package services

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The stack's secret list read from its files holds a value as it is
// written, but a container gets it as compose resolved it: a ${X:-default},
// a $$ escape, a partial pre${X}, and every value of an env_file the walk
// could not read. A secret the locked compose view hides only by its line or
// URL pass (`command: app --password=x`, a URL credential under any name) is
// not in the file list at all (agent-os-rbmi round-2 review F2). So the
// views that mask by value for a locked session (container logs, inspect)
// also mask what the stack's containers really hold.

// runtimeSecretLookupTimeout bounds one lookup: the container list and every
// inspect it needs.
const runtimeSecretLookupTimeout = 5 * time.Second

// runtimeSecretEntry is what one container holds: its Config does not change
// after create (a restart keeps it, a recreate is a new ID), so a container
// is inspected once per ID and creation time.
type runtimeSecretEntry struct {
	project string
	created int64
	values  []string
}

// runtimeSecretCache is what RuntimeSecretValues learned, by container ID.
type runtimeSecretCache struct {
	mu   sync.Mutex
	byID map[string]runtimeSecretEntry
}

// lookup is the cached values of container id, when cached for this
// creation time.
func (c *runtimeSecretCache) lookup(id string, created int64) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byID[id]
	if !ok || e.created != created {
		return nil, false
	}
	return e.values, true
}

func (c *runtimeSecretCache) store(id string, e runtimeSecretEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byID == nil {
		c.byID = map[string]runtimeSecretEntry{}
	}
	c.byID[id] = e
}

// keepOnly drops project's containers that are not in live, after a list
// that succeeded.
func (c *runtimeSecretCache) keepOnly(project string, live map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, e := range c.byID {
		if e.project == project && !live[id] {
			delete(c.byID, id)
		}
	}
}

// project is every value cached for project's containers.
func (c *runtimeSecretCache) project(project string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, e := range c.byID {
		if e.project == project {
			out = append(out, e.values...)
		}
	}
	return out
}

// RuntimeSecretValues returns the secret values the stack's containers hold
// (containerSecretValues), stopped ones too, since compose logs prints their
// lines. On an error it returns what is cached for the stack's containers
// with the error, so a caller that unions it with the file list never masks
// less than before.
func (s *DockerService) RuntimeSecretValues(ctx context.Context, stack models.Stack) ([]string, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}
	if stack.ProjectName == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, runtimeSecretLookupTimeout)
	defer cancel()
	api := s.updateAPI()
	list, err := api.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+stack.ProjectName)),
	})
	if err != nil {
		return s.runtimeSecrets.project(stack.ProjectName), err
	}
	var out []string
	var firstErr error
	live := map[string]bool{}
	for _, c := range list {
		live[c.ID] = true
		if values, ok := s.runtimeSecrets.lookup(c.ID, c.Created); ok {
			out = append(out, values...)
			continue
		}
		inspect, err := api.ContainerInspect(ctx, c.ID)
		if cerrdefs.IsNotFound(err) {
			// Removed since the list: it prints nothing more.
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		values := containerSecretValues(inspect)
		s.runtimeSecrets.store(c.ID, runtimeSecretEntry{project: stack.ProjectName, created: c.Created, values: values})
		out = append(out, values...)
	}
	s.runtimeSecrets.keepOnly(stack.ProjectName, live)
	return out, firstErr
}

// LockedSecretValues is the list a view masks with for a locked session when
// it serves what the stack's containers print or hold (logs, inspect): the
// file list (ComposeSecretValues) and RuntimeSecretValues, through the same
// filter (secretSet). A container lookup that fails is warned about once and
// masks with the file list plus what is cached, never with less.
func (s *DockerService) LockedSecretValues(ctx context.Context, stack models.Stack) ([]string, error) {
	files, err := s.ComposeSecretValues(stack)
	if err != nil {
		return nil, err
	}
	runtime, err := s.RuntimeSecretValues(ctx, stack)
	if err != nil {
		warnSecretSourceOnce(stack, "containers", "Masking without the values of some of a stack's containers: they could not be inspected",
			"error", err)
	}
	var set secretSet
	for _, v := range files {
		set.add(v)
	}
	for _, v := range runtime {
		set.add(v)
	}
	return set.sorted(), nil
}

// containerSecretValues is what one container holds that a locked view
// hides: the value of every env entry the env views mask (IsSensitiveEnvVar),
// trimmed too; and every span the masker hides in an env value (a URL
// credential under any name, a KEY=value inside it) or in the container's
// argv, Cmd, Entrypoint, Args and Healthcheck.Test, through MaskArgs, its
// after-flag rule included (maskedSpans). Labels are not read.
func containerSecretValues(inspect container.InspectResponse) []string {
	var out []string
	addSpans := func(orig, masked string) {
		out = append(out, spanValues(orig, masked, spanValuesDepth)...)
	}
	argv := func(args []string) {
		masked := MaskArgs(args, nil)
		for i := range args {
			addSpans(args[i], masked[i])
		}
	}
	if inspect.ContainerJSONBase != nil {
		argv(append([]string{inspect.Path}, inspect.Args...))
	}
	if cfg := inspect.Config; cfg != nil {
		for _, kv := range cfg.Env {
			k, v, _ := strings.Cut(kv, "=")
			if IsSensitiveEnvVar(k, v) {
				out = append(out, v, strings.TrimSpace(v))
			}
			masked, _ := MaskSecrets(v, "", nil)
			addSpans(v, masked)
		}
		argv(cfg.Cmd)
		argv(cfg.Entrypoint)
		if cfg.Healthcheck != nil {
			argv(cfg.Healthcheck.Test)
		}
	}
	return out
}

// spanValues is the values in what the masker hid of orig (maskedSpans):
// each span; the part after its first ":", since a URL's userinfo is starred
// whole and the password alone is what an app prints; the span's first word
// cut at the first shell separator (shellSeparators: `x; exec app`,
// `x&& ..`, `x| ..`, `(.. x)`), where a shell ends the value; the password
// of every word that carries a URL credential (HasURLUserinfo), which a
// longer span holds whole (rv-dm-rbmi2 round 3, R3-1); and, for a span that
// runs on past a space (the masker hides the rest of a `PASSWORD=x exec app`
// line), its first word, where the inline KEY=value rule ends a value, and
// the values the masker finds in the span itself, down to depth levels:
// each sensitive KEY=value on such a line hides the rest of it again.
func spanValues(orig, masked string, depth int) []string {
	var out []string
	// A span cut inside a quoted value keeps one of its quotes ("x from
	// {"k":"x"}): the value is printed without it (agent-os-w4p2).
	add := func(v string) {
		out = append(out, v)
		if t := strings.Trim(v, `"'`); t != v {
			out = append(out, t)
		}
	}
	for _, sp := range maskedSpans(orig, masked) {
		add(sp)
		if _, pw, ok := strings.Cut(sp, ":"); ok {
			add(pw)
		}
		words := strings.Fields(sp)
		if len(words) > 0 {
			if i := strings.IndexAny(words[0], shellSeparators); i > 0 {
				out = append(out, words[0][:i])
			}
		}
		for _, w := range words {
			out = append(out, urlUserinfoPassword(w)...)
		}
		if len(words) > 1 {
			out = append(out, words[0])
		}
		// What the masker hides inside the span itself: a compact JSON
		// field is one word (agent-os-w4p2). Only on text shorter than orig,
		// so it always ends.
		if depth > 0 && len(sp) < len(orig) {
			inner, _ := MaskSecrets(sp, "", nil)
			out = append(out, spanValues(sp, inner, depth-1)...)
		}
	}
	return out
}

// shellSeparators end a value in a shell line: a command list, a pipe, the
// end of a subshell.
const shellSeparators = ";&|)"

// urlUserinfoPassword is the password of word when it carries a URL
// credential, as an app prints it (decoded); nil otherwise.
func urlUserinfoPassword(word string) []string {
	if !HasURLUserinfo(word) {
		return nil
	}
	u, err := url.Parse(strings.TrimRight(strings.Trim(word, `"'`), shellSeparators))
	if err != nil {
		// HasURLUserinfo found one this parse can't read: the whole word is
		// still a needle, as part of its span.
		return nil
	}
	if u.User == nil {
		// An opaque form (sftp:user:pw@host): the span's ":" split above
		// already holds its password.
		return nil
	}
	pw, ok := u.User.Password()
	if !ok || pw == "" {
		return nil
	}
	out := []string{pw}
	// The password as written too: an app can print it percent-encoded
	// (agent-os-w4p2, R4-1).
	if raw := rawUserinfoPassword(word); raw != "" && raw != pw {
		out = append(out, raw)
	}
	return out
}

// rawUserinfoPassword is the password text of word's first scheme://
// authority as written, before any decoding; "" when there is none.
func rawUserinfoPassword(word string) string {
	_, rest, ok := strings.Cut(word, "://")
	if !ok {
		return ""
	}
	if e := strings.IndexAny(rest, "/?#"); e >= 0 {
		rest = rest[:e]
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return ""
	}
	_, pw, _ := strings.Cut(rest[:at], ":")
	return pw
}

// spanValuesDepth bounds spanValues' look into a span: one level per
// sensitive KEY=value on one argv item or env value, each a masker run over
// the rest of it.
const spanValuesDepth = 8

// maskedSpans is what a masker hid: the text of orig that masked shows as
// secretMask. It is how containerSecretValues reads the masker's own rules
// as values, so there is no second rule to drift from the first.
//
// The pieces of masked between masks are lined up in orig. With one mask
// that is exact; with more, a piece could sit inside the span before it, so
// both the leftmost and the rightmost line-up are returned, which only ever
// masks more. When a piece between two masks is empty, or nothing lines up,
// the whole of orig is returned. When orig already holds secretMask, the
// masks can't be told apart from it: the whole of orig is returned, and each
// piece of it around secretMask, so a value that holds the text is hidden
// by the rest of it.
func maskedSpans(orig, masked string) []string {
	if orig == masked {
		return nil
	}
	whole := []string{orig}
	if strings.Contains(orig, secretMask) {
		for _, piece := range strings.Split(orig, secretMask) {
			if piece != "" {
				whole = append(whole, piece)
			}
		}
		return whole
	}
	segs := strings.Split(masked, secretMask)
	if len(segs) < 2 {
		return whole
	}
	first, last := segs[0], segs[len(segs)-1]
	if len(first)+len(last) >= len(orig) || !strings.HasPrefix(orig, first) || !strings.HasSuffix(orig, last) {
		return whole
	}
	lo, hi := len(first), len(orig)-len(last)
	middle := segs[1 : len(segs)-1]
	if len(middle) == 0 {
		return []string{orig[lo:hi]}
	}
	for _, m := range middle {
		if m == "" {
			return whole
		}
	}
	left, okL := alignSpansLeft(orig[lo:hi], middle)
	right, okR := alignSpansRight(orig[lo:hi], middle)
	if !okL || !okR {
		return whole
	}
	return append(left, right...)
}

// alignSpansLeft splits s at each of middle in turn, each found as early as
// it can be with a non-empty span before it.
func alignSpansLeft(s string, middle []string) ([]string, bool) {
	var spans []string
	pos := 0
	for _, m := range middle {
		if pos+1 > len(s) {
			return nil, false
		}
		i := strings.Index(s[pos+1:], m)
		if i < 0 {
			return nil, false
		}
		start := pos + 1 + i
		spans = append(spans, s[pos:start])
		pos = start + len(m)
	}
	if pos >= len(s) {
		return nil, false
	}
	return append(spans, s[pos:]), true
}

// alignSpansRight is alignSpansLeft from the end: each of middle found as
// late as it can be with a non-empty span after it.
func alignSpansRight(s string, middle []string) ([]string, bool) {
	var spans []string
	end := len(s)
	for k := len(middle) - 1; k >= 0; k-- {
		m := middle[k]
		if end-1 < 0 {
			return nil, false
		}
		i := strings.LastIndex(s[:end-1], m)
		if i < 0 {
			return nil, false
		}
		spans = append(spans, s[i+len(m):end])
		end = i
	}
	if end <= 0 {
		return nil, false
	}
	return append(spans, s[:end]), true
}
