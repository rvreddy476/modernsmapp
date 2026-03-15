package http

import (
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterSlotRoutes registers the media slot endpoints.
func (h *Handler) RegisterSlotRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	slots := r.Group("/v1/media/slots")
	{
		// Write endpoints — require authentication
		slots.PUT("/:ownerType/:ownerId/:slotName", authMW, h.AssignSlot)
		slots.DELETE("/:ownerType/:ownerId/:slotName", authMW, h.RemoveSlot)

		// Read endpoints — public
		slots.GET("/:ownerType/:ownerId", h.GetOwnerSlots)
		slots.POST("/batch", h.GetOwnerSlotsBatch)
	}
}

type assignSlotRequest struct {
	MediaAssetID string              `json:"media_asset_id" binding:"required"`
	Crop         *service.CropParams `json:"crop"`
}

func (h *Handler) AssignSlot(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	ownerType := c.Param("ownerType")
	ownerID := c.Param("ownerId")
	slotName := c.Param("slotName")

	var req assignSlotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	mediaAssetID, err := uuid.Parse(req.MediaAssetID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media_asset_id", nil, nil)
		return
	}

	err = h.svc.AssignSlot(c.Request.Context(), userID, ownerType, ownerID, slotName, mediaAssetID, req.Crop)
	if err != nil {
		switch {
		case containsAny(err.Error(), "invalid owner_type", "invalid slot_name", "invalid owner_id", "not available"):
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		case containsAny(err.Error(), "forbidden"):
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
		case containsAny(err.Error(), "not found"):
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "assigned"}, nil)
}

func (h *Handler) RemoveSlot(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	ownerType := c.Param("ownerType")
	ownerID := c.Param("ownerId")
	slotName := c.Param("slotName")

	err = h.svc.RemoveSlot(c.Request.Context(), userID, ownerType, ownerID, slotName)
	if err != nil {
		if containsAny(err.Error(), "no rows", "not found") {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Slot not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) GetOwnerSlots(c *gin.Context) {
	ownerType := c.Param("ownerType")
	ownerIDStr := c.Param("ownerId")

	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid owner ID", nil, nil)
		return
	}

	slots, err := h.svc.GetOwnerMedia(c.Request.Context(), ownerType, ownerID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if slots == nil {
		slots = []postgres.ResolvedSlot{}
	}

	api.JSON(c.Writer, http.StatusOK, slots, nil)
}

type batchOwnersRequest struct {
	Owners []postgres.OwnerRef `json:"owners" binding:"required,min=1,max=100"`
}

func (h *Handler) GetOwnerSlotsBatch(c *gin.Context) {
	var req batchOwnersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	result, err := h.svc.GetOwnerMediaBatch(c.Request.Context(), req.Owners)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if result == nil {
		result = make(map[string][]postgres.ResolvedSlot)
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// containsAny checks if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
