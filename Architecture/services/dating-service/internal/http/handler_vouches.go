// HTTP handlers for /v1/dating/vouches (spec §12).
package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// requestVouchRequest is the body of POST /v1/dating/vouches.
type requestVouchRequest struct {
	VoucheeID    string  `json:"vouchee_id"`
	Relationship string  `json:"relationship"`
	CommunityID  *string `json:"community_id,omitempty"`
	Note         string  `json:"note,omitempty"`
}

// CreateVouch — POST /v1/dating/vouches.
func (h *Handler) CreateVouch(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body requestVouchRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	vouchee, err := parseUUIDValue(body.VoucheeID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid vouchee_id", nil)
		return
	}
	var commPtr *uuid.UUID
	if body.CommunityID != nil && *body.CommunityID != "" {
		c2, err := parseUUIDValue(*body.CommunityID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid community_id", nil)
			return
		}
		commPtr = &c2
	}
	v, err := h.svc.RequestVouch(c.Request.Context(), userID, vouchee, body.Relationship, commPtr, body.Note)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VOUCH_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, v, nil)
}

// AcceptVouch — POST /v1/dating/vouches/:id/accept.
func (h *Handler) AcceptVouch(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	vouchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.AcceptVouch(c.Request.Context(), vouchID, userID); err != nil {
		if errors.Is(err, store.ErrVouchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "vouch not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "VOUCH_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "accepted"}, nil)
}

// DeclineVouch — POST /v1/dating/vouches/:id/decline.
func (h *Handler) DeclineVouch(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	vouchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeclineVouch(c.Request.Context(), vouchID, userID); err != nil {
		if errors.Is(err, store.ErrVouchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "vouch not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "VOUCH_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "declined"}, nil)
}

// RevokeVouch — DELETE /v1/dating/vouches/:id.
func (h *Handler) RevokeVouch(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	vouchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.RevokeVouch(c.Request.Context(), vouchID, userID); err != nil {
		if errors.Is(err, store.ErrVouchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "vouch not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "VOUCH_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "revoked"}, nil)
}

// ListVouchesFor — GET /v1/dating/vouches/for/:userId.
// Returns the public-displayable (status='accepted') subset by default.
func (h *Handler) ListVouchesFor(c *gin.Context) {
	target, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	status := c.DefaultQuery("status", "accepted")
	out, err := h.svc.ListVouchesFor(c.Request.Context(), target, status)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ListVouchesSent — GET /v1/dating/vouches/sent.
func (h *Handler) ListVouchesSent(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListVouchesSent(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
