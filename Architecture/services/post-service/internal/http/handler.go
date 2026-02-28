package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/facebook-like/post-service/internal/http/middleware"
	"github.com/facebook-like/post-service/internal/service"
	"github.com/facebook-like/post-service/internal/store/postgres"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	svc *service.Service
	rdb *redis.Client
}

func New(svc *service.Service, rdb *redis.Client) *Handler {
	return &Handler{svc: svc, rdb: rdb}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	idempotent := middleware.Idempotency(h.rdb)

	v1 := r.Group("/v1/posts")
	{
		v1.POST("", h.CreatePost)
		v1.POST("/batch", h.BatchGetPosts)
		v1.GET("/recent", h.GetRecentPosts)
		v1.GET("/bookmarks", h.GetBookmarks)
		v1.GET("/by-author/:authorId", h.GetPostsByAuthor)
		v1.GET("/by-author/:authorId/counts", h.GetAuthorCounts)
		v1.GET("/:postId", h.GetPost)
		v1.PUT("/:postId/pin", h.TogglePin)

		// Legacy reaction routes (kept for backward compat)
		v1.POST("/:postId/reactions", h.React)
		v1.DELETE("/:postId/reactions", h.Unreact)
		v1.GET("/:postId/reactions/me", h.GetMyReaction)

		// New engagement routes
		v1.POST("/:postId/like", h.ToggleLike)
		v1.POST("/:postId/share", h.SharePost)
		v1.POST("/:postId/comments", idempotent, h.AddComment)
		v1.GET("/:postId/comments", h.ListComments)
		v1.GET("/:postId/comments/around/:commentId", h.ListCommentsAround)
		v1.POST("/:postId/bookmark", h.ToggleBookmark)
		v1.DELETE("/:postId/bookmark", h.RemoveBookmark)
		v1.GET("/:postId/poll", h.GetPoll)
		v1.POST("/:postId/vote", h.CastVote)
	}

	// Comment-level routes
	comments := r.Group("/v1/comments")
	{
		comments.POST("/:commentId/reply", idempotent, h.CreateReply)
		comments.POST("/:commentId/like", h.ToggleCommentLike)
		comments.POST("/:commentId/dislike", h.ToggleCommentDislike)
		comments.DELETE("/:commentId", h.DeleteComment)
		comments.PATCH("/:commentId", h.EditComment)
	}
}

type CreatePollRequest struct {
	Question       string   `json:"question" binding:"required"`
	Options        []string `json:"options" binding:"required,min=2,max=6"`
	AllowsMultiple bool     `json:"allows_multiple"`
	DurationHours  *int     `json:"duration_hours"`
}

type CreatePostRequest struct {
	Text           string          `json:"text"`
	Visibility     string          `json:"visibility" binding:"required,oneof=public followers private"`
	ContentType    string          `json:"content_type"`
	MediaIDs       []string        `json:"media_ids"`
	Feeling        *string         `json:"feeling"`
	Activity       *string         `json:"activity"`
	ActivityDetail *string         `json:"activity_detail"`
	RichText       json.RawMessage `json:"rich_text"`
	Poll           *CreatePollRequest `json:"poll"`
	NoComments     bool            `json:"no_comments"`
	NoLikes        bool            `json:"no_likes"`
}

func (h *Handler) CreatePost(c *gin.Context) {
	authorIDStr := c.GetHeader("X-User-Id")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req CreatePostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	// Validate media IDs
	if len(req.MediaIDs) > 10 {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Maximum 10 media attachments", nil, nil)
		return
	}

	var mediaIDs []uuid.UUID
	for _, idStr := range req.MediaIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid media ID: "+idStr, nil, nil)
			return
		}
		mediaIDs = append(mediaIDs, id)
	}

	input := &service.CreatePostInput{
		AuthorID:       authorID,
		Text:           req.Text,
		Visibility:     req.Visibility,
		ContentType:    req.ContentType,
		MediaIDs:       mediaIDs,
		Feeling:        req.Feeling,
		Activity:       req.Activity,
		ActivityDetail: req.ActivityDetail,
		RichText:       req.RichText,
		NoComments:     req.NoComments,
		NoLikes:        req.NoLikes,
	}

	if req.Poll != nil {
		input.Poll = &service.CreatePollInput{
			Question:       req.Poll.Question,
			Options:        req.Poll.Options,
			AllowsMultiple: req.Poll.AllowsMultiple,
			DurationHours:  req.Poll.DurationHours,
		}
	}

	p, err := h.svc.CreatePost(c.Request.Context(), input)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

