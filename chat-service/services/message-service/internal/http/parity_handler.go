package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat parity programme (CONTRACT §1-3, §5-7, lane B1, 2026-10-18).

// ParityService is the chat-parity surface the handler needs. The real
// *service.Service implements it; stubs in tests embed ChatService and
// implement only what they exercise.
type ParityService interface {
	ListInbox(ctx context.Context, userID uuid.UUID, lane string, limit int, cursor *service.ConversationCursor) ([]service.ConversationResponse, *service.ConversationCursor, error)
	DeleteConversationForMe(ctx context.Context, userID, convID uuid.UUID) error
	ClearConversationForMe(ctx context.Context, userID, convID uuid.UUID) error
	ListShared(ctx context.Context, userID, convID uuid.UUID, kind string, cursor *service.SharedCursor, limit int) (interface{}, *service.SharedCursor, error)
	SendRich(ctx context.Context, userID, convID uuid.UUID, in service.SendInput) (*service.MessageResponse, error)
	DeleteMessageForMe(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time) error
	LocateMessage(ctx context.Context, convID, msgID uuid.UUID) (string, time.Time, error)
	ForwardMessage(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time, targets []uuid.UUID, idempotencyKey string) (*service.ForwardResponse, error)
	ListReactions(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time) ([]service.ReactionGroup, error)
	ReportMessage(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time, reason, note string) error
	ReadSummaryFor(ctx context.Context, viewerID, convID, msgID uuid.UUID, tsHint *time.Time) (*service.ReadSummary, error)
	ListStickers(ctx context.Context, viewerID uuid.UUID) (*service.StickerCatalogue, error)
	UpsertStickerPackAdmin(ctx context.Context, id uuid.UUID, name string, sortOrder int, active bool) (uuid.UUID, error)
	UpsertStickerAdmin(ctx context.Context, st store.Sticker, active bool) (uuid.UUID, error)
	PutMyNote(ctx context.Context, userID uuid.UUID, text, audience string) (*store.Note, error)
	DeleteMyNote(ctx context.Context, userID uuid.UUID) error
	NotesFeed(ctx context.Context, userID uuid.UUID) (*service.NotesFeed, error)
	SearchChat(ctx context.Context, userID uuid.UUID, q, cursor string) (*service.ChatSearchResult, error)
	ListRequestsFiltered(ctx context.Context, userID uuid.UUID, filter string, limit, offset int) ([]service.RequestRow, error)
	ListRequestsDirected(ctx context.Context, userID uuid.UUID, filter, direction string, limit, offset int) ([]service.RequestRow, error)
	SetTypingState(ctx context.Context, userID, convID uuid.UUID, isTyping bool) error
}

func (h *Handler) parity(c *gin.Context) (ParityService, bool) {
	ps, ok := h.svc.(ParityService)
	if !ok {
		api.Error(c.Writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "not available", nil, nil)
		return nil, false
	}
	return ps, true
}

// registerParityRoutes adds the parity routes to the /v1/chat group and the
// internal sticker admin routes.
func (h *Handler) registerParityRoutes(v1 *gin.RouterGroup, internal *gin.RouterGroup) {
	v1.DELETE("/conversations/:id/for-me", h.DeleteConversationForMe)
	v1.POST("/conversations/:id/clear", h.ClearConversation)
	v1.GET("/conversations/:id/shared", h.ListSharedItems)
	v1.POST("/conversations/:id/messages/:messageId/forward", h.ForwardMessage)
	v1.GET("/conversations/:id/messages/:messageId/reactions", h.ListMessageReactions)
	v1.POST("/conversations/:id/messages/:messageId/report", h.ReportMessage)
	v1.GET("/conversations/:id/read-summary", h.ReadSummary)
	v1.GET("/stickers", h.ListStickers)
	v1.PUT("/notes/me", h.PutMyNote)
	v1.DELETE("/notes/me", h.DeleteMyNote)
	v1.GET("/notes", h.NotesFeed)
	v1.GET("/search", h.SearchChat)
	v1.PUT("/conversations/:id/unread", h.SetConversationUnread)
	v1.POST("/conversations/:id/report", h.ReportConversation)

	internal.POST("/stickers/packs", h.AdminUpsertStickerPack)
	internal.POST("/stickers", h.AdminUpsertSticker)
}

