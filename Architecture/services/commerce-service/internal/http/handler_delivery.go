package http

import (
	"errors"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/partnerwork"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
)

func (h *Handler) registerDeliveryRoutes(r *gin.Engine) {
	g := r.Group("/v1/commerce/delivery")
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private")
		if _, ok := getUserID(c); !ok {
			c.Abort()
			return
		}
		c.Next()
	})
	g.GET("/profile", h.GetDeliveryProfile)
	g.PUT("/profile", h.SaveDeliveryProfile)
	g.PUT("/documents/:kind", h.SaveDeliveryDocument)
	g.POST("/submit", h.SubmitDeliveryProfile)
	g.GET("/coverage", h.GetDeliveryCoverage)
	h.registerDeliveryOperations(g, r)
	a := r.Group(InternalAdminPrefix+"/delivery", h.requireAdminToken(PermKYCVerify))
	a.GET("/profiles", h.AdminDeliveryProfiles)
	a.GET("/profiles/:profileId", h.AdminDeliveryProfile)
	a.POST("/profiles/:profileId/review", h.AdminReviewDelivery)
	a.GET("/profiles/:profileId/documents/:documentId/image", h.AdminDeliveryDocumentImage)
	a.GET("/profiles/:profileId/documents/:documentId/evidence", h.AdminDeliveryEvidence)
	config := r.Group(InternalAdminPrefix+"/delivery/areas", h.requireAdminToken(PermCatalogueEdit))
	config.GET("", h.AdminDeliveryAreas)
	config.POST("", h.AdminSaveDeliveryArea)
	config.PUT("/:areaId", h.AdminSaveDeliveryArea)
	config.GET("/:areaId/sellers", h.AdminDeliveryAreaSellers)
	config.PUT("/:areaId/sellers/:sellerId", h.AdminSaveDeliveryAreaSeller)
}
func (h *Handler) AdminDeliveryEvidence(c *gin.Context) {
	profile, ok := parseUUID(c, "profileId")
	if !ok {
		return
	}
	doc, ok := parseUUID(c, "documentId")
	if !ok {
		return
	}
	actor, ok := tokenActor(c)
	if !ok {
		return
	}
	ref, err := h.svc.AdminDeliveryEvidence(c.Request.Context(), profile, doc, actor)
	deliveryResult(c, ref, err)
}

