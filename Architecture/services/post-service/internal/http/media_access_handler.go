package http

import (
	"errors"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 4 M4-P0-5 — the content-authority side of protected media delivery.
//
// media-service owns assets; it does not own audiences. This endpoint answers
// the one question it cannot answer for itself: may THIS viewer receive the
// bytes of THIS asset, given the content that references it.
//
// The answer reuses EvaluateStoryVisibility rather than restating the rules.
// A second copy of the policy here would be free to drift from the first, and
// the drift would be silent — media would keep being served under rules that
// no longer match what the feed enforces.
//
// SERVICE-TO-SERVICE ONLY
//
// The route is registered only when an internal key is configured. An empty
// credential must never produce a permissive endpoint, so with no key the route
// does not exist at all and a request 404s without revealing anything.

type mediaAccessRequest struct {
	ViewerID string `json:"viewer_id"`
	MediaID  string `json:"media_id"`
}

type mediaAccessResponse struct {
	Allowed  bool                        `json:"allowed"`
	Decision service.MediaAccessDecision `json:"decision,omitempty"`
	Reason   string                      `json:"reason,omitempty"`
}

type mediaAccessBatchRequest struct {
	ViewerID string   `json:"viewer_id"`
	MediaIDs []string `json:"media_ids"`
}

type mediaAccessBatchResponse struct {
	Allowed   map[string]bool                        `json:"allowed"`
	Decisions map[string]service.MediaAccessDecision `json:"decisions,omitempty"`
	Reasons   map[string]string                      `json:"reasons,omitempty"`
}

// MediaAccessBatch is the page-sized authorization contract used by
// media-service. A false map entry is a resolved denial; any unresolved policy
// dependency fails the entire batch with 503.
func (h *Handler) MediaAccessBatch(c *gin.Context) {
	var req mediaAccessBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.MediaIDs) == 0 || len(req.MediaIDs) > 50 {
		c.JSON(http.StatusBadRequest, mediaAccessBatchResponse{Allowed: map[string]bool{}})
		return
	}
	viewerID, err := uuid.Parse(req.ViewerID)
	if err != nil {
		c.JSON(http.StatusForbidden, mediaAccessBatchResponse{Allowed: map[string]bool{}})
		return
	}
	mediaIDs := make([]uuid.UUID, len(req.MediaIDs))
	for i, raw := range req.MediaIDs {
		mediaIDs[i], err = uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusForbidden, mediaAccessBatchResponse{Allowed: map[string]bool{}})
			return
		}
	}
	result, err := h.svc.ViewerMayAccessMediaBatch(c.Request.Context(), viewerID, mediaIDs)
	if err != nil {
		if errors.Is(err, service.ErrStoryPolicyUnresolved) {
			c.JSON(http.StatusServiceUnavailable, mediaAccessBatchResponse{Allowed: map[string]bool{}})
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	wireAllowed := make(map[string]bool, len(mediaIDs))
	wireDecisions := make(map[string]service.MediaAccessDecision, len(mediaIDs))
	wireReasons := make(map[string]string, len(mediaIDs))
	for _, id := range mediaIDs {
		res := result[id]
		wireAllowed[id.String()] = res.Allowed
		wireDecisions[id.String()] = res.Decision
		wireReasons[id.String()] = res.Reason
	}
	c.JSON(http.StatusOK, mediaAccessBatchResponse{
		Allowed:   wireAllowed,
		Decisions: wireDecisions,
		Reasons:   wireReasons,
	})
}

// MediaAccess reports whether a viewer may receive an asset's bytes.
func (h *Handler) MediaAccess(c *gin.Context) {
	var req mediaAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// A malformed question gets no permission. It is not a 400-with-allowed
		// -true; the caller fails closed on any non-affirmative answer anyway,
		// but being explicit keeps that from depending on the caller.
		c.JSON(http.StatusBadRequest, mediaAccessResponse{Allowed: false, Decision: service.DecisionDenied, Reason: "invalid_request"})
		return
	}
	viewerID, err := uuid.Parse(req.ViewerID)
	if err != nil {
		c.JSON(http.StatusForbidden, mediaAccessResponse{Allowed: false, Decision: service.DecisionDenied, Reason: "invalid_viewer_id"})
		return
	}
	mediaID, err := uuid.Parse(req.MediaID)
	if err != nil {
		c.JSON(http.StatusForbidden, mediaAccessResponse{Allowed: false, Decision: service.DecisionDenied, Reason: "invalid_media_id"})
		return
	}

	res, err := h.svc.ViewerMayAccessMedia(c.Request.Context(), viewerID, mediaID)
	if err != nil {
		if errors.Is(err, service.ErrStoryPolicyUnresolved) {
			// Unresolved, not denied. The caller must retry rather than cache
			// a denial produced by an outage.
			c.JSON(http.StatusServiceUnavailable, mediaAccessResponse{Allowed: false, Decision: service.DecisionDenied, Reason: "policy_unresolved"})
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	if !res.Allowed {
		c.JSON(http.StatusForbidden, mediaAccessResponse{Allowed: false, Decision: res.Decision, Reason: res.Reason})
		return
	}
	c.JSON(http.StatusOK, mediaAccessResponse{Allowed: true, Decision: res.Decision, Reason: res.Reason})
}
