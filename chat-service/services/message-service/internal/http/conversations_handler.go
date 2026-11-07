// Conversation routes: creating direct and group conversations, reading
// one, listing with an opaque cursor, adding and removing members and
// updating the conversation.

package http

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Conversations ---

type CreateDirectConversationRequest struct {
	OtherUserID string `json:"other_user_id" binding:"required"`
}

func (h *Handler) CreateDirectConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req CreateDirectConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	otherID, err := uuid.Parse(req.OtherUserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid other_user_id format", nil, nil)
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")

	conv, err := h.svc.CreateDirectConversation(c.Request.Context(), userID, otherID, idempotencyKey)
	if err != nil {
		h.writeServiceError(c, "failed to create direct conversation", err)
		return
	}
	// Hidden chats: opening a DM the caller hid returns it redacted (no
	// preview, no unread) unless the request holds an open token.
	if !h.redactIfLocked(c, userID, conv) {
		return
	}

	api.JSON(c.Writer, http.StatusOK, conv, nil)
}

type CreateGroupConversationRequest struct {
	Title     string   `json:"title" binding:"required,min=1,max=100"`
	MemberIDs []string `json:"member_ids" binding:"required,min=1"`
}

func (h *Handler) CreateGroupConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req CreateGroupConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	memberIDs := make([]uuid.UUID, 0, len(req.MemberIDs))
	for _, idStr := range req.MemberIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid member_id: "+idStr, nil, nil)
			return
		}
		memberIDs = append(memberIDs, id)
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")

	// Production chat pass: creation resolves every candidate through their
	// who_can_add_to_groups policy. `data` stays the conversation (wire-
	// compatible with existing clients); per-target outcomes ride in `meta`.
	result, err := h.svc.CreateGroupWithPolicy(c.Request.Context(), userID, req.Title, memberIDs, idempotencyKey)
	if err != nil {
		h.writeServiceError(c, "failed to create group conversation", err)
		return
	}

	// Embedding keeps the conversation's fields at the top level of `data`
	// (wire-compatible with pre-pass clients); the per-target outcomes ride
	// alongside as `member_outcomes`.
	api.JSON(c.Writer, http.StatusCreated, struct {
		*service.ConversationResponse
		MemberOutcomes []service.AddOutcome `json:"member_outcomes,omitempty"`
	}{result.Conversation, result.Outcomes}, nil)
}

func (h *Handler) GetConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	conv, err := h.svc.GetConversation(c.Request.Context(), userID, convID)
	if err != nil {
		h.writeServiceError(c, "failed to get conversation", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, conv, nil)
}

func (h *Handler) ListConversations(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	var cursor *service.ConversationCursor
	if raw := c.Query("cursor"); raw != "" {
		cur, err := decodeConvCursor(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil, nil)
			return
		}
		cursor = cur
	}

	// Chat parity §1: ?lane=primary|requests|archived (default primary).
	var convs []service.ConversationResponse
	var next *service.ConversationCursor
	var err error
	if ps, ok := h.svc.(ParityService); ok {
		convs, next, err = ps.ListInbox(c.Request.Context(), userID, c.Query("lane"), limit, cursor)
		if errors.Is(err, service.ErrInvalidLane) {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", err.Error(), nil, nil)
			return
		}
	} else {
		convs, next, err = h.svc.ListConversations(c.Request.Context(), userID, limit, cursor)
	}
	if err != nil {
		h.log.Error("failed to list conversations", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list conversations", nil, nil)
		return
	}

	var meta *api.Meta
	if next != nil {
		if encoded, err := encodeConvCursor(*next); err == nil {
			meta = &api.Meta{NextCursor: encoded}
		}
	}

	api.JSON(c.Writer, http.StatusOK, convs, meta)
}

// --- Member Management ---

type AddMemberRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *Handler) AddMember(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req AddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user_id format", nil, nil)
		return
	}

	outcome, err := h.svc.AddMemberWithPolicy(c.Request.Context(), userID, convID, targetID)
	if err != nil {
		h.writeServiceError(c, "failed to add member", err)
		return
	}

	// Honest outcome: added, invited (consent pending) or skipped (privacy).
	api.JSON(c.Writer, http.StatusOK, outcome, nil)
}

func (h *Handler) RemoveMember(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	targetIDStr := c.Param("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user ID format", nil, nil)
		return
	}

	if err := h.svc.RemoveMemberGoverned(c.Request.Context(), userID, convID, targetID); err != nil {
		h.writeServiceError(c, "failed to remove member", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "member removed"}, nil)
}

type UpdateConversationRequest struct {
	Title         *string `json:"title" binding:"omitempty,min=1,max=100"`
	AvatarMediaID *string `json:"avatar_media_id" binding:"omitempty,uuid"`
	// Description is the group "about" text; "" clears it. Max 300 runes
	// (enforced in the service so multi-byte text counts characters).
	Description *string `json:"description" binding:"omitempty,max=1200"`
}

func (h *Handler) UpdateConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req UpdateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	if req.Title == nil && req.AvatarMediaID == nil && req.Description == nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Nothing to update", nil, nil)
		return
	}
	var avatarID *uuid.UUID
	if req.AvatarMediaID != nil {
		parsed, err := uuid.Parse(*req.AvatarMediaID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid avatar_media_id", nil, nil)
			return
		}
		avatarID = &parsed
	}

	if err := h.svc.UpdateGroupInfoGoverned(c.Request.Context(), userID, convID, req.Title, avatarID, req.Description); err != nil {
		h.writeServiceError(c, "failed to update conversation", err)
		return
	}

	// Chat-app pass: return the updated conversation (description, avatar
	// URL resolved for the caller) instead of a bare status.
	conv, err := h.svc.GetConversation(c.Request.Context(), userID, convID)
	if err != nil {
		api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, conv, nil)
}

// --- Cursor encoding ---

type convCursorPayload struct {
	Ts time.Time `json:"ts"`
	ID string    `json:"id"`
}

func encodeConvCursor(cur service.ConversationCursor) (string, error) {
	payload := convCursorPayload{Ts: cur.UpdatedAt, ID: cur.ID.String()}
	raw, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeConvCursor(raw string) (*service.ConversationCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var payload convCursorPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(payload.ID)
	if err != nil {
		return nil, err
	}
	return &service.ConversationCursor{UpdatedAt: payload.Ts, ID: id}, nil
}
