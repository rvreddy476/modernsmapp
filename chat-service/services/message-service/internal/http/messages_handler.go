// Message routes: sending (with reply previews), paged history with an
// opaque cursor, editing, deleting and toggling reactions.

package http

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Messages ---

// replyPreviewMaxRunes bounds the stored quote snapshot; longer input is
// truncated, not rejected — the preview is display data.
const replyPreviewMaxRunes = 140

type SendMessageRequest struct {
	// Keep oneof in step with service.KnownMessageTypes (a test pins it).
	Type    string     `json:"type" binding:"required,oneof=text media post voice sticker gif profile sound live location"`
	Text    string     `json:"text" binding:"max=2000"`
	MediaID *uuid.UUID `json:"media_id"`
	// PostID is the shared post on a "post" card ("Send to friends"). Text
	// is then an optional caption under the same 2000-character cap. A
	// malformed UUID fails binding (400).
	PostID *uuid.UUID `json:"post_id"`
	// Quoted reply. The preview is a display snapshot of the quoted message
	// as the sender saw it; the server truncates rather than rejects.
	ReplyToID       *uuid.UUID `json:"reply_to_id"`
	ReplyToPreview  string     `json:"reply_to_preview" binding:"max=500"`
	ReplyToSenderID *uuid.UUID `json:"reply_to_sender_id"`

	// Chat parity §2.1 (additive). A malformed UUID fails binding (400).
	ReplyNoteID *uuid.UUID      `json:"reply_note_id"`
	DurationMs  int             `json:"duration_ms"`
	Waveform    []int           `json:"waveform" binding:"max=64"`
	StickerID   *uuid.UUID      `json:"sticker_id"`
	GIF         *service.GIFRef `json:"gif"`
	UserID      *uuid.UUID      `json:"user_id"`
	SoundID     *uuid.UUID      `json:"sound_id"`
	StreamID    *uuid.UUID      `json:"stream_id"`
	// Location is a shared place (23 Oct 2026): {lat, lng, accuracy_m?,
	// label?, address?}, validated and rounded by the service. A non-number
	// fails binding (400); the value is never logged.
	Location *service.LocationInput `json:"location"`
}

func (h *Handler) SendMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	if req.Type == "text" && req.Text == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Text is required for text messages", nil, nil)
		return
	}
	if req.Type == "media" && req.MediaID == nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "media_id is required for media messages", nil, nil)
		return
	}
	if req.Type == service.MessageTypePost {
		if req.PostID == nil || *req.PostID == uuid.Nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "post_id is required for post messages", nil, nil)
			return
		}
		if req.MediaID != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "media_id is not allowed on post messages", nil, nil)
			return
		}
	} else if req.PostID != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "post_id is only allowed on post messages", nil, nil)
		return
	}
	if req.ReplyNoteID != nil && req.ReplyToID != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "reply_note_id and reply_to_id are exclusive", nil, nil)
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")

	var replyTo *service.ReplyRef
	if req.ReplyToID != nil {
		preview := req.ReplyToPreview
		if runes := []rune(preview); len(runes) > replyPreviewMaxRunes {
			preview = string(runes[:replyPreviewMaxRunes])
		}
		replyTo = &service.ReplyRef{ID: *req.ReplyToID, Preview: preview}
		if req.ReplyToSenderID != nil {
			replyTo.SenderID = *req.ReplyToSenderID
		}
	}

	var msg *service.MessageResponse
	var err error
	if ps, ok := h.svc.(ParityService); ok {
		msg, err = ps.SendRich(c.Request.Context(), userID, convID, service.SendInput{
			Type: req.Type, Text: req.Text, MediaID: req.MediaID, PostID: req.PostID, ReplyTo: replyTo,
			IdempotencyKey: idempotencyKey, ReplyNoteID: req.ReplyNoteID, DurationMs: req.DurationMs,
			Waveform: req.Waveform, StickerID: req.StickerID, GIF: req.GIF, ProfileUserID: req.UserID,
			SoundID: req.SoundID, StreamID: req.StreamID, Location: req.Location,
		})
	} else {
		msg, err = h.svc.SendMessage(c.Request.Context(), userID, convID, req.Type, req.Text, req.MediaID, req.PostID, replyTo, idempotencyKey)
	}
	if err != nil {
		h.writeServiceError(c, "failed to send message", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msg, nil)
}

