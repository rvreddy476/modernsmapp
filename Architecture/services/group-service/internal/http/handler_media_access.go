package http

import (
	"net/http"

	"github.com/atpost/group-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Service-to-service only. media-service calls these with the internal
	service key (the engine-wide RequireInternalKey middleware guards them
	like every other route here); the viewer is named in the body because
	media-service, not a browser, is the caller. The wire shape is the one
	post-service answers with, so media-service's HTTPContentAuthorizer reads
	both without a special case:

	  POST /v1/internal/groups/media-access        {viewer_id, media_id}
	    → 200 {allowed:true, decision, reason} | 403 {allowed:false, …}
	  POST /v1/internal/groups/media-access/batch  {viewer_id, media_ids[]}
	    → 200 {allowed:{id:bool}, decisions:{…}, reasons:{…}}

	A malformed question gets no permission (400/403 with allowed:false);
	media-service fails closed on any non-affirmative answer anyway.
*/

const mediaAccessBatchMax = 50

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

func (h *Handler) MediaAccess(c *gin.Context) {
	var req mediaAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, mediaAccessResponse{Allowed: false, Decision: service.MediaDenied, Reason: "invalid_request"})
		return
	}
	viewerID, err := uuid.Parse(req.ViewerID)
	if err != nil {
		c.JSON(http.StatusForbidden, mediaAccessResponse{Allowed: false, Decision: service.MediaDenied, Reason: "invalid_viewer_id"})
		return
	}
	mediaID, err := uuid.Parse(req.MediaID)
	if err != nil {
		c.JSON(http.StatusForbidden, mediaAccessResponse{Allowed: false, Decision: service.MediaDenied, Reason: "invalid_media_id"})
		return
	}
	res, err := h.svc.ViewerMayAccessMedia(c.Request.Context(), viewerID, mediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	status := http.StatusOK
	if !res.Allowed {
		status = http.StatusForbidden
	}
	c.JSON(status, mediaAccessResponse{Allowed: res.Allowed, Decision: res.Decision, Reason: res.Reason})
}

func (h *Handler) MediaAccessBatch(c *gin.Context) {
	var req mediaAccessBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.MediaIDs) == 0 || len(req.MediaIDs) > mediaAccessBatchMax {
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
	results, err := h.svc.ViewerMayAccessMediaBatch(c.Request.Context(), viewerID, mediaIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	resp := mediaAccessBatchResponse{
		Allowed:   make(map[string]bool, len(mediaIDs)),
		Decisions: make(map[string]service.MediaAccessDecision, len(mediaIDs)),
		Reasons:   make(map[string]string, len(mediaIDs)),
	}
	for _, id := range mediaIDs {
		r := results[id]
		resp.Allowed[id.String()] = r.Allowed
		resp.Decisions[id.String()] = r.Decision
		resp.Reasons[id.String()] = r.Reason
	}
	c.JSON(http.StatusOK, resp)
}
