package middleware

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
)

var (
	// Spaces are allowed: stacks scanned from existing directories can have
	// spaces in their name (e.g. "backup script-test"), and the ID is only a DB
	// lookup key — the on-disk path comes from the stack record, not the ID.
	stackIDRegex  = regexp.MustCompile(`^[a-zA-Z0-9 ._:~-]+$`)
	usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	commonPasswords = map[string]bool{
		"password":    true,
		"password123": true,
		"123456":      true,
		"12345678":    true,
		"qwerty":      true,
		"abc123":      true,
		"monkey":      true,
		"letmein":     true,
		"dragon":      true,
		"baseball":    true,
		"football":    true,
		"iloveyou":    true,
		"master":      true,
		"hello":       true,
		"welcome":     true,
		"admin":       true,
		"root":        true,
		"pass":        true,
		"test":        true,
		"guest":       true,
		"user":        true,
		"login":       true,
		"qwerty123":   true,
		"password1":   true,
		"password!":   true,
	}
)

func ValidateStackID(stackID string) bool {
	return stackIDRegex.MatchString(stackID)
}

func ValidateUsername(username string) bool {
	if len(username) < 3 || len(username) > 50 {
		return false
	}
	return usernameRegex.MatchString(username)
}

func ValidatePassword(password string) (bool, string) {
	if len(password) < 8 {
		return false, "Password must be at least 8 characters long"
	}
	if len(password) > 128 {
		return false, "Password must not exceed 128 characters"
	}

	var hasUpper, hasLower, hasNumber, hasSpecial bool
	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	if !hasUpper {
		return false, "Password must contain at least one uppercase letter"
	}
	if !hasLower {
		return false, "Password must contain at least one lowercase letter"
	}
	if !hasNumber {
		return false, "Password must contain at least one number"
	}
	if !hasSpecial {
		return false, "Password must contain at least one special character"
	}

	lowerPassword := strings.ToLower(password)
	if commonPasswords[lowerPassword] {
		return false, "Password is too common and not allowed"
	}

	return true, ""
}

const maxRequestBodySize = 10 << 20

func BodySizeLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)
		}
		c.Next()
	}
}

// ValidateInput validates path params before they reach handlers.
// Body validation must happen in handlers — reading the body here would consume
// the stream and break downstream re-binding (gin's ShouldBindJSON does not cache).
func ValidateInput() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if strings.Contains(path, "/stacks/") {
			stackID := c.Param("id")
			if stackID != "" && !ValidateStackID(stackID) {
				slog.Warn("Invalid stack ID", "stackID", stackID)
				c.JSON(400, gin.H{
					"code":    "VALIDATION_ERROR",
					"message": "Invalid stack ID format",
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}
