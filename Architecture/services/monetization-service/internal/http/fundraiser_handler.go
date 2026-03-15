package http

import (
	"net/http"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

type CreateFundraiserRequest struct {
	Type        string   `json:"type" binding:"required"`
	Title       string   `json:"title" binding:"required"`
	Description string   `json:"description" binding:"required"`
	GoalAmount  float64  `json:"goal_amount" binding:"required"`
	EndsAt      *string  `json:"ends_at"`
}

type DonateRequest struct {
	Amount          float64  `json:"amount" binding:"required"`
	PaymentIntentID string   `json:"payment_intent_id" binding:"required"`
	IsAnonymous     bool     `json:"is_anonymous"`
	Message         *string  `json:"message"`
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (h *Handler) CreateFundraiser(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateFundraiserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	var endsAt *time.Time
	if req.EndsAt != nil && *req.EndsAt != "" {
		t, err := time.Parse(time.RFC3339, *req.EndsAt)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_DATE", "ends_at must be RFC3339 format", nil, nil)
			return
		}
		endsAt = &t
	}

	fundraiser, err := h.svc.CreateFundraiser(c.Request.Context(), userID, req.Type, req.Title, req.Description, req.GoalAmount, endsAt)
	if err != nil {
		switch err.Error() {
		case "INVALID_GOAL: goal_amount must be greater than zero":
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_GOAL", "goal_amount must be greater than zero", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, fundraiser, nil)
}

func (h *Handler) ListActiveFundraisers(c *gin.Context) {
	limit, offset := parsePagination(c)

	fundraisers, err := h.svc.ListActiveFundraisers(c.Request.Context(), limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if fundraisers == nil {
		fundraisers = []postgres.Fundraiser{}
	}

	api.JSON(c.Writer, http.StatusOK, fundraisers, nil)
}

func (h *Handler) GetFundraiser(c *gin.Context) {
	fundraiserID, err := uuid.Parse(c.Param("fundraiserId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil, nil)
		return
	}

	fundraiser, err := h.svc.GetFundraiser(c.Request.Context(), fundraiserID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if fundraiser == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Fundraiser not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, fundraiser, nil)
}

func (h *Handler) ListMyFundraisers(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parsePagination(c)

	fundraisers, err := h.svc.ListMyFundraisers(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if fundraisers == nil {
		fundraisers = []postgres.Fundraiser{}
	}

	api.JSON(c.Writer, http.StatusOK, fundraisers, nil)
}

func (h *Handler) PauseFundraiser(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	fundraiserID, err := uuid.Parse(c.Param("fundraiserId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil, nil)
		return
	}

	if err := h.svc.PauseFundraiser(c.Request.Context(), userID, fundraiserID); err != nil {
		switch err.Error() {
		case "FUNDRAISER_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Fundraiser not found", nil, nil)
		case "FORBIDDEN: not the fundraiser owner":
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this fundraiser", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "paused"}, nil)
}

func (h *Handler) Donate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	fundraiserID, err := uuid.Parse(c.Param("fundraiserId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil, nil)
		return
	}

	var req DonateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	paymentIntentID, err := uuid.Parse(req.PaymentIntentID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid payment_intent_id", nil, nil)
		return
	}

	donation, err := h.svc.Donate(c.Request.Context(), fundraiserID, userID, paymentIntentID, req.Amount, req.IsAnonymous, req.Message)
	if err != nil {
		switch err.Error() {
		case "FUNDRAISER_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Fundraiser not found", nil, nil)
		case "INVALID_AMOUNT: donation must be greater than zero":
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_AMOUNT", "Donation amount must be greater than zero", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, donation, nil)
}

func (h *Handler) GetDonationsByFundraiser(c *gin.Context) {
	fundraiserID, err := uuid.Parse(c.Param("fundraiserId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	donations, err := h.svc.GetDonationsByFundraiser(c.Request.Context(), fundraiserID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if donations == nil {
		donations = []postgres.Donation{}
	}

	api.JSON(c.Writer, http.StatusOK, donations, nil)
}
