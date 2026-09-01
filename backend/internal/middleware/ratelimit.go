package middleware

import (
	"bytes"
	"container/list"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type RateLimiter struct {
	mu         sync.RWMutex
	requests   map[string]*userRequests
	window     time.Duration
	maxReqs    int
	maxEntries int

	// accessOrder tracks LRU order: Front() is the least-recently-used key,
	// Back() is the most recently used. accessIndex maps a key to its node so
	// check() can refresh or evictLRU can pop in O(1) instead of scanning.
	accessOrder *list.List
	accessIndex map[string]*list.Element
}

type userRequests struct {
	timestamps []time.Time
}

var (
	ipRegex   = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	// loginKeyRegex matches the account half of a composite login key: the
	// normalised username charset, or the sentinel used for anything that could
	// not be a real account. Deliberately narrower than "any string" so the
	// composite form is a third accepted shape, not an escape hatch.
	loginKeyRegex = regexp.MustCompile(`^([a-z0-9_-]{3,50}|` + regexp.QuoteMeta(loginKeyUnknownAccount) + `)$`)
)

const (
	// loginKeyPrefix marks a composite "scope + account" rate limit key, where
	// scope is a client IP or loginKeyAnyIP. The ':' and '|' separators cannot
	// appear in an IPv4 address, a UUID, or a username, so a composite key can
	// never be confused with a plain one.
	loginKeyPrefix = "login:"

	// loginKeyAnyIP is the scope of the account-wide bucket, which counts
	// attempts against one account from every source address at once.
	loginKeyAnyIP = "*"

	// loginKeyUnknownAccount buckets every login attempt whose submitted
	// username could not be a real account (missing, wrong charset, wrong
	// length) under one key per client IP, so unparseable input cannot be used
	// to mint unlimited buckets.
	loginKeyUnknownAccount = "-"

	// loginBodyPeekLimit caps how much of a login request body is buffered to
	// read the username. Real login bodies are well under 1 KB; anything larger
	// is passed through untouched and falls back to IP-only keying rather than
	// being held in memory.
	loginBodyPeekLimit = 8 << 10
)

func NewRateLimiter(window time.Duration, maxReqs int) *RateLimiter {
	rl := &RateLimiter{
		requests:    make(map[string]*userRequests),
		window:      window,
		maxReqs:     maxReqs,
		maxEntries:  10000,
		accessOrder: list.New(),
		accessIndex: make(map[string]*list.Element, 100),
	}

	go rl.cleanup()

	return rl
}

// validateRateLimitKey reports whether key is one of the three shapes this
// package issues: a client IP, a user UUID, or a composite login key
// ("login:<scope>|<account>"). Every shape is validated in full; the composite
// form is not a wildcard.
func validateRateLimitKey(key string) bool {
	if key == "" {
		return false
	}

	if len(key) > 256 {
		return false
	}

	if strings.HasPrefix(key, loginKeyPrefix) {
		return validateLoginKey(key)
	}

	if uuidRegex.MatchString(key) {
		return true
	}

	return validateIPKey(key)
}

func validateIPKey(key string) bool {
	if ipRegex.MatchString(key) {
		parts := strings.Split(key, ".")
		for _, part := range parts {
			num, err := strconv.Atoi(part)
			if err != nil || num < 0 || num > 255 {
				return false
			}
		}
		return true
	}

	if key == "::1" {
		return true
	}

	return net.ParseIP(key) != nil
}

// validateLoginKey validates the composite "login:<scope>|<account>" form: the
// scope half must be a valid IP or loginKeyAnyIP, and the account half must
// already be normalised. Keys are only ever built by loginRateLimitKey, so a
// key that fails here is a bug or a forgery, not user input to be accommodated.
func validateLoginKey(key string) bool {
	body := strings.TrimPrefix(key, loginKeyPrefix)
	scope, account, found := strings.Cut(body, "|")
	if !found {
		return false
	}
	if scope != loginKeyAnyIP && !validateIPKey(scope) {
		return false
	}
	return loginKeyRegex.MatchString(account)
}

// normalizeAccount folds a submitted username into the account half of a
// composite key.
//
// The username is attacker-controlled, so it is lower-cased and required to
// match the account charset before it reaches a map key or a log line;
// everything else collapses to the sentinel. This folding is now consistent
// with the DB layer (agent-os-tmo): database.GetUserByUsername compares
// case-insensitively (COLLATE NOCASE, migration 13, idx_users_username_nocase),
// so "Alice" and "alice" are the same account at login too, and this bucket
// key groups exactly the login attempts that land on one account. Do not
// "fix" this to preserve case — that would split one account's budget across
// buckets again, the opposite of what this key is for.
//
// Before migration 13, usernames compared case-sensitively at login while
// this limiter already folded case for a different reason: preventing case
// rotation from multiplying an attacker's per-account budget. That rationale
// assumed a two-account scenario ("Alice" and "alice" both real, sharing a
// bucket) that turned out to be unreachable in this codebase — CreateUser
// has exactly one call site (handlers.AuthHandler.Setup), which refuses once
// a single user exists, so a second real account by any casing never
// existed to share a bucket with. The folding was kept anyway because it is
// now simply correct, not because of the original multi-account rationale.
func normalizeAccount(username string) string {
	account := strings.ToLower(strings.TrimSpace(username))
	if !loginKeyRegex.MatchString(account) {
		return loginKeyUnknownAccount
	}
	return account
}

// loginRateLimitKey builds a composite bucket key. Pass loginKeyAnyIP as scope
// for the account-wide bucket.
func loginRateLimitKey(scope, account string) string {
	return loginKeyPrefix + scope + "|" + account
}

// removeKey deletes key from requests, accessOrder, and accessIndex together.
// This is the only place any of the three is removed, so — unlike the slice
// design this replaced — dropping a key from one without the other is not a
// mistake a future call site can make; there is only one call site. That
// matters because with an account half derived from submitted usernames the
// key space is unbounded and attacker-influenced, so a drifting mirror is a
// remotely-driven leak, not just a bookkeeping bug (agent-os-boe). Caller holds
// rl.mu. Pinned by TestAccessOrderMirrorsRequestsAfterExpiry.
func removeKey(rl *RateLimiter, key string) {
	delete(rl.requests, key)
	if el, ok := rl.accessIndex[key]; ok {
		rl.accessOrder.Remove(el)
		delete(rl.accessIndex, key)
	}
}

// evictLRU pops the least-recently-used key — accessOrder.Front() — until
// requests is back at maxEntries. Pinned by TestEvictLRUPicksLeastRecentlyUsed.
func evictLRU(rl *RateLimiter) {
	for len(rl.requests) > rl.maxEntries {
		oldest := rl.accessOrder.Front()
		if oldest == nil {
			return
		}
		removeKey(rl, oldest.Value.(string))
	}
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		for key, ur := range rl.requests {
			now := time.Now()
			valid := make([]time.Time, 0, len(ur.timestamps))
			for _, ts := range ur.timestamps {
				if now.Sub(ts) < rl.window {
					valid = append(valid, ts)
				}
			}
			if len(valid) == 0 {
				removeKey(rl, key)
			} else {
				ur.timestamps = valid
			}
		}
		rl.mu.Unlock()
	}
}

