package handlers

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

type EnvHandler struct {
	db        *database.DB
	config    *config.Config
	actionLog *services.ActionLogger
	// opLock serialises file writes with every other operation on the stack
	// (agent-os-a1ye.4). Set by SetOperationLock; nil (tests) means no locking.
	opLock *services.OperationLock
}

type EnvRequest struct {
	Entries []EnvEntry `json:"entries"`
	Raw     string     `json:"raw"`
}

// envCreateBeforeRecordHook is TEST-ONLY and nil in production. It runs in
// Create after the file is on disk and before the stack row records it, which
// is the window agent-os-z91e.23's tests park a concurrent scan in.
var envCreateBeforeRecordHook func()

// envCreateBeforeLockHook is TEST-ONLY and nil in production. It runs in
// Create after the stack row is read and before the lock is taken, the window
// agent-os-0j8a's test retargets the stack's folder link in.
var envCreateBeforeLockHook func()

func NewEnvHandler(db *database.DB, config *config.Config) *EnvHandler {
	return &EnvHandler{
		db:        db,
		config:    config,
		actionLog: services.NewActionLogger(db),
	}
}

// SetOperationLock installs the per-stack operation lock shared with the
// lifecycle, update and backup paths. main.go passes the same instance.
func (h *EnvHandler) SetOperationLock(l *services.OperationLock) {
	h.opLock = l
}

func (h *EnvHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/:id/env", h.Get)
	group.PUT("/:id/env", h.Put)
	group.POST("/:id/env", h.Create)
}

func (h *EnvHandler) Get(c *gin.Context) {
	id := c.Param("id")

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
		return
	}

	// A stack with no env file is ordinary configuration, not a client mistake:
	// most stacks have none, and the frontend asks this of every stack whose
	// Editor tab is opened. Answering 200 with an explicit discriminator rather
	// than a 404 is what keeps that from painting a red entry in the browser
	// console (agent-os-bt5y) — the console line is produced by the STATUS, so
	// agent-os-hjmf's demotion of the log level could not reach it.
	//
	// The env-file fields — filename, entries, raw, locked — are absent rather
	// than zero-valued, mirroring the shape agent-os-x40a and agent-os-4a4a
	// settled on for GET /api/v1/git: a `filename: ""` with `entries: []` would
	// be indistinguishable from an empty file that really exists, which is a
	// state this endpoint genuinely has and answers separately.
	//
	// "Configured env file missing from disk" below keeps its 404 and is NOT
	// this state: the DB and the filesystem disagreeing is something that went
	// wrong, and fusing the two is the regression this change must not make.
	// The write path keeps its 404 too — a PUT naming env content for a stack
	// with no env file cannot be fulfilled, and there is a separate verb for
	// it (Create, POST /stacks/:id/env).
	if stack.EnvFile == "" {
		c.JSON(http.StatusOK, gin.H{"hasEnvFile": false})
		return
	}

	envPath := filepath.Join(stack.Directory, stack.EnvFile)

	if err := validateStackPath(envPath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid env file path",
		))
		return
	}

	//nolint:gosec // envPath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
	content, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"Env file not found on disk",
			))
			return
		}
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "READ_ERROR", "Failed to read env file", err))
		return
	}

	entries := h.parseEnvFile(string(content))

	resp := EnvResponse{
		HasEnvFile: true,
		Filename:   stack.EnvFile,
		Entries:    entries,
		Raw:        string(content),
		Locked:     !envUnlocked(c),
	}
	if resp.Locked {
		redactEnvResponse(&resp)
	}

	c.JSON(http.StatusOK, resp)
}

// envUnlocked reports whether this request carried a valid env-unlock token, as
// determined upstream by middleware.EnvUnlock. Reading the context (rather than
// the header) keeps token validation in one place and makes a route that was
// never wired through the middleware read false — i.e. fail closed.
func envUnlocked(c *gin.Context) bool {
	return c.GetBool(middleware.CtxEnvUnlocked)
}

