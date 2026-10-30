package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/qa-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Handler struct {
	svc         *service.Service
	internalKey string
	// verifier admits admin-service tokens on /v1/qa/internal/admin/*
	// (admin_token.go); nil accepts none.
	verifier *servicetoken.Verifier
	// adminStore backs the token path; nil means svc.Store().
	adminStore AdminStore
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// WithInternalKey sets the shared internal-service-key required on every
// inbound request. Audit (Communities/Q&A 2026-05-14): qa-service had
// no internal-key gate at all — every endpoint was reachable directly
// without going through the API gateway, including the entire
// moderation surface. Empty key keeps dev unblocked.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Token-only admin family for admin-service, registered on the engine
	// OUTSIDE the internal-key group: the key is no evidence of who is
	// calling, the admin-service token is (admin_token.go).
	h.registerAdminTokenRoutes(r)

	qa := r.Group("/v1/qa")
	if h.internalKey != "" {
		qa.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	{
		// Questions
		qa.POST("/questions", h.CreateQuestion)
		qa.GET("/questions", h.ListQuestions)
		qa.GET("/questions/my", h.GetMyQuestions)
		qa.GET("/questions/similar", h.GetSimilarQuestions)
		qa.GET("/questions/:questionId", h.GetQuestion)
		qa.GET("/questions/slug/:slug", h.GetQuestionBySlug)
		qa.PUT("/questions/:questionId", h.UpdateQuestion)
		qa.DELETE("/questions/:questionId", h.DeleteQuestion)
		qa.POST("/questions/:questionId/close", h.CloseQuestion)
		qa.POST("/questions/:questionId/reopen", h.ReopenQuestion)

		// Topics
		qa.GET("/topics", h.ListTopics)
		qa.GET("/topics/:topicId", h.GetTopic)
		qa.GET("/topics/slug/:slug", h.GetTopicBySlug)
		qa.POST("/topics", retired(CodeTopicCreateMoved, "topics are created in the admin console"))
		qa.GET("/topics/:topicId/questions", h.GetTopicQuestions)
		qa.GET("/topics/:topicId/contributors", h.GetTopContributors)

		// Answers
		qa.POST("/questions/:questionId/answers", h.CreateAnswer)
		qa.GET("/questions/:questionId/answers", h.ListAnswers)
		qa.GET("/answers/:answerId", h.GetAnswer)
		qa.PUT("/answers/:answerId", h.UpdateAnswer)
		qa.DELETE("/answers/:answerId", h.DeleteAnswer)
		qa.POST("/questions/:questionId/best-answer", h.SelectBestAnswer)
		qa.DELETE("/questions/:questionId/best-answer", h.UnselectBestAnswer)

		// Comments
		qa.POST("/answers/:answerId/comments", h.CreateComment)
		qa.GET("/answers/:answerId/comments", h.ListComments)
		qa.PUT("/comments/:commentId", h.UpdateComment)
		qa.DELETE("/comments/:commentId", h.DeleteComment)

		// Votes
		qa.POST("/questions/:questionId/vote", h.VoteQuestion)
		qa.DELETE("/questions/:questionId/vote", h.RemoveQuestionVote)
		qa.POST("/answers/:answerId/vote", h.VoteAnswer)
		qa.DELETE("/answers/:answerId/vote", h.RemoveAnswerVote)
		qa.POST("/comments/:commentId/vote", h.VoteComment)
		qa.DELETE("/comments/:commentId/vote", h.RemoveCommentVote)

		// Engagement (follows, saves, requests)
		qa.POST("/questions/:questionId/follow", h.FollowQuestion)
		qa.DELETE("/questions/:questionId/follow", h.UnfollowQuestion)
		qa.POST("/topics/:topicId/follow", h.FollowTopic)
		qa.DELETE("/topics/:topicId/follow", h.UnfollowTopic)
		qa.GET("/topics/following", h.GetFollowedTopics)
		qa.POST("/contributors/:userId/follow", h.FollowContributor)
		qa.DELETE("/contributors/:userId/follow", h.UnfollowContributor)
		qa.POST("/questions/:questionId/save", h.SaveQuestion)
		qa.DELETE("/questions/:questionId/save", h.UnsaveQuestion)
		qa.GET("/saved/questions", h.GetSavedQuestions)
		qa.POST("/answers/:answerId/save", h.SaveAnswer)
		qa.DELETE("/answers/:answerId/save", h.UnsaveAnswer)
		qa.GET("/saved/answers", h.GetSavedAnswers)
		qa.POST("/questions/:questionId/request-answer", h.CreateAnswerRequest)
		qa.GET("/answer-requests", h.GetMyAnswerRequests)
		qa.POST("/answer-requests/:requestId/respond", h.RespondToAnswerRequest)

		// Profile
		qa.GET("/profile", h.GetMyProfile)
		qa.GET("/profile/:userId", h.GetProfile)
		qa.PUT("/profile", h.UpdateProfile)
		qa.GET("/profile/:userId/reputation", h.GetReputationHistory)
		qa.GET("/profile/:userId/badges", h.GetBadges)
		qa.GET("/profile/:userId/questions", h.GetUserQuestions)
		qa.GET("/profile/:userId/answers", h.GetUserAnswers)
		qa.GET("/leaderboard", h.GetLeaderboard)

		// Feeds
		qa.GET("/feed/home", h.GetHomeFeed)
		qa.GET("/feed/trending", h.GetTrendingFeed)
		qa.GET("/feed/unanswered", h.GetUnansweredFeed)
		qa.GET("/feed/following", h.GetFollowingFeed)
		qa.GET("/feed/for-you", h.GetForYouFeed)
		qa.GET("/feed/local", h.GetLocalFeed)
		qa.GET("/feed/answer-queue", h.GetAnswerQueue)

		// Reports
		qa.POST("/reports", h.CreateReport)

		// Search (v1: reuse similar-questions trigram lookup)
		qa.GET("/search", h.SearchQuestions)

		// Drafts (server-backed)
		qa.GET("/drafts/questions", h.ListQuestionDrafts)
		qa.POST("/drafts/questions", h.UpsertQuestionDraft)
		qa.DELETE("/drafts/questions/:draftId", h.DeleteQuestionDraft)
		qa.GET("/drafts/answers", h.ListAnswerDrafts)
		qa.POST("/drafts/answers", h.UpsertAnswerDraft)
		qa.DELETE("/drafts/answers/:draftId", h.DeleteAnswerDraft)

		// Community-scoped Q&A (lives under /v1/qa to avoid clashing with community-service)
		communities := qa.Group("/communities")
		{
			communities.GET("/:communityId/questions", h.ListCommunityQuestions)
			communities.POST("/:communityId/questions/:questionId/pin", h.PinCommunityQuestion)
			communities.DELETE("/:communityId/questions/:questionId/pin", h.UnpinCommunityQuestion)
			communities.GET("/:communityId/qa-settings", h.GetCommunityQASettings)
			communities.PUT("/:communityId/qa-settings", h.UpdateCommunityQASettings)
			communities.GET("/:communityId/topics/popular", h.GetCommunityPopularTopics)
			// The approval queue of communities that require approval (H2).
			communities.POST("/:communityId/questions/:questionId/approve", h.ApproveCommunityQuestion)
			communities.POST("/:communityId/questions/:questionId/reject", h.RejectCommunityQuestion)
		}

		// The legacy moderation group (QA_MODERATOR_USER_IDS + X-User-ID)
		// is retired (audit M1): moderation is the admin console, through
		// admin-service tokens on /v1/qa/internal/admin (admin_token.go).
		qa.Any("/admin/*path", retired(CodeModerationMoved, "moderation moved to the admin console"))
	}
}

// --- helpers ---

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-ID")
	if raw == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "missing user id", nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return uuid.Nil, false
	}
	return id, true
}

