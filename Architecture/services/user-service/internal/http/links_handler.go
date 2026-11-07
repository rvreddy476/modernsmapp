package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Profile links and the owner's link analytics. Routes are in RegisterRoutes.

func (h *Handler) GetUserLinks(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	links, err := h.svc.GetUserLinks(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if links == nil {
		links = []store.UserLink{}
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

type UpdateLinksRequest struct {
	Links []LinkItem `json:"links"`
}

type LinkItem struct {
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	DisplayLabel string `json:"display_label"`
	SortOrder    int    `json:"sort_order"`
}

func (h *Handler) UpdateMyLinks(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req UpdateLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	links := make([]store.UserLink, len(req.Links))
	for i, l := range req.Links {
		links[i] = store.UserLink{
			UserID:       userID,
			Platform:     l.Platform,
			URL:          l.URL,
			DisplayLabel: l.DisplayLabel,
			SortOrder:    l.SortOrder,
		}
	}

	if err := h.svc.UpdateUserLinks(c.Request.Context(), userID, links); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

func (h *Handler) GetLinkAnalytics(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	analytics, err := h.svc.GetLinkAnalytics(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if analytics == nil {
		analytics = []store.LinkAnalytics{}
	}
	api.JSON(c.Writer, http.StatusOK, analytics, nil)
}
