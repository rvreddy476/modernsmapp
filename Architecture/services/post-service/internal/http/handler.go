package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/http/middleware"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	svc         *service.Service
	rdb         *redis.Client
	internalKey string
}

func New(svc *service.Service, rdb *redis.Client) *Handler {
	return &Handler{svc: svc, rdb: rdb}
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	idempotent := middleware.Idempotency(h.rdb)

	// Apply internal service key enforcement to all /v1 routes.
	// Health and metrics endpoints registered outside this group remain public.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

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

		// Poll results (new)
		v1.POST("/:postId/poll/vote", h.CastPollVote)
		v1.GET("/:postId/poll/results", h.GetPollResults)

		// Tune (private negative signal)
		v1.POST("/:postId/tune", h.CreateTune)
		v1.DELETE("/:postId/tune", h.DeleteTune)
	}

	// Events
	r.POST("/v1/events", h.CreateEvent)
	r.GET("/v1/events/:eventId", h.GetEvent)
	r.POST("/v1/events/:eventId/rsvp", h.RSVPEvent)
	r.GET("/v1/events/:eventId/rsvps", h.GetEventRSVPs)

	// Stories
	stories := r.Group("/v1/stories")
	{
		stories.POST("", h.CreateStory)
		stories.GET("/feed", h.GetStoriesFeed)
		stories.GET("/:storyId", h.GetStory)
		stories.DELETE("/:storyId", h.DeleteStory)
		stories.POST("/:storyId/view", h.ViewStory)
	}

	// Multi-reactions (new)
	v1.POST("/:postId/react", h.ToggleReaction)
	v1.GET("/:postId/reactions/counts", h.GetReactionCounts)

	// Saved items
	saved := r.Group("/v1/saved")
	{
		saved.POST("", h.SaveItem)
		saved.GET("", h.ListSavedItems)
		saved.DELETE("/:savedId", h.UnsaveItem)
		saved.GET("/collections", h.ListCollections)
	}

	// Hashtag search
	r.GET("/v1/hashtags/:tag/posts", h.GetPostsByHashtag)

	// Video creator tools
	videos := r.Group("/v1/videos")
	{
		videos.GET("/:videoId", h.GetVideoDetail)
		videos.PATCH("/:videoId/trim", h.UpdateTrim)
		videos.PATCH("/:videoId/category", h.OverrideCategory)
		videos.POST("/:videoId/cover-frame", h.SetCoverFrame)
		videos.POST("/:videoId/publish", h.PublishVideo)
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

	// Flick Series
	series := r.Group("/v1/series")
	{
		series.POST("", h.CreateFlickSeries)
		series.GET("/:seriesId", h.GetFlickSeries)
		series.GET("/:seriesId/episodes", h.GetSeriesEpisodes)
		series.POST("/:seriesId/episodes", h.AddEpisodeToSeries)
		series.POST("/:seriesId/follow", h.FollowSeries)
		series.DELETE("/:seriesId/follow", h.UnfollowSeries)
	}
	// Creator's series list
	r.GET("/v1/creators/:creatorId/series", h.ListCreatorSeries)

	// Remix token
	v1.GET("/:postId/remix-token", h.GetRemixToken)
}

type CreatePollRequest struct {
	Question       string   `json:"question" binding:"required"`
	Options        []string `json:"options" binding:"required,min=2,max=6"`
	AllowsMultiple bool     `json:"allows_multiple"`
	DurationHours  *int     `json:"duration_hours"`
}

