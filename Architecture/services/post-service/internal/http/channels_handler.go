package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Tube channels (2026-09-05). One channel per account, owned by post-service.
//
//	POST  /v1/channels                    create the caller's channel
//	GET   /v1/channels/me                 the caller's channel (404 NO_CHANNEL)
//	PATCH /v1/channels/me                 any subset of name/handle/about/avatar_media_id
//	GET   /v1/channels/handle-available   ?handle= -> {available, suggestion}
//	GET   /v1/channels/batch              ?user_ids=a,b -> {user_id: channel ref} (feed hydration)
//	GET   /v1/channels/search             ?q=&limit= -> {"data":[channel…]} (Tube search page)
//	GET   /v1/channels/:ref               public channel by handle or user id

const maxChannelBatch = 100

func (h *Handler) registerChannelRoutes(r *gin.Engine) {
	channels := r.Group("/v1/channels")
	{
		channels.POST("", h.CreateChannel)
		channels.GET("/me", h.GetMyChannel)
		channels.PATCH("/me", h.UpdateMyChannel)
		channels.GET("/handle-available", h.ChannelHandleAvailable)
		channels.GET("/batch", h.GetChannelsBatch)
		channels.GET("/search", h.SearchChannels)
		channels.GET("/:ref", h.GetChannelByRef)
	}
}

// SearchChannels handles GET /v1/channels/search?q=&limit=.
//
// q: trimmed, lowercased, a leading '@' dropped; at least 1 character
// (400 INVALID_QUERY otherwise). Matches handle prefix or name substring.
// limit: default 20, max 50. Rows are the GET /v1/channels/{handle} JSON,
// handle-prefix matches first, then by video_count desc.
func (h *Handler) SearchChannels(c *gin.Context) {
	viewerID, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a non-negative integer", nil)
			return
		}
		limit = n
	}
	views, err := h.svc.SearchChannels(c.Request.Context(), viewerID, c.Query("q"), limit)
	if err != nil {
		if errors.Is(err, service.ErrEmptyChannelQuery) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if views == nil {
		views = []*service.ChannelView{}
	}
	api.JSON(c.Writer, http.StatusOK, views, nil)
}

type createChannelRequest struct {
	Name          string  `json:"name"`
	Handle        string  `json:"handle"`
	About         string  `json:"about"`
	AvatarMediaID *string `json:"avatar_media_id"`
}

// updateChannelRequest keeps avatar_media_id raw so the handler can tell
// "absent" (leave it) from `null` (clear it) from a new id.
type updateChannelRequest struct {
	Name          *string         `json:"name"`
	Handle        *string         `json:"handle"`
	About         *string         `json:"about"`
	AvatarMediaID json.RawMessage `json:"avatar_media_id"`
}

// writeChannelError maps the channel flows' typed errors. Returns false when
// the error is not one of them.
func writeChannelError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidChannelName):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_NAME", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidHandle):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_HANDLE", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidChannelAbout):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ABOUT", err.Error(), nil)
	case errors.Is(err, postgres.ErrChannelExists):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "CHANNEL_EXISTS", "This account already has a channel", nil)
	case errors.Is(err, postgres.ErrHandleTaken):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "HANDLE_TAKEN", "That handle is taken", nil)
	case errors.Is(err, postgres.ErrChannelOwnerUnknown):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "UNKNOWN_OWNER", "Account is not provisioned for channels yet", nil)
	default:
		return false
	}
	return true
}

func channelCaller(c *gin.Context) (uuid.UUID, bool) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return userID, true
}

// CreateChannel handles POST /v1/channels.
func (h *Handler) CreateChannel(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	var req createChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	in := service.CreateChannelInput{Name: req.Name, Handle: req.Handle, About: req.About}
	if req.AvatarMediaID != nil && strings.TrimSpace(*req.AvatarMediaID) != "" {
		id, err := uuid.Parse(*req.AvatarMediaID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid avatar_media_id", nil)
			return
		}
		in.AvatarMediaID = &id
	}
	view, err := h.svc.CreateChannel(c.Request.Context(), userID, in)
	if err != nil {
		if writeChannelError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, view, nil)
}

// GetMyChannel handles GET /v1/channels/me.
func (h *Handler) GetMyChannel(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	view, err := h.svc.GetMyChannel(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, service.ErrChannelNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NO_CHANNEL", "Create your channel before posting a video", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// UpdateMyChannel handles PATCH /v1/channels/me.
func (h *Handler) UpdateMyChannel(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	var req updateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	in := service.UpdateChannelInput{Name: req.Name, Handle: req.Handle, About: req.About}
	if raw := bytes.TrimSpace(req.AvatarMediaID); len(raw) > 0 {
		if bytes.Equal(raw, []byte("null")) {
			in.ClearAvatar = true
		} else {
			var idStr string
			if err := json.Unmarshal(raw, &idStr); err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid avatar_media_id", nil)
				return
			}
			if strings.TrimSpace(idStr) == "" {
				in.ClearAvatar = true
			} else {
				id, err := uuid.Parse(idStr)
				if err != nil {
					api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid avatar_media_id", nil)
					return
				}
				in.AvatarMediaID = &id
			}
		}
	}
	view, err := h.svc.UpdateMyChannel(c.Request.Context(), userID, in)
	if err != nil {
		if writeChannelError(c, err) {
			return
		}
		if errors.Is(err, service.ErrChannelNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NO_CHANNEL", "Create your channel before posting a video", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// ChannelHandleAvailable handles GET /v1/channels/handle-available?handle=
// (optionally &name= as the seed when no handle is given).
func (h *Handler) ChannelHandleAvailable(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	available, suggestion, err := h.svc.ChannelHandleAvailability(c.Request.Context(), userID, c.Query("handle"), c.Query("name"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"available": available, "suggestion": suggestion}, nil)
}

// GetChannelsBatch handles GET /v1/channels/batch?user_ids=a,b — the feed's
// one-call-per-page channel resolution. Unknown ids are simply absent.
func (h *Handler) GetChannelsBatch(c *gin.Context) {
	viewerID, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	raw := strings.Split(c.Query("user_ids"), ",")
	ids := make([]uuid.UUID, 0, len(raw))
	seen := make(map[uuid.UUID]bool, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid user id: "+part, nil)
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > maxChannelBatch {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "At most 100 user ids per batch", nil)
		return
	}
	refs, err := h.svc.ChannelRefsForUsers(c.Request.Context(), viewerID, ids)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	data := make(map[string]*service.ChannelRef, len(refs))
	for id, ref := range refs {
		data[id.String()] = ref
	}
	api.JSON(c.Writer, http.StatusOK, data, nil)
}

// GetChannelByRef handles GET /v1/channels/:ref (handle, @handle or user id).
func (h *Handler) GetChannelByRef(c *gin.Context) {
	viewerID, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	view, err := h.svc.GetChannelByRef(c.Request.Context(), viewerID, c.Param("ref"))
	if err != nil {
		if errors.Is(err, service.ErrChannelNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}
