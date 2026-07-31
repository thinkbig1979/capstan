package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDKey is the gin context key holding the current request's ID.
const RequestIDKey = "requestID"

// RequestIDHeader is the response header carrying it back to the caller, and
// the request header honoured when a reverse proxy has already assigned one.
const RequestIDHeader = "X-Request-ID"

// RequestID assigns every request an ID, exposes it on the context, and returns
// it in a response header.
//
// Without it there is no way to join a 500 in the HTTP log to the row it
// produced in action_log, or to follow one user action across several log
// lines. The header also gives a user something concrete to quote in a bug
// report (agent-os-7li).
//
// An inbound header is honoured so a request keeps one identity across a
// reverse proxy — but only when it is a well-formed UUID. An arbitrary
// caller-supplied string would otherwise end up in log lines and audit rows,
// where a crafted value could forge log entries or bloat the database.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := ""
		if inbound := c.GetHeader(RequestIDHeader); inbound != "" {
			if parsed, err := uuid.Parse(inbound); err == nil {
				id = parsed.String()
			}
		}
		if id == "" {
			id = uuid.New().String()
		}

		c.Set(RequestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// RequestIDFrom returns the request ID stored on the context, or "" when the
// middleware did not run (background jobs, tests).
func RequestIDFrom(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, ok := c.Get(RequestIDKey); ok {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}
