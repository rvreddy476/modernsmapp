// Platform suspension of a community, on the admin-service token family
// (admin_token.go). admin-service resolves the admin's permissions, enforces
// MFA, step-up and two-person rules and writes its own audit row, then calls
// here with a token scoped to chat:communities.moderate whose act claim is the
// admin. This service writes the change and its community_admin_audit row in
// one transaction and announces the row on community.updated (search hides a
// suspended community). The internal key and X-User-Id are no evidence here.
//
//	POST /v1/communities/internal/admin/communities/:communityId/suspend
//	     {"reason_code":"spam","note":"..."}     reason_code required
//	POST /v1/communities/internal/admin/communities/:communityId/unsuspend
//	     {"note":"..."}                          note required
//
// Both answer 200 with the change ({community_id, previous_status, status,
// changed, suspended_at, suspended_by, reason_code}); a repeat answers 200
// with changed=false. 404 for a missing or deleted community, 409 for an
// archived one, 422 for a bad body.
package http

import (
	"context"
	"net/http"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PermCommunitiesModerate is the permission suspend and unsuspend need. NEW:
// not in identity's catalogue yet (the channel equivalent is
// chat:channels.moderate).
const PermCommunitiesModerate = "chat:communities.moderate"

// CodeCommunitySuspended is what a member-side write on a suspended
// community answers (403).
const CodeCommunitySuspended = "COMMUNITY_SUSPENDED"

// CommunityModeration is what the suspend and unsuspend handlers need; the
// service in production.
type CommunityModeration interface {
	SuspendCommunity(ctx context.Context, actorID, communityID uuid.UUID, reasonCode, note string) (*store.CommunitySuspensionChange, error)
	UnsuspendCommunity(ctx context.Context, actorID, communityID uuid.UUID, note string) (*store.CommunitySuspensionChange, error)
}

// WithModeration installs the suspend/unsuspend backend.
func (h *Handler) WithModeration(m CommunityModeration) *Handler {
	h.moderation = m
	return h
}

// AdminSuspendRequest is the body of POST .../communities/:communityId/suspend.
type AdminSuspendRequest struct {
	ReasonCode string `json:"reason_code"`
	Note       string `json:"note"`
}

// AdminUnsuspendRequest is the body of POST .../communities/:communityId/unsuspend.
type AdminUnsuspendRequest struct {
	Note string `json:"note"`
}

func (h *Handler) registerAdminModerationRoutes(g *gin.RouterGroup) {
	g.POST("/communities/:communityId/suspend", h.requireAdminToken(PermCommunitiesModerate), h.AdminSuspendCommunity)
	g.POST("/communities/:communityId/unsuspend", h.requireAdminToken(PermCommunitiesModerate), h.AdminUnsuspendCommunity)
}

// AdminSuspendCommunity — POST .../communities/:communityId/suspend
// (chat:communities.moderate).
func (h *Handler) AdminSuspendCommunity(c *gin.Context) {
	h.adminSuspension(c, true)
}

// AdminUnsuspendCommunity — POST .../communities/:communityId/unsuspend
// (chat:communities.moderate).
func (h *Handler) AdminUnsuspendCommunity(c *gin.Context) {
	h.adminSuspension(c, false)
}

func (h *Handler) adminSuspension(c *gin.Context, suspend bool) {
	ctx := c.Request.Context()
	if h.moderation == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE", "admin backend unavailable", nil)
		return
	}
	actor, ok := adminActor(c)
	if !ok {
		writeAdminError(c, store.ErrAdminActorRequired)
		return
	}
	id, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid communityId", nil)
		return
	}
	var (
		out     *store.CommunitySuspensionChange
		callErr error
	)
	if suspend {
		var req AdminSuspendRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
			return
		}
		out, callErr = h.moderation.SuspendCommunity(ctx, actor, id, req.ReasonCode, req.Note)
	} else {
		var req AdminUnsuspendRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
			return
		}
		out, callErr = h.moderation.UnsuspendCommunity(ctx, actor, id, req.Note)
	}
	if callErr != nil {
		writeAdminError(c, callErr)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
