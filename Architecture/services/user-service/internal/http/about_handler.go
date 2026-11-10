package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// About sections, read for a viewer through graph-service's relationship facts
// (about_visibility.go). The writes are retired (retired_social_routes.go).

// resolveViewerAccess determines the viewer's About-section audience from
// graph-service's relationship facts. See about_visibility.go for the rule;
// every failure leaves the viewer with public items only.
func (h *Handler) resolveViewerAccess(ctx *gin.Context, ownerID uuid.UUID) service.ViewerAccess {
	viewerIDStr := ctx.GetHeader("X-User-Id")
	viewerID, err := uuid.Parse(viewerIDStr)
	if err != nil {
		return service.ViewerAccess{}
	}
	if viewerID == ownerID {
		return service.ViewerAccess{IsSelf: true}
	}

	rel, err := h.fetchViewerRelationship(ctx.Request.Context(), viewerID, ownerID)
	if err != nil {
		return service.ViewerAccess{}
	}
	return aboutAccessFromRelationship(rel)
}

// fetchViewerRelationship is the one GET /v1/graph/relationship call this
// service makes: viewer as user_id, owner as other_id. It is an in-cluster
// call (internal key, no X-Graph-Write-Source), so graph-service answers both
// block directions truthfully; only the edge copy of that route hides the
// target's block of the viewer. Any non-200 or undecodable answer is an
// error, never "no relationship".
func (h *Handler) fetchViewerRelationship(ctx context.Context, viewerID, ownerID uuid.UUID) (aboutRelationship, error) {
	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s", h.graphURL, viewerID, ownerID)
	graphReq, err := h.newGraphRequest(ctx, url)
	if err != nil {
		return aboutRelationship{}, err
	}
	graphReq.Header.Set("X-User-Id", viewerID.String())
	resp, err := h.graphClient.Do(graphReq)
	if err != nil {
		return aboutRelationship{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return aboutRelationship{}, fmt.Errorf("graph relationship: status %d", resp.StatusCode)
	}

	var body struct {
		Data aboutRelationship `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return aboutRelationship{}, fmt.Errorf("graph relationship: decode: %w", err)
	}
	return body.Data, nil
}

func (h *Handler) GetAbout(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAllAbout(c.Request.Context(), userID, access)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = make(map[string][]store.AboutItem)
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}

func (h *Handler) GetAboutSection(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	section := c.Param("section")

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAboutSection(c.Request.Context(), userID, section, access)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = []store.AboutItem{}
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}
