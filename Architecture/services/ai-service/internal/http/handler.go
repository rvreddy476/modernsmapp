package http

import (
	"log/slog"
	"net/http"

	"github.com/atpost/ai-service/internal/service"
	"github.com/atpost/ai-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler holds HTTP handler dependencies for the ai-service.
type Handler struct {
	svc *service.Service
}

// New returns a new Handler.
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes registers all AI service routes on the given Gin engine.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/ai")
	{
		v1.POST("/jobs", h.EnqueueJob)
		v1.GET("/jobs/:jobId", h.GetJob)
		v1.POST("/caption/suggest", h.SuggestCaption)
		v1.POST("/hashtag/suggest", h.SuggestHashtags)
		v1.POST("/smart-reply", h.SmartReply)
		v1.POST("/moderation/check", h.ModerationCheck)
		v1.POST("/translation", h.Translation)
		v1.POST("/summary", h.Summary)
		v1.POST("/engagement/predict", h.EngagementPredict)
		v1.POST("/scam/check", h.ScamCheck)
	}
}

// requesterID extracts the caller's user ID from X-User-Id header.
func requesterID(c *gin.Context) uuid.UUID {
	raw := c.GetHeader("X-User-Id")
	if raw == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// EnqueueJob handles POST /v1/ai/jobs
func (h *Handler) EnqueueJob(c *gin.Context) {
	var req struct {
		JobType string    `json:"job_type" binding:"required"`
		RefType string    `json:"ref_type" binding:"required"`
		RefID   uuid.UUID `json:"ref_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	job, err := h.svc.EnqueueJob(c.Request.Context(), req.JobType, req.RefType, req.RefID, requesterID(c))
	if err != nil {
		slog.Error("EnqueueJob failed", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "ENQUEUE_FAILED", "failed to enqueue job", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, job, nil)
}

// GetJob handles GET /v1/ai/jobs/:jobId
func (h *Handler) GetJob(c *gin.Context) {
	jobID, err := uuid.Parse(c.Param("jobId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_JOB_ID", "invalid job ID", nil, nil)
		return
	}

	job, err := h.svc.GetJob(c.Request.Context(), jobID)
	if err != nil {
		slog.Error("GetJob failed", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "GET_JOB_FAILED", "failed to get job", nil, nil)
		return
	}
	if job == nil {
		api.Error(c.Writer, http.StatusNotFound, "JOB_NOT_FOUND", "job not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, job, nil)
}

// SuggestCaption handles POST /v1/ai/caption/suggest
func (h *Handler) SuggestCaption(c *gin.Context) {
	var req struct {
		DraftID uuid.UUID `json:"draft_id" binding:"required"`
		Text    string    `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	ctx := c.Request.Context()

	// Check cache first.
	cached, err := h.svc.GetCachedCaption(ctx, req.DraftID)
	if err != nil {
		slog.Warn("caption cache lookup failed", "error", err)
	}
	if cached != nil {
		api.JSON(c.Writer, http.StatusOK, gin.H{"suggestions": cached, "cached": true}, nil)
		return
	}

	// Stub: generate canned suggestions.
	suggestions := stubCaptions(req.Text)

	// Persist to cache.
	if cacheErr := h.svc.CacheCaption(ctx, req.DraftID, suggestions); cacheErr != nil {
		slog.Warn("caption cache store failed", "error", cacheErr)
	}

	// Record a job entry.
	_, _ = h.svc.EnqueueJob(ctx, "caption_suggestion", "draft", req.DraftID, requesterID(c))

	api.JSON(c.Writer, http.StatusOK, gin.H{"suggestions": suggestions, "cached": false}, nil)
}

// SuggestHashtags handles POST /v1/ai/hashtag/suggest
func (h *Handler) SuggestHashtags(c *gin.Context) {
	var req struct {
		DraftID uuid.UUID `json:"draft_id" binding:"required"`
		Content string    `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	ctx := c.Request.Context()

	cached, err := h.svc.GetCachedHashtags(ctx, req.DraftID)
	if err != nil {
		slog.Warn("hashtag cache lookup failed", "error", err)
	}
	if cached != nil {
		api.JSON(c.Writer, http.StatusOK, gin.H{"hashtags": cached, "cached": true}, nil)
		return
	}

	tags := stubHashtags(req.Content)

	if cacheErr := h.svc.CacheHashtags(ctx, req.DraftID, tags); cacheErr != nil {
		slog.Warn("hashtag cache store failed", "error", cacheErr)
	}

	_, _ = h.svc.EnqueueJob(ctx, "hashtag_suggestion", "draft", req.DraftID, requesterID(c))

	api.JSON(c.Writer, http.StatusOK, gin.H{"hashtags": tags, "cached": false}, nil)
}

// SmartReply handles POST /v1/ai/smart-reply
func (h *Handler) SmartReply(c *gin.Context) {
	var req struct {
		ConversationID uuid.UUID `json:"conversation_id" binding:"required"`
		LastMessages   []string  `json:"last_messages"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	ctx := c.Request.Context()

	cached, err := h.svc.GetCachedSmartReplies(ctx, req.ConversationID)
	if err != nil {
		slog.Warn("smart reply cache lookup failed", "error", err)
	}
	if cached != nil {
		api.JSON(c.Writer, http.StatusOK, gin.H{"replies": cached, "cached": true}, nil)
		return
	}

	replies := stubSmartReplies()

	if cacheErr := h.svc.CacheSmartReplies(ctx, req.ConversationID, replies); cacheErr != nil {
		slog.Warn("smart reply cache store failed", "error", cacheErr)
	}

	_, _ = h.svc.EnqueueJob(ctx, "smart_reply", "conversation", req.ConversationID, requesterID(c))

	api.JSON(c.Writer, http.StatusOK, gin.H{"replies": replies, "cached": false}, nil)
}

// ModerationCheck handles POST /v1/ai/moderation/check
func (h *Handler) ModerationCheck(c *gin.Context) {
	var req struct {
		ContentType string    `json:"content_type" binding:"required"`
		ContentID   uuid.UUID `json:"content_id" binding:"required"`
		Text        string    `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	ctx := c.Request.Context()

	existing, err := h.svc.CheckModeration(ctx, req.ContentType, req.ContentID)
	if err != nil {
		slog.Warn("moderation check lookup failed", "error", err)
	}
	if existing != nil {
		api.JSON(c.Writer, http.StatusOK, existing, nil)
		return
	}

	// Stub moderation result.
	result := &postgres.ModerationResult{
		ContentType: req.ContentType,
		ContentID:   req.ContentID,
		Action:      "allow",
	}
	if len(req.Text) > 0 {
		score := stubTextToxicityScore(req.Text)
		result.TextScore = &score
		if score > 0.8 {
			result.Action = "flag"
			result.Flags = []string{"potentially_toxic"}
		}
	}

	if recordErr := h.svc.RecordModerationResult(ctx, result); recordErr != nil {
		slog.Warn("moderation result record failed", "error", recordErr)
	}

	_, _ = h.svc.EnqueueJob(ctx, "moderation_check", req.ContentType, req.ContentID, requesterID(c))

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// Translation handles POST /v1/ai/translation (stub — returns original text)
func (h *Handler) Translation(c *gin.Context) {
	var req struct {
		Text       string `json:"text" binding:"required"`
		SourceLang string `json:"source_lang"`
		TargetLang string `json:"target_lang"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"translated_text": req.Text}, nil)
}

// Summary handles POST /v1/ai/summary — enqueues a summary job
func (h *Handler) Summary(c *gin.Context) {
	var req struct {
		ContentType string    `json:"content_type" binding:"required"`
		RefID       uuid.UUID `json:"ref_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	job, err := h.svc.EnqueueJob(c.Request.Context(), "summary", req.ContentType, req.RefID, requesterID(c))
	if err != nil {
		slog.Error("Summary enqueue failed", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "ENQUEUE_FAILED", "failed to enqueue summary job", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, job, nil)
}

// EngagementPredict handles POST /v1/ai/engagement/predict (stub score)
func (h *Handler) EngagementPredict(c *gin.Context) {
	var req struct {
		PostDraft map[string]interface{} `json:"post_draft"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"predicted_likes":    42,
		"predicted_comments": 7,
		"predicted_shares":   3,
		"engagement_score":   0.62,
		"confidence":         0.55,
	}, nil)
}

// ScamCheck handles POST /v1/ai/scam/check — enqueues a scam detection job
func (h *Handler) ScamCheck(c *gin.Context) {
	var req struct {
		ProfileID   uuid.UUID `json:"profile_id" binding:"required"`
		MessageText string    `json:"message_text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	job, err := h.svc.EnqueueJob(c.Request.Context(), "scam_detection", "profile", req.ProfileID, requesterID(c))
	if err != nil {
		slog.Error("ScamCheck enqueue failed", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "ENQUEUE_FAILED", "failed to enqueue scam check job", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, job, nil)
}

// --- Stub helpers ---

func stubCaptions(text string) []string {
	if text == "" {
		return []string{
			"Sharing this moment with you ✨",
			"Life is beautiful 🌟",
			"Making memories every day",
		}
	}
	return []string{
		"Check this out! " + truncate(text, 40),
		"Loving every moment of this 💫",
		"Just another amazing day in the life",
	}
}

func stubHashtags(content string) []string {
	_ = content
	return []string{
		"#trending", "#viral", "#explore",
		"#postbook", "#lifestyle", "#content",
	}
}

func stubSmartReplies() []string {
	return []string{
		"Sounds great!",
		"Thanks for letting me know 😊",
		"I'll get back to you soon",
	}
}

func stubTextToxicityScore(text string) float32 {
	// Very naive stub: score based on text length as a placeholder.
	if len(text) > 500 {
		return 0.1
	}
	return 0.05
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
