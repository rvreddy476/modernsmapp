// HTTP handlers for /v1/dating/sparks.
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// CreateSparkRequest is the body shape accepted by POST /v1/dating/sparks.
type createSparkRequest struct {
	ToUserID   string `json:"to_user_id"`
	TargetKind string `json:"target_kind"`
	TargetRef  string `json:"target_ref"`
	Note       string `json:"note,omitempty"`
}

// CreateSpark — POST /v1/dating/sparks.
// Responds 201 with the new spark; if a mutual-spark match was formed as a
// side effect the response includes "match_id".
func (h *Handler) CreateSpark(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body createSparkRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	to, err := parseUUIDValue(body.ToUserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid to_user_id", nil)
		return
	}
	sp, matchID, err := h.svc.CreateSpark(c.Request.Context(), userID, to, body.TargetKind, body.TargetRef, body.Note)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
		return
	}
	out := gin.H{"spark": sp}
	if matchID != nil {
		out["match_id"] = matchID.String()
		out["matched"] = true
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// ListIncomingSparks — GET /v1/dating/sparks/incoming.
func (h *Handler) ListIncomingSparks(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	out, err := h.svc.ListIncomingSparks(c.Request.Context(), userID, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// RevokeSpark — DELETE /v1/dating/sparks/:id.
func (h *Handler) RevokeSpark(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	sparkID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.RevokeSpark(c.Request.Context(), sparkID, userID); err != nil {
		if errors.Is(err, store.ErrSparkNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "spark not found or not yours", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"revoked": true}, nil)
}
