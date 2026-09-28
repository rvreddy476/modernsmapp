package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// End screens and cards that viewers see (2026-09-29; the rules and shapes
// are in service/end_screens.go):
//
//	POST /v1/posts/:postId/end-screens                      owner; 401 / 400 / 404 / 403 / 422 END_SCREEN_*
//	GET  /v1/posts/:postId/end-screens                      the detail's gate: 404 / 401 / 403 AGE_*
//	POST /v1/posts/:postId/end-screens/:elementId/impression   204; 404 unknown element
//	POST /v1/posts/:postId/end-screens/:elementId/click        204
//	POST /v1/posts/:postId/cards                            owner; 422 CARD_*
//	GET  /v1/posts/:postId/cards
//	POST /v1/posts/:postId/cards/:cardId/impression            204
//	POST /v1/posts/:postId/cards/:cardId/click                 204
//
// Shape checks answer 400 here, before the service: an unknown type or
// video_mode, a target_id or id that is not a uuid, a title over 60 runes.
// Everything the contract numbers is a 422 from the service, in its order.

type endScreenInput struct {
	// ID is optional: the editor may echo an element's id to keep its stats.
	ID        *string         `json:"id,omitempty"`
	Type      string          `json:"type" binding:"required"`
	VideoMode string          `json:"video_mode,omitempty"`
	TargetID  *string         `json:"target_id"`
	TargetURL *string         `json:"target_url"`
	Title     *string         `json:"title"`
	Position  json.RawMessage `json:"position" binding:"required"`
	StartMs   int             `json:"start_ms"`
	EndMs     int             `json:"end_ms"`
}

type saveEndScreensRequest struct {
	Screens []endScreenInput `json:"screens" binding:"required"`
}

// writeAuthoringRuleError answers a save refused by a numbered rule: 422
// with the rule's code. false when err is not one.
func writeAuthoringRuleError(c *gin.Context, err error) bool {
	var re *service.AuthoringRuleError
	if !errors.As(err, &re) {
		return false
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, re.Code, re.Message, nil)
	return true
}

// parseOptionalElementID reads the optional echoed id.
func parseOptionalElementID(raw *string) (*uuid.UUID, bool) {
	return parseOptionalTargetID(raw)
}

// SaveEndScreens — POST /v1/posts/:postId/end-screens {screens:[...]}.
// Replaces the post's elements; an empty array clears them. 200 {"saved": n}.
func (h *Handler) SaveEndScreens(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req saveEndScreensRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	in := make([]service.EndScreenInput, len(req.Screens))
	for i, sc := range req.Screens {
		// Validated here, not left to the video_end_screens CHECK: a bad
		// value used to reach Postgres and come back as a 500 with a
		// constraint name in it.
		if !validEndScreenTypes[sc.Type] {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TYPE",
				invalidEnumMessage("screens", i, "type", sc.Type, endScreenTypeList), nil)
			return
		}
		if sc.VideoMode != "" && !validEndScreenModes[sc.VideoMode] {
			badRequest(c, "INVALID_VIDEO_MODE",
				invalidEnumMessage("screens", i, "video_mode", sc.VideoMode, service.EndScreenModes))
			return
		}
		targetID, ok := parseOptionalTargetID(sc.TargetID)
		if !ok {
			badRequest(c, "INVALID_TARGET_ID",
				invalidFieldMessage("screens", i, "target_id", "must be a UUID"))
			return
		}
		elementID, ok := parseOptionalElementID(sc.ID)
		if !ok {
			badRequest(c, "INVALID_ID", invalidFieldMessage("screens", i, "id", "must be a UUID"))
			return
		}
		if !service.ValidEndScreenTitle(sc.Title) {
			badRequest(c, "INVALID_TITLE",
				invalidFieldMessage("screens", i, "title", "must be at most 60 characters"))
			return
		}
		pos := service.ParseEndScreenPosition(sc.Position, sc.Type, i)
		in[i] = service.EndScreenInput{
			ID: elementID, Type: sc.Type, VideoMode: sc.VideoMode, TargetID: targetID, TargetURL: sc.TargetURL,
			Title: sc.Title, Position: pos, StartMs: sc.StartMs, EndMs: sc.EndMs,
		}
	}

	saved, err := h.svc.SaveEndScreens(c.Request.Context(), userID, postID, in)
	if err != nil {
		if !writeAuthoringRuleError(c, err) {
			writeVideoAuthoringError(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"saved": len(saved)}, nil)
}

var validEndScreenModes = sliceToSet(service.EndScreenModes)