// check records a request against key and reports whether it is allowed.
//
// It fails CLOSED: a key that does not validate is denied rather than waved
// through. Callers reach check() through enforceLimit, which validates first so
// the caller can answer 400 rather than 429; this branch is the backstop for a
// future caller that forgets. Pinned by TestCheckFailsClosedOnInvalidKey.
func (rl *RateLimiter) check(key string) bool {
	if !validateRateLimitKey(key) {
		slog.Warn("Invalid rate limit key rejected", "key", key)
		return false
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	evictLRU(rl)

	now := time.Now()
	ur, exists := rl.requests[key]
	if !exists {
		ur = &userRequests{timestamps: []time.Time{}}
		rl.requests[key] = ur
		rl.accessIndex[key] = rl.accessOrder.PushBack(key)
	} else if el, ok := rl.accessIndex[key]; ok {
		rl.accessOrder.MoveToBack(el)
	} else {
		// requests and accessIndex are written together above and removed
		// together by removeKey, so this branch is unreachable today. But
		// insertion, unlike removal, is still two adjacent statements rather
		// than one call site — nothing structural stops a future edit from
		// separating them. list.MoveToBack dereferences its argument's
		// internal list pointer, so a nil *list.Element there would panic on
		// the login path rather than silently miss a refresh. Re-inserting
		// instead heals the mirror and degrades a future bug to a missed LRU
		// refresh, not a crash.
		rl.accessIndex[key] = rl.accessOrder.PushBack(key)
	}

	cutoff := now.Add(-rl.window)
	valid := make([]time.Time, 0, len(ur.timestamps))
	for _, ts := range ur.timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= rl.maxReqs {
		return false
	}

	valid = append(valid, now)
	ur.timestamps = valid
	return true
}

// enforceLimit is the single place a request key is validated and turned into a
// response. Both rate limit middlewares go through it, so the validate-then-400
// policy exists once rather than at every call site. It returns false when the
// request has been answered and must not continue.
func enforceLimit(c *gin.Context, rl *RateLimiter, key string) bool {
	if !validateRateLimitKey(key) {
		slog.Warn("Invalid rate limit key", "key", key)
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    "INVALID_KEY",
			"message": "Invalid request identifier",
		})
		c.Abort()
		return false
	}

	if !rl.check(key) {
		slog.Warn("Rate limit exceeded", "key", key)
		c.Header("Retry-After", "60")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"code":    "RATE_LIMITED",
			"message": "Too many requests. Please try again later.",
		})
		c.Abort()
		return false
	}

	return true
}

