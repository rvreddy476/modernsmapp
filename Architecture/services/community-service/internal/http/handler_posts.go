package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/atpost/community-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Post, engagement and wiki routes. Every one calls a service method that
// opens with the access gate (service/access.go); none reaches the store
// (R-L1: the store calls from these handlers were where K-M1 and K-M2 lived).

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

// pathIDs parses the named uuid path params, answering 400 for the first bad
// one.
func pathIDs(c *gin.Context, names ...string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, len(names))
	for i, n := range names {
		id, err := uuid.Parse(c.Param(n))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+n, nil)
			return nil, false
		}
		out[i] = id
	}
	return out, true
}

// --- Posts ---

func (h *Handler) CreateCommunityPost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "spaceId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
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

	post, err := h.svc.CreateCommunityPost(c.Request.Context(), ids[0], ids[1], actorID, service.CreatePostParams{
		ContentType:  body.ContentType,
		Title:        body.Title,
		Body:         body.Body,
		TypePayload:  body.TypePayload,
		Attachments:  body.Attachments,
		Tags:         body.Tags,
		ParentPostID: body.ParentPostID,
	})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, post, nil)
}

func (h *Handler) GetCommunityPost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	viewer, ok := viewerFromHeader(c)
	if !ok {
		return
	}
	post, err := h.svc.GetCommunityPost(c.Request.Context(), ids[0], ids[1], viewer)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, post, nil)
}

func (h *Handler) ListCommunityPosts(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	viewer, ok := viewerFromHeader(c)
	if !ok {
		return
	}
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

	var spaceID *uuid.UUID
	if raw := c.Query("space_id"); raw != "" {
		sid, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid space_id", nil)
			return
		}
		spaceID = &sid
	}

	posts, err := h.svc.ListCommunityPosts(c.Request.Context(), ids[0], spaceID, viewer, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": posts}, nil)
}

func (h *Handler) ListFeaturedPosts(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	viewer, ok := viewerFromHeader(c)
	if !ok {
		return
	}
	posts, err := h.svc.ListFeaturedPosts(c.Request.Context(), ids[0], viewer)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": posts}, nil)
}

func (h *Handler) DeleteCommunityPost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteCommunityPost(c.Request.Context(), ids[0], ids[1], actorID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// --- Engagement ---

func (h *Handler) SparkCommunityPost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		IsSupernova bool `json:"is_supernova"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.SparkCommunityPost(c.Request.Context(), ids[0], ids[1], actorID, body.IsSupernova); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "sparked"}, nil)
}

func (h *Handler) StashCommunityPost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.StashCommunityPost(c.Request.Context(), ids[0], ids[1], actorID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "stashed"}, nil)
}

func (h *Handler) RecordCommunityPostView(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.RecordCommunityPostView(c.Request.Context(), ids[0], ids[1], actorID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "recorded"}, nil)
}

func (h *Handler) FeaturePost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Featured bool `json:"featured"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.FeaturePost(c.Request.Context(), ids[0], ids[1], actorID, body.Featured); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) PinCommunityPost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.PinCommunityPost(c.Request.Context(), ids[0], ids[1], actorID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "pinned"}, nil)
}

func (h *Handler) AcceptAnswerPost(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId", "postId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		AnswerID uuid.UUID `json:"answer_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AcceptAnswer(c.Request.Context(), ids[0], ids[1], body.AnswerID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

// --- Wiki ---

func (h *Handler) ListWikiPages(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	viewer, ok := viewerFromHeader(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	pages, err := h.svc.ListWikiPages(c.Request.Context(), ids[0], viewer, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": pages}, nil)
}

func (h *Handler) CreateWikiPage(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
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
	page, err := h.svc.CreateWikiPage(c.Request.Context(), ids[0], actorID, body.Title, body.Slug, body.Content, body.Category)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, page, nil)
}

func (h *Handler) GetWikiPage(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	viewer, ok := viewerFromHeader(c)
	if !ok {
		return
	}
	page, err := h.svc.GetWikiPage(c.Request.Context(), ids[0], c.Param("slug"), viewer)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

func (h *Handler) UpdateWikiPage(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Title   string `json:"title" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.UpdateWikiPage(c.Request.Context(), ids[0], actorID, c.Param("slug"), body.Title, body.Content); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}