func optionalUserID(c *gin.Context) *uuid.UUID {
	raw := c.GetHeader("X-User-ID")
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	raw := c.Param(param)
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+param, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseUUIDString(c *gin.Context, raw, field string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+field, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parsePagination(c *gin.Context) (limit, offset int) {
	limit = 20
	offset = 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	// Audit HQ3: bound offset to keep PostgreSQL's worst-case scan
	// cost finite (same pattern as graph HG2). Runaway clients with
	// offset > 10k were issuing scans of millions of rows.
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 10000 {
			offset = n
		}
	}
	return
}

// Codes for retired routes (410).
const (
	CodeModerationMoved  = "QA_MODERATION_MOVED"
	CodeTopicCreateMoved = "QA_TOPIC_CREATE_MOVED"
)

// retired answers a route that no longer exists here with 410 and a code.
func retired(code, msg string) gin.HandlerFunc {
	return func(c *gin.Context) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, code, msg, nil)
	}
}

// respondServiceError maps a service or store refusal to its status by the
// message prefix ("invalid: ", "forbidden: ", "not_found: ", "conflict: ",
// "rate_limited: ") and sends only the text after it. Anything else is an
// internal failure: the detail (SQL text, constraint names, SQLSTATEs) is
// logged, and the client gets defaultCodeName with a fixed message (audit
// L1).
func respondServiceError(c *gin.Context, err error, defaultCode int, defaultCodeName string) {
	if err == nil {
		return
	}
	if writeMediaError(c, err) {
		return
	}
	ctx := c.Request.Context()
	msg := err.Error()
	for _, m := range []struct {
		prefix string
		status int
		code   string
	}{
		{"invalid: ", http.StatusBadRequest, "INVALID_REQUEST"},
		{"forbidden: ", http.StatusForbidden, "FORBIDDEN"},
		{"not_found: ", http.StatusNotFound, "NOT_FOUND"},
		{"conflict: ", http.StatusConflict, "CONFLICT"},
		{"rate_limited: ", http.StatusTooManyRequests, "RATE_LIMITED"},
	} {
		if detail, ok := strings.CutPrefix(msg, m.prefix); ok {
			api.ErrorWithContext(ctx, c.Writer, m.status, m.code, detail, nil)
			return
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	slog.ErrorContext(ctx, "qa: request failed", "path", c.Request.URL.Path, "code", defaultCodeName, "error", err)
	message := "the request could not be completed"
	if defaultCode == http.StatusNotFound {
		message = "not found"
	}
	api.ErrorWithContext(ctx, c.Writer, defaultCode, defaultCodeName, message, nil)
}