// redactEnvResponse strips the secrets from an env payload for a session that
// has not re-entered its password.
//
// It blanks sensitive entry values as well as dropping Raw, because Raw is not
// the only copy: parseEnvFile puts the same plaintext into every
// entries[].Value, and Sensitive is only a client-side masking hint. Dropping
// Raw alone would leave a caller refused the raw file free to read
// entries[].value instead.
//
// Keys, line numbers, comments and non-sensitive values survive, so a locked
// session can still see the shape of the file (which variables a stack defines,
// what its TZ is) without seeing any secret.
func redactEnvResponse(resp *EnvResponse) {
	resp.Raw = ""
	for i := range resp.Entries {
		if resp.Entries[i].Sensitive {
			// The quote fields can hold part of the value: a line that falls
			// back to the unquoted cut leaves its tail in InlineComment.
			resp.Entries[i].Value = ""
			resp.Entries[i].Quote = ""
			resp.Entries[i].InlineComment = ""
		}
	}
}

func (h *EnvHandler) Put(c *gin.Context) {
	id := c.Param("id")

	// The write path is gated as well as the read path, and not for symmetry: a
	// locked session was handed blanked sensitive values by Get, so letting it
	// PUT what it holds would overwrite every secret in the file with "". The
	// gate is what makes the redaction safe rather than destructive.
	if !envUnlocked(c) {
		c.JSON(http.StatusForbidden, models.NewAppError(
			http.StatusForbidden,
			models.ErrForbidden,
			"Re-enter your password to edit environment variables",
		))
		return
	}

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
		return
	}

	// The write path keeps the 404 that the read path above gave up
	// (agent-os-bt5y): a PUT naming env content for a stack with no env file
	// CANNOT be fulfilled, so a 200 here would report success on a write that
	// never happened. It is also a client mistake in its own right — POST to
	// Create is the verb for this — which is why it is out of the class bt5y
	// changed. So the marker stays: this is still a 404 that must not WARN
	// (agent-os-hjmf). It is marked per-site rather than by adding
	// models.ErrNotFound to respond.go's routineErrorCodes, because the SAME
	// code answers genuine client errors elsewhere, such as "Env file not found
	// on disk" in Get, which must keep warning. (An unknown stack answers
	// STACK_NOT_FOUND, via handleDBError above.)
	// prfj_routine_404_log_test.go's TestHandleError_MarksOnlyListedCodes pins
	// ErrNotFound out of that list.
	if stack.EnvFile == "" {
		middleware.MarkRoutineOutcome(c)
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"No env file associated with this stack",
		))
		return
	}

	envPath := filepath.Join(stack.Directory, stack.EnvFile)

	// The body is read before the lock, so a slow client cannot hold the stack.
	var req EnvRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	// Held across the path check, the write and its round-trip verification.
	// The path is checked under the lock (agent-os-0j8a): a git pull holds the
	// same lock, and one that retargeted a link between an earlier check and
	// the write had the write follow a link nobody checked.
	releaseLock, ok := lockStacksSharingFiles(c, h.opLock, h.db, stack, services.OpKindEnv, envPath)
	if !ok {
		return
	}
	defer releaseLock()

	if err := validateStackPath(envPath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid env file path",
		))
		return
	}

	var content string
	if req.Raw != "" {
		content = req.Raw
	} else if len(req.Entries) > 0 {
		// Validate before serialising: reject entries with a non-comment, non-blank
		// key that is empty — they would produce a corrupt "=value" line (#15).
		if err := validateEnvEntries(req.Entries); err != nil {
			renderResult(c, truth.Failed("env validation failed: "+err.Error(), err))
			return
		}
		content = serializeEnvFile(req.Entries)
	} else {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Must provide either 'entries' or 'raw'",
		))
		return
	}

	if err := writeEnvFileAtomic(envPath, content); err != nil {
		renderResult(c, truth.Failed("failed to write env file", err))
		return
	}

	// Round-trip verify: re-read the file and confirm the bytes were persisted
	// faithfully (#15 file-save contract).
	if ar := verifyEnvRoundTrip(envPath, content); ar != nil {
		renderResult(c, *ar)
		return
	}

	h.logAction(c, id, "update_env", "Updated env file: "+stack.EnvFile)

	renderResult(c, truth.Success("env file saved",
		truth.KV("filename", stack.EnvFile),
	))
}

