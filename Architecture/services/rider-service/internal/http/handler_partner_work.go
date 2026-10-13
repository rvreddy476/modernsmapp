package http

import (
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/partnerwork"
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
)

func (h *Handler) GetPartnerWorkSession(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return
	}
	if !partnerwork.Enabled(os.Getenv) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "WORK_COORDINATION_UNAVAILABLE"})
		return
	}
	s, err := h.svc.PartnerWorkState(c.Request.Context(), u)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "WORK_COORDINATION_UNAVAILABLE"})
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, s)
}
