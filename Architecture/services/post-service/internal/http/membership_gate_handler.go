// membership_gate_handler.go holds the members-only gate on a post: the
// route that sets it and the parsers and error mapper it uses. Moved out of
// handler.go unchanged.

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SetPostMembershipGate handles PUT /v1/posts/:postId/membership.
// Body: {"tier_required_id": "<uuid>"} sets gate, or {} / {"tier_required_id": null}
// clears it. Only the post author can change this.
func (h *Handler) SetPostMembershipGate(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	actorIDStr := c.GetHeader("X-User-Id")
	actorID, err := uuid.Parse(actorIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user", nil)
		return
	}

	// The body is read before anything is changed, and strictly: the bind
	// error used to be ignored, so a malformed body (bad JSON, a tier that
	// is not a UUID, a misspelt key, no body at all) decoded as "no tier"
	// and silently made a members-only post public.
	tierID, ok := parseMembershipGateBody(c)
	if !ok {
		return
	}

	owner, err := h.svc.IsPostAuthor(c.Request.Context(), postID, actorID)
	if err != nil {
		if errors.Is(err, service.ErrPostNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !owner {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Only the post author can change membership gating", nil)
		return
	}

	if err := h.svc.SetPostMembershipGate(c.Request.Context(), postID, tierID); err != nil {
		if writeTierGateError(c, err) {
			return
		}
		if err.Error() == "POST_NOT_FOUND" || errors.Is(err, service.ErrPostNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"tier_required_id": tierID}, nil)
}

// parseMembersOnlyParam reads ?members_only: "" and "false" are false,
// "true" is true (case-insensitive); anything else is not ok.
func parseMembersOnlyParam(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "false", "0":
		return false, true
	case "true", "1":
		return true, true
	}
	return false, false
}

// writeTierGateError answers a refused members-only gate on any write
// (create, both draft kinds, edit, PUT /membership): 400 INVALID_TIER when
// the tier is not one of the author's active tiers, 503
// MEMBERSHIP_TIERS_UNAVAILABLE when monetization-service could not say —
// nothing was written either way. false when err is neither.
func writeTierGateError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidTier):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_TIER",
			"tier_required_id must be one of your active membership tiers", nil)
	case errors.Is(err, service.ErrTierLookupUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "MEMBERSHIP_TIERS_UNAVAILABLE",
			"Membership tiers could not be checked right now; nothing was saved. Try again.", nil)
	case errors.Is(err, service.ErrCopyrightReviewTier):
		writeCopyrightReviewTier(c)
	default:
		return false
	}
	return true
}

// parseMembershipGateBody reads PUT /membership's body: exactly one JSON
// object whose only key is tier_required_id, a UUID string or null. {} and
// {"tier_required_id": null} clear the gate. Anything else answers 400
// INVALID_REQUEST and returns ok=false; nothing has been changed by then.
func parseMembershipGateBody(c *gin.Context) (*uuid.UUID, bool) {
	refuse := func(msg string) (*uuid.UUID, bool) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", msg, nil)
		return nil, false
	}
	raw, err := c.GetRawData()
	if err != nil {
		return refuse("unreadable body")
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return refuse(`body must be a JSON object: {"tier_required_id": "<uuid>"} sets the gate, {} or {"tier_required_id": null} clears it`)
	}
	var body struct {
		TierRequiredID *uuid.UUID `json:"tier_required_id"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return refuse("tier_required_id must be a UUID string or null: " + err.Error())
	}
	if dec.More() {
		return refuse("body must be a single JSON object")
	}
	if body.TierRequiredID != nil && *body.TierRequiredID == uuid.Nil {
		return refuse("tier_required_id must not be the nil UUID")
	}
	return body.TierRequiredID, true
}
