package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/version"
)

// VersionHandler serves the build identity of the running binary.
//
// The route is deliberately public (see RegisterVersionRoutes). "What is
// actually running on this host?" is the first question of every incident, and
// an uptime check or a support conversation should be able to answer it without
// a session. The trade is that an unauthenticated caller learns the exact
// version; for a self-hosted admin tool on a trusted network that is worth the
// operability, and the same information is already visible in the image tag.
type VersionHandler struct{}

func NewVersionHandler() *VersionHandler {
	return &VersionHandler{}
}

// RegisterVersionRoutes mounts GET /version on the given group.
//
// It must be mounted on the *unauthenticated* /api/v1 group, and the resulting
// path must appear in middleware.PublicPaths — that list is consulted by both
// the auth middleware and the CSRF middleware, so the two stay consistent if the
// route is ever moved under the protected group.
func (h *VersionHandler) RegisterVersionRoutes(group *gin.RouterGroup) {
	group.GET("/version", h.Get)
}

func (h *VersionHandler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, version.Get())
}