func (h *Handler) GetMessages(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	limit := 30
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	var cursor *scylla.MessageCursor
	if raw := c.Query("cursor"); raw != "" {
		cur, err := decodeMsgCursor(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil, nil)
			return
		}
		cursor = cur
	}

	msgs, nextCursor, err := h.svc.GetMessages(c.Request.Context(), userID, convID, cursor, limit)
	if err != nil {
		h.writeServiceError(c, "failed to get messages", err)
		return
	}

	var meta *api.Meta
	if nextCursor != nil {
		if encoded, err := encodeMsgCursor(*nextCursor); err == nil {
			meta = &api.Meta{NextCursor: encoded}
		}
	}

	api.JSON(c.Writer, http.StatusOK, msgs, meta)
}

// DeleteMessageRequest identifies the row to act on.
//
// `bucket` is derivable from `ts` (it is the YYYYMM the message was written
// in), so it is optional: the web sends only a timestamp, and requiring the
// bucket made every delete from the web a 400.
type DeleteMessageRequest struct {
	Bucket string `json:"bucket"`
	// Ts is optional since chat parity: without it the message is located
	// from its durable delivery record.
	Ts time.Time `json:"ts"`
	// Scope (chat parity §2.3): "everyone" (default, unsend) or "me".
	Scope string `json:"scope" binding:"omitempty,oneof=everyone me"`
}

// EditMessageRequest is DeleteMessageRequest plus the replacement text.
// `timestamp` is accepted as an alias for `ts` because that is the field
// name the web client has always sent.
type EditMessageRequest struct {
	// Same cap as send (H2/H4); the service enforces it too.
	Text      string     `json:"text" binding:"required,max=2000"`
	Bucket    string     `json:"bucket"`
	Ts        *time.Time `json:"ts"`
	Timestamp *time.Time `json:"timestamp"`
}

// bucketFor returns the explicit bucket, or derives it from the timestamp.
// Messages are partitioned by the UTC month they were written in.
func bucketFor(bucket string, ts time.Time) string {
	if bucket != "" {
		return bucket
	}
	return ts.UTC().Format("200601")
}

// EditMessage handles PATCH /conversations/:id/messages/:messageId.
func (h *Handler) EditMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgID, err := uuid.Parse(c.Param("messageId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	var req EditMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	ts := req.Ts
	if ts == nil {
		ts = req.Timestamp
	}
	if ts == nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "ts (or timestamp) is required", nil, nil)
		return
	}

	msg, err := h.svc.EditMessage(c.Request.Context(), userID, convID, msgID, bucketFor(req.Bucket, *ts), *ts, req.Text)
	if err != nil {
		h.writeServiceError(c, "edit message failed", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msg, nil)
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgIDStr := c.Param("messageId")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	var req DeleteMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	if req.Scope == "me" {
		var ts *time.Time
		if !req.Ts.IsZero() {
			ts = &req.Ts
		}
		h.deleteForMe(c, userID, convID, msgID, ts)
		return
	}
	if req.Ts.IsZero() {
		ps, ok := h.svc.(ParityService)
		if !ok {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "ts is required", nil, nil)
			return
		}
		bucket, ts, err := ps.LocateMessage(c.Request.Context(), convID, msgID)
		if err != nil {
			if writeParityError(c, err) {
				return
			}
			h.internalError(c, "could not locate the message", err)
			return
		}
		req.Bucket, req.Ts = bucket, ts
	}

	if err := h.svc.DeleteMessage(c.Request.Context(), userID, convID, msgID, bucketFor(req.Bucket, req.Ts), req.Ts); err != nil {
		h.writeServiceError(c, "failed to delete message", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "message deleted"}, nil)
}

// --- Reactions ---

type ToggleReactionRequest struct {
	Emoji  string    `json:"emoji" binding:"required"`
	Bucket string    `json:"bucket" binding:"required"`
	Ts     time.Time `json:"ts" binding:"required"`
}

func (h *Handler) ToggleReaction(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgIDStr := c.Param("messageId")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	var req ToggleReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	result, err := h.svc.ToggleReaction(c.Request.Context(), userID, convID, msgID, req.Bucket, req.Ts, req.Emoji)
	if err != nil {
		h.writeServiceError(c, "failed to toggle reaction", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

type msgCursorPayload struct {
	Bucket string    `json:"b"`
	Ts     time.Time `json:"t"`
	MsgID  string    `json:"m"`
}

func encodeMsgCursor(cur scylla.MessageCursor) (string, error) {
	payload := msgCursorPayload{Bucket: cur.Bucket, Ts: cur.Ts, MsgID: cur.MsgID.String()}
	raw, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeMsgCursor(raw string) (*scylla.MessageCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var payload msgCursorPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, err
	}
	msgID, err := uuid.Parse(payload.MsgID)
	if err != nil {
		return nil, err
	}
	return &scylla.MessageCursor{Bucket: payload.Bucket, Ts: payload.Ts, MsgID: msgID}, nil
}
