package middleware

import (
	"strings"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func AuthMiddleware(db *database.DB, jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authDisabled {
			c.Set("userID", "anonymous")
			c.Set("username", "admin")
			c.Next()
			return
		}

		path := c.Request.URL.Path
		publicPaths := []string{
			"/api/v1/auth/login",
			"/api/v1/auth/setup",
			"/api/v1/auth/status",
			"/health",
		}

		for _, pp := range publicPaths {
			if path == pp {
				c.Next()
				return
			}
		}

		if c.Request.URL.Path == "/ws/terminal" ||
			strings.HasPrefix(c.Request.URL.Path, "/api/v1/stacks") ||
			strings.HasPrefix(c.Request.URL.Path, "/api/v1/directories") {
			token := c.GetHeader("Authorization")
			if token == "" {
				token = c.Query("token")
			}

			if token != "" && !strings.HasPrefix(token, "Bearer ") {
				token = "Bearer " + token
			}

			if token == "" {
				c.JSON(401, models.NewAppError(401, models.ErrUnauthorized, "Missing authorization token"))
				c.Abort()
				return
			}

			token = strings.TrimPrefix(token, "Bearer ")

			claims, err := validateJWT(token, jwtSecret)
			if err != nil {
				if strings.Contains(err.Error(), "expired") {
					c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session expired"))
				} else {
					c.JSON(401, models.NewAppError(401, models.ErrUnauthorized, "Invalid authorization token"))
				}
				c.Abort()
				return
			}

			if jti, ok := claims["jti"].(string); ok {
				session, err := db.GetSession(jti)
				if err != nil || session == nil {
					c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session not found or expired"))
					c.Abort()
					return
				}
			}

			if userID, ok := claims["sub"].(string); ok {
				c.Set("userID", userID)
			}

			if username, ok := claims["username"].(string); ok {
				c.Set("username", username)
			}

			c.Next()
			return
		}

		c.Next()
	}
}

func validateJWT(token, secret string) (jwt.MapClaims, error) {
	parsedToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := parsedToken.Claims.(jwt.MapClaims); ok && parsedToken.Valid {
		return claims, nil
	}

	return nil, jwt.ErrInvalidKey
}
