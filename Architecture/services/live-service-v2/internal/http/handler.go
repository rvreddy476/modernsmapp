// Package http exposes the live-service-v2 REST surface. All v1
// endpoints sit behind the shared X-Internal-Service-Key middleware —
// every client goes through api-gateway. The single exception is
// /v1/live/egress/webhook, which is invoked directly by the LiveKit
// Egress service and authenticated by an HMAC-signed payload.
package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
)

type Handler struct {
	svc             *service.Service
	internalKey     string
	egressSecret    string // HMAC secret used to verify LiveKit Egress webhooks
}

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) WithEgressSecret(secret string) *Handler {
	h.egressSecret = secret
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Webhook is intentionally registered OUTSIDE the internal-key
	// group: LiveKit signs with HMAC instead of carrying our internal
	// service key. The handler verifies the signature in-line.
	r.POST("/v1/live/egress/webhook", h.OnEgressWebhook)

	v1 := r.Group("/v1/live")
	if h.internalKey != "" {
		v1.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	v1.POST("/streams", h.CreateStream)
	v1.POST("/streams/:id/start", h.StartStream)
	v1.POST("/streams/:id/end", h.EndStream)
	v1.GET("/streams/:id", h.GetStream)
	v1.GET("/streams/:id/viewer-token", h.IssueViewerToken)
	v1.GET("/streams", h.ListLiveNow)
}

// --- request / response bodies ---

type createStreamRequest struct {
	Title        string     `json:"title" binding:"required"`
	Description  string     `json:"description"`
	Visibility   string     `json:"visibility"`
	CoverMediaID *uuid.UUID `json:"cover_media_id"`
	ScheduledAt  *time.Time `json:"scheduled_at"`
}

// --- handlers ---

func (h *Handler) CreateStream(c *gin.Context) {
	creatorID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req createStreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	stream, err := h.svc.CreateStream(c.Request.Context(), creatorID, service.CreateStreamParams{
		Title:        req.Title,
		Description:  req.Description,
		Visibility:   req.Visibility,
		CoverMediaID: req.CoverMediaID,
		ScheduledAt:  req.ScheduledAt,
	})
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, stream, nil)
}

func (h *Handler) StartStream(c *gin.Context) {
	creatorID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	res, err := h.svc.StartStream(c.Request.Context(), streamID, creatorID)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

func (h *Handler) EndStream(c *gin.Context) {
	creatorID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.EndStream(c.Request.Context(), streamID, creatorID); err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ended"}, nil)
}

func (h *Handler) GetStream(c *gin.Context) {
	viewerID := optionalUserID(c)
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	st, err := h.svc.GetStream(c.Request.Context(), streamID, viewerID)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

func (h *Handler) IssueViewerToken(c *gin.Context) {
	viewerID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	res, err := h.svc.IssueViewerToken(c.Request.Context(), streamID, viewerID)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

func (h *Handler) ListLiveNow(c *gin.Context) {
	viewerID := optionalUserID(c)
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	cursor := c.Query("cursor")
	res, err := h.svc.ListLiveNow(c.Request.Context(), viewerID, limit, cursor)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	meta := &api.Meta{}
	if res.NextCursor != "" {
		meta.NextCursor = res.NextCursor
	}
	api.JSON(c.Writer, http.StatusOK, res.Streams, meta)
}

// LiveKit Egress webhook payload — we read only the fields we care
// about (event, file URL, duration) and ignore the rest.
//
// Egress webhook payload (relevant fields):
//
//	{
//	  "event":  "egress_ended" | "participant_joined" | "participant_left",
//	  "egress_info": { "room_name": "stream_<uuid>", "file": { "location": "...", "duration": ms } },
//	  "participant": { "identity": "<viewer-uuid>" },
//	  "room": { "name": "stream_<uuid>" }
//	}
type egressWebhookPayload struct {
	Event      string `json:"event"`
	EgressInfo *struct {
		EgressID string `json:"egress_id"`
		RoomName string `json:"room_name"`
		File     *struct {
			Location string `json:"location"`
			Duration int64  `json:"duration"` // nanoseconds per LiveKit spec
		} `json:"file"`
	} `json:"egress_info"`
	Room *struct {
		Name string `json:"name"`
	} `json:"room"`
	Participant *struct {
		Identity string `json:"identity"`
	} `json:"participant"`
}

// OnEgressWebhook handles three LiveKit webhook event types:
//
//   - egress_ended       — VOD ready; persist URL + duration, fire vod_ready
//   - participant_joined — bump the Redis hot-counter for the stream
//   - participant_left   — decrement the counter
//
// Verifies the LiveKit-signed body via HMAC if h.egressSecret is set.
func (h *Handler) OnEgressWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if h.egressSecret != "" {
		sig := c.GetHeader("X-LiveKit-Signature")
		if !verifyHMAC(body, sig, h.egressSecret) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid signature", nil)
			return
		}
	}
	var p egressWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	switch p.Event {
	case "egress_ended":
		if p.EgressInfo == nil {
			c.Status(http.StatusOK)
			return
		}
		streamID, ok := streamIDFromRoom(p.EgressInfo.RoomName)
		if !ok {
			c.Status(http.StatusOK)
			return
		}
		recordingURL := ""
		durationSec := 0
		if p.EgressInfo.File != nil {
			recordingURL = p.EgressInfo.File.Location
			// LiveKit reports duration in nanoseconds.
			durationSec = int(p.EgressInfo.File.Duration / 1_000_000_000)
		}
		if err := h.svc.OnEgressFinished(c.Request.Context(), streamID, recordingURL, durationSec); err != nil {
			slog.Warn("live-v2 egress webhook: persist failed", "stream_id", streamID, "err", err)
		}
	case "participant_joined", "participant_left":
		if p.Room == nil || p.Participant == nil {
			c.Status(http.StatusOK)
			return
		}
		streamID, ok := streamIDFromRoom(p.Room.Name)
		if !ok {
			c.Status(http.StatusOK)
			return
		}
		userID, err := uuid.Parse(p.Participant.Identity)
		if err != nil {
			c.Status(http.StatusOK)
			return
		}
		evt := "join"
		if p.Event == "participant_left" {
			evt = "leave"
		}
		if err := h.svc.RecordParticipantEvent(c.Request.Context(), streamID, userID, evt); err != nil {
			slog.Warn("live-v2 participant webhook: persist failed", "stream_id", streamID, "err", err)
		}
	}
	c.Status(http.StatusOK)
}

// --- helpers ---

func requireUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid X-User-Id", nil)
		return uuid.Nil, false
	}
	return id, true
}

func optionalUserID(c *gin.Context) uuid.UUID {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		return uuid.Nil
	}
	return id
}

func requireUUID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+name, nil)
		return uuid.Nil, false
	}
	return id, true
}

func writeServiceErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrStreamNotFound):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrNotCreator):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
	case errors.Is(err, service.ErrNotFollower):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_FOLLOWER", err.Error(), nil)
	case errors.Is(err, service.ErrPaidNotSupported):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusPaymentRequired, "PAID_REQUIRED", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidVisibility), errors.Is(err, service.ErrInvalidTitle):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

// streamIDFromRoom unpacks "stream_<uuid>" → uuid.
func streamIDFromRoom(room string) (uuid.UUID, bool) {
	const prefix = "stream_"
	if len(room) <= len(prefix) || room[:len(prefix)] != prefix {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(room[len(prefix):])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func verifyHMAC(body []byte, sig, secret string) bool {
	if sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}
