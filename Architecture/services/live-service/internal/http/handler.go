package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/live-service/internal/service"
	"github.com/atpost/live-service/internal/store/postgres"
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
	v1 := r.Group("/v1/live")
	{
		// Stream lifecycle
		v1.POST("/streams", h.CreateStream)
		v1.GET("/streams", h.ListLiveStreams)
		v1.GET("/streams/:streamId", h.GetStream)
		v1.GET("/streams/:streamId/playback/*asset", h.ProxyPlayback)
		v1.HEAD("/streams/:streamId/playback/*asset", h.ProxyPlayback)
		v1.OPTIONS("/streams/:streamId/playback/*asset", h.ProxyPlayback)
		v1.POST("/streams/:streamId/go-live", h.GoLive)
		v1.POST("/streams/:streamId/end", h.EndStream)
		v1.GET("/hosts/:hostId/streams", h.ListHostStreams)

		// Viewer interaction
		v1.POST("/streams/:streamId/join", h.JoinStream)
		v1.POST("/streams/:streamId/leave", h.LeaveStream)
		v1.POST("/streams/:streamId/like", h.LikeStream)
		v1.GET("/streams/:streamId/viewers", h.GetViewerCount)

		// Chat
		v1.POST("/streams/:streamId/chat", h.SendChatMessage)
		v1.GET("/streams/:streamId/chat", h.GetChatMessages)
		v1.POST("/streams/:streamId/chat/:messageId/pin", h.PinMessage)

		// Scheduled
		v1.POST("/schedule", h.ScheduleStream)
		v1.GET("/schedule/upcoming", h.ListUpcomingStreams)

		// Guests
		v1.POST("/streams/:streamId/guests", h.InviteGuest)
		v1.PATCH("/streams/:streamId/guests/:userId/status", h.UpdateGuestStatus)
		v1.GET("/streams/:streamId/guests", h.GetStreamGuests)

		// Polls
		v1.POST("/streams/:streamId/polls", h.CreateLivePoll)
		v1.POST("/streams/:streamId/polls/:pollId/vote", h.VoteOnPoll)
		v1.GET("/streams/:streamId/polls", h.GetLivePolls)

		// Gifts
		v1.POST("/streams/:streamId/gifts", h.SendGift)
		v1.GET("/streams/:streamId/gifts", h.GetStreamGifts)
		v1.GET("/streams/:streamId/gifts/leaderboard", h.GetGiftLeaderboard)

		// Moderation
		v1.POST("/streams/:streamId/mutes", h.MuteUser)
		v1.DELETE("/streams/:streamId/mutes/:userId", h.UnmuteUser)
		v1.GET("/streams/:streamId/mutes", h.GetMutedUsers)
		v1.POST("/streams/:streamId/word-filters", h.AddWordFilter)
		v1.DELETE("/streams/:streamId/word-filters", h.RemoveWordFilter)
		v1.GET("/streams/:streamId/word-filters", h.GetWordFilters)

		// DVR
		v1.GET("/streams/:streamId/dvr", h.GetDVRSegments)
	}

	// Audio Rooms
	audioRooms := r.Group("/v1/audio-rooms")
	{
		audioRooms.POST("", h.CreateAudioRoom)
		audioRooms.GET("", h.ListLiveAudioRooms)
		audioRooms.GET("/:roomId", h.GetAudioRoom)
		audioRooms.POST("/:roomId/start", h.StartAudioRoom)
		audioRooms.POST("/:roomId/end", h.EndAudioRoom)
		audioRooms.POST("/:roomId/join", h.JoinAudioRoom)
		audioRooms.POST("/:roomId/leave", h.LeaveAudioRoom)
		audioRooms.GET("/:roomId/members", h.GetAudioRoomMembers)
	}
}

// --- Stream Lifecycle ---

