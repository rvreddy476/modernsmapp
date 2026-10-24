package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Owner match, service-only (founder 24 Oct 2026; migrations 075 / 076):
	the entry point for a future fingerprint provider. There is no provider
	yet; the route lands a match on service.ApplyOwnerMatch.

	POST /v1/posts/internal/copyright/owner-match
	     {"post_id": uuid, "title_id": uuid, "reference": "fp-123" (≤200, optional),
	      "policy": "block" | "track" | "mute"}
	     → 200 {"outcome": blocked | held_for_review | tracked | already_blocked |
	            already_tracked | reviewer_cleared | post_deleted | owner_upload |
	            entry_inactive | under_appeal, "review_id"?}
	     400 INVALID_REQUEST, 404 POST_NOT_FOUND / TITLE_NOT_FOUND,
	     503 COPYRIGHT_REVIEW_UNAVAILABLE

	Idempotent: the same match again answers already_blocked / already_tracked
	(or held_for_review / under_appeal for a pending review, recording the
	repeat in the review's history) and changes nothing else.

	Who may call it. The engine-wide internal-key gate applies (401 without
	the key), AND — because the API gateway stamps that same key on every
	request it proxies to /v1/posts (internalroutes.StampPolicy), so the key
	proves nothing about the caller (security sweep #11) — a signed service
	token from trust-safety-service carrying OpCopyrightOwnerMatchWrite. The
	gateway itself never forwards this path: internalroutes.Middleware
	answers 404 for every edge request with an "internal" path segment, under
	any token or scope, before any upstream is chosen. trust-safety owns the
	copyright cases (its restriction command is the other copyright writer),
	so a provider integration is expected to call through it; until its
	SERVICE_CALLER_TRUST_SAFETY_SERVICE_OPS lists the op, every call is 403.
*/

// OpCopyrightOwnerMatchWrite is the service-token operation the owner-match
// route requires.
const OpCopyrightOwnerMatchWrite = "post:copyright-owner-match.write"

// copyrightOwnerMatchPath is the route (also named by the route tests).
const copyrightOwnerMatchPath = "/v1/posts/internal/copyright/owner-match"

type copyrightOwnerMatchBody struct {
	PostID    string `json:"post_id"`
	TitleID   string `json:"title_id"`
	Reference string `json:"reference"`
	Policy    string `json:"policy"`
}

func (h *Handler) registerCopyrightOwnerMatchInternalRoute(r *gin.Engine) {
	r.POST(copyrightOwnerMatchPath, h.requireTrustSafetyToken(OpCopyrightOwnerMatchWrite), h.ApplyCopyrightOwnerMatchInternal)
}

// ApplyCopyrightOwnerMatchInternal — POST /v1/posts/internal/copyright/owner-match.
func (h *Handler) ApplyCopyrightOwnerMatchInternal(c *gin.Context) {
	ctx := c.Request.Context()
	var body copyrightOwnerMatchBody
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"body must be {post_id, title_id, reference, policy}", nil)
		return
	}
	postID, perr := uuid.Parse(strings.TrimSpace(body.PostID))
	titleID, terr := uuid.Parse(strings.TrimSpace(body.TitleID))
	if perr != nil || terr != nil || postID == uuid.Nil || titleID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "post_id and title_id must be UUIDs", nil)
		return
	}
	policy := service.OwnerPolicy(strings.ToLower(strings.TrimSpace(body.Policy)))
	switch policy {
	case service.OwnerPolicyBlock, service.OwnerPolicyTrack, service.OwnerPolicyMute:
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "policy must be block, track or mute", nil)
		return
	}
	res, err := h.svc.ApplyOwnerMatch(ctx, postID, service.OwnerMatch{TitleID: titleID, Reference: body.Reference}, policy)
	if err != nil {
		writeCopyrightReviewError(c, "apply copyright owner match", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}
