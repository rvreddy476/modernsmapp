package http

import (
	"net/http"

	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type UpdateRulesRequest struct {
	Rules []RuleItem `json:"rules" binding:"required"`
}

type RuleItem struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
}

// --- Rules ---

func (h *Handler) GetGroupRules(c *gin.Context) {
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	actorID, ok := optionalUserID(c)
	if !ok {
		return
	}
	rules, err := h.svc.GetGroupRules(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if rules == nil {
		rules = []store.GroupRule{}
	}
	api.JSON(c.Writer, http.StatusOK, rules, nil)
}

func (h *Handler) UpdateGroupRules(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	var req UpdateRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	var rules []store.GroupRule
	for _, item := range req.Rules {
		rules = append(rules, store.GroupRule{
			Title:       item.Title,
			Description: item.Description,
		})
	}

	if err := h.svc.UpdateGroupRules(c.Request.Context(), actorID, groupID, rules); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rules_updated"}, nil)
}

// --- Media ---

func (h *Handler) GetGroupMedia(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}
	limit, offset := parsePagination(c)
	posts, err := h.svc.GetGroupMedia(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if posts == nil {
		posts = []store.GroupMediaItem{}
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}

// ── Group Channels ───────────────────────────────────────────

func (h *Handler) ListGroupChannels(c *gin.Context) {
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	actorID, ok := optionalUserID(c)
	if !ok {
		return
	}
	channels, err := h.svc.ListGroupChannels(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if channels == nil {
		channels = []store.GroupChannel{}
	}
	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

func (h *Handler) CreateGroupChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	var req struct {
		Name        string `json:"name" binding:"required"`
		Type        string `json:"type" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	ch, err := h.svc.CreateGroupChannel(c.Request.Context(), actorID, groupID, req.Name, req.Type, req.Description)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ch, nil)
}

func (h *Handler) DeleteGroupChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid channel id", nil)
		return
	}
	if err := h.svc.DeleteGroupChannel(c.Request.Context(), actorID, groupID, channelID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Wiki ─────────────────────────────────────────────────────

func (h *Handler) ListWikiPages(c *gin.Context) {
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	actorID, ok := optionalUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	pages, err := h.svc.ListWikiPages(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if pages == nil {
		pages = []store.WikiPage{}
	}
	api.JSON(c.Writer, http.StatusOK, pages, nil)
}

func (h *Handler) CreateWikiPage(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	var req struct {
		Title   string `json:"title" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	page, err := h.svc.CreateWikiPage(c.Request.Context(), actorID, groupID, req.Title, req.Content)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, page, nil)
}

func (h *Handler) UpdateWikiPage(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	pageID, err := uuid.Parse(c.Param("pageId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid page id", nil)
		return
	}
	var req struct {
		Title   string `json:"title" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	page, err := h.svc.UpdateWikiPage(c.Request.Context(), actorID, groupID, pageID, req.Title, req.Content)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

func (h *Handler) DeleteWikiPage(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	pageID, err := uuid.Parse(c.Param("pageId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid page id", nil)
		return
	}
	if err := h.svc.DeleteWikiPage(c.Request.Context(), actorID, groupID, pageID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}
