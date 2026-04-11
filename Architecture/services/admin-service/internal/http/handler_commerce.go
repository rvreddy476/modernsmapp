package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/admin-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterCommerceRoutes adds seller/product approval routes under /v1/admin/commerce.
func (h *Handler) RegisterCommerceRoutes(r *gin.Engine, cc *service.CommerceClient) {
	g := r.Group("/v1/admin/commerce")

	// Seller moderation
	g.GET("/sellers/queue", requireScopeFn("moderator", "admin", "superadmin"), func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		data, status, err := cc.ListSellerQueue(c.Request.Context(), limit, offset)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Data(status, "application/json", data)
	})

	g.GET("/sellers/:sellerId", requireScopeFn("moderator", "admin", "superadmin"), func(c *gin.Context) {
		data, status, err := cc.GetSeller(c.Request.Context(), c.Param("sellerId"))
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Data(status, "application/json", data)
	})

	g.POST("/sellers/:sellerId/approve", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		actorID := actorIDFromCtx(c)
		status, err := cc.ApproveSeller(c.Request.Context(), c.Param("sellerId"), actorID, req.Notes)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Status(status)
	})

	g.POST("/sellers/:sellerId/reject", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		actorID := actorIDFromCtx(c)
		status, err := cc.RejectSeller(c.Request.Context(), c.Param("sellerId"), actorID, req.Reason, req.Notes)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Status(status)
	})

	g.POST("/sellers/:sellerId/request-changes", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		actorID := actorIDFromCtx(c)
		status, err := cc.RequestSellerChanges(c.Request.Context(), c.Param("sellerId"), actorID, req.Changes, req.Notes)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Status(status)
	})

	g.POST("/sellers/:sellerId/suspend", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		actorID := actorIDFromCtx(c)
		status, err := cc.SuspendSeller(c.Request.Context(), c.Param("sellerId"), actorID, req.Reason, req.Notes)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Status(status)
	})

	// Product moderation
	g.GET("/products/queue", requireScopeFn("moderator", "admin", "superadmin"), func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		data, status, err := cc.ListProductQueue(c.Request.Context(), limit, offset)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Data(status, "application/json", data)
	})

	g.POST("/products/:productId/approve", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		actorID := actorIDFromCtx(c)
		status, err := cc.ApproveProduct(c.Request.Context(), c.Param("productId"), actorID, req.Notes)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Status(status)
	})

	g.POST("/products/:productId/reject", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		actorID := actorIDFromCtx(c)
		status, err := cc.RejectProduct(c.Request.Context(), c.Param("productId"), actorID, req.Reason)
		if err != nil {
			api.Error(c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil, nil)
			return
		}
		c.Status(status)
	})
}

type adminCommerceActionReq struct {
	Reason  string `json:"reason"`
	Notes   string `json:"notes"`
	Changes string `json:"changes"`
}

// requireScopeFn returns a gin.HandlerFunc that enforces scope requirements.
func requireScopeFn(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAnyScope(c, scopes...) {
			c.Abort()
			return
		}
		c.Next()
	}
}

// actorIDFromCtx extracts the admin's user ID from the request header.
func actorIDFromCtx(c *gin.Context) string {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		return ""
	}
	return id.String()
}
