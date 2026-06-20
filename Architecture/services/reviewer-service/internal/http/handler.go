package http

import (
	"errors"
	"net/http"

	"github.com/atpost/reviewer-service/internal/service"
	"github.com/atpost/reviewer-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/v1/reviewer")
	{
		g.POST("/opt-in", h.OptIn)
		g.GET("/me", h.Me)
		g.POST("/online", h.SetOnline)
		g.GET("/assignments/next", h.NextAssignment)
		g.POST("/assignments/:id/heartbeat", h.Heartbeat)
		g.POST("/assignments/:id/decision", h.Decide)
		// Service-internal: post-service (or an admin tool) enqueues content
		// that needs human review (review_status='flagged'/ambiguous).
		g.POST("/internal/enqueue", h.Enqueue)
	}
}

func userID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

type optInRequest struct {
	Languages []string `json:"languages"`
	Region    string   `json:"region"`
}

func (h *Handler) OptIn(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var req optInRequest
	_ = c.ShouldBindJSON(&req)
	r, err := h.svc.OptIn(c.Request.Context(), uid, req.Languages, req.Region)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, r, nil)
}

func (h *Handler) Me(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	r, err := h.svc.Me(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Not a reviewer", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, r, nil)
}

type onlineRequest struct {
	Online bool `json:"online"`
}

func (h *Handler) SetOnline(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var req onlineRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.SetOnline(c.Request.Context(), uid, req.Online); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"online": req.Online}, nil)
}

func (h *Handler) NextAssignment(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	a, err := h.svc.NextAssignment(c.Request.Context(), uid)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotReviewer):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_REVIEWER", "Opt in to review first", nil)
		case errors.Is(err, service.ErrSuspended):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "SUSPENDED", "Reviewer suspended", nil)
		case errors.Is(err, service.ErrAtCapacity):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "AT_CAPACITY", "Finish your current review first", nil)
		case errors.Is(err, service.ErrNoWork):
			api.JSON(c.Writer, http.StatusOK, nil, nil) // no work right now
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, a, nil)
}

type heartbeatRequest struct {
	Seconds int `json:"seconds"`
}

func (h *Handler) Heartbeat(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid assignment ID", nil)
		return
	}
	var req heartbeatRequest
	_ = c.ShouldBindJSON(&req)
	watched, err := h.svc.Heartbeat(c.Request.Context(), uid, assignmentID, req.Seconds)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"watched_seconds": watched}, nil)
}

type decideRequest struct {
	Decision string `json:"decision" binding:"required"`
	Reason   string `json:"reason"`
}

func (h *Handler) Decide(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid assignment ID", nil)
		return
	}
	var req decideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	a, err := h.svc.Decide(c.Request.Context(), uid, assignmentID, req.Decision, req.Reason)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid decision", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, a, nil)
}

type enqueueRequest struct {
	ContentID      string   `json:"content_id" binding:"required"`
	CreatorID      string   `json:"creator_id" binding:"required"`
	ContentType    string   `json:"content_type"`
	Languages      []string `json:"languages"`
	ContentSeconds int      `json:"content_seconds"`
	SpamScore      float64  `json:"spam_score"`
}

func (h *Handler) Enqueue(c *gin.Context) {
	var req enqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	contentID, err1 := uuid.Parse(req.ContentID)
	creatorID, err2 := uuid.Parse(req.CreatorID)
	if err1 != nil || err2 != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid content_id/creator_id", nil)
		return
	}
	err := h.svc.Enqueue(c.Request.Context(), postgres.QueueItem{
		ContentID:      contentID,
		CreatorID:      creatorID,
		ContentType:    req.ContentType,
		Languages:      req.Languages,
		ContentSeconds: req.ContentSeconds,
		SpamScore:      req.SpamScore,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"enqueued": req.ContentID}, nil)
}
