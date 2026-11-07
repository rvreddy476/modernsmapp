package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Channels and publisher onboarding. Routes are in RegisterRoutes.

func (h *Handler) GetUserChannels(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	channels, err := h.svc.GetUserChannels(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if channels == nil {
		channels = []store.Channel{}
	}
	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

type CreateChannelRequest struct {
	Handle          string `json:"handle" binding:"required"`
	Name            string `json:"name" binding:"required"`
	Description     string `json:"description"`
	Category        string `json:"category"`
	Country         string `json:"country"`
	Language        string `json:"language"`
	ContactEmail    string `json:"contact_email"`
	CollabStatus    string `json:"collab_status"`
	ContentSchedule string `json:"content_schedule"`
}

func (h *Handler) CreateChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req CreateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	ch := &store.Channel{
		UserID:          userID,
		Handle:          req.Handle,
		Name:            req.Name,
		Description:     req.Description,
		Category:        req.Category,
		Country:         req.Country,
		Language:        req.Language,
		ContactEmail:    req.ContactEmail,
		CollabStatus:    req.CollabStatus,
		ContentSchedule: req.ContentSchedule,
	}

	if err := h.svc.CreateChannel(c.Request.Context(), ch); err != nil {
		if writeMediaError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ch, nil)
}

func (h *Handler) GetChannel(c *gin.Context) {
	handle := c.Param("id")
	detail, err := h.svc.GetChannel(c.Request.Context(), handle)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, detail, nil)
}

func (h *Handler) ListMyChannels(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	channels, err := h.svc.GetUserChannels(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if channels == nil {
		channels = []store.Channel{}
	}
	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

type UpdateChannelRequest struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	AvatarMediaID   *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID `json:"banner_media_id"`
	Category        *string    `json:"category"`
	Country         *string    `json:"country"`
	Language        *string    `json:"language"`
	ContactEmail    *string    `json:"contact_email"`
	CollabStatus    *string    `json:"collab_status"`
	ContentSchedule *string    `json:"content_schedule"`
}

func (h *Handler) UpdateChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var req UpdateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	upd := &store.ChannelUpdate{
		ID:              channelID,
		UserID:          userID,
		Name:            req.Name,
		Description:     req.Description,
		AvatarMediaID:   req.AvatarMediaID,
		BannerMediaID:   req.BannerMediaID,
		Category:        req.Category,
		Country:         req.Country,
		Language:        req.Language,
		ContactEmail:    req.ContactEmail,
		CollabStatus:    req.CollabStatus,
		ContentSchedule: req.ContentSchedule,
	}

	if err := h.svc.UpdateChannel(c.Request.Context(), upd); err != nil {
		if writeMediaError(c, err) {
			return
		}
		if err.Error() == "CHANNEL_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found or not owned by you", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	if err := h.svc.DeleteChannel(c.Request.Context(), channelID, userID); err != nil {
		if err.Error() == "CHANNEL_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found or not owned by you", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// EnsurePublisher handles POST /v1/onboarding/ensure-publisher.
// It atomically creates a handle and default channel if the user doesn't have them.
func (h *Handler) EnsurePublisher(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	result, err := h.svc.EnsurePublisher(c.Request.Context(), userID)
	if err != nil {
		if err.Error() == "user not found: "+userID.String() {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "USER_NOT_FOUND", "User not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to ensure publisher identity", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}
