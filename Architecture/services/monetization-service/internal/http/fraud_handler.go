package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getAdminID(c *gin.Context) (uuid.UUID, bool) {
	adminRole := c.GetHeader("X-Admin-Role")
	if adminRole == "" {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin access required", nil, nil)
		return uuid.Nil, false
	}

	adminIDStr := c.GetHeader("X-User-Id")
	if adminIDStr == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return uuid.Nil, false
	}
	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return adminID, true
}

func parseLimitOffset(c *gin.Context) (int, int) {
	limit := 20
	offset := 0
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if o := c.Query("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

// ---------------------------------------------------------------------------
// Disputes
// ---------------------------------------------------------------------------

type CreateDisputeRequest struct {
	TransactionID string `json:"transaction_id" binding:"required"`
	Reason        string `json:"reason" binding:"required"`
	Description   string `json:"description"`
}

func (h *Handler) CreateDispute(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateDisputeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	txnID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid transaction ID", nil, nil)
		return
	}

	dispute, err := h.svc.CreateDispute(c.Request.Context(), userID, txnID, req.Reason, req.Description)
	if err != nil {
		switch err.Error() {
		case "TRANSACTION_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "TRANSACTION_NOT_FOUND", "Transaction not found", nil, nil)
		case "TRANSACTION_NOT_OWNED":
			api.Error(c.Writer, http.StatusForbidden, "TRANSACTION_NOT_OWNED", "Transaction does not belong to you", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, dispute, nil)
}

func (h *Handler) ListUserDisputes(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parseLimitOffset(c)

	disputes, err := h.svc.ListUserDisputes(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if disputes == nil {
		disputes = []postgres.Dispute{}
	}

	api.JSON(c.Writer, http.StatusOK, disputes, nil)
}

func (h *Handler) GetDisputeByID(c *gin.Context) {
	_, ok := getUserID(c)
	if !ok {
		return
	}

	disputeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid dispute ID", nil, nil)
		return
	}

	dispute, err := h.svc.GetDispute(c.Request.Context(), disputeID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if dispute == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Dispute not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, dispute, nil)
}

type ResolveDisputeRequest struct {
	Status          string `json:"status" binding:"required"`
	ResolutionNotes string `json:"resolution_notes"`
}

func (h *Handler) ResolveDisputeAdmin(c *gin.Context) {
	adminID, ok := getAdminID(c)
	if !ok {
		return
	}

	disputeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid dispute ID", nil, nil)
		return
	}

	var req ResolveDisputeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.ResolveDispute(c.Request.Context(), disputeID, req.Status, req.ResolutionNotes, adminID); err != nil {
		if err.Error() == "DISPUTE_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Dispute not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "resolved"}, nil)
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

type ProcessRefundRequest struct {
	TransactionID string  `json:"transaction_id" binding:"required"`
	AmountPaise   int64   `json:"amount_paise" binding:"required"`
	Reason        string  `json:"reason" binding:"required"`
	DisputeID     *string `json:"dispute_id"`
}

func (h *Handler) ProcessRefund(c *gin.Context) {
	_, ok := getAdminID(c)
	if !ok {
		return
	}

	var req ProcessRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	txnID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid transaction ID", nil, nil)
		return
	}

	var disputeID *uuid.UUID
	if req.DisputeID != nil && *req.DisputeID != "" {
		parsed, err := uuid.Parse(*req.DisputeID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid dispute ID", nil, nil)
			return
		}
		disputeID = &parsed
	}

	refund, err := h.svc.ProcessRefund(c.Request.Context(), txnID, req.AmountPaise, req.Reason, disputeID)
	if err != nil {
		switch err.Error() {
		case "TRANSACTION_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "TRANSACTION_NOT_FOUND", "Transaction not found", nil, nil)
		case "REFUND_ALREADY_EXISTS":
			api.Error(c.Writer, http.StatusConflict, "REFUND_ALREADY_EXISTS", "Refund already exists for this transaction", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, refund, nil)
}

// ---------------------------------------------------------------------------
// Fraud Reviews (admin)
// ---------------------------------------------------------------------------

func (h *Handler) ListPendingFraudReviews(c *gin.Context) {
	_, ok := getAdminID(c)
	if !ok {
		return
	}

	limit, offset := parseLimitOffset(c)

	reviews, err := h.svc.ListPendingFraudReviews(c.Request.Context(), limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if reviews == nil {
		reviews = []postgres.FraudReview{}
	}

	api.JSON(c.Writer, http.StatusOK, reviews, nil)
}

type ResolveFraudReviewRequest struct {
	Status string `json:"status" binding:"required"`
	Notes  string `json:"notes"`
}

func (h *Handler) ResolveFraudReviewAdmin(c *gin.Context) {
	adminID, ok := getAdminID(c)
	if !ok {
		return
	}

	reviewID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid review ID", nil, nil)
		return
	}

	var req ResolveFraudReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.ResolveFraudReview(c.Request.Context(), reviewID, req.Status, req.Notes, adminID); err != nil {
		if err.Error() == "FRAUD_REVIEW_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Fraud review not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "resolved"}, nil)
}