func (h *Handler) GetPost(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	viewerID := c.GetHeader("X-User-Id")
	var viewerUUID *uuid.UUID
	if id, err := uuid.Parse(viewerID); err == nil {
		viewerUUID = &id
	}

	p, err := h.svc.GetPost(c.Request.Context(), postID, viewerUUID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if p == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) GetRecentPosts(c *gin.Context) {
	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	posts, nextCursor, err := h.svc.GetRecentPosts(c.Request.Context(), nil, limit, cursor)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, posts, meta)
}

func (h *Handler) GetPostsByAuthor(c *gin.Context) {
	authorIDStr := c.Param("authorId")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid author ID", nil, nil)
		return
	}

	contentType := c.DefaultQuery("type", "")
	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	posts, nextCursor, err := h.svc.GetPostsByAuthor(c.Request.Context(), authorID, contentType, limit, cursor)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, posts, meta)
}

func (h *Handler) GetAuthorCounts(c *gin.Context) {
	authorIDStr := c.Param("authorId")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid author ID", nil, nil)
		return
	}

	counts, err := h.svc.GetAuthorCounts(c.Request.Context(), authorID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

type PinRequest struct {
	Pinned bool `json:"pinned"`
}

func (h *Handler) TogglePin(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	var req PinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.TogglePin(c.Request.Context(), postID, userID, req.Pinned); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]bool{"pinned": req.Pinned}, nil)
}

type ReactionRequest struct {
	Reaction string `json:"reaction" binding:"required"`
}

func (h *Handler) React(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	var req ReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.React(c.Request.Context(), postID, userID, req.Reaction); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "reacted"}, nil)
}

type CommentRequest struct {
	Text string `json:"text" binding:"required"`
}

