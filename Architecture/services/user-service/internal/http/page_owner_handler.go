package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetPageOwner answers GET /internal/pages/:pageId/owner with the page's
// owner and status, for commerce-service to check that a seller names their
// OWN business page before it stores the id (audit US-M3). Internal only:
// behind requireInternalRouteKey and refused at the edge. A missing page is
// 404 PAGE_NOT_FOUND.
func (h *Handler) GetPageOwner(c *gin.Context) {
	pageID, err := uuid.Parse(c.Param("pageId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid page id", nil)
		return
	}
	owner, status, err := h.store.PageOwner(c.Request.Context(), pageID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "page lookup failed", nil)
		return
	}
	if owner == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "PAGE_NOT_FOUND", "page not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"page_id": pageID, "user_id": owner, "status": status}, nil)
}
