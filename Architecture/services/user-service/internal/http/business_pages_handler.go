package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/pages"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Business Pages ---

type CreatePageRequest struct {
	PageHandle    string `json:"page_handle" binding:"required"`
	PageName      string `json:"page_name" binding:"required"`
	PageType      string `json:"page_type" binding:"required"`
	Category      string `json:"category"`
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
}

const maxPagesPerUser = 20 // spec §12 ownership cap

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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}

	// page_type must be one of the 13 canonical types (spec §2).
	if !pages.IsValidPageType(req.PageType) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", "invalid page_type", nil)
		return
	}

	// Ownership cap (spec §12): max 20 non-disabled pages per user.
	if n, err := h.svc.CountActivePagesOwned(c.Request.Context(), userID); err == nil && n >= maxPagesPerUser {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "rate_limited", "page ownership limit reached", nil)
		return
	}

	p := &store.BusinessPage{
		UserID:        userID,
		PageHandle:    strings.ToLower(strings.TrimSpace(req.PageHandle)),
		PageName:      req.PageName,
		PageType:      req.PageType,
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
		Status:        pages.StatusDraft, // new pages always start in draft (spec §6.1)
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

// PageActions is the computed capability set returned with a page (spec §6.10).
type PageActions struct {
	CanFollow         bool `json:"canFollow"`
	CanUnfollow       bool `json:"canUnfollow"`
	CanManage         bool `json:"canManage"`
	CanMessage        bool `json:"canMessage"`
	CanAddFriend      bool `json:"canAddFriend"` // ALWAYS false on a page
	CanEdit           bool `json:"canEdit"`
	CanUploadDocument bool `json:"canUploadDocument"`
	CanSubmitForReview bool `json:"canSubmitForReview"`
}

// PageActionButton is a render hint (spec §8): gated buttons are backend-enforced.
type PageActionButton struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Primary bool   `json:"primary,omitempty"`
	Gated   bool   `json:"gated"`
}

