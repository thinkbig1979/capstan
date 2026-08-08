package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"golang.org/x/crypto/bcrypt"
)

// jwtIssuer is set as the "iss" claim on issued tokens and required by the
// validators, so tokens are bound to this application (L2). Must match
// middleware.jwtIssuer.
const jwtIssuer = "capstan"

// dummyBcryptHash is compared against on the username-not-found login path so
// that a bcrypt comparison is always performed regardless of whether the user
// exists. This equalizes response timing between the user-exists and
// user-missing branches and removes the username-enumeration oracle (H3).
// It is a bcrypt hash (at the same DefaultCost as real password hashes) of a
// random value computed once at startup; no real password can ever match it.
var dummyBcryptHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("capstan-login-timing-equalizer-"+uuid.NewString()), bcrypt.DefaultCost)
	if err != nil {
		// bcrypt.GenerateFromPassword only fails on an invalid cost, which is a
		// compile-time constant here; panic so the misconfiguration is loud.
		panic("failed to generate dummy bcrypt hash: " + err.Error())
	}
	return h
}()

type AuthHandler struct {
	db           *database.DB
	jwtSecret    string
	authDisabled bool
	actionLog    *services.ActionLogger
	// envUnlock mints the short-lived tokens that gate the secret-reveal
	// surfaces. Injected after construction (SetEnvUnlockStore) because the same
	// store instance has to be shared with middleware.EnvUnlock, and threading it
	// through every NewAuthHandler call site would churn 20-odd tests for no
	// behavioural gain. A nil store mints nothing, which leaves those surfaces
	// locked — the safe direction if wiring is ever forgotten.
	envUnlock *services.EnvUnlockStore
}

func NewAuthHandler(db *database.DB, jwtSecret string, authDisabled bool) *AuthHandler {
	return &AuthHandler{
		db:           db,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
		actionLog:    services.NewActionLogger(db),
	}
}

// SetEnvUnlockStore wires the shared unlock-token store. Call it with the same
// instance handed to middleware.EnvUnlock, or nothing this handler mints will
// ever validate.
func (h *AuthHandler) SetEnvUnlockStore(store *services.EnvUnlockStore) {
	h.envUnlock = store
}

// mintUnlock issues an unlock token for userID and writes the verify-password
// success body. On a minting failure it still reports ok:true — the password
// really was correct — but without a token, so the client re-prompts rather than
// being told its credentials were wrong.
func (h *AuthHandler) mintUnlock(c *gin.Context, userID string) {
	if h.envUnlock == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	token, ttl, err := h.envUnlock.Mint(userID)
	if err != nil {
		slog.Error("Failed to mint env unlock token", "error", err)
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"unlockToken": token,
		"expiresIn":   int(ttl.Seconds()),
	})
}

// RegisterPublicRoutes registers public bootstrap endpoints that must not be
// subject to brute-force rate limiting. The frontend polls /status on every
// navigation, so it cannot share a bucket with /login.
func (h *AuthHandler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.GET("/status", h.Status)
}

func (h *AuthHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/setup", h.Setup)
	group.POST("/login", h.Login)
}

func (h *AuthHandler) RegisterProtectedRoutes(group *gin.RouterGroup) {
	group.POST("/auth/logout", h.Logout)
	group.GET("/auth/me", h.Me)
	group.POST("/auth/verify-password", h.VerifyPassword)
}

func (h *AuthHandler) Status(c *gin.Context) {
	userCount, _ := h.db.UserCount()
	needsSetup := userCount == 0

	if h.authDisabled {
		slog.Warn("AUTHENTICATION DISABLED - Only safe on trusted networks!")
	}

	c.JSON(http.StatusOK, gin.H{
		"needsSetup":   needsSetup,
		"authDisabled": h.authDisabled,
	})
}

