package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Engagement request structs ---

type SparkRequest struct {
	IsSupernova bool `json:"is_supernova"`
}

type EchoRequest struct {
	EchoType string `json:"echo_type"`
}

type AddCommentRequest struct {
	Body     string     `json:"body" binding:"required"`
	ParentID *uuid.UUID `json:"parent_id"`
}

type VoteRequest struct {
	OptionIndexes []int `json:"option_indexes" binding:"required"`
}

type RSVPRequest struct {
	Status string `json:"status" binding:"required"`
}

// --- Engagement handlers ---

func (h *Handler) SparkUpdate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	var req SparkRequest
	// Body is optional; default isSupernova = false
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.SparkUpdate(c.Request.Context(), channelID, updateID, userID, req.IsSupernova); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "sparked"}, nil)
}

func (h *Handler) UnsparkUpdate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	if err := h.svc.UnsparkUpdate(c.Request.Context(), channelID, updateID, userID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsparked"}, nil)
}

func (h *Handler) StashUpdate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	if err := h.svc.StashUpdate(c.Request.Context(), channelID, updateID, userID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "stashed"}, nil)
}

func (h *Handler) UnstashUpdate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	if err := h.svc.UnstashUpdate(c.Request.Context(), channelID, updateID, userID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unstashed"}, nil)
}

func (h *Handler) EchoUpdate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	var req EchoRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.EchoUpdate(c.Request.Context(), channelID, updateID, userID, req.EchoType); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, map[string]string{"status": "echoed"}, nil)
}

func (h *Handler) RecordView(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	if err := h.svc.RecordView(c.Request.Context(), channelID, updateID, userID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "viewed"}, nil)
}

func (h *Handler) ListComments(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	sort := c.DefaultQuery("sort", "newest")
	limit, offset := parsePagination(c)

	comments, err := h.svc.ListComments(c.Request.Context(), channelID, updateID, sort, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, comments, nil)
}

func (h *Handler) AddComment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	var req AddCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	comment, err := h.svc.AddComment(c.Request.Context(), channelID, updateID, userID, req.Body, req.ParentID)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, comment, nil)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	if err := h.svc.DeleteComment(c.Request.Context(), channelID, updateID, commentID, userID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) PinComment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil, nil)
		return
	}

	if err := h.svc.PinComment(c.Request.Context(), channelID, updateID, commentID, userID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "pinned"}, nil)
}

func (h *Handler) VoteOnPoll(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	var req VoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VoteOnPoll(c.Request.Context(), channelID, updateID, userID, req.OptionIndexes); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "voted"}, nil)
}

func (h *Handler) GetPollResults(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	var viewerID *uuid.UUID
	if uid, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		viewerID = &uid
	}

	results, err := h.svc.GetPollResults(c.Request.Context(), channelID, updateID, viewerID)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, results, nil)
}

func (h *Handler) RSVPEvent(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	var req RSVPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.RSVPEvent(c.Request.Context(), channelID, updateID, userID, req.Status); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rsvp_recorded"}, nil)
}

func (h *Handler) ListAttendees(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil, nil)
		return
	}

	status := c.Query("status")
	limit, offset := parsePagination(c)

	attendees, err := h.svc.ListAttendees(c.Request.Context(), channelID, updateID, status, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, attendees, nil)
}