type CreatePostRequest struct {
	Text           string          `json:"text"`
	Visibility     string          `json:"visibility" binding:"required,oneof=public followers private unlisted"`
	VisibilityPolicy *struct {
		Mode       string   `json:"mode"`
		AllowLists []string `json:"allow_lists,omitempty"`
		AllowUsers []string `json:"allow_users,omitempty"`
		DenyUsers  []string `json:"deny_users,omitempty"`
	} `json:"visibility_policy,omitempty"`
	ContentType    string          `json:"content_type"`
	MediaIDs       []string        `json:"media_ids"`
	Feeling        *string         `json:"feeling"`
	Activity       *string         `json:"activity"`
	ActivityDetail *string         `json:"activity_detail"`
	RichText       json.RawMessage `json:"rich_text"`
	Poll           *CreatePollRequest `json:"poll"`
	NoComments     bool            `json:"no_comments"`
	NoLikes        bool            `json:"no_likes"`
	LocationName   *string         `json:"location_name"`
	LocationLat    *float64        `json:"location_lat"`
	LocationLng    *float64        `json:"location_lng"`
	PostType       string          `json:"post_type"`
	AppOrigin       string          `json:"app_origin"`
	ShareToPostbook bool            `json:"share_to_postbook"`
	// Reel metadata
	Title              string   `json:"title"`
	Tags               []string `json:"tags"`
	Category           string   `json:"category"`
	Language           string   `json:"language"`
	SEOTitle           string   `json:"seo_title"`
	PaidPromotion      bool     `json:"paid_promotion"`
	AlteredContent     bool     `json:"altered_content"`
	IsMadeForKids      bool     `json:"is_made_for_kids"`
	License            string   `json:"license"`
	AllowEmbedding     *bool    `json:"allow_embedding"`
	PublishToFeed      *bool    `json:"publish_to_feed"`
	RemixSetting       string   `json:"remix_setting"`
	CommentModeration  string   `json:"comment_moderation"`
	CommentAccess      string   `json:"comment_access"`
	RecordingDate      *string  `json:"recording_date"`
	RecordingLocation  string   `json:"recording_location"`
	CoverMediaID       *string  `json:"cover_media_id"`
	OriginalAudioVol   float32  `json:"original_audio_volume"`
	OverlayAudioVol    float32  `json:"overlay_audio_volume"`
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

	// Parse optional recording date
	var recordingDate *time.Time
	if req.RecordingDate != nil && *req.RecordingDate != "" {
		if t, err := time.Parse("2006-01-02", *req.RecordingDate); err == nil {
			recordingDate = &t
		}
	}

	// Parse optional cover media ID
	var coverMediaID *uuid.UUID
	if req.CoverMediaID != nil {
		if id, err := uuid.Parse(*req.CoverMediaID); err == nil {
			coverMediaID = &id
		}
	}

	// Default booleans for optional pointer fields
	allowEmbedding := true
	if req.AllowEmbedding != nil {
		allowEmbedding = *req.AllowEmbedding
	}
	publishToFeed := true
	if req.PublishToFeed != nil {
		publishToFeed = *req.PublishToFeed
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
		LocationName:   req.LocationName,
		LocationLat:    req.LocationLat,
		LocationLng:    req.LocationLng,
		PostType:       req.PostType,
		AppOrigin:       req.AppOrigin,
		ShareToPostbook: req.ShareToPostbook,
		Title:               req.Title,
		Tags:               req.Tags,
		Category:           req.Category,
		Language:           req.Language,
		SEOTitle:           req.SEOTitle,
		PaidPromotion:      req.PaidPromotion,
		AlteredContent:     req.AlteredContent,
		IsMadeForKids:      req.IsMadeForKids,
		License:            req.License,
		AllowEmbedding:     allowEmbedding,
		PublishToFeed:      publishToFeed,
		RemixSetting:       req.RemixSetting,
		CommentModeration:  req.CommentModeration,
		CommentAccess:      req.CommentAccess,
		RecordingDate:      recordingDate,
		RecordingLocation:  req.RecordingLocation,
		CoverMediaID:       coverMediaID,
		OriginalAudioVol:   req.OriginalAudioVol,
		OverlayAudioVol:    req.OverlayAudioVol,
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

// ============================================================
// Story Handlers
// ============================================================

type CreateStoryRequest struct {
	MediaURL       string  `json:"media_url" binding:"required"`
	MediaType      string  `json:"media_type" binding:"required,oneof=image video"`
	Caption        string  `json:"caption"`
	Visibility     string  `json:"visibility" binding:"required,oneof=public followers close_friends"`
	IsHighlight    bool    `json:"is_highlight"`
	HighlightGroup *string `json:"highlight_group"`
}

func (h *Handler) CreateStory(c *gin.Context) {
	authorIDStr := c.GetHeader("X-User-Id")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req CreateStoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	story, err := h.svc.CreateStory(c.Request.Context(), &service.CreateStoryInput{
		AuthorID:       authorID,
		MediaURL:       req.MediaURL,
		MediaType:      req.MediaType,
		Caption:        req.Caption,
		Visibility:     req.Visibility,
		IsHighlight:    req.IsHighlight,
		HighlightGroup: req.HighlightGroup,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, story, nil)
}

func (h *Handler) GetStory(c *gin.Context) {
	storyIDStr := c.Param("storyId")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid story ID", nil, nil)
		return
	}

	story, err := h.svc.GetStory(c.Request.Context(), storyID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if story == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Story not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, story, nil)
}

func (h *Handler) GetStoriesFeed(c *gin.Context) {
	// The followed user IDs come as a comma-separated query param set by the API gateway
	// or the client passes them explicitly.
	followedStr := c.DefaultQuery("followed_ids", "")
	if followedStr == "" {
		api.JSON(c.Writer, http.StatusOK, []postgres.Story{}, nil)
		return
	}

	parts := strings.Split(followedStr, ",")
	var followedIDs []uuid.UUID
	for _, p := range parts {
		id, err := uuid.Parse(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		followedIDs = append(followedIDs, id)
	}

	stories, err := h.svc.GetStoriesFeed(c.Request.Context(), followedIDs)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if stories == nil {
		stories = []postgres.Story{}
	}

	api.JSON(c.Writer, http.StatusOK, stories, nil)
}

func (h *Handler) DeleteStory(c *gin.Context) {
	authorIDStr := c.GetHeader("X-User-Id")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	storyIDStr := c.Param("storyId")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid story ID", nil, nil)
		return
	}

	if err := h.svc.DeleteStory(c.Request.Context(), storyID, authorID); err != nil {
		if err.Error() == "STORY_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Story not found or not yours", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) ViewStory(c *gin.Context) {
	storyIDStr := c.Param("storyId")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid story ID", nil, nil)
		return
	}

	if err := h.svc.ViewStory(c.Request.Context(), storyID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "viewed"}, nil)
}

// ============================================================
// Multi-Reaction Handlers
// ============================================================

type ToggleReactionRequest struct {
	ReactionType string `json:"reaction_type" binding:"required"`
}

func (h *Handler) ToggleReaction(c *gin.Context) {
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

	var req ToggleReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	result, err := h.svc.ToggleReaction(c.Request.Context(), postID, userID, req.ReactionType)
	if err != nil {
		switch err.Error() {
		case "INVALID_REACTION_TYPE":
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REACTION_TYPE", "Valid types: like, love, haha, wow, sad, angry", nil, nil)
		case "RATE_LIMITED":
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many reactions, please slow down", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) GetReactionCounts(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	counts, err := h.svc.GetReactionCounts(c.Request.Context(), postID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

// ============================================================
// Saved Item Handlers
// ============================================================

type SaveItemRequest struct {
	TargetType     string `json:"target_type" binding:"required,oneof=post video reel"`
	TargetID       string `json:"target_id" binding:"required"`
	CollectionName string `json:"collection_name"`
}

func (h *Handler) SaveItem(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req SaveItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	targetID, err := uuid.Parse(req.TargetID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid target ID", nil, nil)
		return
	}

	item, err := h.svc.SaveItem(c.Request.Context(), userID, req.TargetType, targetID, req.CollectionName)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, item, nil)
}

func (h *Handler) ListSavedItems(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	collectionName := c.DefaultQuery("collection", "")
	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	items, nextCursor, err := h.svc.ListSavedItems(c.Request.Context(), userID, collectionName, limit, cursor)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if items == nil {
		items = []postgres.SavedItem{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, items, meta)
}

func (h *Handler) UnsaveItem(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	savedIDStr := c.Param("savedId")
	savedID, err := uuid.Parse(savedIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid saved item ID", nil, nil)
		return
	}

	if err := h.svc.UnsaveItem(c.Request.Context(), savedID, userID); err != nil {
		if err.Error() == "SAVED_ITEM_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Saved item not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsaved"}, nil)
}

func (h *Handler) ListCollections(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	collections, err := h.svc.ListCollections(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if collections == nil {
		collections = []postgres.SavedCollection{}
	}

	api.JSON(c.Writer, http.StatusOK, collections, nil)
}

// ============================================================
// Hashtag Handler
// ============================================================

func (h *Handler) GetPostsByHashtag(c *gin.Context) {
	tag := c.Param("tag")
	if tag == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Hashtag is required", nil, nil)
		return
	}
	// Strip leading # if present
	tag = strings.TrimPrefix(tag, "#")

	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	posts, nextCursor, err := h.svc.GetPostsByHashtag(c.Request.Context(), tag, limit, cursor)
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
// Video Creator Tools Handlers
// ============================================================

func (h *Handler) GetVideoDetail(c *gin.Context) {
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil, nil)
		return
	}
	vm, err := h.svc.GetVideoDetail(c.Request.Context(), videoID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Video metadata not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, vm, nil)
}

func (h *Handler) UpdateTrim(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil, nil)
		return
	}

	var req struct {
		TrimStartMs int  `json:"trim_start_ms"`
		TrimEndMs   *int `json:"trim_end_ms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateVideoTrim(c.Request.Context(), videoID, userID, req.TrimStartMs, req.TrimEndMs); err != nil {
		if strings.Contains(err.Error(), "unauthorized") {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) OverrideCategory(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil, nil)
		return
	}

	var req struct {
		Category string `json:"category" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.OverrideCategory(c.Request.Context(), videoID, userID, req.Category); err != nil {
		if strings.Contains(err.Error(), "unauthorized") {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		if strings.Contains(err.Error(), "cannot classify") {
			api.Error(c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) SetCoverFrame(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil, nil)
		return
	}

	var req struct {
		CoverMediaID *string `json:"cover_media_id"`
		ThumbnailURL *string `json:"thumbnail_url"`
		TimestampMs  *int    `json:"timestamp_ms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	var coverMediaID *uuid.UUID
	if req.CoverMediaID != nil {
		id, err := uuid.Parse(*req.CoverMediaID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid cover media ID", nil, nil)
			return
		}
		coverMediaID = &id
	}

	if err := h.svc.SetCoverFrame(c.Request.Context(), videoID, userID, coverMediaID, req.ThumbnailURL); err != nil {
		if strings.Contains(err.Error(), "unauthorized") {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) PublishVideo(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil, nil)
		return
	}

	if err := h.svc.PublishVideo(c.Request.Context(), videoID, userID); err != nil {
		if strings.Contains(err.Error(), "unauthorized") {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		if strings.Contains(err.Error(), "not ready") {
			api.Error(c.Writer, http.StatusConflict, "NOT_READY", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "published"}, nil)
}

// ============================================================
// Poll Results Handlers
// ============================================================

func (h *Handler) CastPollVote(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	var req struct {
		OptionID uuid.UUID `json:"option_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.CastPollVote(c.Request.Context(), postID, req.OptionID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "VOTE_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (h *Handler) GetPollResults(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	results, err := h.svc.GetPollResults(c.Request.Context(), postID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if results == nil {
		results = []postgres.PollVoteResult{}
	}
	api.JSON(c.Writer, http.StatusOK, results, nil)
}

// ============================================================
// Tune Handlers
// ============================================================

func (h *Handler) CreateTune(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	if err := h.svc.CreateTune(c.Request.Context(), userID, postID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (h *Handler) DeleteTune(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	if err := h.svc.DeleteTune(c.Request.Context(), userID, postID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

// ============================================================
// Event Handlers
// ============================================================

func (h *Handler) CreateEvent(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req struct {
		Title        string     `json:"title" binding:"required"`
		Description  string     `json:"description"`
		StartsAt     time.Time  `json:"starts_at" binding:"required"`
		EndsAt       *time.Time `json:"ends_at"`
		LocationName *string    `json:"location_name"`
		LocationLat  *float64   `json:"location_lat"`
		LocationLng  *float64   `json:"location_lng"`
		CoverMediaID *uuid.UUID `json:"cover_media_id"`
		IsTicketed   bool       `json:"is_ticketed"`
		TicketPrice  *float64   `json:"ticket_price"`
		MaxAttendees *int       `json:"max_attendees"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	event, err := h.svc.CreateEvent(c.Request.Context(), service.CreateEventInput{
		CreatorID:    userID,
		Title:        req.Title,
		Description:  req.Description,
		StartsAt:     req.StartsAt,
		EndsAt:       req.EndsAt,
		LocationName: req.LocationName,
		LocationLat:  req.LocationLat,
		LocationLng:  req.LocationLng,
		CoverMediaID: req.CoverMediaID,
		IsTicketed:   req.IsTicketed,
		TicketPrice:  req.TicketPrice,
		MaxAttendees: req.MaxAttendees,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_EVENT_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, event, nil)
}

func (h *Handler) GetEvent(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid event ID", nil, nil)
		return
	}
	event, err := h.svc.GetEvent(c.Request.Context(), eventID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, event, nil)
}

func (h *Handler) RSVPEvent(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid event ID", nil, nil)
		return
	}
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.RSVPEvent(c.Request.Context(), eventID, userID, req.Status); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "RSVP_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (h *Handler) GetEventRSVPs(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid event ID", nil, nil)
		return
	}
	limit := 20
	offset := 0
	rsvps, err := h.svc.GetEventRSVPs(c.Request.Context(), eventID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if rsvps == nil {
		rsvps = []postgres.EventRSVP{}
	}
	api.JSON(c.Writer, http.StatusOK, rsvps, nil)
}

// ── Flick Series ─────────────────────────────────────────────

func (h *Handler) CreateFlickSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	var req struct {
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	fs, err := h.svc.CreateFlickSeries(c.Request.Context(), userID, req.Title, req.Description)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_SERIES_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, fs, nil)
}

func (h *Handler) GetFlickSeries(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil, nil)
		return
	}
	fs, err := h.svc.GetFlickSeries(c.Request.Context(), seriesID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, fs, nil)
}

func (h *Handler) GetSeriesEpisodes(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil, nil)
		return
	}
	items, err := h.svc.GetSeriesEpisodes(c.Request.Context(), seriesID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if items == nil {
		items = []postgres.FlickSeriesItem{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

func (h *Handler) AddEpisodeToSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil, nil)
		return
	}
	var req struct {
		PostID     uuid.UUID `json:"post_id" binding:"required"`
		EpisodeNum int       `json:"episode_num" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	item, err := h.svc.AddEpisodeToSeries(c.Request.Context(), userID, seriesID, req.PostID, req.EpisodeNum)
	if err != nil {
		if strings.Contains(err.Error(), "forbidden") {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusBadRequest, "ADD_EPISODE_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, item, nil)
}

func (h *Handler) FollowSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil, nil)
		return
	}
	if err := h.svc.FollowSeries(c.Request.Context(), userID, seriesID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnfollowSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil, nil)
		return
	}
	if err := h.svc.UnfollowSeries(c.Request.Context(), userID, seriesID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) ListCreatorSeries(c *gin.Context) {
	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid creator id", nil, nil)
		return
	}
	series, err := h.svc.ListFlickSeriesByCreator(c.Request.Context(), creatorID, 20, 0)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if series == nil {
		series = []postgres.FlickSeries{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"series": series}, nil)
}

func (h *Handler) GetRemixToken(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid post id", nil, nil)
		return
	}
	result, err := h.svc.GetRemixToken(c.Request.Context(), postID)
	if err != nil {
		if strings.Contains(err.Error(), "does not allow") {
			api.Error(c.Writer, http.StatusForbidden, "REMIX_NOT_ALLOWED", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}