func (h *AuthHandler) Setup(c *gin.Context) {
	// Fast path only: reject an obviously-already-setup instance before hashing.
	// This is NOT the race guard — two concurrent callers can both read count==0
	// here. The authoritative check is the atomic CreateFirstUser below.
	userCount, _ := h.db.UserCount()
	if userCount > 0 {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			models.ErrSetupAlreadyDone,
			"Setup has already been completed",
		))
		return
	}

	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	if err := validateUsername(req.Username); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}

	if err := validatePassword(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to hash password",
		))
		return
	}

	userID := uuid.New().String()
	now := time.Now()

	user := models.User{
		ID:        userID,
		Username:  req.Username,
		Password:  string(hashedPassword),
		CreatedAt: now,
		UpdatedAt: now,
	}

	created, err := h.db.CreateFirstUser(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to create user",
		))
		return
	}
	// A concurrent setup won the race and created the first admin between our
	// fast-path check and this insert. Report the same 409 as the fast path
	// rather than pretending to have bootstrapped this request (agent-os-iut).
	if !created {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			models.ErrSetupAlreadyDone,
			"Setup has already been completed",
		))
		return
	}

	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(24 * time.Hour)

	session := models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}

	if err := h.db.CreateSession(session); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to create session",
		))
		return
	}

	token, err := generateJWT(userID, req.Username, sessionID, h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to generate token",
		))
		return
	}

	csrfToken := middleware.GenerateCSRFToken()
	setAuthCookies(c, token, csrfToken)

	h.actionLog.Log(userID, nil, services.ActionSetup, gin.H{"username": req.Username})

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":       userID,
			"username": req.Username,
		},
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	user, lookupErr := h.db.GetUserByUsername(req.Username)

	// Always perform a bcrypt comparison, even when the user does not exist, so
	// the user-exists and user-missing paths take comparable time. This removes
	// the username-enumeration timing oracle (H3). On the missing-user path we
	// compare against a dummy hash so the result is still a mismatch.
	hashToCompare := dummyBcryptHash
	userExists := lookupErr == nil && user != nil
	if userExists {
		hashToCompare = []byte(user.Password)
	}
	cmpErr := bcrypt.CompareHashAndPassword(hashToCompare, []byte(req.Password))

	if !userExists || cmpErr != nil {
		reason := "invalid_password"
		if !userExists {
			reason = "user_not_found"
		}
		slog.Warn("Failed authentication attempt",
			"ip", c.ClientIP(),
			"username", req.Username,
			"timestamp", time.Now(),
			"reason", reason)
		// The action_log.user_id FK requires a real user, so only failed attempts
		// against an existing account are persisted to the audit trail (the
		// high-value "someone is attacking this account" signal). Attempts on
		// unknown usernames are still recorded via slog above.
		if userExists {
			h.actionLog.Log(user.ID, nil, services.ActionLoginFailed, gin.H{
				"username": req.Username,
				"reason":   reason,
				"ip":       c.ClientIP(),
			})
		}
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Invalid credentials",
		))
		return
	}

	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(24 * time.Hour)
	now := time.Now()

	session := models.Session{
		ID:        sessionID,
		UserID:    user.ID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}

	if err := h.db.CreateSession(session); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to create session",
		))
		return
	}

	token, err := generateJWT(user.ID, user.Username, sessionID, h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to generate token",
		))
		return
	}

	csrfToken := middleware.GenerateCSRFToken()
	setAuthCookies(c, token, csrfToken)

	h.actionLog.Log(user.ID, nil, services.ActionLogin, gin.H{
		"username": user.Username,
		"ip":       c.ClientIP(),
	})

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
		},
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Not authenticated",
		))
		return
	}

	// Take the session id from the context, where AuthMiddleware published it
	// after parsing the token and validating the session row (middleware/auth.go).
	// This deliberately does NOT re-read the Authorization header: that is what
	// this handler used to do, and the browser never sends that header — App.tsx
	// registers `() => null` as getToken so api.ts never sets it — so parseJWT("")
	// errored and DeleteSession was never reached, leaving every UI logout
	// client-side only while still returning 204 (agent-os-h9o). Reading the
	// already-validated jti removes the second parse and makes the transport
	// (header vs cookie) irrelevant to whether revocation happens.
	//
	// A missing jti is normal under AUTH_DISABLED, where the bypass sets userID
	// without minting a session — there is no row to revoke, so skipping is
	// correct rather than an error.
	if jti := c.GetString("jti"); jti != "" {
		// Logout still succeeds client-side even if the revoke fails —
		// the session expires on its own regardless — but a failed
		// revoke leaves the session valid in the DB until then, which
		// is worth an operator's attention, not silent discard.
		if delErr := h.db.DeleteSession(jti); delErr != nil {
			slog.Warn("Failed to revoke session on logout", "error", delErr)
		}
	}

	// Drop any live env-unlock window with the session. Without this a token
	// minted seconds before logout would keep unlocking secrets for the rest of
	// its 5 minutes on the next login.
	if h.envUnlock != nil {
		h.envUnlock.RevokeUser(userID.(string))
	}

	slog.Info("User logged out", "userID", userID)
	logActionFromContext(h.actionLog, c, nil, services.ActionLogout, gin.H{})

	clearAuthCookies(c)

	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Not authenticated",
		))
		return
	}

	// Same reasoning as VerifyPassword and settings.ChangePassword below: a
	// valid session pointing at a user row that no longer exists is session
	// loss, not a bad credential, and can never resolve.
	user, err := h.db.GetUserByID(userID.(string))
	if err != nil || user == nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrSessionExpired,
			"User not found",
		))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":       user.ID,
		"username": user.Username,
	})
}

