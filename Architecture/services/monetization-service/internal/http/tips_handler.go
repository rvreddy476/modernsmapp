package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SendTip — POST /v1/monetization/tips
// Body:
//
//	{
//	  "recipient_id": "<uuid>",
//	  "amount_paise": 500,
//	  "message": "great video",
//	  "post_id": "<uuid>"   // optional, mutually exclusive with stream_id
//	  "stream_id": "<uuid>" // optional
//	}
//
// Sender is taken from X-User-Id. Wallet charge is atomic; on
// failure (insufficient balance, frozen wallet) the response surfaces
// the error code and no rows are written.
func (h *Handler) SendTip(c *gin.Context) {
	senderID, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		RecipientID string     `json:"recipient_id" binding:"required"`
		AmountPaise int64      `json:"amount_paise" binding:"required"`
		Message     string     `json:"message"`
		PostID      *uuid.UUID `json:"post_id,omitempty"`
		StreamID    *uuid.UUID `json:"stream_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	recipientID, err := uuid.Parse(body.RecipientID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "recipient_id must be a UUID", nil)
		return
	}

	in := service.SendTipInput{
		SenderID:    senderID,
		RecipientID: recipientID,
		AmountPaise: body.AmountPaise,
		Message:     body.Message,
		PostID:      body.PostID,
		StreamID:    body.StreamID,
	}
	res, err := h.svc.SendTip(c.Request.Context(), in)
	if err != nil {
		// Validation errors → 400; daily cap → 429; charge failures →
		// 402; everything else → 500.
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"
		switch {
		case startsWith(err.Error(), "INVALID_", "AMOUNT_", "CANNOT_", "MESSAGE_"):
			status, code = http.StatusBadRequest, "INVALID_REQUEST"
		case startsWith(err.Error(), "DAILY_TIP_CAP"):
			status, code = http.StatusTooManyRequests, "DAILY_TIP_CAP_EXCEEDED"
		case startsWith(err.Error(), "charge failed"):
			status, code = http.StatusPaymentRequired, "CHARGE_FAILED"
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, res, nil)
}

// ListSentTips — GET /v1/monetization/tips/sent?cursor=&limit=
func (h *Handler) ListSentTips(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	cursor := c.Query("cursor")
	limit := tipsLimit(c.Query("limit"))
	tips, err := h.svc.ListSentTips(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tips == nil {
		tips = []postgres.Tip{}
	}
	api.JSON(c.Writer, http.StatusOK, tips, nextTipsCursor(tips, limit))
}

// ListReceivedTips — GET /v1/monetization/tips/received?cursor=&limit=
func (h *Handler) ListReceivedTips(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	cursor := c.Query("cursor")
	limit := tipsLimit(c.Query("limit"))
	tips, err := h.svc.ListReceivedTips(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tips == nil {
		tips = []postgres.Tip{}
	}
	api.JSON(c.Writer, http.StatusOK, tips, nextTipsCursor(tips, limit))
}

// ListTipsForPost — GET /v1/monetization/tips/post/:postId
// Public; the post author already chose to allow tips. Used by the
// "supporters wall" UI on a tipped post.
func (h *Handler) ListTipsForPost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	cursor := c.Query("cursor")
	limit := tipsLimit(c.Query("limit"))
	tips, err := h.svc.ListTipsForPost(c.Request.Context(), postID, cursor, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tips == nil {
		tips = []postgres.Tip{}
	}
	api.JSON(c.Writer, http.StatusOK, tips, nextTipsCursor(tips, limit))
}

func tipsLimit(s string) int {
	if s == "" {
		return 50
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}

func nextTipsCursor(tips []postgres.Tip, limit int) *api.Meta {
	if len(tips) < limit {
		return nil
	}
	return &api.Meta{NextCursor: tips[len(tips)-1].CreatedAt.Format(time.RFC3339Nano)}
}

func startsWith(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}