// writeParityError maps the parity sentinels. A membership refusal is a
// 404 so no route confirms a conversation exists.
func writeParityError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrNotMember), errors.Is(err, service.ErrMessageNotFound):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "not found", nil, nil)
	case errors.Is(err, service.ErrNoteRejected):
		api.Error(c.Writer, http.StatusUnprocessableEntity, "NOTE_REJECTED", err.Error(), nil, nil)
	case errors.Is(err, service.ErrInvalidReaction), errors.Is(err, service.ErrInvalidForward),
		errors.Is(err, service.ErrInvalidReport), errors.Is(err, service.ErrInvalidNote),
		errors.Is(err, service.ErrSearchQueryTooShort), errors.Is(err, service.ErrInvalidLane),
		errors.Is(err, service.ErrInvalidSticker):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
	default:
		if writeIdempotencyError(c, err) || writeMessagingError(c, err) {
			return true
		}
		return false
	}
	return true
}

func (h *Handler) internalError(c *gin.Context, msg string, err error) {
	h.log.Error(msg, "err", err, "request_id", RequestIDFromContext(c))
	api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", msg, nil, nil)
}

func parseMessageParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("messageId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

// tsQuery reads the optional ?ts= partition hint (RFC 3339).
func tsQuery(c *gin.Context) (*time.Time, bool) {
	raw := c.Query("ts")
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "ts must be RFC 3339", nil, nil)
		return nil, false
	}
	return &t, true
}

// --- §1.2 / §1.3 ---

func (h *Handler) DeleteConversationForMe(c *gin.Context) {
	h.clearForMe(c, true)
}

func (h *Handler) ClearConversation(c *gin.Context) {
	h.clearForMe(c, false)
}

func (h *Handler) clearForMe(c *gin.Context, hide bool) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	var err error
	if hide {
		err = ps.DeleteConversationForMe(c.Request.Context(), userID, convID)
	} else {
		err = ps.ClearConversationForMe(c.Request.Context(), userID, convID)
	}
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not clear the conversation", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- §1.4 ---

func encodeOpaque(v any) string {
	raw, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeOpaque(raw string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (h *Handler) ListSharedItems(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	kind := c.DefaultQuery("kind", "posts")
	var cursor *service.SharedCursor
	if raw := c.Query("cursor"); raw != "" {
		var cur service.SharedCursor
		if err := decodeOpaque(raw, &cur); err != nil || cur.ID == uuid.Nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil, nil)
			return
		}
		cursor = &cur
	}
	limit := 30
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	items, next, err := ps.ListShared(c.Request.Context(), userID, convID, kind, cursor, limit)
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not list shared items", err)
		return
	}
	var meta *api.Meta
	if next != nil {
		meta = &api.Meta{NextCursor: encodeOpaque(next)}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

// --- §2.3 delete scope ---

// deleteForMe handles DELETE .../messages/:messageId with scope "me".
func (h *Handler) deleteForMe(c *gin.Context, userID, convID, msgID uuid.UUID, ts *time.Time) {
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	if err := ps.DeleteMessageForMe(c.Request.Context(), userID, convID, msgID, ts); err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not delete the message for you", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "message deleted for you"}, nil)
}

// --- §2.4 ---

type ForwardRequest struct {
	ConversationIDs []string   `json:"conversation_ids" binding:"required,min=1,max=5"`
	IdempotencyKey  string     `json:"idempotency_key"`
	Ts              *time.Time `json:"ts"`
}

func (h *Handler) ForwardMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgID, ok := parseMessageParam(c)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	var req ForwardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	targets := make([]uuid.UUID, 0, len(req.ConversationIDs))
	for _, raw := range req.ConversationIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation_ids", nil, nil)
			return
		}
		targets = append(targets, id)
	}
	key := req.IdempotencyKey
	if key == "" {
		key = c.GetHeader("Idempotency-Key")
	}
	resp, err := ps.ForwardMessage(c.Request.Context(), userID, convID, msgID, req.Ts, targets, key)
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not forward the message", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// --- §2.5 ---

func (h *Handler) ListMessageReactions(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgID, ok := parseMessageParam(c)
	if !ok {
		return
	}
	ts, ok := tsQuery(c)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	groups, err := ps.ListReactions(c.Request.Context(), userID, convID, msgID, ts)
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not list reactions", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, groups, nil)
}

// --- §2.6 ---

type ReportMessageRequest struct {
	Reason string     `json:"reason" binding:"required"`
	Note   string     `json:"note" binding:"max=2000"`
	Ts     *time.Time `json:"ts"`
}

func (h *Handler) ReportMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgID, ok := parseMessageParam(c)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	var req ReportMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	if err := ps.ReportMessage(c.Request.Context(), userID, convID, msgID, req.Ts, req.Reason, req.Note); err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not report the message", err)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, map[string]string{"status": "reported"}, nil)
}

