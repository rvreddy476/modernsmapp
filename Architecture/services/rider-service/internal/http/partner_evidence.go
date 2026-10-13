package http

import (
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *Handler) AdminEvidence(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	ref, err := h.svc.AdminEvidence(c.Request.Context(), id)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, ref)
}
