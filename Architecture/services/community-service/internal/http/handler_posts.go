package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) registerPostRoutes(v1 *gin.RouterGroup) {
	v1.GET("/:communityId/posts", h.ListCommunityPosts)
	v1.POST("/:communityId/spaces/:spaceId/posts", h.CreateCommunityPost)
	v1.GET("/:communityId/posts/:postId", h.GetCommunityPost)
	v1.DELETE("/:communityId/posts/:postId", h.DeleteCommunityPost)
	v1.GET("/:communityId/featured", h.ListFeaturedPosts)
	v1.POST("/:communityId/posts/:postId/spark", h.SparkCommunityPost)
	v1.POST("/:communityId/posts/:postId/stash", h.StashCommunityPost)
	v1.POST("/:communityId/posts/:postId/view", h.RecordCommunityPostView)
	v1.POST("/:communityId/posts/:postId/feature", h.FeaturePost)
	v1.POST("/:communityId/posts/:postId/pin", h.PinCommunityPost)
	v1.POST("/:communityId/posts/:postId/accept-answer", h.AcceptAnswerPost)

	// Wiki
	v1.GET("/:communityId/wiki", h.ListWikiPages)
	v1.POST("/:communityId/wiki", h.CreateWikiPage)
	v1.GET("/:communityId/wiki/:slug", h.GetWikiPage)
	v1.PUT("/:communityId/wiki/:slug", h.UpdateWikiPage)
}

// --- Posts ---

func (h *Handler) CreateCommunityPost(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}
	spaceID, err := uuid.Parse(c.Param("spaceId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid space id", nil)
		return
	}
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "missing user id", nil)
		return
	}

	var body struct {
		ContentType  string          `json:"content_type"`
		Title        *string         `json:"title"`
		Body         *string         `json:"body"`
		TypePayload  json.RawMessage `json:"type_payload"`
		Attachments  json.RawMessage `json:"attachments"`
		Tags         []string        `json:"tags"`
		ParentPostID *uuid.UUID      `json:"parent_post_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	ct := body.ContentType
	if ct == "" {
		ct = "text"
	}
	tp := body.TypePayload
	if tp == nil {
		tp = json.RawMessage(`{}`)
	}
	att := body.Attachments
	if att == nil {
		att = json.RawMessage(`[]`)
	}

	depth := 0
	if body.ParentPostID != nil {
		depth = 1 // max 2-level threading
	}

	post := &store.CommunityPost{
		CommunityID:  communityID,
		SpaceID:      spaceID,
		AuthorID:     userID,
		ContentType:  ct,
		Title:        body.Title,
		Body:         body.Body,
		TypePayload:  tp,
		Attachments:  att,
		Tags:         body.Tags,
		ParentPostID: body.ParentPostID,
		ThreadDepth:  depth,
	}

	if err := h.svc.Store().CreateCommunityPost(c.Request.Context(), post); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, post, nil)
}

func (h *Handler) GetCommunityPost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	post, err := h.svc.Store().GetCommunityPost(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "post not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, post, nil)
}

func (h *Handler) ListCommunityPosts(c *gin.Context) {
	spaceID := c.Query("space_id")
	limit := 20
	offset := 0
	if v := c.Query("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}
	if v := c.Query("offset"); v != "" {
		if o, err := strconv.Atoi(v); err == nil && o >= 0 {
			offset = o
		}
	}

	if spaceID != "" {
		sid, err := uuid.Parse(spaceID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid space_id", nil)
			return
		}
		posts, err := h.svc.Store().ListSpacePosts(c.Request.Context(), sid, limit, offset)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, map[string]any{"items": posts}, nil)
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}
	// List all posts across community (aggregated feed)
	posts, err := h.svc.Store().ListCommunityPosts(c.Request.Context(), communityID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": posts}, nil)
}

func (h *Handler) ListFeaturedPosts(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}
	posts, err := h.svc.Store().ListFeaturedPosts(c.Request.Context(), communityID, 10)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": posts}, nil)
}

func (h *Handler) DeleteCommunityPost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	if err := h.svc.Store().DeleteCommunityPost(c.Request.Context(), postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "DELETE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// --- Engagement ---

func (h *Handler) SparkCommunityPost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "missing user id", nil)
		return
	}
	var body struct {
		IsSupernova bool `json:"is_supernova"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.Store().SparkCommunityPost(c.Request.Context(), postID, userID, body.IsSupernova); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "SPARK_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "sparked"}, nil)
}

func (h *Handler) StashCommunityPost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	userID := c.GetHeader("X-User-Id")
	if err := h.svc.Store().StashCommunityPost(c.Request.Context(), postID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "STASH_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "stashed"}, nil)
}

func (h *Handler) RecordCommunityPostView(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	userID := c.GetHeader("X-User-Id")
	_ = h.svc.Store().RecordCommunityPostView(c.Request.Context(), postID, userID)
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "recorded"}, nil)
}

func (h *Handler) FeaturePost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	var body struct {
		Featured bool `json:"featured"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.Store().MarkFeatured(c.Request.Context(), postID, body.Featured); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FEATURE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) PinCommunityPost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	if err := h.svc.Store().PinCommunityPost(c.Request.Context(), postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "PIN_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "pinned"}, nil)
}

func (h *Handler) AcceptAnswerPost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	var body struct {
		AnswerID uuid.UUID `json:"answer_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.Store().AcceptAnswer(c.Request.Context(), postID, body.AnswerID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "ACCEPT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

// --- Wiki ---

func (h *Handler) ListWikiPages(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}
	pages, err := h.svc.Store().ListWikiPages(c.Request.Context(), communityID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": pages}, nil)
}

func (h *Handler) CreateWikiPage(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}
	userID := c.GetHeader("X-User-Id")
	var body struct {
		Title    string  `json:"title" binding:"required"`
		Slug     string  `json:"slug" binding:"required"`
		Content  string  `json:"content" binding:"required"`
		Category *string `json:"category"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	page := &store.WikiPage{
		CommunityID: communityID,
		Title:       body.Title,
		Slug:        body.Slug,
		Content:     body.Content,
		Category:    body.Category,
		CreatedBy:   userID,
	}
	if err := h.svc.Store().CreateWikiPage(c.Request.Context(), page); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, page, nil)
}

func (h *Handler) GetWikiPage(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}
	slug := c.Param("slug")
	page, err := h.svc.Store().GetWikiPage(c.Request.Context(), communityID, slug)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "wiki page not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

func (h *Handler) UpdateWikiPage(c *gin.Context) {
	communityID, _ := uuid.Parse(c.Param("communityId"))
	slug := c.Param("slug")
	userID := c.GetHeader("X-User-Id")
	var body struct {
		Title   string `json:"title" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	page, err := h.svc.Store().GetWikiPage(c.Request.Context(), communityID, slug)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "page not found", nil)
		return
	}
	if err := h.svc.Store().UpdateWikiPage(c.Request.Context(), page.ID, body.Title, body.Content, "", userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}