// PageResponse is the enriched page payload (spec §6.10).
type PageResponse struct {
	*store.BusinessPage
	DisplayType   string             `json:"displayType"`
	ViewerRole    string             `json:"viewerRole"`
	IsOwner       bool               `json:"isOwner"`
	BannerMessage string             `json:"bannerMessage,omitempty"`
	Actions       PageActions        `json:"actions"`
	ActionButtons []PageActionButton `json:"actionButtons"`
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
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Resolve viewer's role on this page.
	viewerRole := "visitor"
	isOwnerOrAdmin := false
	if viewerID != nil {
		if role, rerr := h.svc.GetPageRole(c.Request.Context(), p.ID, *viewerID); rerr == nil && role != "" {
			viewerRole = role
			isOwnerOrAdmin = role == "owner" || role == "admin"
		}
	}

	// --- Visibility resolution (spec §4 / §6.10) ---
	switch p.Status {
	case pages.StatusDisabled:
		// Terminal — 404 to everyone (treated as deleted publicly).
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
		return
	case pages.StatusDraft, pages.StatusPendingReview, pages.StatusRejected:
		// 404 to non-owners; full payload to owner/admin only.
		if !isOwnerOrAdmin {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
			return
		}
	}

	resp := h.buildPageResponse(c, p, viewerRole, isOwnerOrAdmin)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// buildPageResponse computes the actions/buttons/visibility envelope per §4/§6.10.
func (h *Handler) buildPageResponse(c *gin.Context, p *store.BusinessPage, viewerRole string, isOwnerOrAdmin bool) PageResponse {
	following := p.IsFollowing != nil && *p.IsFollowing
	approved := p.Status == pages.StatusApproved
	suspended := p.Status == pages.StatusSuspended
	isVisitor := !isOwnerOrAdmin

	actions := PageActions{
		CanAddFriend: false, // INVARIANT: never true on a page (spec §4)
		CanFollow:    approved && isVisitor && !following,
		CanUnfollow:  following && (approved || suspended),
		CanManage:    isOwnerOrAdmin,
		CanEdit:      isOwnerOrAdmin && p.Status != pages.StatusDisabled,
		CanMessage:   approved && isVisitor,
		CanUploadDocument: isOwnerOrAdmin &&
			(p.Status == pages.StatusDraft || p.Status == pages.StatusRejected || p.Status == pages.StatusPendingReview),
		CanSubmitForReview: isOwnerOrAdmin &&
			(p.Status == pages.StatusDraft || p.Status == pages.StatusRejected) &&
			h.requiredDocsUploaded(c, p),
	}

	// Build actionButtons from the per-type config; follow swaps to unfollow
	// when already following. Hide follow/message for owners.
	var buttons []PageActionButton
	for _, id := range pages.ActionButtons(p.PageType) {
		gated := pages.IsGatedButton(id)
		if id == "follow" {
			if isOwnerOrAdmin {
				continue
			}
			if following {
				buttons = append(buttons, PageActionButton{ID: "unfollow", Label: "Following", Primary: true, Gated: true})
				continue
			}
			buttons = append(buttons, PageActionButton{ID: "follow", Label: "Follow", Primary: true, Gated: true})
			continue
		}
		if id == "message" && isOwnerOrAdmin {
			continue
		}
		buttons = append(buttons, PageActionButton{ID: id, Label: buttonLabel(id), Gated: gated})
	}

	resp := PageResponse{
		BusinessPage:  p,
		DisplayType:   pages.DisplayType(p.PageType),
		ViewerRole:    viewerRole,
		IsOwner:       viewerRole == "owner",
		Actions:       actions,
		ActionButtons: buttons,
	}
	if suspended {
		resp.BannerMessage = "This page is temporarily suspended."
	}
	return resp
}

// requiredDocsUploaded reports whether all required docs for the page type are
// uploaded (pending|approved) — gates canSubmitForReview (spec §6.10).
func (h *Handler) requiredDocsUploaded(c *gin.Context, p *store.BusinessPage) bool {
	required := pages.RequiredDocs(p.PageType)
	if len(required) == 0 {
		return true
	}
	uploaded, err := h.svc.UploadedDocTypes(c.Request.Context(), p.ID)
	if err != nil {
		return false
	}
	for _, r := range required {
		if !uploaded[r] {
			return false
		}
	}
	return true
}

func buttonLabel(id string) string {
	parts := strings.Split(id, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
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

	count, err := h.svc.FollowPage(c.Request.Context(), pageID, userID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPageNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
		case errors.Is(err, store.ErrPageNotFollowable):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "page_not_followable", "Page is not approved", nil)
		case errors.Is(err, store.ErrCannotFollowOwn):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "cannot_follow_own_page", "You cannot follow a page you own or manage", nil)
		case errors.Is(err, store.ErrAlreadyFollowing):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "already_following", "Already following", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]any{"following": true, "followerCount": count}, nil)
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

	count, err := h.svc.UnfollowPage(c.Request.Context(), pageID, userID)
	if err != nil {
		if errors.Is(err, store.ErrPageNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Idempotent: 200 whether or not an active follow existed (spec §6.9).
	api.JSON(c.Writer, http.StatusOK, map[string]any{"following": false, "followerCount": count}, nil)
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

// --- Page lifecycle (submit-review + admin approve/reject/suspend/disable) ---

// resolvePageOwnerAdmin loads the page by id/handle and returns it plus whether
// the caller (X-User-Id) is its owner/admin. Writes the appropriate error and
// returns ok=false when the caller is unauthenticated or the page is missing.
func (h *Handler) resolvePageForActor(c *gin.Context) (*store.BusinessPage, uuid.UUID, bool) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "unauthenticated", "Missing user ID", nil)
		return nil, uuid.Nil, false
	}
	p, err := h.svc.GetBusinessPage(c.Request.Context(), c.Param("id"), nil)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
			return nil, uuid.Nil, false
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return nil, uuid.Nil, false
	}
	return p, userID, true
}

// SubmitPageForReview — POST /v1/pages/:id/submit-review (spec §6.3).
func (h *Handler) SubmitPageForReview(c *gin.Context) {
	p, userID, ok := h.resolvePageForActor(c)
	if !ok {
		return
	}
	if isAdmin, _ := h.svc.IsPageOwnerOrAdmin(c.Request.Context(), p.ID, userID); !isAdmin {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "forbidden", "Only the page owner/admin can submit for review", nil)
		return
	}
	if !pages.CanTransition(p.Status, pages.StatusPendingReview) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "conflict", "Page cannot be submitted from its current status", nil)
		return
	}
	// All required documents must be uploaded (spec §9 step 1).
	if !h.requiredDocsUploaded(c, p) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "unprocessable", "Upload all required documents before submitting", nil)
		return
	}
	if err := h.svc.UpdatePageStatus(c.Request.Context(), p.ID, userID, pages.StatusPendingReview, ""); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": pages.StatusPendingReview}, nil)
}