// --- §2.9 ---

func (h *Handler) ReadSummary(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgID, err := uuid.Parse(c.Query("message_id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "message_id is required", nil, nil)
		return
	}
	ts, ok := tsQuery(c)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	sum, err := ps.ReadSummaryFor(c.Request.Context(), userID, convID, msgID, ts)
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not read the summary", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, sum, nil)
}

// --- §2.7 ---

func (h *Handler) ListStickers(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	cat, err := ps.ListStickers(c.Request.Context(), userID)
	if err != nil {
		h.internalError(c, "could not list stickers", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, cat, nil)
}

// authorizeServiceOnly is the three-gate check every internal-only route
// uses: key configured (fail closed), no end-user identity on the request,
// the correct internal key.
func (h *Handler) authorizeServiceOnly(c *gin.Context) bool {
	if h.internalServiceKey == "" {
		api.Error(c.Writer, http.StatusServiceUnavailable, "MISCONFIGURED", "internal-only endpoint not configured", nil, nil)
		return false
	}
	if carriesUserIdentity(c) {
		api.Error(c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED", "service-only endpoint; user requests are not accepted", nil, nil)
		return false
	}
	if c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return false
	}
	return true
}

type adminStickerPackRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name" binding:"required"`
	SortOrder int    `json:"sort_order"`
	Active    *bool  `json:"active"`
}

func (h *Handler) AdminUpsertStickerPack(c *gin.Context) {
	if !h.authorizeServiceOnly(c) {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	var req adminStickerPackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	var id uuid.UUID
	if req.ID != "" {
		parsed, err := uuid.Parse(req.ID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid id", nil, nil)
			return
		}
		id = parsed
	}
	active := req.Active == nil || *req.Active
	out, err := ps.UpsertStickerPackAdmin(c.Request.Context(), id, req.Name, req.SortOrder, active)
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not save the sticker pack", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"id": out.String()}, nil)
}

type adminStickerRequest struct {
	ID        string `json:"id"`
	PackID    string `json:"pack_id" binding:"required"`
	MediaID   string `json:"media_id" binding:"required"`
	URL       string `json:"url"`
	Width     int    `json:"width" binding:"required"`
	Height    int    `json:"height" binding:"required"`
	SortOrder int    `json:"sort_order"`
	Active    *bool  `json:"active"`
}

func (h *Handler) AdminUpsertSticker(c *gin.Context) {
	if !h.authorizeServiceOnly(c) {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	var req adminStickerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	st := store.Sticker{URL: strings.TrimSpace(req.URL), Width: req.Width, Height: req.Height, SortOrd: req.SortOrder}
	var err error
	if req.ID != "" {
		if st.ID, err = uuid.Parse(req.ID); err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid id", nil, nil)
			return
		}
	}
	if st.PackID, err = uuid.Parse(req.PackID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid pack_id", nil, nil)
		return
	}
	if st.MediaID, err = uuid.Parse(req.MediaID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid media_id", nil, nil)
		return
	}
	active := req.Active == nil || *req.Active
	out, err := ps.UpsertStickerAdmin(c.Request.Context(), st, active)
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not save the sticker", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"id": out.String()}, nil)
}

// --- §5 ---

type PutNoteRequest struct {
	Text     string `json:"text" binding:"required,max=600"`
	Audience string `json:"audience"`
}

func (h *Handler) PutMyNote(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	var req PutNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	note, err := ps.PutMyNote(c.Request.Context(), userID, req.Text, req.Audience)
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not save the note", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, note, nil)
}

func (h *Handler) DeleteMyNote(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	if err := ps.DeleteMyNote(c.Request.Context(), userID); err != nil {
		h.internalError(c, "could not delete the note", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) NotesFeed(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	feed, err := ps.NotesFeed(c.Request.Context(), userID)
	if err != nil {
		h.internalError(c, "could not load notes", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, feed, nil)
}

// --- §6 ---

func (h *Handler) SearchChat(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	ps, ok := h.parity(c)
	if !ok {
		return
	}
	res, err := ps.SearchChat(c.Request.Context(), userID, c.Query("q"), c.Query("cursor"))
	if err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "search failed", err)
		return
	}
	var meta *api.Meta
	if res.NextCursor != "" {
		meta = &api.Meta{NextCursor: res.NextCursor}
	}
	api.JSON(c.Writer, http.StatusOK, res, meta)
}
