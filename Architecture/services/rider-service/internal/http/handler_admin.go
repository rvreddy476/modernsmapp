package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// adminUserID resolves the admin actor id from gin context (populated by
// middleware.AdminGuard). Writes a 401 + returns false if missing.
func adminUserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(middleware.AdminUserKey)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "missing admin user", nil)
		return uuid.Nil, false
	}
	id, _ := v.(uuid.UUID)
	if id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "invalid admin user", nil)
		return uuid.Nil, false
	}
	return id, true
}

// readPaging parses ?limit=&offset= with the given default + max cap.
func readPaging(c *gin.Context, defaultLimit, maxLimit int) (limit, offset int) {
	limit = defaultLimit
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if o := c.Query("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

// AdminDashboard — GET /v1/rider/admin/dashboard.
func (h *Handler) AdminDashboard(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "dashboard.view")
	out, err := h.svc.Dashboard(c.Request.Context())
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DASHBOARD_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// --- Partners --------------------------------------------------------------

// AdminListPartners — GET /v1/rider/admin/partners?status=&q=.
func (h *Handler) AdminListPartners(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "partner.list")
	status := c.Query("status")
	query := c.Query("q")
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListPartnersForAdmin(c.Request.Context(), status, query, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminGetPartner — GET /v1/rider/admin/partners/:id.
func (h *Handler) AdminGetPartner(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "partner.read")
	c.Set(middleware.AuditTargetKindKey, "partner")
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.GetPartnerForAdmin(c.Request.Context(), id)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// reasonRequest is the shared body for reject / suspend / block / cancel.
type reasonRequest struct {
	Reason string `json:"reason"`
}

// AdminApprovePartner — POST /v1/rider/admin/partners/:id/approve.
func (h *Handler) AdminApprovePartner(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "partner.approve")
	c.Set(middleware.AuditTargetKindKey, "partner")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	partnerID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.ApprovePartner(c.Request.Context(), partnerID, adminID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_APPROVE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminRejectPartner — POST /v1/rider/admin/partners/:id/reject.
func (h *Handler) AdminRejectPartner(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "partner.reject")
	c.Set(middleware.AuditTargetKindKey, "partner")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	partnerID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.RejectPartner(c.Request.Context(), partnerID, adminID, body.Reason); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_REJECT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// AdminSuspendPartner — POST /v1/rider/admin/partners/:id/suspend.
func (h *Handler) AdminSuspendPartner(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "partner.suspend")
	c.Set(middleware.AuditTargetKindKey, "partner")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	partnerID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.SuspendPartner(c.Request.Context(), partnerID, adminID, body.Reason); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_SUSPEND_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// AdminBlockPartner — POST /v1/rider/admin/partners/:id/block.
func (h *Handler) AdminBlockPartner(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "partner.block")
	c.Set(middleware.AuditTargetKindKey, "partner")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	partnerID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.BlockPartner(c.Request.Context(), partnerID, adminID, body.Reason); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_BLOCK_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// --- Documents -------------------------------------------------------------

// AdminListDocuments — GET /v1/rider/admin/documents?status=.
func (h *Handler) AdminListDocuments(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "document.list")
	status := c.Query("status")
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListPartnerDocumentsForAdmin(c.Request.Context(), status, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DOCUMENT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminVerifyDocument — POST /v1/rider/admin/documents/:id/verify.
func (h *Handler) AdminVerifyDocument(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "document.verify")
	c.Set(middleware.AuditTargetKindKey, "document")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	docID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.VerifyDocument(c.Request.Context(), docID, adminID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DOCUMENT_VERIFY_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminRejectDocument — POST /v1/rider/admin/documents/:id/reject.
func (h *Handler) AdminRejectDocument(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "document.reject")
	c.Set(middleware.AuditTargetKindKey, "document")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	docID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.RejectDocument(c.Request.Context(), docID, adminID, body.Reason)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DOCUMENT_REJECT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// --- Vehicles --------------------------------------------------------------

// AdminListVehicles — GET /v1/rider/admin/vehicles?status=.
func (h *Handler) AdminListVehicles(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "vehicle.list")
	status := c.Query("status")
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListVehiclesForAdmin(c.Request.Context(), status, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VEHICLE_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminVerifyVehicle — POST /v1/rider/admin/vehicles/:id/verify.
func (h *Handler) AdminVerifyVehicle(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "vehicle.verify")
	c.Set(middleware.AuditTargetKindKey, "vehicle")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	vehicleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.VerifyVehicle(c.Request.Context(), vehicleID, adminID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VEHICLE_VERIFY_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// AdminRejectVehicle — POST /v1/rider/admin/vehicles/:id/reject.
func (h *Handler) AdminRejectVehicle(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "vehicle.reject")
	c.Set(middleware.AuditTargetKindKey, "vehicle")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	vehicleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.RejectVehicle(c.Request.Context(), vehicleID, adminID, body.Reason); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VEHICLE_REJECT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// --- Subscription payments -------------------------------------------------

// AdminListPayments — GET /v1/rider/admin/payments?status=.
func (h *Handler) AdminListPayments(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "payment.list")
	status := c.Query("status")
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListSubscriptionPaymentsForAdmin(c.Request.Context(), status, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminVerifyPayment — POST /v1/rider/admin/payments/:id/verify.
func (h *Handler) AdminVerifyPayment(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "payment.verify")
	c.Set(middleware.AuditTargetKindKey, "payment")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	paymentID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.VerifySubscriptionPayment(c.Request.Context(), paymentID, adminID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_VERIFY_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminRejectPayment — POST /v1/rider/admin/payments/:id/reject.
func (h *Handler) AdminRejectPayment(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "payment.reject")
	c.Set(middleware.AuditTargetKindKey, "payment")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	paymentID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.RejectSubscriptionPayment(c.Request.Context(), paymentID, adminID, body.Reason); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_REJECT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// --- Rides admin ----------------------------------------------------------

// AdminListRides — GET /v1/rider/admin/rides?status=&q=&since=.
func (h *Handler) AdminListRides(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "ride.list")
	status := c.Query("status")
	query := c.Query("q")
	var since *time.Time
	if s := c.Query("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			since = &t
		}
	}
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListRidesForAdmin(c.Request.Context(), status, query, since, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminListLiveRides — GET /v1/rider/admin/rides/live.
func (h *Handler) AdminListLiveRides(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "ride.live")
	limit, _ := readPaging(c, 200, 500)
	out, err := h.svc.ListLiveRidesForAdmin(c.Request.Context(), limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_LIVE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminCancelRide — POST /v1/rider/admin/rides/:id/cancel.
func (h *Handler) AdminCancelRide(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "ride.cancel")
	c.Set(middleware.AuditTargetKindKey, "ride")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.AdminCancelRide(c.Request.Context(), rideID, adminID, body.Reason)
	if err != nil {
		mapTransitionError(c, err, "RIDE_CANCEL_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// --- Safety incidents -----------------------------------------------------

// AdminListSafetyIncidents — GET /v1/rider/admin/safety-incidents?status=.
func (h *Handler) AdminListSafetyIncidents(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "safety_incident.list")
	status := c.Query("status")
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.Store().ListSafetyIncidents(c.Request.Context(), status, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SAFETY_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminAcknowledgeIncident — POST /v1/rider/admin/safety-incidents/:id/acknowledge.
func (h *Handler) AdminAcknowledgeIncident(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "safety_incident.acknowledge")
	c.Set(middleware.AuditTargetKindKey, "safety_incident")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	incidentID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.AcknowledgeSafetyIncident(c.Request.Context(), incidentID, adminID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SAFETY_ACK_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// resolveIncidentRequest is the body for resolve.
type resolveIncidentRequest struct {
	Note string `json:"note"`
}

// AdminResolveIncident — POST /v1/rider/admin/safety-incidents/:id/resolve.
func (h *Handler) AdminResolveIncident(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "safety_incident.resolve")
	c.Set(middleware.AuditTargetKindKey, "safety_incident")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	incidentID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body resolveIncidentRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.ResolveSafetyIncident(c.Request.Context(), incidentID, adminID, body.Note)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SAFETY_RESOLVE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// --- Cities / Zones / Fare rules CRUD --------------------------------------

// createCityRequest is the body for POST /v1/rider/admin/cities.
type createCityRequest struct {
	Name         string `json:"name"`
	State        string `json:"state,omitempty"`
	Country      string `json:"country,omitempty"`
	CurrencyCode string `json:"currency_code,omitempty"`
}

// AdminCreateCity — POST /v1/rider/admin/cities.
func (h *Handler) AdminCreateCity(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "city.create")
	c.Set(middleware.AuditTargetKindKey, "city")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	var body createCityRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateCity(c.Request.Context(), adminID, service.CreateCityRequest{
		Name:         body.Name,
		State:        body.State,
		Country:      body.Country,
		CurrencyCode: body.CurrencyCode,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CITY_CREATE_FAILED")
		return
	}
	c.Set(middleware.AuditTargetIDKey, out.ID)
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// updateCityRequest is the body for PATCH /v1/rider/admin/cities/:id.
type updateCityRequest struct {
	Name         *string `json:"name,omitempty"`
	State        *string `json:"state,omitempty"`
	Country      *string `json:"country,omitempty"`
	CurrencyCode *string `json:"currency_code,omitempty"`
	IsActive     *bool   `json:"is_active,omitempty"`
}

// AdminUpdateCity — PATCH /v1/rider/admin/cities/:id.
func (h *Handler) AdminUpdateCity(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "city.update")
	c.Set(middleware.AuditTargetKindKey, "city")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	cityID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body updateCityRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.UpdateCity(c.Request.Context(), adminID, cityID, service.UpdateCityRequest{
		Name:         body.Name,
		State:        body.State,
		Country:      body.Country,
		CurrencyCode: body.CurrencyCode,
		IsActive:     body.IsActive,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CITY_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// createZoneRequest is the body for POST /v1/rider/admin/zones.
type createZoneRequest struct {
	CityID      uuid.UUID `json:"city_id"`
	Name        string    `json:"name"`
	BoundaryWKT string    `json:"boundary_wkt"`
}

// AdminCreateZone — POST /v1/rider/admin/zones.
func (h *Handler) AdminCreateZone(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "zone.create")
	c.Set(middleware.AuditTargetKindKey, "zone")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	var body createZoneRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateZone(c.Request.Context(), adminID, service.CreateZoneRequest{
		CityID:      body.CityID,
		Name:        body.Name,
		BoundaryWKT: body.BoundaryWKT,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ZONE_CREATE_FAILED")
		return
	}
	c.Set(middleware.AuditTargetIDKey, out.ID)
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// updateZoneRequest is the body for PATCH /v1/rider/admin/zones/:id.
type updateZoneRequest struct {
	Name        *string `json:"name,omitempty"`
	BoundaryWKT *string `json:"boundary_wkt,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
}

// AdminUpdateZone — PATCH /v1/rider/admin/zones/:id.
func (h *Handler) AdminUpdateZone(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "zone.update")
	c.Set(middleware.AuditTargetKindKey, "zone")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	zoneID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body updateZoneRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.UpdateZone(c.Request.Context(), adminID, zoneID, service.UpdateZoneRequest{
		Name:        body.Name,
		BoundaryWKT: body.BoundaryWKT,
		IsActive:    body.IsActive,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ZONE_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// createFareRuleRequest is the body for POST /v1/rider/admin/fare-rules.
type createFareRuleRequest struct {
	CityID          uuid.UUID `json:"city_id"`
	VehicleType     string    `json:"vehicle_type"`
	BaseFare        float64   `json:"base_fare"`
	PerKMFare       float64   `json:"per_km_fare"`
	PerMinuteFare   float64   `json:"per_minute_fare"`
	MinimumFare     float64   `json:"minimum_fare"`
	PlatformFee     float64   `json:"platform_fee"`
	NightMultiplier float64   `json:"night_multiplier"`
	PeakMultiplier  float64   `json:"peak_multiplier"`
	CancellationFee float64   `json:"cancellation_fee"`
}

// AdminCreateFareRule — POST /v1/rider/admin/fare-rules.
func (h *Handler) AdminCreateFareRule(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "fare_rule.create")
	c.Set(middleware.AuditTargetKindKey, "fare_rule")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	var body createFareRuleRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateFareRule(c.Request.Context(), adminID, service.CreateFareRuleRequest{
		CityID:          body.CityID,
		VehicleType:     body.VehicleType,
		BaseFare:        body.BaseFare,
		PerKMFare:       body.PerKMFare,
		PerMinuteFare:   body.PerMinuteFare,
		MinimumFare:     body.MinimumFare,
		PlatformFee:     body.PlatformFee,
		NightMultiplier: body.NightMultiplier,
		PeakMultiplier:  body.PeakMultiplier,
		CancellationFee: body.CancellationFee,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_RULE_CREATE_FAILED")
		return
	}
	c.Set(middleware.AuditTargetIDKey, out.ID)
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// updateFareRuleRequest is the PATCH body.
type updateFareRuleRequest struct {
	BaseFare        *float64 `json:"base_fare,omitempty"`
	PerKMFare       *float64 `json:"per_km_fare,omitempty"`
	PerMinuteFare   *float64 `json:"per_minute_fare,omitempty"`
	MinimumFare     *float64 `json:"minimum_fare,omitempty"`
	PlatformFee     *float64 `json:"platform_fee,omitempty"`
	NightMultiplier *float64 `json:"night_multiplier,omitempty"`
	PeakMultiplier  *float64 `json:"peak_multiplier,omitempty"`
	CancellationFee *float64 `json:"cancellation_fee,omitempty"`
	IsActive        *bool    `json:"is_active,omitempty"`
}

// AdminUpdateFareRule — PATCH /v1/rider/admin/fare-rules/:id.
func (h *Handler) AdminUpdateFareRule(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "fare_rule.update")
	c.Set(middleware.AuditTargetKindKey, "fare_rule")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	ruleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body updateFareRuleRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.UpdateFareRule(c.Request.Context(), adminID, ruleID, service.UpdateFareRuleRequest{
		BaseFare:        body.BaseFare,
		PerKMFare:       body.PerKMFare,
		PerMinuteFare:   body.PerMinuteFare,
		MinimumFare:     body.MinimumFare,
		PlatformFee:     body.PlatformFee,
		NightMultiplier: body.NightMultiplier,
		PeakMultiplier:  body.PeakMultiplier,
		CancellationFee: body.CancellationFee,
		IsActive:        body.IsActive,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_RULE_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// --- Audit logs -----------------------------------------------------------

// AdminListAuditLogs — GET /v1/rider/admin/audit-logs?actor=&action=&target_kind=&since=.
func (h *Handler) AdminListAuditLogs(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "audit_log.list")
	limit, offset := readPaging(c, 100, 500)
	filter := store.AuditFilter{
		Action:     c.Query("action"),
		EntityType: c.Query("target_kind"),
		Limit:      limit,
		Offset:     offset,
	}
	if a := c.Query("actor"); a != "" {
		if u, err := uuid.Parse(a); err == nil {
			filter.Actor = &u
		}
	}
	if s := c.Query("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			filter.Since = &t
		}
	}
	out, err := h.svc.ListAuditLogs(c.Request.Context(), filter)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "AUDIT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}
