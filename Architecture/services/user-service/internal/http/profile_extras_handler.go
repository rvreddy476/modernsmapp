package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// registerProfileExtrasRoutes attaches profile extras, wellbeing, and QR routes.
func (h *Handler) registerProfileExtrasRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/users")

	// Pins
	v1.POST("/me/pins", h.PinContent)
	v1.DELETE("/me/pins/:contentType/:contentId", h.UnpinContent)
	v1.GET("/:userId/pins", h.GetPins)

	// Portfolio
	v1.POST("/me/portfolio", h.AddPortfolioItem)
	v1.PATCH("/me/portfolio/:id", h.UpdatePortfolioItem)
	v1.DELETE("/me/portfolio/:id", h.DeletePortfolioItem)
	v1.GET("/:userId/portfolio", h.GetPortfolio)

	// QR Code
	v1.GET("/me/qr", h.GetMyQRCode)
	v1.POST("/:userId/qr/scan", h.TrackQRScan)

	// Digital Wellbeing
	v1.GET("/me/wellbeing", h.GetWellbeing)
	v1.PUT("/me/wellbeing", h.UpdateWellbeing)
	v1.POST("/me/screen-time", h.LogScreenTime)
	v1.GET("/me/screen-time", h.GetScreenTime)
}

// --- Pins ---

type PinContentRequest struct {
	ContentType string `json:"content_type" binding:"required"`
	ContentID   string `json:"content_id" binding:"required"`
	PinOrder    int    `json:"pin_order"`
}

func (h *Handler) PinContent(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req PinContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	pin, err := h.svc.PinContent(c.Request.Context(), userID, req.ContentType, req.ContentID, req.PinOrder)
	if err != nil {
		if err.Error() == "MAX_PINS_REACHED" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "MAX_PINS_REACHED", "Maximum 3 pins allowed per profile", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, pin, nil)
}

func (h *Handler) UnpinContent(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	contentType := c.Param("contentType")
	contentID := c.Param("contentId")

	if err := h.svc.UnpinContent(c.Request.Context(), userID, contentType, contentID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unpinned"}, nil)
}

func (h *Handler) GetPins(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	pins, err := h.svc.GetProfilePins(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if pins == nil {
		pins = []store.ProfilePin{}
	}

	api.JSON(c.Writer, http.StatusOK, pins, nil)
}

// --- Portfolio ---

type AddPortfolioItemRequest struct {
	Title       string     `json:"title" binding:"required"`
	Description string     `json:"description"`
	Type        string     `json:"type" binding:"required"`
	URL         string     `json:"url"`
	MediaID     *uuid.UUID `json:"media_id"`
	SortOrder   int        `json:"sort_order"`
}

func (h *Handler) AddPortfolioItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req AddPortfolioItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	item, err := h.svc.AddPortfolioItem(c.Request.Context(), userID, req.Title, req.Description, req.Type, req.URL, req.MediaID, req.SortOrder)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, item, nil)
}

type UpdatePortfolioItemRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	URL         string `json:"url"`
	SortOrder   int    `json:"sort_order"`
}

func (h *Handler) UpdatePortfolioItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item ID", nil)
		return
	}

	var req UpdatePortfolioItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	item := &store.PortfolioItem{
		ID:          itemID,
		UserID:      userID,
		Title:       req.Title,
		Description: req.Description,
		URL:         req.URL,
		SortOrder:   req.SortOrder,
	}

	if err := h.svc.UpdatePortfolioItem(c.Request.Context(), item); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, item, nil)
}

func (h *Handler) DeletePortfolioItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item ID", nil)
		return
	}

	if err := h.svc.DeletePortfolioItem(c.Request.Context(), itemID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) GetPortfolio(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	items, err := h.svc.GetPortfolio(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = []store.PortfolioItem{}
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}

// --- QR Code ---

func (h *Handler) GetMyQRCode(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	// Fetch user to get handle
	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil || u == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil)
		return
	}

	handle := userID.String()
	if u.Username != nil && *u.Username != "" {
		handle = *u.Username
	}

	qr, err := h.svc.GetOrCreateQRCode(c.Request.Context(), userID, handle)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, qr, nil)
}

func (h *Handler) TrackQRScan(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	if err := h.svc.TrackQRScan(c.Request.Context(), userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "tracked"}, nil)
}

// --- Digital Wellbeing ---

func (h *Handler) GetWellbeing(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	w, err := h.svc.GetWellbeing(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, w, nil)
}

func (h *Handler) UpdateWellbeing(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req store.DigitalWellbeing
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	req.UserID = userID

	if err := h.svc.UpdateWellbeing(c.Request.Context(), &req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, req, nil)
}

type LogScreenTimeRequest struct {
	Minutes  int `json:"minutes" binding:"required,min=1"`
	Sessions int `json:"sessions"`
}

func (h *Handler) LogScreenTime(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req LogScreenTimeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.LogScreenTime(c.Request.Context(), userID, req.Minutes, req.Sessions); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "logged"}, nil)
}

func (h *Handler) GetScreenTime(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	logs, err := h.svc.GetScreenTime(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if logs == nil {
		logs = []store.ScreenTimeLog{}
	}

	api.JSON(c.Writer, http.StatusOK, logs, nil)
}
