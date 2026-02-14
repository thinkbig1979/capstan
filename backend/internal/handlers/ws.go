package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	CloseCodeAuthFailure = 4401
	CloseCodeRateLimit   = 4429
)

const (
	DefaultReadBufferSize  = 1024
	DefaultWriteBufferSize = 1024
	DefaultPingInterval    = 30 * time.Second
	DefaultConnectionLimit = 10
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  DefaultReadBufferSize,
	WriteBufferSize: DefaultWriteBufferSize,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Connection struct {
	ID        string
	UserID    string
	Conn      *websocket.Conn
	CreatedAt time.Time
}

type ConnectionManager struct {
	connections map[string]*Connection
	userCounts  map[string]int
	mu          sync.RWMutex
	maxPerUser  int
}

func NewConnectionManager(maxPerUser int) *ConnectionManager {
	if maxPerUser <= 0 {
		maxPerUser = DefaultConnectionLimit
	}
	return &ConnectionManager{
		connections: make(map[string]*Connection),
		userCounts:  make(map[string]int),
		maxPerUser:  maxPerUser,
	}
}

func (cm *ConnectionManager) Add(id string, conn *Connection) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	count := cm.userCounts[conn.UserID]
	if count >= cm.maxPerUser {
		return &models.AppError{
			Code:    models.ErrRateLimited,
			Message: "Connection limit per user exceeded",
			Status:  http.StatusTooManyRequests,
		}
	}

	cm.connections[id] = conn
	cm.userCounts[conn.UserID] = count + 1
	return nil
}

func (cm *ConnectionManager) Remove(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if conn, exists := cm.connections[id]; exists {
		cm.userCounts[conn.UserID]--
		if cm.userCounts[conn.UserID] <= 0 {
			delete(cm.userCounts, conn.UserID)
		}
		delete(cm.connections, id)
	}
}

func (cm *ConnectionManager) Get(id string) (*Connection, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	conn, exists := cm.connections[id]
	return conn, exists
}

func (cm *ConnectionManager) Count() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.connections)
}

func (cm *ConnectionManager) CountByUser(userID string) int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.userCounts[userID]
}

func (cm *ConnectionManager) CloseAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for id, conn := range cm.connections {
		if conn.Conn != nil {
			writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Server shutting down")
			conn.Conn.Close()
		}
		delete(cm.connections, id)
	}
	cm.userCounts = make(map[string]int)
}

func authenticateWS(c *gin.Context, db *database.DB, jwtSecret string, authDisabled bool) (string, error) {
	if authDisabled {
		return "anonymous", nil
	}

	token := c.Query("token")
	if token == "" {
		return "", &models.AppError{
			Code:    models.ErrUnauthorized,
			Message: "Missing token parameter",
			Status:  http.StatusUnauthorized,
		}
	}

	claims, err := validateJWT(token, jwtSecret)
	if err != nil {
		if err.Error() == "token is expired by" {
			return "", &models.AppError{
				Code:    models.ErrSessionExpired,
				Message: "Token has expired",
				Status:  http.StatusUnauthorized,
			}
		}
		return "", &models.AppError{
			Code:    models.ErrUnauthorized,
			Message: "Invalid token",
			Status:  http.StatusUnauthorized,
		}
	}

	if jti, ok := claims["jti"].(string); ok {
		session, err := db.GetSession(jti)
		if err != nil || session == nil {
			return "", &models.AppError{
				Code:    models.ErrSessionExpired,
				Message: "Session not found or expired",
				Status:  http.StatusUnauthorized,
			}
		}

		if time.Now().After(session.ExpiresAt) {
			return "", &models.AppError{
				Code:    models.ErrSessionExpired,
				Message: "Session has expired",
				Status:  http.StatusUnauthorized,
			}
		}
	}

	userID, ok := claims["sub"].(string)
	if !ok || userID == "" {
		return "", &models.AppError{
			Code:    models.ErrUnauthorized,
			Message: "Invalid user ID in token",
			Status:  http.StatusUnauthorized,
		}
	}

	return userID, nil
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

func writeJSON(conn *websocket.Conn, v interface{}) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(v)
}

func readJSON(conn *websocket.Conn, v interface{}) error {
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	return conn.ReadJSON(v)
}

func pingLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Debug("Failed to send ping", "error", err)
				return
			}
		}
	}
}

func writeCloseMessage(conn *websocket.Conn, closeCode int, reason string) {
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	msg := websocket.FormatCloseMessage(closeCode, reason)
	if err := conn.WriteMessage(websocket.CloseMessage, msg); err != nil {
		slog.Debug("Failed to send close message", "error", err)
		return
	}

	time.Sleep(100 * time.Millisecond)
}

func upgradeConnection(c *gin.Context, db *database.DB, jwtSecret string, authDisabled bool) (*Connection, error) {
	userID, err := authenticateWS(c, db, jwtSecret, authDisabled)
	if err != nil {
		return nil, err
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, err
	}

	connectionID := uuid.New().String()
	connection := &Connection{
		ID:        connectionID,
		UserID:    userID,
		Conn:      conn,
		CreatedAt: time.Now(),
	}

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	return connection, nil
}