// GetEndScreens — GET /v1/posts/:postId/end-screens. The owner receives the
// editor shape (raw fields + 28-day stats), everyone else the resolved one.
func (h *Handler) GetEndScreens(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	out, err := h.svc.GetEndScreensFor(c.Request.Context(), postID, optionalCallerID(c))
	if err != nil {
		if !writeReadGateError(c, err) {
			writeVideoAuthoringError(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ─── Video Cards ──────────────────────────────────────────────────────────────

type videoCardInput struct {
	ID         *string `json:"id,omitempty"`
	Type       string  `json:"type" binding:"required"`
	TargetID   *string `json:"target_id"`
	TargetURL  *string `json:"target_url"`
	Title      string  `json:"title" binding:"required"`
	TeaserText *string `json:"teaser_text"`
	AppearAtMs int     `json:"appear_at_ms"`
}

type saveVideoCardsRequest struct {
	Cards []videoCardInput `json:"cards" binding:"required"`
}

// SaveVideoCards — POST /v1/posts/:postId/cards {cards:[...]}. 200 {"saved": n}.
func (h *Handler) SaveVideoCards(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req saveVideoCardsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	in := make([]service.VideoCardInput, len(req.Cards))
	for i, card := range req.Cards {
		// See SaveEndScreens: same reason, deliberately different list
		// (cards have poll, end screens have channel_subscribe / channel).
		if !validVideoCardTypes[card.Type] {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TYPE",
				invalidEnumMessage("cards", i, "type", card.Type, videoCardTypeList), nil)
			return
		}
		// title is NOT NULL in the table but "" satisfies that, so a card
		// with no title stored an empty string and the player drew an empty
		// box. A card is a label on a link; without one there is nothing to
		// render.
		if strings.TrimSpace(card.Title) == "" {
			badRequest(c, "INVALID_TITLE",
				invalidFieldMessage("cards", i, "title", "is required and must not be blank"))
			return
		}
		targetID, ok := parseOptionalTargetID(card.TargetID)
		if !ok {
			badRequest(c, "INVALID_TARGET_ID",
				invalidFieldMessage("cards", i, "target_id", "must be a UUID"))
			return
		}
		cardID, ok := parseOptionalElementID(card.ID)
		if !ok {
			badRequest(c, "INVALID_ID", invalidFieldMessage("cards", i, "id", "must be a UUID"))
			return
		}
		in[i] = service.VideoCardInput{
			ID: cardID, Type: card.Type, TargetID: targetID, TargetURL: card.TargetURL, Title: card.Title,
			TeaserText: card.TeaserText, AppearAtMs: card.AppearAtMs,
		}
	}

	saved, err := h.svc.SaveVideoCards(c.Request.Context(), userID, postID, in)
	if err != nil {
		if !writeAuthoringRuleError(c, err) {
			writeVideoAuthoringError(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"saved": len(saved)}, nil)
}

// GetVideoCards — GET /v1/posts/:postId/cards, gated and resolved like the
// end screens.
func (h *Handler) GetVideoCards(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	out, err := h.svc.GetVideoCardsFor(c.Request.Context(), postID, optionalCallerID(c))
	if err != nil {
		if !writeReadGateError(c, err) {
			writeVideoAuthoringError(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ─── impressions and clicks ──────────────────────────────────────────────────

// statViewer is who the event is from: the signed-in user, else the hashed
// client IP (the gateway's X-Device-Id exists only for signed-in tokens).
func statViewer(c *gin.Context) service.StatViewer {
	if id := optionalCallerID(c); id != nil {
		return service.StatViewer{UserID: id}
	}
	return service.StatViewer{AnonID: hashClientIP(c)}
}

func (h *Handler) recordElementStat(c *gin.Context, param string, card, click bool) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	elementID, err := uuid.Parse(c.Param(param))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid element ID", nil)
		return
	}
	viewer := statViewer(c)
	if card {
		err = h.svc.RecordCardEvent(c.Request.Context(), postID, elementID, viewer, click)
	} else {
		err = h.svc.RecordEndScreenEvent(c.Request.Context(), postID, elementID, viewer, click)
	}
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEndScreenElementNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Element not found", nil)
		case writeReadGateError(c, err):
		default:
			writeVideoAuthoringError(c, err)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// RecordEndScreenImpression — POST /v1/posts/:postId/end-screens/:elementId/impression.
func (h *Handler) RecordEndScreenImpression(c *gin.Context) {
	h.recordElementStat(c, "elementId", false, false)
}

// RecordEndScreenClick — POST /v1/posts/:postId/end-screens/:elementId/click.
func (h *Handler) RecordEndScreenClick(c *gin.Context) {
	h.recordElementStat(c, "elementId", false, true)
}

// RecordCardImpression — POST /v1/posts/:postId/cards/:cardId/impression.
func (h *Handler) RecordCardImpression(c *gin.Context) {
	h.recordElementStat(c, "cardId", true, false)
}

// RecordCardClick — POST /v1/posts/:postId/cards/:cardId/click.
func (h *Handler) RecordCardClick(c *gin.Context) {
	h.recordElementStat(c, "cardId", true, true)
}
