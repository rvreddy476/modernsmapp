package http

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Business Pages ---

type CreatePageRequest struct {
	PageHandle    string `json:"page_handle" binding:"required"`
	PageName      string `json:"page_name" binding:"required"`
	Category      string `json:"category" binding:"required"`
	Description   string `json:"description"`
	Address       string `json:"address"`
	Phone         string `json:"phone"`
	Whatsapp      string `json:"whatsapp"`
	BusinessEmail string `json:"business_email"`
	Website       string `json:"website"`
	PriceRange    string `json:"price_range"`
	BookingURL    string `json:"booking_url"`
	CoverMediaID  string `json:"cover_media_id"`
	AvatarMediaID string `json:"avatar_media_id"`
	Status        string `json:"status"`
}

type UpdatePageRequest struct {
	PageName      string  `json:"page_name"`
	Category      string  `json:"category"`
	Description   string  `json:"description"`
	Address       string  `json:"address"`
	Lat           *float64 `json:"lat"`
	Lng           *float64 `json:"lng"`
	Phone         string  `json:"phone"`
	Whatsapp      string  `json:"whatsapp"`
	BusinessEmail string  `json:"business_email"`
	Website       string  `json:"website"`
	PriceRange    string  `json:"price_range"`
	BookingURL    string  `json:"booking_url"`
	CoverMediaID  string  `json:"cover_media_id"`
	AvatarMediaID string  `json:"avatar_media_id"`
}

func (h *Handler) CreateBusinessPage(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	var req CreatePageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	status := req.Status
	if status == "" {
		status = "active"
	}
	if status != "draft" && status != "active" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_STATUS", "status must be draft or active", nil)
		return
	}

	p := &store.BusinessPage{
		UserID:        userID,
		PageHandle:    strings.ToLower(strings.TrimSpace(req.PageHandle)),
		PageName:      req.PageName,
		Category:      req.Category,
		Description:   req.Description,
		Address:       req.Address,
		Phone:         req.Phone,
		Whatsapp:      req.Whatsapp,
		BusinessEmail: req.BusinessEmail,
		Website:       req.Website,
		PriceRange:    req.PriceRange,
		BookingURL:    req.BookingURL,
		CoverMediaID:  req.CoverMediaID,
		AvatarMediaID: req.AvatarMediaID,
		Status:        status,
	}

	if err := h.svc.CreateBusinessPage(c.Request.Context(), p); err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "HANDLE_TAKEN", "Page handle already taken", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

func (h *Handler) GetBusinessPage(c *gin.Context) {
	handle := c.Param("id")

	var viewerID *uuid.UUID
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			viewerID = &id
		}
	}

	p, err := h.svc.GetBusinessPage(c.Request.Context(), handle, viewerID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) UpdateBusinessPage(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	handle := c.Param("id")

	existing, err := h.svc.GetBusinessPage(c.Request.Context(), handle, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if existing.UserID != userID {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Not your page", nil)
		return
	}

	var req UpdatePageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if req.PageName != "" {
		existing.PageName = req.PageName
	}
	if req.Category != "" {
		existing.Category = req.Category
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Address != "" {
		existing.Address = req.Address
	}
	if req.Lat != nil {
		existing.Lat = req.Lat
	}
	if req.Lng != nil {
		existing.Lng = req.Lng
	}
	if req.Phone != "" {
		existing.Phone = req.Phone
	}
	if req.Whatsapp != "" {
		existing.Whatsapp = req.Whatsapp
	}
	if req.BusinessEmail != "" {
		existing.BusinessEmail = req.BusinessEmail
	}
	if req.Website != "" {
		existing.Website = req.Website
	}
	if req.PriceRange != "" {
		existing.PriceRange = req.PriceRange
	}
	if req.BookingURL != "" {
		existing.BookingURL = req.BookingURL
	}
	if req.CoverMediaID != "" {
		existing.CoverMediaID = req.CoverMediaID
	}
	if req.AvatarMediaID != "" {
		existing.AvatarMediaID = req.AvatarMediaID
	}

	if err := h.svc.UpdateBusinessPage(c.Request.Context(), existing); err != nil {
		if err.Error() == "PAGE_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, existing, nil)
}

func (h *Handler) DeleteBusinessPage(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	pageID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid page ID", nil)
		return
	}

	if err := h.svc.DeleteBusinessPage(c.Request.Context(), pageID, userID); err != nil {
		if err.Error() == "PAGE_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) ListMyBusinessPages(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	pages, err := h.svc.GetUserBusinessPages(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if pages == nil {
		pages = []store.BusinessPage{}
	}

	api.JSON(c.Writer, http.StatusOK, pages, nil)
}

func (h *Handler) DiscoverPages(c *gin.Context) {
	category := c.Query("category")
	search := c.Query("q")

	limit := 20
	offset := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	pages, err := h.svc.DiscoverPages(c.Request.Context(), category, search, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if pages == nil {
		pages = []store.BusinessPage{}
	}

	api.JSON(c.Writer, http.StatusOK, pages, nil)
}

func (h *Handler) FollowPage(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	pageID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid page ID", nil)
		return
	}

	if err := h.svc.FollowPage(c.Request.Context(), pageID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "following"}, nil)
}

func (h *Handler) UnfollowPage(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	pageID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid page ID", nil)
		return
	}

	if err := h.svc.UnfollowPage(c.Request.Context(), pageID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unfollowed"}, nil)
}

// --- Reviews ---

type SubmitReviewRequest struct {
	Rating     int    `json:"rating" binding:"required,min=1,max=5"`
	ReviewText string `json:"review_text"`
}

func (h *Handler) GetPageReviews(c *gin.Context) {
	handle := c.Param("id")

	existing, err := h.svc.GetBusinessPage(c.Request.Context(), handle, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	cursor := time.Now().Add(time.Second)
	if v := c.Query("before"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			cursor = t
		}
	}

	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	reviews, err := h.svc.GetPageReviews(c.Request.Context(), existing.ID, cursor, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if reviews == nil {
		reviews = []store.BusinessReview{}
	}

	api.JSON(c.Writer, http.StatusOK, reviews, nil)
}

func (h *Handler) SubmitReview(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	handle := c.Param("id")

	existing, err := h.svc.GetBusinessPage(c.Request.Context(), handle, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	var req SubmitReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	r := &store.BusinessReview{
		PageID:     existing.ID,
		ReviewerID: userID,
		Rating:     req.Rating,
		ReviewText: req.ReviewText,
	}

	if err := h.svc.SubmitReview(c.Request.Context(), r); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, r, nil)
}
