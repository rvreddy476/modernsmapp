package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// Money on these routes is integer paise (review 2026-11-07).
type CreateFundraiserRequest struct {
	Type        string  `json:"type" binding:"required"`
	Title       string  `json:"title" binding:"required"`
	Description string  `json:"description" binding:"required"`
	GoalPaise   int64   `json:"goal_paise" binding:"required"`
	EndsAt      *string `json:"ends_at"`
}

// DonateRequest asks to donate. There is no payment field: a donation is
// paid through payments-service and counts only on its signed event, never
// on an id or amount the client states.
type DonateRequest struct {
	AmountPaise int64   `json:"amount_paise" binding:"required"`
	IsAnonymous bool    `json:"is_anonymous"`
	Message     *string `json:"message"`
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	var endsAt *time.Time
	if req.EndsAt != nil && *req.EndsAt != "" {
		t, err := time.Parse(time.RFC3339, *req.EndsAt)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DATE", "ends_at must be RFC3339 format", nil)
			return
		}
		endsAt = &t
	}

	fundraiser, err := h.svc.CreateFundraiser(c.Request.Context(), userID, req.Type, req.Title, req.Description, req.GoalPaise, endsAt)
	if err != nil {
		switch err.Error() {
		case "INVALID_GOAL: goal_paise must be greater than zero":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_GOAL", "goal_paise must be greater than zero", nil)
		default:
			serverError(c, err)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, fundraiser, nil)
}

func (h *Handler) ListActiveFundraisers(c *gin.Context) {
	limit, offset := parsePagination(c)

	fundraisers, err := h.svc.ListActiveFundraisers(c.Request.Context(), limit, offset)
	if err != nil {
		serverError(c, err)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil)
		return
	}

	fundraiser, err := h.svc.GetFundraiser(c.Request.Context(), fundraiserID)
	if err != nil {
		serverError(c, err)
		return
	}
	if fundraiser == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Fundraiser not found", nil)
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
		serverError(c, err)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil)
		return
	}

	if err := h.svc.PauseFundraiser(c.Request.Context(), userID, fundraiserID); err != nil {
		switch err.Error() {
		case "FUNDRAISER_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Fundraiser not found", nil)
		case "FORBIDDEN: not the fundraiser owner":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this fundraiser", nil)
		default:
			serverError(c, err)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "paused"}, nil)
}

// Donate records a PENDING donation and answers 202 with the payments
// reference it must be paid under. Nothing about the fundraiser changes
// here: the donation counts only when payments-service's signed
// payment.succeeded event for that reference is applied.
func (h *Handler) Donate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	fundraiserID, err := uuid.Parse(c.Param("fundraiserId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil)
		return
	}

	var req DonateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	pending, err := h.svc.Donate(c.Request.Context(), fundraiserID, userID, req.AmountPaise, req.IsAnonymous, req.Message)
	if err != nil {
		switch {
		case err.Error() == "FUNDRAISER_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Fundraiser not found", nil)
		case err.Error() == "INVALID_AMOUNT: donation must be greater than zero":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_AMOUNT", "Donation amount must be greater than zero", nil)
		case strings.HasPrefix(err.Error(), "FUNDRAISER_NOT_ACTIVE"):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "FUNDRAISER_NOT_ACTIVE", err.Error(), nil)
		default:
			serverError(c, err)
		}
		return
	}

	api.JSON(c.Writer, http.StatusAccepted, pending, nil)
}

// GetDonationsByFundraiser lists the donations that count. An anonymous
// donor's id is shown only to that donor (X-User-Id); a signed-out caller
// sees none.
func (h *Handler) GetDonationsByFundraiser(c *gin.Context) {
	fundraiserID, err := uuid.Parse(c.Param("fundraiserId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid fundraiser ID", nil)
		return
	}

	limit, offset := parsePagination(c)

	viewer, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		viewer = uuid.Nil
	}
	donations, err := h.svc.GetDonationsByFundraiser(c.Request.Context(), viewer, fundraiserID, limit, offset)
	if err != nil {
		serverError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, donations, nil)
}
