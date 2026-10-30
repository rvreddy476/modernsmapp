package http

import (
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) registerDeliveryOperations(g *gin.RouterGroup, r *gin.Engine) {
	g.GET("/availability", h.GetDeliveryAvailability)
	g.PUT("/availability", h.PutDeliveryAvailability)
	g.PUT("/location", h.UpdateDeliveryLocation)
	g.GET("/offers", h.GetDeliveryOffers)
	g.POST("/offers/:offerId/accept", h.AcceptDeliveryOffer)
	g.POST("/offers/:offerId/decline", h.DeclineDeliveryOffer)
	g.GET("/jobs", h.GetDeliveryJobs)
	g.GET("/jobs/current", h.GetCurrentDeliveryJob)
	g.GET("/jobs/:jobId", h.GetDeliveryJob)
	g.GET("/jobs/:jobId/contact", h.GetDeliveryJobContact)
	g.POST("/jobs/:jobId/pickup", h.PickupDeliveryJob)
	g.POST("/jobs/:jobId/deliver", h.DeliverDeliveryJob)
	g.POST("/jobs/:jobId/failed", h.FailDeliveryJob)
	g.POST("/jobs/:jobId/support", h.SupportDeliveryJob)
	g.GET("/earnings", h.GetDeliveryEarnings)
	// Customer and seller ownership is enforced in the store. These routes do
	// not grant either party delivery-partner approval or access to other jobs.
	g.PUT("/orders/:orderId/location", h.PutDeliveryOrderLocation)
	g.GET("/orders/:orderId/jobs", h.GetOrderDeliveryJobs)
	g.PUT("/sellers/:sellerId/location", h.PutDeliverySellerLocation)
	g.GET("/sellers/:sellerId/location", h.GetDeliverySellerLocation)
	g.POST("/seller/jobs/:jobId/ready", h.SellerDeliveryReady)
	g.GET("/customer/jobs/:jobId/code", h.GetDeliveryCustomerCode)
	a := r.Group(InternalAdminPrefix+"/delivery", h.requireAdminToken(PermCatalogueEdit))
	a.GET("/jobs", h.AdminDeliveryJobs)
	a.GET("/jobs/:jobId", h.AdminDeliveryJob)
	a.POST("/jobs/:jobId/cancel", h.AdminCancelDeliveryJob)
	a.POST("/jobs/:jobId/retry", h.AdminRetryDeliveryJob)
	a.GET("/support", h.AdminDeliverySupport)
	a.POST("/support/:ticketId/resolve", h.AdminResolveDeliverySupport)
}
func (h *Handler) GetDeliverySellerLocation(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	v, e := h.svc.SellerDeliveryLocation(c.Request.Context(), u, id)
	deliveryResult(c, v, e)
}
func deliveryResult(c *gin.Context, result any, err error) {
	if err != nil {
		deliveryError(c, err)
		return
	}
	api.JSON(c.Writer, 200, result, nil)
}
func (h *Handler) GetDeliveryAvailability(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	v, e := h.svc.DeliveryAvailability(c.Request.Context(), u)
	deliveryResult(c, v, e)
}
func (h *Handler) PutDeliveryAvailability(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	var v postgres.DeliveryAvailability
	if c.ShouldBindJSON(&v) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	if e := h.svc.SaveDeliveryAvailability(c.Request.Context(), u, v); e != nil {
		deliveryError(c, e)
		return
	}
	h.GetDeliveryAvailability(c)
}
func (h *Handler) GetDeliveryOffers(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	v, e := h.svc.DeliveryOffers(c.Request.Context(), u)
	deliveryResult(c, v, e)
}
func (h *Handler) decideDeliveryOffer(c *gin.Context, accept bool) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "offerId")
	if !ok {
		return
	}
	v, e := h.svc.DecideDeliveryOffer(c.Request.Context(), u, id, accept)
	deliveryResult(c, v, e)
}
func (h *Handler) AcceptDeliveryOffer(c *gin.Context)  { h.decideDeliveryOffer(c, true) }
func (h *Handler) DeclineDeliveryOffer(c *gin.Context) { h.decideDeliveryOffer(c, false) }
func (h *Handler) GetDeliveryJobs(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	v, e := h.svc.DeliveryJobs(c.Request.Context(), &u)
	deliveryResult(c, v, e)
}
func (h *Handler) GetCurrentDeliveryJob(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	rows, e := h.svc.DeliveryJobs(c.Request.Context(), &u)
	if e != nil {
		deliveryError(c, e)
		return
	}
	for _, j := range rows {
		if j.Status == "assigned" || j.Status == "picked_up" {
			deliveryResult(c, j, nil)
			return
		}
	}
	var none *postgres.DeliveryJob
	deliveryResult(c, none, nil)
}
func (h *Handler) GetDeliveryJob(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	v, e := h.svc.DeliveryJob(c.Request.Context(), &u, id)
	deliveryResult(c, v, e)
}
func (h *Handler) GetDeliveryJobContact(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	v, e := h.svc.DeliveryJobContact(c.Request.Context(), u, id)
	deliveryResult(c, v, e)
}
func (h *Handler) transitionDelivery(c *gin.Context, action string) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	var b struct {
		Code             string `json:"code"`
		Reason           string `json:"reason"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if c.ShouldBindJSON(&b) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	v, e := h.svc.TransitionDeliveryJob(c.Request.Context(), u, id, action, b.Code, b.Reason, b.ExpectedRevision)
	deliveryResult(c, v, e)
}
func (h *Handler) PickupDeliveryJob(c *gin.Context)  { h.transitionDelivery(c, "pickup") }
func (h *Handler) DeliverDeliveryJob(c *gin.Context) { h.transitionDelivery(c, "deliver") }
func (h *Handler) FailDeliveryJob(c *gin.Context)    { h.transitionDelivery(c, "failed") }
func (h *Handler) SupportDeliveryJob(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	var b struct {
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&b) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	deliveryResult(c, gin.H{"submitted": true}, h.svc.DeliverySupport(c.Request.Context(), u, id, b.Reason))
}
func (h *Handler) GetDeliveryEarnings(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	v, e := h.svc.DeliveryEarnings(c.Request.Context(), u)
	deliveryResult(c, v, e)
}
func (h *Handler) putDeliveryLocation(c *gin.Context, kind, param string) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, param)
	if !ok {
		return
	}
	var l postgres.DeliveryLocation
	if c.ShouldBindJSON(&l) != nil {
		deliveryError(c, postgres.ErrDeliveryConfigInvalid)
		return
	}
	deliveryResult(c, gin.H{"saved": true}, h.svc.SaveDeliveryLocation(c.Request.Context(), u, id, kind, l))
}
func (h *Handler) PutDeliveryOrderLocation(c *gin.Context) {
	h.putDeliveryLocation(c, "order", "orderId")
}
func (h *Handler) PutDeliverySellerLocation(c *gin.Context) {
	h.putDeliveryLocation(c, "seller", "sellerId")
}
func (h *Handler) SellerDeliveryReady(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	code, e := h.svc.SellerDeliveryReady(c.Request.Context(), u, id)
	deliveryResult(c, gin.H{"pickup_code": code}, e)
}
func (h *Handler) GetDeliveryCustomerCode(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	code, e := h.svc.DeliveryCustomerCode(c.Request.Context(), u, id)
	deliveryResult(c, gin.H{"delivery_code": code}, e)
}
func (h *Handler) AdminDeliveryJobs(c *gin.Context) {
	v, e := h.svc.DeliveryJobs(c.Request.Context(), nil)
	deliveryResult(c, v, e)
}
func (h *Handler) AdminDeliveryJob(c *gin.Context) {
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	v, e := h.svc.DeliveryJob(c.Request.Context(), nil, id)
	deliveryResult(c, v, e)
}
func (h *Handler) adminDeliveryAction(c *gin.Context, action string) {
	actor, ok := tokenActor(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	var b struct {
		ExpectedRevision int64  `json:"expected_revision"`
		Reason           string `json:"reason"`
	}
	if c.ShouldBindJSON(&b) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	e := h.svc.AdminDeliveryAction(c.Request.Context(), actor, id, action, b.Reason, b.ExpectedRevision)
	if e != nil {
		deliveryError(c, e)
		return
	}
	h.AdminDeliveryJob(c)
}
func (h *Handler) AdminCancelDeliveryJob(c *gin.Context) { h.adminDeliveryAction(c, "cancel") }
func (h *Handler) AdminRetryDeliveryJob(c *gin.Context)  { h.adminDeliveryAction(c, "retry") }
func (h *Handler) AdminDeliverySupport(c *gin.Context) {
	v, e := h.svc.DeliverySupportTickets(c.Request.Context())
	deliveryResult(c, v, e)
}
func (h *Handler) AdminResolveDeliverySupport(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "ticketId")
	if !ok {
		return
	}
	var b struct {
		Note string `json:"note"`
	}
	if c.ShouldBindJSON(&b) != nil {
		deliveryError(c, postgres.ErrDeliveryInvalid)
		return
	}
	deliveryResult(c, gin.H{"resolved": true}, h.svc.ResolveDeliverySupport(c.Request.Context(), actor, id, b.Note))
}

func (h *Handler) UpdateDeliveryLocation(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	var l postgres.DeliveryLocation
	if c.ShouldBindJSON(&l) != nil {
		deliveryError(c, postgres.ErrDeliveryConfigInvalid)
		return
	}
	if e := h.svc.UpdateDeliveryLocation(c.Request.Context(), u, l); e != nil {
		deliveryError(c, e)
		return
	}
	h.GetDeliveryAvailability(c)
}
func (h *Handler) GetOrderDeliveryJobs(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return // L3 (audit 2026-10-30): getUserID already wrote the 401
	}
	id, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	v, e := h.svc.OrderDeliveryJobs(c.Request.Context(), u, id)
	deliveryResult(c, v, e)
}