func (h *Handler) CreateStream(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Title        string  `json:"title"`
		Description  string  `json:"description"`
		ThumbnailURL *string `json:"thumbnail_url"`
		Visibility   string  `json:"visibility"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	st, err := h.svc.CreateStream(c.Request.Context(), &service.CreateStreamInput{
		HostID:       userID,
		Title:        body.Title,
		Description:  body.Description,
		ThumbnailURL: body.ThumbnailURL,
		Visibility:   body.Visibility,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, st, nil)
}

func (h *Handler) GetStream(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	st, err := h.svc.GetStream(c.Request.Context(), streamID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "stream not found", nil, nil)
		return
	}
	sanitizeStreamForRequester(st, c.GetHeader("X-User-Id") == st.HostID.String())
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

func (h *Handler) ListLiveStreams(c *gin.Context) {
	limit, offset := parsePagination(c)
	streams, err := h.svc.ListLiveStreams(c.Request.Context(), limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": streams}, nil)
}

func (h *Handler) GoLive(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	if err := h.svc.GoLive(c.Request.Context(), streamID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "GO_LIVE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "live"}, nil)
}

func (h *Handler) EndStream(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	if err := h.svc.EndStream(c.Request.Context(), streamID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "END_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ended"}, nil)
}

func (h *Handler) ListHostStreams(c *gin.Context) {
	hostID, err := uuid.Parse(c.Param("hostId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid host id", nil, nil)
		return
	}
	limit, offset := parsePagination(c)

	streams, err := h.svc.ListHostStreams(c.Request.Context(), hostID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	sanitizeStreamsForRequester(streams, c.GetHeader("X-User-Id") == hostID.String())
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": streams}, nil)
}

// --- Viewer Interaction ---

func (h *Handler) JoinStream(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	count, err := h.svc.JoinStream(c.Request.Context(), streamID, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "JOIN_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"viewer_count": count}, nil)
}

func (h *Handler) LeaveStream(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	if err := h.svc.LeaveStream(c.Request.Context(), streamID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LEAVE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "left"}, nil)
}

func (h *Handler) LikeStream(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	if err := h.svc.LikeStream(c.Request.Context(), streamID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIKE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "liked"}, nil)
}

func (h *Handler) GetViewerCount(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}

	count, err := h.svc.GetViewerCount(c.Request.Context(), streamID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "COUNT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"viewer_count": count}, nil)
}

// --- Chat ---

func (h *Handler) SendChatMessage(c *gin.Context) {
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
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	msg, err := h.svc.SendChatMessage(c.Request.Context(), streamID, userID, body.Message)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CHAT_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, msg, nil)
}

func (h *Handler) GetChatMessages(c *gin.Context) {
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

	var before *time.Time
	if v := c.Query("before"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = &t
		}
	}

	messages, err := h.svc.GetChatMessages(c.Request.Context(), streamID, limit, before)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "CHAT_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": messages}, nil)
}

func (h *Handler) PinMessage(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid stream id", nil, nil)
		return
	}
	messageID, err := uuid.Parse(c.Param("messageId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid message id", nil, nil)
		return
	}

	if err := h.svc.PinMessage(c.Request.Context(), streamID, messageID, userID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "PIN_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "pinned"}, nil)
}

// --- Scheduled ---

func (h *Handler) ScheduleStream(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		ScheduledAt string `json:"scheduled_at"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	scheduledAt, err := time.Parse(time.RFC3339, body.ScheduledAt)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_TIME", "scheduled_at must be RFC3339", nil, nil)
		return
	}

	ss, err := h.svc.ScheduleStream(c.Request.Context(), userID, body.Title, body.Description, scheduledAt)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "SCHEDULE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ss, nil)
}

func (h *Handler) ListUpcomingStreams(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}

	streams, err := h.svc.ListUpcomingStreams(c.Request.Context(), limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": streams}, nil)
}

// --- Helpers ---

func parseUserID(c *gin.Context) (uuid.UUID, bool) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return uuid.Nil, false
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_USER_ID", "invalid user id", nil, nil)
		return uuid.Nil, false
	}
	return uid, true
}

func parsePagination(c *gin.Context) (int, int) {
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
	return limit, offset
}

func sanitizeStreamForRequester(st *postgres.Stream, requesterIsHost bool) {
	if requesterIsHost {
		return
	}
	st.StreamKey = ""
	st.IngestURL = nil
	st.IngestProtocol = nil
}

func sanitizeStreamsForRequester(streams []postgres.Stream, requesterIsHost bool) {
	if requesterIsHost {
		return
	}
	for i := range streams {
		streams[i].StreamKey = ""
		streams[i].IngestURL = nil
		streams[i].IngestProtocol = nil
	}
}