// Create handles POST /api/v1/stacks/:id/env — creates the stack's .env file.
// Returns no_change (409) if the file already exists, success once the file is
// verified to exist on disk (#16).
func (h *EnvHandler) Create(c *gin.Context) {
	id := c.Param("id")

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
		return
	}

	// Determine the env file path: use the configured one when set, otherwise
	// default to ".env" in the stack directory.
	envFileName := stack.EnvFile
	if envFileName == "" {
		envFileName = ".env"
	}
	envPath := filepath.Join(stack.Directory, envFileName)

	if envCreateBeforeLockHook != nil {
		envCreateBeforeLockHook()
	}

	// Held from the path check to after the create and its verification, so
	// the check, the existence check and the write see the same directory
	// (agent-os-0j8a: checked before the lock, a folder link retargeted in
	// between was followed unchecked).
	releaseLock, ok := lockStacksSharingFiles(c, h.opLock, h.db, stack, services.OpKindEnv, envPath)
	if !ok {
		return
	}
	defer releaseLock()

	if err := validateStackPath(envPath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid env file path",
		))
		return
	}

	// Refuse if the file already exists.
	if _, statErr := os.Stat(envPath); statErr == nil { //geterrors:ignore existence probe: a stat error that is not NotExist reads as "not there" and the create below is the real gate, which fails loudly
		renderResultWithStatus(c, http.StatusConflict, truth.NoChange("env file already exists",
			truth.KV("filename", envFileName),
		))
		return
	}

	// Accept optional initial content from the request body.
	var req struct {
		Content string `json:"content"`
		Raw     string `json:"raw"`
	}
	// agent-os-40bp: an ABSENT body is intentional and creates an empty file; a
	// MALFORMED one is a client error and must write nothing. Discarding the bind
	// error conflated them, so `{"content": 123}` and `{not json` both bound to the
	// zero value and this handler created an EMPTY file and answered success — the
	// operator's intended initial content silently dropped. (It could never
	// overwrite anything: the os.Stat guard above answers 409 when the file already
	// exists, which is why this is a dropped-input bug and not a data-loss one.)
	//
	// io.EOF is the discriminator, and it has to be, because the two cases are
	// indistinguishable by their RESULT — both leave req at its zero value.
	// MEASURED, and two of these are easy to get wrong:
	//   - an absent body, an empty body and a WHITESPACE-ONLY body all yield EOF,
	//     so all three still create an empty file;
	//   - `{}` and `null` are VALID JSON that bind cleanly with content "", so they
	//     must also still create an empty file. Do not "tighten" this to a check on
	//     whether content is empty: that would reject valid JSON while still
	//     accepting every malformed body this guard exists to catch.
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	content := req.Content
	if content == "" {
		content = req.Raw
	}

	if err := writeEnvFileAtomic(envPath, content); err != nil {
		renderResult(c, truth.Failed("failed to create env file", err))
		return
	}

	// Verify the file was actually created on disk.
	if _, statErr := os.Stat(envPath); statErr != nil {
		renderResult(c, truth.Failed("env file was not created on disk", statErr,
			truth.KV("filename", envFileName),
		))
		return
	}

	if envCreateBeforeRecordHook != nil {
		envCreateBeforeRecordHook()
	}

	// Record the env file if the stack had none configured. One column only:
	// the row read at the top of this request may be stale by now, because the
	// scanner does not take the OperationLock held here (agent-os-z91e.23).
	// Zero rows updated means the scanner pruned the row meanwhile; the file
	// still exists, and the watcher's rescan records it if the stack is real.
	if stack.EnvFile == "" {
		if _, err := h.db.SetStackEnvFileIfUnset(id, envFileName); err != nil {
			// Non-fatal: file exists, DB update failed. Surface as partial.
			renderResult(c, truth.Partial("env file created but DB not updated",
				truth.KV("filename", envFileName),
				truth.KV("dbError", err.Error()),
			))
			return
		}
	}

	h.logAction(c, id, "create_env", "Created env file: "+envFileName)

	renderResultWithStatus(c, http.StatusCreated, truth.Success("env file created",
		truth.KV("filename", envFileName),
	))
}

