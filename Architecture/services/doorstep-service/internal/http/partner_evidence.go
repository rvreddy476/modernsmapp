package http

import (
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *Handler) adminEvidence(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	id, ok := uuidParam(c, "id")
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