// adminTransition is the shared body for the four admin lifecycle endpoints.
func (h *Handler) adminTransition(c *gin.Context, to string, reasonRequired bool) {
	if !h.isPageAdmin(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "forbidden", "Platform admin only", nil)
		return
	}
	adminID, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	p, err := h.svc.GetBusinessPage(c.Request.Context(), c.Param("id"), nil)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	var reason string
	if reasonRequired {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = c.ShouldBindJSON(&body)
		if strings.TrimSpace(body.Reason) == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", "reason is required", nil)
			return
		}
		reason = body.Reason
	}
	if !pages.CanTransition(p.Status, to) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "conflict", "Illegal status transition", nil)
		return
	}
	if err := h.svc.UpdatePageStatus(c.Request.Context(), p.ID, adminID, to, reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": to}, nil)
}

// ApprovePage — POST /v1/pages/:id/approve (admin; spec §6.4).
func (h *Handler) ApprovePage(c *gin.Context) { h.adminTransition(c, pages.StatusApproved, false) }

// RejectPage — POST /v1/pages/:id/reject (admin; spec §6.5, reason required).
func (h *Handler) RejectPage(c *gin.Context) { h.adminTransition(c, pages.StatusRejected, true) }

// SuspendPage — POST /v1/pages/:id/suspend (admin; spec §6.6, reason required).
func (h *Handler) SuspendPage(c *gin.Context) { h.adminTransition(c, pages.StatusSuspended, true) }

// DisablePage — POST /v1/pages/:id/disable (admin; spec §6.7, terminal).
func (h *Handler) DisablePage(c *gin.Context) { h.adminTransition(c, pages.StatusDisabled, false) }

// --- Page verification documents (spec §6.15, §6.16) ---

type AddDocumentRequest struct {
	DocumentType string `json:"document_type" binding:"required"`
	DocumentURL  string `json:"document_url" binding:"required"`
}

// AddPageDocument — POST /v1/pages/:id/documents (owner/admin).
func (h *Handler) AddPageDocument(c *gin.Context) {
	p, userID, ok := h.resolvePageForActor(c)
	if !ok {
		return
	}
	if isAdmin, _ := h.svc.IsPageOwnerOrAdmin(c.Request.Context(), p.ID, userID); !isAdmin {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "forbidden", "Only the page owner/admin can upload documents", nil)
		return
	}
	var req AddDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if !pages.IsValidDocumentType(req.DocumentType) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", "invalid document_type", nil)
		return
	}
	d := &store.PageDocument{PageID: p.ID, DocumentType: req.DocumentType, DocumentURL: req.DocumentURL}
	if err := h.svc.AddPageDocument(c.Request.Context(), d); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, d, nil)
}

// ReviewPageDocument — POST /v1/pages/:id/documents/:docId/:action (admin).
func (h *Handler) ReviewPageDocument(c *gin.Context) {
	if !h.isPageAdmin(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "forbidden", "Platform admin only", nil)
		return
	}
	adminID, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	docID, err := uuid.Parse(c.Param("docId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", "Invalid document ID", nil)
		return
	}
	action := c.Param("action")
	status := "approved"
	if action == "reject" {
		status = "rejected"
	} else if action != "approve" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", "action must be approve or reject", nil)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.SetPageDocStatus(c.Request.Context(), docID, adminID, status, body.Reason); err != nil {
		if errors.Is(err, store.ErrPageNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "not_found", "Document not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
}

// ListPageDocuments — GET /v1/pages/:id/documents (owner/admin).
func (h *Handler) ListPageDocuments(c *gin.Context) {
	p, userID, ok := h.resolvePageForActor(c)
	if !ok {
		return
	}
	if isAdmin, _ := h.svc.IsPageOwnerOrAdmin(c.Request.Context(), p.ID, userID); !isAdmin && !h.isPageAdmin(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "forbidden", "Not authorized", nil)
		return
	}
	docs, err := h.svc.ListPageDocuments(c.Request.Context(), p.ID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if docs == nil {
		docs = []store.PageDocument{}
	}
	api.JSON(c.Writer, http.StatusOK, docs, nil)
}
