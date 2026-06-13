package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateItemReviewRequest carries the customer's per-item review.
type CreateItemReviewRequest struct {
	OrderID    uuid.UUID `json:"order_id"`
	MenuItemID uuid.UUID `json:"menu_item_id"`
	Rating     int       `json:"rating"`
	Review     string    `json:"review,omitempty"`
	PhotoURLs  []string  `json:"photo_urls,omitempty"`
}

// CreateItemReview — POST /v1/food/menu-items/:itemId/reviews
//
// itemId is taken from the URL for ergonomics; the body's menu_item_id
// must match to avoid surprises.
func (h *Handler) CreateItemReview(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ITEM_ID", err.Error(), nil)
		return
	}
	var req CreateItemReviewRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.MenuItemID != uuid.Nil && req.MenuItemID != itemID {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "menu_item_id mismatch", nil)
		return
	}
	rv, err := h.svc.CreateItemReview(c.Request.Context(), postgres.CreateItemReviewInput{
		OrderID:    req.OrderID,
		MenuItemID: itemID,
		CustomerID: uid,
		Rating:     req.Rating,
		Review:     req.Review,
		PhotoURLs:  req.PhotoURLs,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "ITEM_REVIEW_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, rv, nil)
}

// ListItemReviews — GET /v1/food/menu-items/:itemId/reviews (public).
func (h *Handler) ListItemReviews(c *gin.Context) {
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ITEM_ID", err.Error(), nil)
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	rows, err := h.svc.ListItemReviews(c.Request.Context(), itemID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_REVIEWS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"reviews": rows}, nil)
}

// AdminHideItemReview — DELETE /v1/food/admin/item-reviews/:reviewId
func (h *Handler) AdminHideItemReview(c *gin.Context) {
	rid, err := uuid.Parse(c.Param("reviewId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REVIEW_ID", err.Error(), nil)
		return
	}
	if err := h.svc.HideItemReview(c.Request.Context(), rid); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "HIDE_REVIEW_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"review_id": rid.String(), "hidden": true}, nil)
}
