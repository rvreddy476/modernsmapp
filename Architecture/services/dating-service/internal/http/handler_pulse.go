package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
)

// GetPulseToday returns the caller's curated daily Pulse list.
//
// Response shape is locked (mobile is consuming) — see service.PulseResponse.
// Cached in Redis for 24 h, invalidated on profile/Tune/preferences updates.
func (h *Handler) GetPulseToday(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetPulseToday(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	// Lane D10: the deck is served in the standard {data, meta} envelope —
	// data is the card array, meta carries generated_at, size, cohort_gated
	// and the request id (see pulseEnvelope for why cohort_gated also stays
	// at the top level).
	c.JSON(http.StatusOK, envelopePulse(c.Request.Context(), resp))
}

// GetPulseNebula handles GET /v1/dating/pulse/nebula?filter=passed
//
// Sprint 2 supports `filter=passed` only (recently-passed candidates).
// Other filter values fall back to an empty list rather than 400 — keeps
// the mobile contract forgiving.
func (h *Handler) GetPulseNebula(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	filter := c.Query("filter")
	if filter == "" {
		filter = "passed"
	}
	limit := parseQueryInt(c, "limit", 100)
	offset := parseQueryInt(c, "offset", 0)

	switch filter {
	case "passed":
		resp, err := h.svc.GetPulseNebulaPassed(c.Request.Context(), userID, limit, offset)
		if err != nil {
			respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
			return
		}
		c.JSON(http.StatusOK, resp)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_FILTER",
			"filter must be one of: passed", nil)
	}
}

// ExplainPulseCandidate — GET /v1/dating/pulse/:targetUserId/explain
//
// §P1-2 transparency control: returns a structured, human-safe
// list of reasons the candidate surfaced in the viewer's deck,
// the distance bucket (code + label), and a boolean for whether
// the candidate is currently promoted. Lane D7: only for a
// candidate in the viewer's current deck (404
// CANDIDATE_UNAVAILABLE otherwise), rate limited per viewer (429
// EXPLAIN_RATE_LIMITED).
//
// Internal-key gated by the parent group; X-User-Id identifies
// the viewer.
func (h *Handler) ExplainPulseCandidate(c *gin.Context) {
	viewerID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, ok := parseUUID(c, "targetUserId")
	if !ok {
		return
	}
	resp, err := h.svc.ExplainCandidate(c.Request.Context(), viewerID, targetID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// passRequest is the optional body of POST /v1/dating/pulse/:candidateId/pass.
type passRequest struct {
	Reason string `json:"reason,omitempty"`
}

// PassCandidate — POST /v1/dating/pulse/:candidateId/pass
//
// Lane D3: records the pass (idempotent), removes the candidate from the
// viewer's cached deck and keeps them out of new decks for the cooldown.
// The body is optional.
func (h *Handler) PassCandidate(c *gin.Context) {
	viewerID, ok := getUserID(c)
	if !ok {
		return
	}
	candidateID, ok := parseUUID(c, "candidateId")
	if !ok {
		return
	}
	var body passRequest
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	resp, err := h.svc.PassCandidate(c.Request.Context(), viewerID, candidateID, body.Reason)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PASS_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// parseQueryInt is a forgiving helper — falls back to fallback on any parse
// problem instead of erroring.
func parseQueryInt(c *gin.Context, key string, fallback int) int {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// pulseMeta is the meta member of the /pulse/today envelope: the deck's own
// metadata plus the standard request id.
type pulseMeta struct {
	GeneratedAt time.Time `json:"generated_at"`
	Size        int       `json:"size"`
	CohortGated bool      `json:"cohort_gated"`
	RequestID   string    `json:"request_id,omitempty"`
}

// pulseEnvelope is the standard {data, meta} envelope for the deck: data is
// the card array and meta carries generated_at, size, cohort_gated and the
// request id.
//
// CohortGated is ALSO kept at the top level. It is the one field the shape
// used to expose there, and Android reads it there today (DatingApi.kt
// parses {data, meta, cohort_gated}); dropping it would silently turn the
// "coming soon" state into an ordinary empty deck. It is additive, so the
// envelope is still {data, meta}.
type pulseEnvelope struct {
	Data        []service.PulseCard `json:"data"`
	Meta        pulseMeta           `json:"meta"`
	CohortGated bool                `json:"cohort_gated,omitempty"`
}

// envelopePulse wraps a service response for the wire.
func envelopePulse(ctx context.Context, resp *service.PulseResponse) pulseEnvelope {
	cards := resp.Data
	if cards == nil {
		cards = []service.PulseCard{}
	}
	return pulseEnvelope{
		Data: cards,
		Meta: pulseMeta{
			GeneratedAt: resp.Meta.GeneratedAt,
			Size:        resp.Meta.Size,
			CohortGated: resp.CohortGated,
			RequestID:   trace.RequestIDFrom(ctx),
		},
		CohortGated: resp.CohortGated,
	}
}
