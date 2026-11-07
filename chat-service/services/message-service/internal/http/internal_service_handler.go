// Service-to-service endpoints under /internal/v1/chat that other
// services call with the internal key: chat media access checks and
// the managed-group create, add-member and remove-member routes.

package http

import (
	"net/http"

	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type chatMediaAccessRequest struct {
	ViewerID string `json:"viewer_id" binding:"required"`
	MediaID  string `json:"media_id" binding:"required"`
	// Hidden chats: media that rides only conversations the viewer hid needs
	// the viewer's open token. media-service forwards the `chat_open` query
	// parameter (an <img>/<video> URL cannot send headers) as open_token and
	// the viewer's access-token session id as session_id (or the client's
	// X-Device-Id as device_id), so the device binding is checked. Never
	// logged on either side.
	OpenToken string `json:"open_token,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
}

func (h *Handler) ChatMediaAccess(c *gin.Context) {
	if !h.authorizeManagedGroup(c) {
		return
	}
	var body chatMediaAccessRequest
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"allowed": false})
		return
	}
	viewerID, viewerErr := uuid.Parse(body.ViewerID)
	mediaID, mediaErr := uuid.Parse(body.MediaID)
	if viewerErr != nil || mediaErr != nil {
		c.JSON(http.StatusForbidden, gin.H{"allowed": false})
		return
	}
	allowed, err := h.svc.ViewerMayAccessChatMedia(c.Request.Context(), viewerID, mediaID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"allowed": false})
		return
	}
	if !allowed {
		if allowed, err = h.lockedChatMediaAllowed(c, viewerID, mediaID, body); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"allowed": false})
			return
		}
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"allowed": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"allowed": true})
}

type managedGroupConversationRequest struct {
	CreatorUserID string `json:"creator_user_id" binding:"required"`
	GroupID       string `json:"group_id" binding:"required"`
	Title         string `json:"title" binding:"required"`
}

func (h *Handler) CreateManagedGroupConversation(c *gin.Context) {
	if !h.authorizeManagedGroup(c) {
		return
	}
	var body managedGroupConversationRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid managed group request", nil, nil)
		return
	}
	// Reparse after binding; the helper only performs the internal-key check.
	creatorID, err := uuid.Parse(body.CreatorUserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid creator_user_id", nil, nil)
		return
	}
	groupID, err := uuid.Parse(body.GroupID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid group_id", nil, nil)
		return
	}
	conversation, err := h.svc.CreateGroupConversation(c.Request.Context(), creatorID, body.Title, []uuid.UUID{creatorID}, "managed-group-"+groupID.String())
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "managed group conversation failed", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, conversation, nil)
}

func (h *Handler) authorizeManagedGroup(c *gin.Context) bool {
	if h.internalServiceKey == "" || c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return false
	}
	return true
}

type managedMemberRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *Handler) AddManagedGroupMember(c *gin.Context) {
	if !h.authorizeManagedGroup(c) {
		return
	}
	conversationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid conversation id", nil, nil)
		return
	}
	var body managedMemberRequest
	if c.ShouldBindJSON(&body) != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid managed member request", nil, nil)
		return
	}
	userID, err := uuid.Parse(body.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user_id", nil, nil)
		return
	}
	if err := h.svc.ManagedAddGroupMember(c.Request.Context(), conversationID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "managed member add failed", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "member added"}, nil)
}

func (h *Handler) RemoveManagedGroupMember(c *gin.Context) {
	if !h.authorizeManagedGroup(c) {
		return
	}
	conversationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid conversation id", nil, nil)
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user id", nil, nil)
		return
	}
	if err := h.svc.ManagedRemoveGroupMember(c.Request.Context(), conversationID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "managed member removal failed", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "member removed"}, nil)
}
