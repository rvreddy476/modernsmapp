package http

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/atpost/identity-shared/api"
	"github.com/atpost/identity-user-service/internal/store"
)

type Handler struct {
	svc UserService
	log *slog.Logger
}

type UserService interface {
	GetUser(ctx context.Context, id uuid.UUID) (*store.User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]store.User, int, error)
	GetSettings(ctx context.Context, id uuid.UUID) (*store.UserSettings, error)
	UpdateSettings(ctx context.Context, settings *store.UserSettings) (*store.UserSettings, error)
}

func New(svc UserService, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, log: logger}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, auth gin.HandlerFunc, csrf gin.HandlerFunc) {
	v1 := r.Group("/v1/users")
	{
		v1.GET("", h.ListUsers)
		v1.GET("/:userId", h.GetUser)
		v1.GET("/health", h.Health)
	}

	protected := v1.Group("")
	protected.Use(auth)
	{
		protected.GET("/me", h.GetMe)
		protected.GET("/me/settings", h.GetMySettings)
		protected.PUT("/me/settings", csrf, h.UpdateMySettings)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) ListUsers(c *gin.Context) {
	limit := 50
	offset := 0

	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	if o := c.Query("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	users, total, err := h.svc.ListUsers(c.Request.Context(), limit, offset)
	if err != nil {
		h.log.Error("failed to list users", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"users":  users,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

func (h *Handler) GetUser(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.log.Warn("invalid user id", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch user", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		h.log.Warn("missing user id header", "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.log.Warn("invalid user id header", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch user", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.log.Warn("invalid user id header", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	s, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch settings", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

func (h *Handler) UpdateMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req store.UserSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	req.UserID = userID

	s, err := h.svc.UpdateSettings(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("failed to update settings", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}
