package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetPageOwner answers GET /internal/pages/:pageId/owner with the page's
// owner and status, for commerce-service to check that a seller names a
// business page they manage before it stores the id (audit US-M3). Internal
// only: behind requireInternalRouteKey and refused at the edge. A missing
// page is 404 PAGE_NOT_FOUND.
//
// With ?user_id=<uuid> the answer also carries user_role, that user's active
// role on the page ("owner", "admin", "editor", … or "" for none), so the
// caller can admit a page admin as well as its owner. Read-only; additive.
func (h *Handler) GetPageOwner(c *gin.Context) {
	pageID, err := uuid.Parse(c.Param("pageId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid page id", nil)
		return
	}
	var userID uuid.UUID
	if raw := c.Query("user_id"); raw != "" {
		if userID, err = uuid.Parse(raw); err != nil || userID == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
			return
		}
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
	body := gin.H{"page_id": pageID, "user_id": owner, "status": status}
	if userID != uuid.Nil {
		role, err := h.store.GetPageRole(c.Request.Context(), pageID, userID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "page role lookup failed", nil)
			return
		}
		body["user_role"] = role
	}
	api.JSON(c.Writer, http.StatusOK, body, nil)
}