// validateEnvEntries returns an error when an entry would not serialise back
// as the one line it stands for: a non-blank entry with an empty key would
// produce a corrupt "=value" line (finding #15), and a comment row must be one
// parseEnvFile itself would produce (validateEnvCommentRow). Errors name the
// index or key, never a value, which
// may be a secret: a failure here is a logged 500 on the stack env routes.
func validateEnvEntries(entries []EnvEntry) error {
	for i, e := range entries {
		if e.Comment {
			if err := validateEnvCommentRow(e); err != nil {
				return fmt.Errorf("entry at index %d %w", i, err)
			}
			continue
		}
		// A blank entry (key=="", value=="") is a blank line — allowed.
		if e.Key == "" && e.Value == "" {
			continue
		}
		// A non-blank value with an empty key would produce "=value".
		if e.Key == "" && e.Value != "" {
			return fmt.Errorf("entry at index %d has an empty key and a non-empty value; this would produce a corrupt env line", i)
		}
		// A literal newline (or carriage return) in a key or value would split
		// the entry across multiple lines on serialisation, silently corrupting
		// the file. The round-trip check can't distinguish this from intent, so
		// reject it up front (finding B4).
		if strings.ContainsAny(e.Key, "\r\n") {
			return fmt.Errorf("entry at index %d has a key containing a newline; this would corrupt the env file", i)
		}
		if strings.ContainsAny(e.Value, "\r\n") {
			return fmt.Errorf("entry at index %d (key %q) has a value containing a newline; this would corrupt the env file", i, e.Key)
		}
		if err := validateEnvQuoting(e); err != nil {
			return fmt.Errorf("entry at index %d (key %q) %w", i, e.Key, err)
		}
	}
	return nil
}

// validateEnvCommentRow accepts exactly the comment rows parseEnvFile produces
// (agent-os-2jq6). serializeEnvFile writes a comment row's value verbatim and
// drops its key, so unchecked a row valued "EVIL=1" or "# x\nEVIL=1" added a
// variable compose reads. A read-only variable line (bare KEY, "KEY: value")
// is a comment row carrying its compose key, and must carry that key: the
// locked view masks by it (agent-os-n43b).
func validateEnvCommentRow(e EnvEntry) error {
	if e.Key == "" && e.Value == "" {
		return nil // serialised as a blank line
	}
	// Keeps a comment row one clean line, the rule validateEnvEntries applies
	// to values. The round trip
	// alone would pass a mid-line "\r" (bufio's ScanLines drops only a trailing
	// one), and compose reads it as a space, so it was never an injection.
	// An unedited file with a mid-line CR in a comment can no longer be saved
	// from the table.
	if strings.ContainsAny(e.Value, "\r\n") {
		return errors.New("is a comment containing a newline; this would corrupt the env file")
	}
	// Ask the parser GET uses, so this accepts exactly the rows it produces.
	back := (&EnvHandler{}).parseEnvFile(serializeEnvFile([]EnvEntry{e}))
	if len(back) != 1 || !back[0].Comment || back[0].Value != e.Value {
		return errors.New("is a comment that would not be written back as the same comment line")
	}
	if back[0].Key != e.Key {
		return errors.New("is a read-only line whose key does not match its text; reload the env file")
	}
	return nil
}