func (h *Handler) AddComment(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	// Check if comments are disabled on this post
	post, _ := h.svc.GetPost(c.Request.Context(), postID, nil)
	if post != nil && post.NoComments {
		api.Error(c.Writer, http.StatusForbidden, "COMMENTS_DISABLED", "Comments are disabled on this post", nil, nil)
		return
	}

	var req CommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	comment, err := h.svc.CreateCommentPG(c.Request.Context(), postID, userID, req.Text)
	if err != nil {
		if err.Error() == "RATE_LIMITED" {
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many comments, please slow down", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, comment, nil)
}

func (h *Handler) GetPoll(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	viewerID := c.GetHeader("X-User-Id")
	var viewerUUID *uuid.UUID
	if id, err := uuid.Parse(viewerID); err == nil {
		viewerUUID = &id
	}

	poll, err := h.svc.GetPoll(c.Request.Context(), postID, viewerUUID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if poll == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Poll not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, poll, nil)
}

type VoteRequest struct {
	OptionID string `json:"option_id" binding:"required"`
}

func (h *Handler) CastVote(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	var req VoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	optionID, err := uuid.Parse(req.OptionID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid option ID", nil, nil)
		return
	}

	if err := h.svc.CastVote(c.Request.Context(), postID, optionID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "voted"}, nil)
}

func (h *Handler) Unreact(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	if err := h.svc.Unreact(c.Request.Context(), postID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unreacted"}, nil)
}

func (h *Handler) GetMyReaction(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	reaction, err := h.svc.GetMyReaction(c.Request.Context(), postID, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if reaction == "" {
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"reaction": nil}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"reaction": reaction}, nil)
}

func (h *Handler) ListComments(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	comments, nextCursor, err := h.svc.ListCommentsPG(c.Request.Context(), postID, cursor, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if comments == nil {
		comments = []postgres.Comment{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, comments, meta)
}

func (h *Handler) ListCommentsAround(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	comments, err := h.svc.GetCommentsAroundPG(c.Request.Context(), postID, commentID, limit)
	if err != nil {
		if err.Error() == "COMMENT_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if comments == nil {
		comments = []postgres.Comment{}
	}

	api.JSON(c.Writer, http.StatusOK, comments, nil)
}

func (h *Handler) AddBookmark(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	if err := h.svc.AddBookmark(c.Request.Context(), userID, postID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "bookmarked"}, nil)
}

func (h *Handler) RemoveBookmark(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	if err := h.svc.RemoveBookmark(c.Request.Context(), userID, postID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unbookmarked"}, nil)
}

func (h *Handler) GetBookmarks(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	posts, nextCursor, err := h.svc.GetBookmarks(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, posts, meta)
}

// ============================================================
// New Engagement Handlers
// ============================================================

func (h *Handler) ToggleLike(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	// Check if likes are disabled on this post
	post, _ := h.svc.GetPost(c.Request.Context(), postID, nil)
	if post != nil && post.NoLikes {
		api.Error(c.Writer, http.StatusForbidden, "LIKES_DISABLED", "Likes are disabled on this post", nil, nil)
		return
	}

	result, err := h.svc.ToggleLike(c.Request.Context(), postID, userID)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many like toggles, please slow down", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

type ShareRequest struct {
	ShareType string `json:"share_type" binding:"required,oneof=repost quote external"`
	QuoteText string `json:"quote_text"`
}

func (h *Handler) SharePost(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	var req ShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	result, err := h.svc.SharePost(c.Request.Context(), postID, userID, req.ShareType, req.QuoteText)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many shares, please slow down", nil, nil)
		case "CIRCLE_SHARE_RESTRICTED":
			api.Error(c.Writer, http.StatusForbidden, "CIRCLE_SHARE_RESTRICTED", "Cannot share this post type externally", nil, nil)
		case "ALREADY_SHARED":
			api.Error(c.Writer, http.StatusConflict, "ALREADY_SHARED", "You already reposted this", nil, nil)
		case "POST_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) ToggleBookmark(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	result, err := h.svc.ToggleBookmarkNew(c.Request.Context(), postID, userID)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many bookmark toggles, please slow down", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) CreateReply(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	var req CommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	reply, err := h.svc.CreateReply(c.Request.Context(), commentID, userID, req.Text)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many replies, please slow down", nil, nil)
		case "COMMENT_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil, nil)
		case "CANNOT_REPLY_TO_REPLY":
			api.Error(c.Writer, http.StatusBadRequest, "CANNOT_REPLY_TO_REPLY", "Cannot reply to a reply", nil, nil)
		case "REPLY_EXISTS":
			api.Error(c.Writer, http.StatusConflict, "REPLY_EXISTS", "This comment already has a reply", nil, nil)
		case "REPLY_OWNER_ONLY":
			api.Error(c.Writer, http.StatusForbidden, "REPLY_OWNER_ONLY", "Only the post owner can reply to comments", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, reply, nil)
}

func (h *Handler) ToggleCommentLike(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	result, err := h.svc.ToggleCommentLike(c.Request.Context(), commentID, userID)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many comment like toggles, please slow down", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) ToggleCommentDislike(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	result, err := h.svc.ToggleCommentDislike(c.Request.Context(), commentID, userID)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many comment dislike toggles, please slow down", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	err = h.svc.SoftDeleteComment(c.Request.Context(), commentID, userID)
	if err != nil {
		switch err.Error() {
		case "COMMENT_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil, nil)
		case "NOT_COMMENT_AUTHOR":
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You can only delete your own comments", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

type EditCommentRequest struct {
	Body string `json:"body" binding:"required"`
}

func (h *Handler) EditComment(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	var req EditCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	err = h.svc.EditComment(c.Request.Context(), commentID, userID, req.Body)
	if err != nil {
		switch err.Error() {
		case "COMMENT_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil, nil)
		case "NOT_COMMENT_AUTHOR":
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You can only edit your own comments", nil, nil)
		case "EDIT_WINDOW_EXPIRED":
			api.Error(c.Writer, http.StatusForbidden, "EDIT_WINDOW_EXPIRED", "Comments can only be edited within 15 minutes of creation", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

type BatchGetPostsRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

func (h *Handler) BatchGetPosts(c *gin.Context) {
	var req BatchGetPostsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if len(req.IDs) == 0 {
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{}, nil)
		return
	}

	if len(req.IDs) > 100 {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Maximum 100 IDs per request", nil, nil)
		return
	}

	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, idStr := range req.IDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID: "+idStr, nil, nil)
			return
		}
		ids = append(ids, id)
	}

	viewerID := c.GetHeader("X-User-Id")
	var viewerUUID *uuid.UUID
	if id, err := uuid.Parse(viewerID); err == nil {
		viewerUUID = &id
	}

	result, err := h.svc.GetPostsByIDs(c.Request.Context(), ids, viewerUUID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	// Convert uuid.UUID keys to strings for JSON serialization
	data := make(map[string]*service.PostDetail, len(result))
	for id, detail := range result {
		data[id.String()] = detail
	}

	api.JSON(c.Writer, http.StatusOK, data, nil)
}
