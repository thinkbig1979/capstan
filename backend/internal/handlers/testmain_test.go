package handlers

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain sets gin's global mode exactly once for the whole package. The mode
// is a process-global; setting it from per-test router helpers raced when tests
// ran in parallel (gin/mode.go writes ginMode without synchronization).
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
