package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterOnboardingRoutes adds seller onboarding + admin internal routes.
func (h *Handler) RegisterOnboardingRoutes(r *gin.Engine) {
	ob := r.Group("/v1/commerce/onboarding")
	ob.POST("/start", h.StartOnboarding)
	ob.GET("/status", h.GetOnboardingStatus)
	ob.PUT("/step/basic", h.SaveBasicInfo)
	ob.PUT("/step/storefront", h.SaveStorefront)
	ob.PUT("/step/documents", h.SaveDocuments)
	ob.PUT("/step/fulfillment", h.SaveFulfillment)
	ob.PUT("/step/payout", h.SavePayout)
	ob.POST("/submit", h.SubmitApplication)

	r.GET("/v1/commerce/dashboard", h.GetDashboard)
	r.POST("/v1/commerce/products/:productId/submit", h.SubmitProduct)

	// Internal admin routes (called by admin-service with X-Internal-Service-Key)
	adm := r.Group("/v1/commerce/internal")
	if h.internalKey != "" {
		adm.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	adm.GET("/sellers/queue", h.AdminListSellerQueue)
	adm.GET("/sellers/:sellerId", h.AdminGetSeller)
	adm.POST("/sellers/:sellerId/approve", h.AdminApproveSeller)
	adm.POST("/sellers/:sellerId/reject", h.AdminRejectSeller)
	adm.POST("/sellers/:sellerId/request-changes", h.AdminRequestSellerChanges)
	adm.POST("/sellers/:sellerId/suspend", h.AdminSuspendSeller)
	adm.POST("/sellers/:sellerId/kyc/verify", h.AdminVerifySellerKYC)
	adm.GET("/products/queue", h.AdminListProductQueue)
	adm.POST("/products/:productId/approve", h.AdminApproveProduct)
	adm.POST("/products/:productId/reject", h.AdminRejectProduct)
	adm.POST("/products/:productId/request-changes", h.AdminRequestProductChanges)
}

// ─── Onboarding handlers ────────────────────────────────────────

type startOnboardingReq struct {
	BusinessPageID *uuid.UUID `json:"business_page_id"`
	StoreName      string     `json:"store_name" binding:"required"`
	Email          string     `json:"email" binding:"required"`
	SellerType     string     `json:"seller_type"`
	BusinessType   string     `json:"business_type"`
}

func (h *Handler) StartOnboarding(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req startOnboardingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	sel, err := h.svc.StartOnboarding(c.Request.Context(), service.StartOnboardingInput{
		UserID:         userID,
		BusinessPageID: req.BusinessPageID,
		StoreName:      req.StoreName,
		Email:          req.Email,
		SellerType:     req.SellerType,
		BusinessType:   req.BusinessType,
	})
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, sel, nil)
}

func (h *Handler) GetOnboardingStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	sel, err := h.svc.GetOnboardingStatus(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "no onboarding found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, sel, nil)
}

type saveBasicReq struct {
	StoreName    string  `json:"store_name" binding:"required"`
	OwnerName    string  `json:"owner_name" binding:"required"`
	BusinessType string  `json:"business_type" binding:"required"`
	SellerType   string  `json:"seller_type"`
	Email        string  `json:"email" binding:"required"`
	Phone        *string `json:"phone"`
	State        *string `json:"state"`
	City         *string `json:"city"`
	PostalCode   *string `json:"postal_code"`
	Description  *string `json:"description"`
}