func (rl *RateLimiter) Middleware(keyFunc func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enforceLimit(c, rl, keyFunc(c)) {
			return
		}
		c.Next()
	}
}

var authIPRateLimiter *RateLimiter
var authAccountRateLimiter *RateLimiter
var authAccountAnyIPRateLimiter *RateLimiter
var apiRateLimiter *RateLimiter

const (
	// authAccountMaxReqs is the brute-force budget for one account from one
	// client IP — the tight layer, and the one a real user notices.
	authAccountMaxReqs = 5

	// authIPMaxReqs caps one client IP across all accounts, so an attacker
	// cannot buy unlimited attempts by rotating usernames. It is deliberately
	// looser than the per-account limit: behind a reverse proxy every user can
	// share one client IP, and a 5/min ceiling there lets one person fumbling
	// their password lock out everyone else.
	authIPMaxReqs = 20

	// authAccountAnyIPMaxReqs caps one account across all source addresses,
	// which is the only layer a distributed attacker rotating IPs still meets.
	// It is loose on purpose: this is a ceiling on aggregate guess rate, not an
	// account lockout (explicitly out of scope), and a legitimate user retyping
	// a password never approaches 60 attempts in a rolling minute.
	authAccountAnyIPMaxReqs = 60
)

// InitRateLimiters builds the four process-wide limiters. apiMaxReqs is the
// general API budget per rolling minute; callers pass
// config.DefaultAPIRateLimitPerMin unless RATE_LIMIT_API_PER_MIN overrides it.
//
// The budget is a parameter rather than a literal because the end-to-end suites
// drive one bucket far harder than any human does: with AUTH_DISABLED=true
// RateLimitByUser finds no userID and keys every request on the client IP, so a
// whole Playwright run shares a single bucket. OBSERVED 2026-09-01 in CI run
// 33508313900: 352 requests in 58.96s from one suite against the 300 ceiling,
// which 429'd tests unrelated to the traffic that spent the budget. In
// production, where auth is on, each user keys on their own UUID and never
// aggregates this way.
//
// The auth budgets stay hardcoded. They are brute-force controls with no
// legitimate reason to vary per deployment, and the E2E problem does not touch
// them.
func InitRateLimiters(apiMaxReqs int) {
	authIPRateLimiter = NewRateLimiter(1*time.Minute, authIPMaxReqs)
	authAccountRateLimiter = NewRateLimiter(1*time.Minute, authAccountMaxReqs)
	authAccountAnyIPRateLimiter = NewRateLimiter(1*time.Minute, authAccountAnyIPMaxReqs)
	apiRateLimiter = NewRateLimiter(1*time.Minute, apiMaxReqs)
	slog.Info("Rate limiters initialized",
		"auth_per_ip", strconv.Itoa(authIPMaxReqs)+"/min",
		"auth_per_ip_account", strconv.Itoa(authAccountMaxReqs)+"/min",
		"auth_per_account", strconv.Itoa(authAccountAnyIPMaxReqs)+"/min",
		"api", strconv.Itoa(apiMaxReqs)+"/min",
	)
}

