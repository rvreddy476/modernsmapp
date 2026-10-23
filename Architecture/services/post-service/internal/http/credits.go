package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Video credits (2026-10-23, service/credits.go).

	  GET    /v1/posts/genres                   [{slug, label}]  static, public
	  GET    /v1/credits/requests?cursor=&limit= my pending tags  {data:[…], meta:{next_cursor}}
	  POST   /v1/credits/:creditId/accept       {status:"accepted"}
	  POST   /v1/credits/:creditId/decline      {status:"declined"}
	  DELETE /v1/credits/:creditId              {status:"unlinked"}  "Remove me from this video"
	  GET    /v1/creators/:creatorId/appearances?cursor=&limit=
	                                            "Appears in": the channel-videos row shape

	401 UNAUTHORIZED without an identity on the tagged person's routes; 404
	NOT_FOUND for a credit that does not tag the caller (whoever else it
	tags, or nobody); 400 INVALID_ID / INVALID_CURSOR. The answers are
	idempotent: a second accept answers accepted again.

	"Appears in" lives under /v1/creators (proxied to this service), not
	/v1/users/:id, which the gateway sends to user-service.
*/

func (h *Handler) registerCreditRoutes(r *gin.Engine) {
	r.GET("/v1/posts/genres", h.ListGenres)
	credits := r.Group("/v1/credits")
	{
		credits.GET("/requests", h.ListCreditRequests)
		credits.POST("/:creditId/accept", h.AcceptCredit)
		credits.POST("/:creditId/decline", h.DeclineCredit)
		credits.DELETE("/:creditId", h.RemoveMeFromCredit)
	}
	r.GET("/v1/creators/:creatorId/appearances", h.ListAppearances)
}

// ListGenres is GET /v1/posts/genres.
func (h *Handler) ListGenres(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, service.VideoGenres(), nil)
}

// creditListParams reads ?limit (1..50, default 20) and ?cursor; false =
// answered (400).
func creditListParams(c *gin.Context) (string, int, bool) {
	limit := service.DefaultCreditListLimit
	if raw := c.Query("limit"); raw != "" {
		l, err := strconv.Atoi(raw)
		if err != nil || l <= 0 || l > service.MaxCreditListLimit {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be an integer between 1 and 50", nil)
			return "", 0, false
		}
		limit = l
	}
	return c.Query("cursor"), limit, true
}

// writeCreditError answers the credit routes' refusals.
func writeCreditError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrCreditNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Credit not found", nil)
	case errors.Is(err, postgres.ErrInvalidCreditCursor):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR", err.Error(), nil)
	case errors.Is(err, service.ErrAuthoringStoreUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "Credits are unavailable", nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
	}
}

func callerID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return uuid.Nil, false
	}
	return id, true
}

// ListCreditRequests is GET /v1/credits/requests.
func (h *Handler) ListCreditRequests(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	cursor, limit, ok := creditListParams(c)
	if !ok {
		return
	}
	items, next, err := h.svc.ListCreditRequests(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		writeCreditError(c, err)
		return
	}
	if items == nil {
		items = []service.CreditRequest{}
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

func (h *Handler) answerCredit(c *gin.Context, action string) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	creditID, err := uuid.Parse(strings.TrimSpace(c.Param("creditId")))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid credit ID", nil)
		return
	}
	answer, err := h.svc.RespondToCredit(c.Request.Context(), userID, creditID, action)
	if err != nil {
		writeCreditError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, answer, nil)
}

// AcceptCredit is POST /v1/credits/:creditId/accept.
func (h *Handler) AcceptCredit(c *gin.Context) { h.answerCredit(c, postgres.CreditActionAccept) }

// DeclineCredit is POST /v1/credits/:creditId/decline.
func (h *Handler) DeclineCredit(c *gin.Context) { h.answerCredit(c, postgres.CreditActionDecline) }

// RemoveMeFromCredit is DELETE /v1/credits/:creditId.
func (h *Handler) RemoveMeFromCredit(c *gin.Context) { h.answerCredit(c, postgres.CreditActionRemove) }

// ListAppearances is GET /v1/creators/:creatorId/appearances. Signed-out
// viewers get the public answer.
func (h *Handler) ListAppearances(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	cursor, limit, ok := creditListParams(c)
	if !ok {
		return
	}
	var viewerID *uuid.UUID
	if id, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		viewerID = &id
	}
	posts, next, err := h.svc.ListAppearances(c.Request.Context(), userID, viewerID, cursor, limit)
	if err != nil {
		writeCreditError(c, err)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, posts, meta)
}