func (h *Handler) SaveBasicInfo(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req saveBasicReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.SaveBasicInfo(c.Request.Context(), userID, postgres.OnboardingBasicInput{
		StoreName:    req.StoreName,
		OwnerName:    req.OwnerName,
		BusinessType: req.BusinessType,
		SellerType:   req.SellerType,
		Email:        req.Email,
		Phone:        req.Phone,
		State:        req.State,
		City:         req.City,
		PostalCode:   req.PostalCode,
		Description:  req.Description,
	}); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type saveStorefrontReq struct {
	BrandName       *string    `json:"brand_name"`
	LogoMediaID     *uuid.UUID `json:"logo_media_id"`
	BannerMediaID   *uuid.UUID `json:"banner_media_id"`
	Tagline         *string    `json:"tagline"`
	SupportPhone    *string    `json:"support_phone"`
	SupportEmail    *string    `json:"support_email"`
	SocialLinksJSON []byte     `json:"social_links"`
}

func (h *Handler) SaveStorefront(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req saveStorefrontReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.SaveStorefront(c.Request.Context(), userID, postgres.OnboardingStorefrontInput{
		BrandName:       req.BrandName,
		LogoMediaID:     req.LogoMediaID,
		BannerMediaID:   req.BannerMediaID,
		Tagline:         req.Tagline,
		SupportPhone:    req.SupportPhone,
		SupportEmail:    req.SupportEmail,
		SocialLinksJSON: req.SocialLinksJSON,
	}); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type docInput struct {
	DocumentType   string     `json:"document_type" binding:"required"`
	DocumentNumber *string    `json:"document_number"`
	MediaID        uuid.UUID  `json:"media_id" binding:"required"`
}

type saveDocumentsReq struct {
	Documents []docInput `json:"documents" binding:"required,min=1"`
}

func (h *Handler) SaveDocuments(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req saveDocumentsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	docs := make([]postgres.SellerDocument, len(req.Documents))
	for i, d := range req.Documents {
		docs[i] = postgres.SellerDocument{
			DocumentType:   d.DocumentType,
			DocumentNumber: d.DocumentNumber,
			MediaID:        d.MediaID,
		}
	}
	if err := h.svc.SaveDocuments(c.Request.Context(), userID, docs); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type saveFulfillmentReq struct {
	DeliveryModes    []string `json:"delivery_modes"`
	CODEnabled       bool     `json:"cod_enabled"`
	DispatchSLAHours int      `json:"dispatch_sla_hours"`
	ReturnSupported  bool     `json:"return_supported"`
	ReturnWindowDays int      `json:"return_window_days"`
}

func (h *Handler) SaveFulfillment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req saveFulfillmentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if len(req.DeliveryModes) == 0 {
		req.DeliveryModes = []string{"platform"}
	}
	if req.DispatchSLAHours == 0 {
		req.DispatchSLAHours = 48
	}
	if req.ReturnWindowDays == 0 {
		req.ReturnWindowDays = 7
	}
	if err := h.svc.SaveFulfillment(c.Request.Context(), userID, postgres.OnboardingFulfillmentInput{
		DeliveryModes:    req.DeliveryModes,
		CODEnabled:       req.CODEnabled,
		DispatchSLAHours: req.DispatchSLAHours,
		ReturnSupported:  req.ReturnSupported,
		ReturnWindowDays: req.ReturnWindowDays,
	}); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type savePayoutReq struct {
	AccountHolderName string  `json:"account_holder_name" binding:"required"`
	BankName          *string `json:"bank_name"`
	AccountNumber     string  `json:"account_number" binding:"required"`
	IFSCCode          *string `json:"ifsc_code"`
	UPIID             *string `json:"upi_id"`
}

func (h *Handler) SavePayout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req savePayoutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.SavePayout(c.Request.Context(), userID, postgres.OnboardingPayoutInput{
		AccountHolderName: req.AccountHolderName,
		BankName:          req.BankName,
		AccountNumber:     req.AccountNumber,
		IFSCCode:          req.IFSCCode,
		UPIID:             req.UPIID,
	}); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) SubmitApplication(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.SubmitApplication(c.Request.Context(), userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SUBMIT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"message": "application submitted for review"}, nil)
}

func (h *Handler) GetDashboard(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	stats, err := h.svc.GetDashboard(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

func (h *Handler) SubmitProduct(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if err := h.svc.SubmitProduct(c.Request.Context(), productID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SUBMIT_FAILED", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Internal admin handlers ────────────────────────────────────

func actorID(c *gin.Context) uuid.UUID {
	id, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	return id
}

func (h *Handler) AdminListSellerQueue(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	sellers, total, err := h.svc.AdminListSellerQueue(c.Request.Context(), limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"sellers": sellers, "total": total}, nil)
}

func (h *Handler) AdminGetSeller(c *gin.Context) {
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	sel, err := h.svc.AdminGetSeller(c.Request.Context(), sellerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "seller not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, sel, nil)
}

type adminActionReq struct {
	Reason  string `json:"reason"`
	Notes   string `json:"notes"`
	Changes string `json:"changes"`
}

func (h *Handler) AdminApproveSeller(c *gin.Context) {
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	var req adminActionReq
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminApproveSeller(c.Request.Context(), sellerID, actorID(c), req.Notes); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) AdminRejectSeller(c *gin.Context) {
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	var req adminActionReq
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminRejectSeller(c.Request.Context(), sellerID, actorID(c), req.Reason, req.Notes); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) AdminRequestSellerChanges(c *gin.Context) {
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	var req adminActionReq
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminRequestSellerChanges(c.Request.Context(), sellerID, actorID(c), req.Changes, req.Notes); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) AdminSuspendSeller(c *gin.Context) {
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	var req adminActionReq
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminSuspendSeller(c.Request.Context(), sellerID, actorID(c), req.Reason, req.Notes); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) AdminListProductQueue(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	products, total, err := h.svc.AdminListProductQueue(c.Request.Context(), limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"products": products, "total": total}, nil)
}

func (h *Handler) AdminApproveProduct(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req adminActionReq
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminApproveProduct(c.Request.Context(), productID, actorID(c), req.Notes); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) AdminRejectProduct(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req adminActionReq
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminRejectProduct(c.Request.Context(), productID, actorID(c), req.Reason); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AdminRequestProductChanges POST /v1/commerce/internal/products/:productId/request-changes — Phase 3.4.
// Moderator parks the listing in changes_requested with feedback for the seller.
func (h *Handler) AdminRequestProductChanges(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req adminActionReq
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminRequestProductChanges(c.Request.Context(), productID, actorID(c), req.Reason); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AdminVerifySellerKYC POST /v1/commerce/internal/sellers/:sellerId/kyc/verify — Phase 3.2.
// Runs the configured KYC adapter against the seller's GSTIN/PAN + primary
// payout account; returns the per-field report. The verdict is also stored
// on the seller row (verification_status = "verified" iff all_valid).
func (h *Handler) AdminVerifySellerKYC(c *gin.Context) {
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	rep, err := h.svc.AdminVerifySellerKYC(c.Request.Context(), sellerID)
	if err != nil {
		if err == service.ErrKYCNotConfigured {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
				"KYC_NOT_CONFIGURED", err.Error(), nil)
			return
		}
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rep, nil)
}