// VerifyPassword re-checks the current user's password without issuing a new
// session, and on success mints a short-lived unlock token. That token is the
// second factor the secret-reveal surfaces require: before agent-os-7o5s this
// handler returned a bare {"ok": true} that nothing consumed, so the password
// prompt was pure UI ceremony and any authenticated caller could read raw .env
// contents without ever calling it.
//
// The token is session-wide rather than bound to one stack id: it unlocks every
// secret surface for its lifetime. That deliberately matches the existing UX of
// one prompt covering the stack .env editor and the global-env settings panel,
// and is deliberately weaker than a per-stack binding.
func (h *AuthHandler) VerifyPassword(c *gin.Context) {
	if h.authDisabled {
		// No password exists to re-check, so there is nothing to verify — but the
		// surfaces still ask for a token, so mint one unconditionally rather than
		// locking the operator out of their own env files. middleware.EnvUnlock
		// also opens the gate outright in this mode; this keeps the endpoint's
		// contract identical either way.
		h.mintUnlock(c, c.GetString("userID"))
		return
	}

	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Not authenticated",
		))
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	// Reaching here means a valid session pointing at a user row that no longer
	// exists (deleted admin, DB restored from an older snapshot). That session
	// can never resolve a user, so it is session loss, not a bad credential.
	user, err := h.db.GetUserByID(userID.(string))
	if err != nil || user == nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrSessionExpired,
			"User not found",
		))
		return
	}

	// The wrong-password 401 below keeps ErrUnauthorized: the session is still
	// valid and the unlock dialog must stay open to retype (agent-os-318).
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		slog.Warn("Failed env unlock attempt",
			"ip", c.ClientIP(),
			"username", user.Username,
			"timestamp", time.Now(),
		)
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Invalid password",
		))
		return
	}

	h.mintUnlock(c, user.ID)
}

func validateUsername(username string) *models.AppError {
	if !middleware.ValidateUsername(username) {
		return models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Username must be between 3 and 50 characters and contain only letters, numbers, underscores, and hyphens")
	}
	return nil
}

func validatePassword(password string) *models.AppError {
	if valid, msg := middleware.ValidatePassword(password); !valid {
		return models.NewAppError(http.StatusBadRequest, models.ErrValidation, msg)
	}
	return nil
}

func generateJWT(userID, username, sessionID, secret string) (string, error) {
	claims := jwtv5.MapClaims{
		"iss":      jwtIssuer,
		"sub":      userID,
		"username": username,
		"jti":      sessionID,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func parseJWT(token, secret string) (jwtv5.MapClaims, error) {
	parsedToken, err := jwtv5.Parse(token, func(token *jwtv5.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwtv5.SigningMethodHMAC); !ok {
			return nil, jwtv5.ErrSignatureInvalid
		}
		return []byte(secret), nil
	}, jwtv5.WithIssuer(jwtIssuer))

	if err != nil {
		return nil, err
	}

	if claims, ok := parsedToken.Claims.(jwtv5.MapClaims); ok && parsedToken.Valid {
		return claims, nil
	}

	return nil, jwtv5.ErrInvalidKey
}

func setAuthCookies(c *gin.Context, token string, csrfToken string) {
	secure := middleware.IsSecureRequest(c)
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // G124: Secure is conditional, set above from middleware.IsSecureRequest, which gosec can't evaluate; HttpOnly:true and SameSite:Lax are both already set
		Name:     "capstan_token",
		Value:    token,
		MaxAge:   86400,
		Path:     "/",
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // G124: Secure is conditional (see above) and HttpOnly is deliberately false so the same-origin SPA can read this CSRF cookie for the double-submit check — see csrf.go:78-92
		Name:     "capstan_csrf",
		Value:    csrfToken,
		MaxAge:   86400,
		Path:     "/",
		Secure:   secure,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
	c.Header("X-CSRF-Token", csrfToken)
}

func clearAuthCookies(c *gin.Context) {
	secure := middleware.IsSecureRequest(c)
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // G124: Secure is conditional, set above from middleware.IsSecureRequest, which gosec can't evaluate; HttpOnly:true and SameSite:Lax are both already set
		Name:     "capstan_token",
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // G124: Secure is conditional (see above) and HttpOnly is deliberately false so the same-origin SPA can read this CSRF cookie for the double-submit check — see csrf.go:78-92
		Name:     "capstan_csrf",
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		Secure:   secure,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}
