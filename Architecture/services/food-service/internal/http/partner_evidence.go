package http

import (
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
)

func (h *Handler) adminEvidence(delivery bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private")
		field := "restaurantId"
		if delivery {
			field = "partnerId"
		}
		parent, e1 := uuid.Parse(c.Param(field))
		id, e2 := uuid.Parse(c.Param("docId"))
		if e1 != nil || e2 != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		ref, err := h.svc.AdminEvidence(c.Request.Context(), delivery, parent, id)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, ref)
	}
}

func (h *Handler) adminRestaurantEvidenceList(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	id, err := uuid.Parse(c.Param("restaurantId"))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	docs, err := h.svc.AdminRestaurantEvidenceList(c.Request.Context(), id)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, docs)
}