// validateEnvQuoting checks the quote fields a client sends back (agent-os-wcrq).
// They are written into the file around and after the value, so unchecked they
// could end the quotes early or add a line. Errors never carry the value.
func validateEnvQuoting(e EnvEntry) error {
	switch e.Quote {
	case "":
	case "'", `"`:
		idx, trailingEscape := closingQuote(e.Value, e.Quote[0])
		if idx != -1 {
			// compose reads \' and \" as the quote itself (compose-go v2
			// dotenv/parser.go extractVarValue; pinned by
			// TestEnvQuote_ComposeReadsEscapedQuoteWithoutBackslash).
			return fmt.Errorf("has an unescaped %s inside its %s-quoted value, which would end the quotes early; write it as \\%s or use the raw view", e.Quote, e.Quote, e.Quote)
		}
		if trailingEscape {
			return fmt.Errorf("has a value ending in a backslash, which would escape its closing quote; use the raw view")
		}
	default:
		return errors.New("has an unsupported quote; use ', \" or none")
	}

	c := e.InlineComment
	if c == "" {
		return nil
	}
	if strings.ContainsAny(c, "\r\n") {
		return errors.New("has an inline comment containing a newline; this would corrupt the env file")
	}
	// Same whitespace set as splitEnvValue, or an unedited line the parser
	// accepted (a no-break space before "#") would be refused on save.
	body := strings.TrimLeftFunc(c, unicode.IsSpace)
	// After a closing quote any "#" starts a comment; after an unquoted value
	// compose only cuts at " #" (ASCII space), so the "#" must follow one.
	if body == "" || body[0] != '#' || (e.Quote == "" && !strings.HasSuffix(c[:len(c)-len(body)], " ")) {
		return errors.New("has an inline comment that is not a # comment")
	}
	return nil
}

// verifyEnvRoundTrip re-reads envPath and confirms the persisted bytes equal
// the intended content. Returns a Failed ActionResult on mismatch, nil on success.
func verifyEnvRoundTrip(envPath, intended string) *truth.ActionResult {
	//nolint:gosec // callers validate envPath against the configured stacks directories before calling this (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
	persisted, err := os.ReadFile(envPath)
	if err != nil {
		ar := truth.Failed("could not re-read env file for round-trip verification", err)
		return &ar
	}
	if string(persisted) != intended {
		ar := truth.Failed("env file round-trip verification failed: persisted bytes differ from intended content",
			nil,
			truth.KV("expectedLen", len(intended)),
			truth.KV("gotLen", len(persisted)),
		)
		return &ar
	}
	return nil
}

func (h *EnvHandler) parseEnvFile(content string) []EnvEntry {
	// Empty, not nil: an existing but empty env file yields zero iterations
	// below, and a nil slice serialises as `"entries": null`. EnvResponse.Entries
	// is declared to the frontend as a non-nullable array (EnvFilePresent in
	// types/index.ts), which EnvEditor maps over unguarded — a null is a
	// TypeError there. The contract is made true here, where the value is
	// minted, rather than by widening the client type to admit the null.
	entries := []EnvEntry{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineNum++

		trimmedLine := strings.TrimSpace(line)

		if trimmedLine == "" {
			entries = append(entries, EnvEntry{
				Key:     "",
				Value:   "",
				Line:    lineNum,
				Comment: false,
			})
			continue
		}

		if strings.HasPrefix(trimmedLine, "#") {
			entries = append(entries, EnvEntry{
				Key:     "",
				Value:   trimmedLine,
				Line:    lineNum,
				Comment: true,
			})
			continue
		}

		eqIndex := strings.Index(trimmedLine, "=")
		if colonIndex := strings.Index(trimmedLine, ":"); eqIndex == -1 || (colonIndex != -1 && colonIndex < eqIndex) {
			// Not a KEY=value line, yet compose may read it as a variable (a
			// bare KEY, or KEY: value). The table keeps it read-only and writes
			// it back verbatim, since KEY= would turn an inherited value empty,
			// but keys it so the locked view masks it like any other variable.
			entry := EnvEntry{Value: trimmedLine, Line: lineNum, Comment: true}
			if key, ok := composeLineKey(trimmedLine); ok {
				entry.Key = key
				entry.Sensitive = services.IsSensitiveEnvKey(key)
			}
			entries = append(entries, entry)
			continue
		}

		key := strings.TrimSpace(trimmedLine[:eqIndex])
		value, quote, inlineComment := splitEnvValue(strings.TrimSpace(trimmedLine[eqIndex+1:]))

		entries = append(entries, EnvEntry{
			Key:           key,
			Value:         value,
			Line:          lineNum,
			Sensitive:     services.IsSensitiveEnvKey(key),
			Comment:       false,
			Quote:         quote,
			InlineComment: inlineComment,
		})
	}

	return entries
}

