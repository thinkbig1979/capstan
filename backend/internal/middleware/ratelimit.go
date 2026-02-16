package middleware

import (
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
	mu          sync.RWMutex
	requests    map[string]*userRequests
	window      time.Duration
	maxReqs     int
	maxEntries  int
	accessOrder []string
}

type userRequests struct {
	timestamps []time.Time
}

var (
	ipRegex   = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func NewRateLimiter(window time.Duration, maxReqs int) *RateLimiter {
	rl := &RateLimiter{
		requests:    make(map[string]*userRequests),
		window:      window,
		maxReqs:     maxReqs,
		maxEntries:  10000,
		accessOrder: make([]string, 0, 100),
	}

	go rl.cleanup()

	return rl
}

func validateRateLimitKey(key string) bool {
	if key == "" {
		return false
	}

	if len(key) > 256 {
		return false
	}

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

	if uuidRegex.MatchString(key) {
		return true
	}

	if net.ParseIP(key) != nil {
		return true
	}

	return false
}

func evictLRU(rl *RateLimiter) {
	if len(rl.requests) <= rl.maxEntries {
		return
	}

	for len(rl.requests) > rl.maxEntries && len(rl.accessOrder) > 0 {
		oldestKey := rl.accessOrder[0]
		delete(rl.requests, oldestKey)
		rl.accessOrder = rl.accessOrder[1:]
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
				delete(rl.requests, key)
			} else {
				ur.timestamps = valid
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) check(key string) bool {
	if !validateRateLimitKey(key) {
		slog.Warn("Invalid rate limit key rejected", "key", key)
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	evictLRU(rl)

	now := time.Now()
	ur, exists := rl.requests[key]
	if !exists {
		ur = &userRequests{timestamps: []time.Time{}}
		rl.requests[key] = ur

		rl.accessOrder = append(rl.accessOrder, key)
	} else {
		for i, k := range rl.accessOrder {
			if k == key {
				rl.accessOrder = append(rl.accessOrder[:i], rl.accessOrder[i+1:]...)
				rl.accessOrder = append(rl.accessOrder, key)
				break
			}
		}
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

func (rl *RateLimiter) Middleware(keyFunc func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFunc(c)

		if !validateRateLimitKey(key) {
			slog.Warn("Invalid rate limit key", "key", key)
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    "INVALID_KEY",
				"message": "Invalid request identifier",
			})
			c.Abort()
			return
		}

		if !rl.check(key) {
			c.Header("Retry-After", "60")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    "RATE_LIMITED",
				"message": "Too many requests. Please try again later.",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

var authRateLimiter *RateLimiter
var apiRateLimiter *RateLimiter

func InitRateLimiters() {
	authRateLimiter = NewRateLimiter(1*time.Minute, 5)
	apiRateLimiter = NewRateLimiter(1*time.Minute, 60)
	slog.Info("Rate limiters initialized",
		"auth", "5/min",
		"api", "60/min",
	)
}

func RateLimitByIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()

		if !validateRateLimitKey(key) {
			slog.Warn("Invalid rate limit key", "key", key)
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    "INVALID_KEY",
				"message": "Invalid request identifier",
			})
			c.Abort()
			return
		}

		if !authRateLimiter.check(key) {
			slog.Warn("Rate limit exceeded", "key", key)
			c.Header("Retry-After", "60")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    "RATE_LIMITED",
				"message": "Too many requests. Please try again later.",
			})
			c.Abort()
			return
		}
		c.Next()
	}
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
