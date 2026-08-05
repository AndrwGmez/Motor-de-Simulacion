package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/incident"
)

func (s *Server) getRunIncident(c *gin.Context) {
	run, ok := s.runWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, incident.Build(run))
}