var exportPrefix = regexp.MustCompile(`^export\s+`)

// composeLineKey returns the variable compose's dotenv parser reads from a
// line with no "=" before its first ":" (compose-go v2 dotenv/parser.go,
// locateKeyName): an optional "export " prefix, then a name ending at the
// first '=' or ':' or the line's end. ok is false where compose rejects the
// name, such as "a b", so that line stays a plain comment. Inside a name
// compose skips its isSpace set and rejects only ' ', so "A\tB_PASSWORD: x"
// is a variable it reads and has to be keyed here for the locked view to
// mask it.
func composeLineKey(line string) (key string, ok bool) {
	s := line
	if loc := exportPrefix.FindStringIndex(s); loc != nil {
		s = s[loc[1]:]
	}
	if end := strings.IndexAny(s, "=:"); end != -1 {
		s = s[:end]
	}
	key = strings.TrimRightFunc(s, unicode.IsSpace)
	if key == "" {
		return "", false
	}
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune("_.-[]\t\v\f\r\u0085\u00a0", r) {
			return "", false
		}
	}
	return key, true
}

// splitEnvValue splits the text after "=" the way compose's dotenv parser
// reads it (compose-go v2 dotenv/parser.go, extractVarValue): a quoted value
// runs to its first unescaped closing quote and may be followed by a "#"
// comment; an unquoted value ends at the first " #". The pieces concatenate
// back to raw, which is what keeps an unedited save byte-identical.
func splitEnvValue(raw string) (value, quote, inlineComment string) {
	if raw != "" && (raw[0] == '\'' || raw[0] == '"') {
		q := raw[0]
		if end, _ := closingQuote(raw[1:], q); end != -1 {
			rest := raw[1+end+1:]
			if trimmed := strings.TrimLeftFunc(rest, unicode.IsSpace); trimmed == "" || trimmed[0] == '#' {
				return raw[1 : 1+end], string(q), rest
			}
		}
		// Unterminated, or followed by something other than a comment: not a
		// shape compose accepts on one line, so it is kept as unquoted text.
	}
	if i := strings.Index(raw, " #"); i != -1 {
		value = strings.TrimRightFunc(raw[:i], unicode.IsSpace)
		return value, "", raw[len(value):]
	}
	return raw, "", ""
}

// closingQuote returns the index in s of the first quote q not escaped by a
// backslash, or -1. Like compose, a backslash escapes whatever follows it.
// trailingEscape reports an unpaired backslash at the end of s, which would
// escape a closing quote written after it.
func closingQuote(s string, q byte) (idx int, trailingEscape bool) {
	escaped := false
	for i := 0; i < len(s); i++ {
		switch {
		case escaped:
			escaped = false
		case s[i] == '\\':
			escaped = true
		case s[i] == q:
			return i, false
		}
	}
	return -1, escaped
}

func serializeEnvFile(entries []EnvEntry) string {
	var buf bytes.Buffer

	for _, entry := range entries {
		if entry.Comment {
			if entry.Key == "" && entry.Value == "" {
				buf.WriteString("\n")
			} else {
				buf.WriteString(entry.Value)
				buf.WriteString("\n")
			}
		} else if entry.Key == "" && entry.Value == "" {
			buf.WriteString("\n")
		} else {
			buf.WriteString(entry.Key)
			buf.WriteString("=")
			buf.WriteString(entry.Quote)
			buf.WriteString(entry.Value)
			buf.WriteString(entry.Quote)
			// Compose reads "KEY= # note" as the value "# note", so an emptied
			// unquoted value loses its comment instead.
			if entry.Quote != "" || entry.Value != "" {
				buf.WriteString(entry.InlineComment)
			}
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

func (h *EnvHandler) logAction(c *gin.Context, stackID, action, detail string) {
	h.actionLog.LogWithRequest(middleware.RequestIDFrom(c), userIDFrom(c), &stackID, action, detail)
}