// RateLimitAuth limits the auth endpoints in three layers: per (client IP,
// account), per client IP, and per account across all addresses.
//
// The per-account layers are what survive a reverse proxy that presents one
// client IP for every user — without them the whole deployment shares a single
// login bucket and one person mistyping a password locks out everybody.
func RateLimitAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		if !enforceLimit(c, authIPRateLimiter, clientIP) {
			return
		}

		if !validateIPKey(clientIP) {
			// enforceLimit already accepted clientIP, so this cannot happen; guard
			// anyway rather than build a composite key around an unvalidated half.
			c.Next()
			return
		}

		account := normalizeAccount(peekLoginUsername(c))

		if !enforceLimit(c, authAccountRateLimiter, loginRateLimitKey(clientIP, account)) {
			return
		}

		// Attempts whose username could not name a real account all share the
		// sentinel; counting them account-wide would let a scanner spraying junk
		// exhaust one globally shared bucket. The two layers above already cap it.
		if account != loginKeyUnknownAccount {
			if !enforceLimit(c, authAccountAnyIPRateLimiter, loginRateLimitKey(loginKeyAnyIP, account)) {
				return
			}
		}

		c.Next()
	}
}

// peekLoginUsername reads the username out of a JSON request body without
// consuming it, so the handler's own binding still sees the full body. Bodies
// over loginBodyPeekLimit are streamed straight back through untouched and
// yield "", which falls back to the sentinel account bucket.
//
// The restored body is byte-identical, so Content-Length stays correct and
// nothing downstream needs to know this ran. Every failure mode — no body, a
// read error, an oversized body, malformed JSON, a username that is not a
// string — returns "" and lets the handler produce its own verdict. The limiter
// must not become a new way to reject a request the handler would have accepted.
// Pinned by TestPeekLoginUsername_PreservesBodyAndContentLength and
// TestAuthLimit_UnparseableBodyFallsThroughToHandler.
func peekLoginUsername(c *gin.Context) string {
	if c.Request == nil || c.Request.Body == nil {
		return ""
	}

	buf, err := io.ReadAll(io.LimitReader(c.Request.Body, loginBodyPeekLimit+1))
	if err != nil {
		// Restore what was read so the handler reports the error itself.
		c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), c.Request.Body))
		return ""
	}

	c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), c.Request.Body))

	if len(buf) > loginBodyPeekLimit {
		return ""
	}

	var body struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(buf, &body); err != nil {
		return ""
	}
	return body.Username
}

func RateLimitByUser() gin.HandlerFunc {
	return apiRateLimiter.Middleware(func(c *gin.Context) string {
		if userID, exists := c.Get("userID"); exists {
			userIDStr := userID.(string)
			if userIDStr == "anonymous" {
				return c.ClientIP()
			}
			return userIDStr
		}
		return c.ClientIP()
	})
}
