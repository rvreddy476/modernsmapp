package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/memories-service/internal/service"
	"github.com/atpost/memories-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc         *service.Service
	internalKey string
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	v1 := r.Group("/v1/memories")
	{
		// On This Day
		v1.GET("/on-this-day", h.GetOnThisDay)

		// SlamBooks
		v1.GET("/slambook-template-packs", h.ListSlambookTemplatePacks)
		v1.POST("/slambooks", h.CreateSlambook)
		v1.GET("/slambooks", h.ListSlambooks)
		v1.GET("/slambooks/:slambookId", h.GetSlambook)
		v1.POST("/slambooks/:slambookId/share-link", h.CreateSlambookShareLink)
		v1.POST("/slambooks/:slambookId/invites", h.CreateSlambookInvites)
		v1.POST("/slambooks/:slambookId/responses", h.SaveSlambookResponse)
		v1.GET("/slambooks/:slambookId/opinion-space", h.ListSlambookOpinionSpace)
		v1.GET("/slambooks/:slambookId/moderation", h.ListSlambookModerationQueue)
		v1.POST("/slambooks/:slambookId/moderation/:sessionId", h.ModerateSlambookSession)
		v1.POST("/slambooks/:slambookId/opinion-space/:itemId/pin", h.SetSlambookOpinionPinned)
		v1.POST("/slambooks/:slambookId/opinion-space/reorder", h.ReorderSlambookOpinionItems)
		v1.POST("/slambooks/:slambookId/archive", h.ArchiveSlambook)
		v1.GET("/share/:token", h.GetSlambookByShareToken)

		// Collections
		v1.POST("/collections", h.CreateCollection)
		v1.GET("/collections", h.ListCollections)
		v1.GET("/collections/:collectionId", h.GetCollection)
		v1.PUT("/collections/:collectionId", h.UpdateCollection)
		v1.DELETE("/collections/:collectionId", h.DeleteCollection)

		// Collection Items
		v1.POST("/collections/:collectionId/items", h.AddCollectionItem)
		v1.GET("/collections/:collectionId/items", h.ListCollectionItems)
		v1.DELETE("/collections/:collectionId/items/:itemId", h.RemoveCollectionItem)

		// Preferences
		v1.GET("/preferences", h.GetPreferences)
		v1.PUT("/preferences", h.UpdatePreferences)
	}
}

func (h *Handler) GetOnThisDay(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	memories, err := h.svc.GetOnThisDay(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MEMORIES_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": memories}, nil)
}

// --- Collections ---

func (h *Handler) CreateCollection(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		CoverURL    *string `json:"cover_url"`
		Visibility  string  `json:"visibility"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	col, err := h.svc.CreateCollection(c.Request.Context(), &service.CreateCollectionInput{
		UserID:      userID,
		Title:       body.Title,
		Description: body.Description,
		CoverURL:    body.CoverURL,
		Visibility:  body.Visibility,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, col, nil)
}

func (h *Handler) GetCollection(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	colID, err := uuid.Parse(c.Param("collectionId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid collection id", nil)
		return
	}

	col, err := h.svc.GetCollection(c.Request.Context(), colID, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, col, nil)
}

func (h *Handler) ListCollections(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	collections, err := h.svc.ListCollections(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": collections}, nil)
}

func (h *Handler) UpdateCollection(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	colID, err := uuid.Parse(c.Param("collectionId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid collection id", nil)
		return
	}

	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateCollection(c.Request.Context(), colID, userID, body.Title, body.Description, body.Visibility); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) DeleteCollection(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	colID, err := uuid.Parse(c.Param("collectionId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid collection id", nil)
		return
	}

	if err := h.svc.DeleteCollection(c.Request.Context(), colID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "DELETE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// --- Collection Items ---

func (h *Handler) AddCollectionItem(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	colID, err := uuid.Parse(c.Param("collectionId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid collection id", nil)
		return
	}

	var body struct {
		PostID   *string `json:"post_id"`
		MediaURL *string `json:"media_url"`
		Caption  string  `json:"caption"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	var postID *uuid.UUID
	if body.PostID != nil {
		parsed, err := uuid.Parse(*body.PostID)
		if err == nil {
			postID = &parsed
		}
	}

	item, err := h.svc.AddCollectionItem(c.Request.Context(), &service.AddItemInput{
		CollectionID: colID,
		UserID:       userID,
		PostID:       postID,
		MediaURL:     body.MediaURL,
		Caption:      body.Caption,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "ADD_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, item, nil)
}

func (h *Handler) ListCollectionItems(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	colID, err := uuid.Parse(c.Param("collectionId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid collection id", nil)
		return
	}
	limit, offset := parsePagination(c)

	items, err := h.svc.ListCollectionItems(c.Request.Context(), colID, userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": items}, nil)
}

func (h *Handler) RemoveCollectionItem(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	colID, err := uuid.Parse(c.Param("collectionId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid collection id", nil)
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid item id", nil)
		return
	}

	if err := h.svc.RemoveCollectionItem(c.Request.Context(), itemID, colID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REMOVE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

// --- Preferences ---

func (h *Handler) GetPreferences(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	prefs, err := h.svc.GetPreferences(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "PREFS_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, prefs, nil)
}

func (h *Handler) UpdatePreferences(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Enabled          *bool    `json:"enabled"`
		HiddenYears      []int    `json:"hidden_years"`
		HiddenPeopleIDs  []string `json:"hidden_people_ids"`
		NotificationTime string   `json:"notification_time"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	prefs := &postgres.Preferences{
		UserID:           userID,
		Enabled:          true,
		HiddenYears:      body.HiddenYears,
		HiddenPeopleIDs:  body.HiddenPeopleIDs,
		NotificationTime: body.NotificationTime,
	}
	if body.Enabled != nil {
		prefs.Enabled = *body.Enabled
	}
	if prefs.NotificationTime == "" {
		prefs.NotificationTime = "09:00"
	}
	if prefs.HiddenYears == nil {
		prefs.HiddenYears = []int{}
	}
	if prefs.HiddenPeopleIDs == nil {
		prefs.HiddenPeopleIDs = []string{}
	}

	if err := h.svc.UpdatePreferences(c.Request.Context(), prefs); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// --- Helpers ---

func parseUserID(c *gin.Context) (uuid.UUID, bool) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return uuid.Nil, false
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_USER_ID", "invalid user id", nil)
		return uuid.Nil, false
	}
	return uid, true
}

func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	offset := 0
	if v := c.Query("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}
	if v := c.Query("offset"); v != "" {
		if o, err := strconv.Atoi(v); err == nil && o >= 0 {
			offset = o
		}
	}
	return limit, offset
}
