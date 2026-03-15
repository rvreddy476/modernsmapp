package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/live-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Guests ---

func (h *Handler) InviteGuest(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	var body struct {
		UserID uuid.UUID `json:"user_id"`
		Role   string    `json:"role"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	_ = userID // host identity recorded for authorization

	if err := h.svc.InviteGuest(c.Request.Context(), streamID, body.UserID, body.Role); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVITE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, map[string]string{"status": "invited"}, nil)
}

func (h *Handler) UpdateGuestStatus(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}
	guestID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil, nil)
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateGuestStatus(c.Request.Context(), streamID, guestID, body.Status); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": body.Status}, nil)
}

func (h *Handler) GetStreamGuests(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	guests, err := h.svc.GetStreamGuests(c.Request.Context(), streamID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": guests}, nil)
}

// --- Polls ---

func (h *Handler) CreateLivePoll(c *gin.Context) {
	_, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	var body struct {
		Question string          `json:"question"`
		Options  json.RawMessage `json:"options"`
		EndsAt   *string         `json:"ends_at"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	input := &service.CreatePollInput{
		StreamID: streamID,
		Question: body.Question,
		Options:  body.Options,
	}
	if body.EndsAt != nil {
		if t, err := parseRFC3339(body.EndsAt); err == nil {
			input.EndsAt = t
		}
	}

	poll, err := h.svc.CreateLivePoll(c.Request.Context(), input)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, poll, nil)
}

func (h *Handler) VoteOnPoll(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	pollID, err := uuid.Parse(c.Param("pollId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid poll id", nil, nil)
		return
	}

	var body struct {
		OptionID string `json:"option_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VoteOnPoll(c.Request.Context(), pollID, userID, body.OptionID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "VOTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "voted"}, nil)
}

func (h *Handler) GetLivePolls(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	polls, err := h.svc.GetLivePolls(c.Request.Context(), streamID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": polls}, nil)
}

// --- Gifts ---

func (h *Handler) SendGift(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	var body struct {
		GiftType  string  `json:"gift_type"`
		GiftCount int     `json:"gift_count"`
		ValueINR  float64 `json:"value_inr"`
		Message   *string `json:"message"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	gift, err := h.svc.SendGift(c.Request.Context(), &service.SendGiftInput{
		StreamID:  streamID,
		SenderID:  userID,
		GiftType:  body.GiftType,
		GiftCount: body.GiftCount,
		ValueINR:  body.ValueINR,
		Message:   body.Message,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "GIFT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gift, nil)
}

func (h *Handler) GetStreamGifts(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	limit := 50
	if v := c.Query("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	gifts, err := h.svc.GetStreamGifts(c.Request.Context(), streamID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": gifts}, nil)
}

// --- Moderation ---

func (h *Handler) MuteUser(c *gin.Context) {
	mutedBy, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	var body struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.MuteUser(c.Request.Context(), streamID, body.UserID, mutedBy); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "MUTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "muted"}, nil)
}

func (h *Handler) UnmuteUser(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil, nil)
		return
	}

	if err := h.svc.UnmuteUser(c.Request.Context(), streamID, targetID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UNMUTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unmuted"}, nil)
}

func (h *Handler) GetMutedUsers(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	mutes, err := h.svc.GetMutedUsers(c.Request.Context(), streamID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": mutes}, nil)
}

func (h *Handler) AddWordFilter(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	var body struct {
		Word string `json:"word"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.AddWordFilter(c.Request.Context(), streamID, body.Word, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "ADD_FILTER_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, map[string]string{"status": "added"}, nil)
}

func (h *Handler) RemoveWordFilter(c *gin.Context) {
	_, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	word := c.Query("word")
	if word == "" {
		var body struct {
			Word string `json:"word"`
		}
		if err := c.ShouldBindJSON(&body); err == nil {
			word = body.Word
		}
	}
	if word == "" {
		api.Error(c.Writer, http.StatusBadRequest, "MISSING_WORD", "word is required", nil, nil)
		return
	}

	if err := h.svc.RemoveWordFilter(c.Request.Context(), streamID, word); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "REMOVE_FILTER_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) GetWordFilters(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	filters, err := h.svc.GetWordFilters(c.Request.Context(), streamID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": filters}, nil)
}

// --- DVR ---

func (h *Handler) GetDVRSegments(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	segments, err := h.svc.GetDVRSegments(c.Request.Context(), streamID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": segments}, nil)
}

// --- Audio Rooms ---

func (h *Handler) CreateAudioRoom(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Topic            string     `json:"topic"`
		Description      string     `json:"description"`
		Type             string     `json:"type"`
		CommunityID      *uuid.UUID `json:"community_id"`
		ScheduledAt      *string    `json:"scheduled_at"`
		RecordingEnabled bool       `json:"recording_enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	input := &service.CreateAudioRoomInput{
		HostID:           userID,
		Topic:            body.Topic,
		Description:      body.Description,
		Type:             body.Type,
		CommunityID:      body.CommunityID,
		RecordingEnabled: body.RecordingEnabled,
	}
	if body.ScheduledAt != nil {
		if t, err := parseRFC3339(body.ScheduledAt); err == nil {
			input.ScheduledAt = t
		}
	}

	room, err := h.svc.CreateAudioRoom(c.Request.Context(), input)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, room, nil)
}

func (h *Handler) ListLiveAudioRooms(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}

	rooms, err := h.svc.ListLiveAudioRooms(c.Request.Context(), limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": rooms}, nil)
}

func (h *Handler) GetAudioRoom(c *gin.Context) {
	roomID, err := uuid.Parse(c.Param("roomId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid room id", nil, nil)
		return
	}

	room, err := h.svc.GetAudioRoom(c.Request.Context(), roomID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "audio room not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, room, nil)
}

func (h *Handler) StartAudioRoom(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	roomID, err := uuid.Parse(c.Param("roomId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid room id", nil, nil)
		return
	}

	if err := h.svc.StartAudioRoom(c.Request.Context(), roomID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "START_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "live"}, nil)
}

func (h *Handler) EndAudioRoom(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	roomID, err := uuid.Parse(c.Param("roomId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid room id", nil, nil)
		return
	}

	if err := h.svc.EndAudioRoom(c.Request.Context(), roomID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "END_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ended"}, nil)
}

func (h *Handler) JoinAudioRoom(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	roomID, err := uuid.Parse(c.Param("roomId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid room id", nil, nil)
		return
	}

	if err := h.svc.JoinAudioRoom(c.Request.Context(), roomID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "JOIN_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "joined"}, nil)
}

func (h *Handler) LeaveAudioRoom(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	roomID, err := uuid.Parse(c.Param("roomId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid room id", nil, nil)
		return
	}

	if err := h.svc.LeaveAudioRoom(c.Request.Context(), roomID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "LEAVE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "left"}, nil)
}

func (h *Handler) GetAudioRoomMembers(c *gin.Context) {
	roomID, err := uuid.Parse(c.Param("roomId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid room id", nil, nil)
		return
	}

	members, err := h.svc.GetAudioRoomMembers(c.Request.Context(), roomID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": members}, nil)
}

// --- Helpers ---

func parseRFC3339(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
