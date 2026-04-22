package middleware

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
)

var (
	stackIDRegex  = regexp.MustCompile(`^[a-zA-Z0-9._:-]+$`)
	usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	pathTraversal = regexp.MustCompile(`\.\.`)

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

	allowedBaseDirs = []string{
		"/opt/stacks",
		"/tmp/docker-manager",
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

func ValidateNoPathTraversal(input string) bool {
	return !pathTraversal.MatchString(input)
}

func validateFilePath(input string) bool {
	normalized := filepath.Clean(input)

	if filepath.IsAbs(normalized) {
		return false
	}

	if strings.Contains(normalized, "..") {
		return false
	}

	normalized = filepath.FromSlash(normalized)
	if strings.Contains(normalized, "..") {
		return false
	}

	decoded := strings.ToLower(input)
	if strings.Contains(decoded, "%2e%2e") || strings.Contains(decoded, "%2e.") || strings.Contains(decoded, ".%2e") {
		return false
	}
	if strings.Contains(decoded, "%5c") || strings.Contains(decoded, "%2f") {
		return false
	}

	for _, baseDir := range allowedBaseDirs {
		fullPath := filepath.Join(baseDir, normalized)
		if strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(baseDir)) {
			return true
		}
	}

	return false
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

		if strings.Contains(path, "/auth/setup") || strings.Contains(path, "/auth/login") {
			var req struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := c.ShouldBindJSON(&req); err == nil {
				if !ValidateUsername(req.Username) {
					c.JSON(400, gin.H{
						"code":    "VALIDATION_ERROR",
						"message": "Username must be between 3 and 50 characters and contain only letters, numbers, underscores, and hyphens",
					})
					c.Abort()
					return
				}
				if valid, msg := ValidatePassword(req.Password); !valid {
					c.JSON(400, gin.H{
						"code":    "VALIDATION_ERROR",
						"message": msg,
					})
					c.Abort()
					return
				}
			}
		}

		c.Next()
	}
}

func SanitizeFilePath(input string) string {
	cleaned := strings.TrimSpace(input)
	cleaned = strings.ReplaceAll(cleaned, "\\", "/")
	cleaned = strings.ReplaceAll(cleaned, "../", "")
	return cleaned
}

func ValidatePathParam(param string) bool {
	return ValidateNoPathTraversal(param) && param != "" && len(param) < 500
}
