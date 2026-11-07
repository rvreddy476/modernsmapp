// Review route handlers: listing and creating product reviews and the
// seller's response to a review. Moved out of handler.go unchanged.

package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) GetProductReviews(c *gin.Context) {
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.productReadable(c, id) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	// ?sort=helpful (default: helpful_count desc, then newest) | recent. Each
	// row carries helpful_count and the caller's own viewer_vote.
	reviews, total, err := h.svc.ProductReviewRows(c.Request.Context(), id, optionalUserID(c),
		c.Query("sort"), limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"reviews": reviews, "total": total}, nil)
}

type createReviewReq struct {
	SellerID    uuid.UUID `json:"seller_id" binding:"required"`
	OrderItemID uuid.UUID `json:"order_item_id" binding:"required"`
	Rating      int       `json:"rating" binding:"required,min=1,max=5"`
	Title       *string   `json:"title"`
	Body        *string   `json:"body"`
}

func (h *Handler) CreateReview(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req createReviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	r := &postgres.Review{
		ProductID:   productID,
		SellerID:    req.SellerID,
		ReviewerID:  userID,
		OrderItemID: req.OrderItemID,
		Rating:      req.Rating,
		Title:       req.Title,
		Body:        req.Body,
		// IsVerifiedPurchase is set by the service layer after it
		// validates the order item; callers may no longer self-declare it.
		IsPublished: true,
	}
	if err := h.svc.CreateReview(c.Request.Context(), r); err != nil {
		switch {
		case errors.Is(err, service.ErrReviewOrderItemInvalid):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "REVIEW_NOT_ELIGIBLE", err.Error(), nil)
		case errors.Is(err, service.ErrReviewItemNotDelivered):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REVIEW_ITEM_NOT_DELIVERED", err.Error(), nil)
		default:
			handleErr(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusCreated, r, nil)
}

// sellerResponseReq is the body for POST /reviews/:reviewId/seller-response.
type sellerResponseReq struct {
	Response string `json:"response" binding:"required"`
}

// SellerRespondToReview lets the seller of a product attach a public
// response to a customer review — Phase 2.4. Only that seller may post.
//
//	POST /v1/commerce/reviews/:reviewId/seller-response
func (h *Handler) SellerRespondToReview(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	reviewID, ok := parseUUID(c, "reviewId")
	if !ok {
		return
	}
	var req sellerResponseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Response) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "response cannot be empty", nil)
		return
	}
	// L2 (audit 2026-10-30): reviews carry a sellers.id, so the caller's user
	// id is resolved to their seller before the comparison.
	seller, ok := h.sellerForCaller(c, actorID)
	if !ok {
		return
	}
	r, err := h.svc.AddSellerResponseToReview(c.Request.Context(), reviewID, seller.ID, req.Response)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrReviewNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case errors.Is(err, service.ErrNotReviewSeller):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		default:
			handleErr(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, r, nil)
}