func (h *Handler) AdminDeliveryDocumentImage(c *gin.Context) {
	id, ok := parseUUID(c, "profileId")
	if !ok {
		return
	}
	doc, ok := parseUUID(c, "documentId")
	if !ok {
		return
	}
	actor, _ := tokenActor(c)
	img, err := h.svc.AdminDeliveryDocumentImage(c.Request.Context(), id, doc, actor)
	if err != nil {
		writeDocumentImageError(c, err)
		return
	}
	defer img.Body.Close()
	raw, err := io.ReadAll(img.Body)
	if err != nil {
		writeDocumentImageError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store, private")
	c.Header("Content-Disposition", "inline")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Data(http.StatusOK, img.ContentType, raw)
}

func deliveryError(c *gin.Context, err error) {
	status, code, message := 0, "", ""
	switch {
	case errors.Is(err, postgres.ErrDeliveryCode):
		status, code, message = 409, "DELIVERY_VERIFICATION_FAILED", "The verification code is incorrect or locked. Ask for the current code, or contact support."
	case errors.Is(err, postgres.ErrDeliveryWorkConflict), errors.Is(err, partnerwork.ErrBusy), errors.Is(err, partnerwork.ErrStale), errors.Is(err, partnerwork.ErrConflict), errors.Is(err, partnerwork.ErrOffDuty), errors.Is(err, partnerwork.ErrExpired):
		status, code, message = 409, "DELIVERY_WORK_CONFLICT", "This request changed, expired or conflicts with your current duty. Refresh to see the current state."
	case errors.Is(err, pgx.ErrNoRows):
		status, code, message = 404, "DELIVERY_NOT_FOUND", "This delivery is unavailable for your account."
	case errors.Is(err, postgres.ErrDeliveryConfigInvalid):
		status, code, message = 400, "DELIVERY_CONFIG_INVALID", "Enter a valid area, radius and seller. Set a positive delivery payout before enabling an area."
	case errors.Is(err, postgres.ErrDeliveryConfigConflict):
		status, code, message = 409, "DELIVERY_CONFIG_CONFLICT", "The delivery configuration changed. Refresh before saving."
	case errors.Is(err, postgres.ErrDeliveryProfileMissing):
		status, code, message = 404, "COMMERCE_DELIVERY_PROFILE_NOT_FOUND", "No Commerce Delivery application yet."
	case errors.Is(err, postgres.ErrDeliveryInvalid):
		status, code, message = 400, "DELIVERY_INVALID", "Check the required fields and document type. Introduction videos must be at most one minute."
	case errors.Is(err, postgres.ErrDeliveryReviewConflict):
		status, code, message = 409, "DELIVERY_REVIEW_CONFLICT", "This application changed or is locked for review. Refresh to see its current status."
	case errors.Is(err, postgres.ErrDeliveryIncomplete):
		status, code, message = 409, "DELIVERY_INCOMPLETE", "Complete the required document checks before continuing."
	default:
		handleErr(c, err)
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, message, nil)
}
func (h *Handler) GetDeliveryProfile(c *gin.Context) {
	user, _ := getUserID(c)
	p, err := h.svc.DeliveryProfile(c.Request.Context(), user)
	if err != nil {
		deliveryError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}
func (h *Handler) SaveDeliveryProfile(c *gin.Context) {
	var b struct {
		FullName    string `json:"full_name"`
		VehicleType string `json:"vehicle_type"`
	}
	if c.ShouldBindJSON(&b) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	user, _ := getUserID(c)
	if err := h.svc.SaveDeliveryProfile(c.Request.Context(), user, b.FullName, b.VehicleType); err != nil {
		deliveryError(c, err)
		return
	}
	h.GetDeliveryProfile(c)
}
func (h *Handler) SaveDeliveryDocument(c *gin.Context) {
	var b struct {
		MediaID uuid.UUID `json:"media_id"`
	}
	if c.ShouldBindJSON(&b) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	user, _ := getUserID(c)
	if err := h.svc.SaveDeliveryDocument(c.Request.Context(), user, c.Param("kind"), b.MediaID); err != nil {
		deliveryError(c, err)
		return
	}
	h.GetDeliveryProfile(c)
}
func (h *Handler) SubmitDeliveryProfile(c *gin.Context) {
	user, _ := getUserID(c)
	if err := h.svc.SubmitDeliveryProfile(c.Request.Context(), user); err != nil {
		deliveryError(c, err)
		return
	}
	h.GetDeliveryProfile(c)
}
func (h *Handler) AdminDeliveryProfiles(c *gin.Context) {
	p, err := h.svc.DeliveryReviewQueue(c.Request.Context())
	if err != nil {
		deliveryError(c, err)
		return
	}
	api.JSON(c.Writer, 200, p, nil)
}
func (h *Handler) AdminDeliveryProfile(c *gin.Context) {
	id, ok := parseUUID(c, "profileId")
	if !ok {
		return
	}
	p, err := h.svc.AdminDeliveryProfile(c.Request.Context(), id)
	if err != nil {
		deliveryError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store, private")
	api.JSON(c.Writer, 200, p, nil)
}
func (h *Handler) AdminReviewDelivery(c *gin.Context) {
	id, ok := parseUUID(c, "profileId")
	if !ok {
		return
	}
	actor, ok := tokenActor(c)
	if !ok {
		return
	}
	var b struct {
		DocumentID uuid.UUID `json:"document_id"`
		Revision   int64     `json:"revision"`
		Decision   string    `json:"decision"`
		Note       string    `json:"note"`
	}
	if c.ShouldBindJSON(&b) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	if err := h.svc.ReviewDelivery(c.Request.Context(), actor, id, b.DocumentID, b.Revision, b.Decision, b.Note); err != nil {
		deliveryError(c, err)
		return
	}
	h.AdminDeliveryProfile(c)
}
