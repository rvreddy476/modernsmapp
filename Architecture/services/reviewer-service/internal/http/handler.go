package http

import (
	"errors"
	"net/http"
	"strings"

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
		g.GET("/me/stats", h.MyDashboard)
		g.POST("/verify-kyc", h.VerifyKYC)
		g.POST("/online", h.SetOnline)
		g.GET("/queue", h.GetQueue)
		g.GET("/assignments/next", h.NextAssignment)
		g.POST("/assignments/:id/heartbeat", h.Heartbeat)
		g.POST("/assignments/:id/decision", h.Decide)
		// Creator-facing: the latest review feedback for the creator's own content
		// (the "needs changes" comments to act on).
		g.GET("/content/:contentId/feedback", h.CreatorFeedback)
		// Super-admin escalation queue (scope-guarded in the handlers).
		g.GET("/admin/stats", h.AdminStats)
		g.GET("/admin/escalations", h.ListEscalations)
		g.POST("/admin/escalations/:id/decision", h.ResolveEscalation)
		// Service-internal: post-service (or an admin tool) enqueues content
		// that needs human review (review_status='flagged'/ambiguous).
		g.POST("/internal/enqueue", h.Enqueue)
	}
}

// isAdmin reports whether the caller carries an admin/superadmin scope (set by
// the gateway from the JWT). Used to guard the super-admin escalation queue.
func isAdmin(c *gin.Context) bool {
	for _, s := range strings.Fields(c.GetHeader("X-Scopes")) {
		if s == "admin" || s == "superadmin" {
			return true
		}
	}
	return false
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

// MyDashboard — GET /v1/reviewer/me/stats. Reviewer console summary (reviewer is
// null if the user hasn't opted in yet).
func (h *Handler) MyDashboard(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	stats, err := h.svc.MyDashboard(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// VerifyKYC — POST /v1/reviewer/verify-kyc. Syncs the reviewer's identity
// verification status from wallet-service. Call after the user completes KYC.
func (h *Handler) VerifyKYC(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	verified, err := h.svc.RefreshKYC(c.Request.Context(), uid)
	if err != nil && !verified {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway, "KYC_CHECK_FAILED", "Could not check verification status", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"kyc_verified": verified}, nil)
}

// AdminStats — GET /v1/reviewer/admin/stats (super-admin overview).
func (h *Handler) AdminStats(c *gin.Context) {
	if !isAdmin(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Super-admin scope required", nil)
		return
	}
	open, queue := h.svc.AdminDashboard(c.Request.Context())
	api.JSON(c.Writer, http.StatusOK, gin.H{"open_escalations": open, "queue_depth": queue}, nil)
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
	var targetContentID uuid.UUID
	if cidStr := c.Query("content_id"); cidStr != "" {
		if id, err := uuid.Parse(cidStr); err == nil {
			targetContentID = id
		}
	}
	a, err := h.svc.NextAssignment(c.Request.Context(), uid, targetContentID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotReviewer):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_REVIEWER", "Opt in to review first", nil)
		case errors.Is(err, service.ErrSuspended):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "SUSPENDED", "Reviewer suspended", nil)
		case errors.Is(err, service.ErrKYCRequired):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "KYC_REQUIRED", "Verify your identity to start reviewing", nil)
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
	Decision string `json:"decision" binding:"required"` // approve | escalate
	Comments string `json:"comments"`                    // required when escalating
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
	a, err := h.svc.Decide(c.Request.Context(), uid, assignmentID, req.Decision, req.Comments)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "decision must be approve, or escalate with comments", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, a, nil)
}

// CreatorFeedback — GET /v1/reviewer/content/:contentId/feedback. Returns the
// latest escalation outcome for the caller's own content (404 if none).
func (h *Handler) CreatorFeedback(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	contentID, err := uuid.Parse(c.Param("contentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid content ID", nil)
		return
	}
	esc, err := h.svc.CreatorFeedback(c.Request.Context(), uid, contentID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "No review feedback", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, esc, nil)
}

// ListEscalations — GET /v1/reviewer/admin/escalations (super-admin only).
func (h *Handler) ListEscalations(c *gin.Context) {
	if !isAdmin(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Super-admin scope required", nil)
		return
	}
	items, err := h.svc.ListEscalations(c.Request.Context(), 50)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = []postgres.Escalation{}
	}
	api.JSON(c.Writer, http.StatusOK, items, nil)
}

type resolveEscalationRequest struct {
	Decision string `json:"decision" binding:"required"` // reject | request_edits | approve
	Notes    string `json:"notes"`
}

// ResolveEscalation — POST /v1/reviewer/admin/escalations/:id/decision (super-admin).
func (h *Handler) ResolveEscalation(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	if !isAdmin(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Super-admin scope required", nil)
		return
	}
	escalationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid escalation ID", nil)
		return
	}
	var req resolveEscalationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	esc, err := h.svc.ResolveEscalation(c.Request.Context(), escalationID, uid, req.Decision, req.Notes)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "decision must be reject, request_edits, or approve", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, esc, nil)
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

func (h *Handler) GetQueue(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	q, err := h.svc.GetReviewerQueue(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if q == nil {
		q = []postgres.QueueItem{}
	}
	api.JSON(c.Writer, http.StatusOK, q, nil)
}
