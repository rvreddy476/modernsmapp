package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// auditRecorder appends one row to admin.audit_log per admin write.
type auditRecorder interface {
	RecordAdminWrite(ctx context.Context, entry postgres.AdminAuditEntry) error
}

// RegisterCommerceRoutes adds seller/product approval routes under /v1/admin/commerce.
func (h *Handler) RegisterCommerceRoutes(r *gin.Engine, cc *service.CommerceClient) {
	g := r.Group("/v1/admin/commerce")

	// Seller moderation
	g.GET("/sellers/queue", requireScopeFn("moderator", "admin", "superadmin"), func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		data, status, err := cc.ListSellerQueue(c.Request.Context(), limit, offset)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil)
			return
		}
		c.Data(status, "application/json", data)
	})

	g.GET("/sellers/:sellerId", requireScopeFn("moderator", "admin", "superadmin"), func(c *gin.Context) {
		data, status, err := cc.GetSeller(c.Request.Context(), c.Param("sellerId"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil)
			return
		}
		c.Data(status, "application/json", data)
	})

	g.POST("/sellers/:sellerId/approve", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		h.forwardCommerceWrite(c, commerceWrite{
			operation: "seller.approve", targetType: "seller", targetID: c.Param("sellerId"),
			payload: notesPayload(req),
			call: func(ctx context.Context, actorID string) ([]byte, int, error) {
				status, err := cc.ApproveSeller(ctx, c.Param("sellerId"), actorID, req.Notes)
				return nil, status, err
			},
		})
	})

	g.POST("/sellers/:sellerId/reject", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		h.forwardCommerceWrite(c, commerceWrite{
			operation: "seller.reject", targetType: "seller", targetID: c.Param("sellerId"),
			reason: req.Reason, payload: notesPayload(req),
			call: func(ctx context.Context, actorID string) ([]byte, int, error) {
				status, err := cc.RejectSeller(ctx, c.Param("sellerId"), actorID, req.Reason, req.Notes)
				return nil, status, err
			},
		})
	})

	g.POST("/sellers/:sellerId/request-changes", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		h.forwardCommerceWrite(c, commerceWrite{
			operation: "seller.request_changes", targetType: "seller", targetID: c.Param("sellerId"),
			reason: req.Reason, payload: notesPayload(req),
			call: func(ctx context.Context, actorID string) ([]byte, int, error) {
				status, err := cc.RequestSellerChanges(ctx, c.Param("sellerId"), actorID, req.Changes, req.Notes)
				return nil, status, err
			},
		})
	})

	g.POST("/sellers/:sellerId/suspend", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		h.forwardCommerceWrite(c, commerceWrite{
			operation: "seller.suspend", targetType: "seller", targetID: c.Param("sellerId"),
			reason: req.Reason, payload: notesPayload(req),
			call: func(ctx context.Context, actorID string) ([]byte, int, error) {
				status, err := cc.SuspendSeller(ctx, c.Param("sellerId"), actorID, req.Reason, req.Notes)
				return nil, status, err
			},
		})
	})

	// Product moderation
	g.GET("/products/queue", requireScopeFn("moderator", "admin", "superadmin"), func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		data, status, err := cc.ListProductQueue(c.Request.Context(), limit, offset)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil)
			return
		}
		c.Data(status, "application/json", data)
	})

	g.POST("/products/:productId/approve", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		h.forwardCommerceWrite(c, commerceWrite{
			operation: "product.approve", targetType: "product", targetID: c.Param("productId"),
			payload: notesPayload(req),
			call: func(ctx context.Context, actorID string) ([]byte, int, error) {
				status, err := cc.ApproveProduct(ctx, c.Param("productId"), actorID, req.Notes)
				return nil, status, err
			},
		})
	})

	g.POST("/products/:productId/reject", requireScopeFn("admin", "superadmin"), func(c *gin.Context) {
		var req adminCommerceActionReq
		_ = c.ShouldBindJSON(&req)
		h.forwardCommerceWrite(c, commerceWrite{
			operation: "product.reject", targetType: "product", targetID: c.Param("productId"),
			reason: req.Reason,
			call: func(ctx context.Context, actorID string) ([]byte, int, error) {
				status, err := cc.RejectProduct(ctx, c.Param("productId"), actorID, req.Reason)
				return nil, status, err
			},
		})
	})
}

type adminCommerceActionReq struct {
	Reason  string `json:"reason"`
	Notes   string `json:"notes"`
	Changes string `json:"changes"`
}

func notesPayload(req adminCommerceActionReq) map[string]any {
	p := map[string]any{}
	if req.Notes != "" {
		p["notes"] = req.Notes
	}
	if req.Changes != "" {
		p["changes"] = req.Changes
	}
	return p
}

// commerceWrite is one admin write that admin-service forwards to commerce.
type commerceWrite struct {
	operation  string
	targetType string
	targetID   string
	reason     string
	payload    map[string]any
	// call performs the downstream request. data is written back to the
	// client when non-empty; otherwise only the status is.
	call func(ctx context.Context, actorID string) (data []byte, status int, err error)
}

// forwardCommerceWrite is the single path every commerce write takes: it refuses a
// write with no acting admin before anything is sent, performs the call, and
// appends one audit row whatever the outcome.
func (h *Handler) forwardCommerceWrite(c *gin.Context, w commerceWrite) {
	ctx := c.Request.Context()
	actorID := actorIDFromCtx(c)
	if actorID == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "ACTOR_REQUIRED",
			"A valid X-User-Id identifying the acting admin is required", nil)
		return
	}
	if h.audit == nil {
		// Without a recorder the write could not be audited; refuse it.
		slog.ErrorContext(ctx, "admin audit recorder not configured; refusing write", "operation", w.operation)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "AUDIT_UNAVAILABLE",
			"Admin writes are unavailable", nil)
		return
	}

	data, status, err := w.call(ctx, actorID)
	if errors.Is(err, service.ErrActorRequired) {
		// Refused by the client before any request was sent.
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "ACTOR_REQUIRED",
			"A valid X-User-Id identifying the acting admin is required", nil)
		return
	}

	entry := postgres.AdminAuditEntry{
		Actor:      actorID,
		App:        "commerce",
		Operation:  w.operation,
		TargetType: w.targetType,
		TargetID:   w.targetID,
		Reason:     w.reason,
		RequestID:  requestIDFrom(c),
		Outcome:    postgres.AuditOutcomeFailure,
		StatusCode: status,
		Payload:    w.payload,
	}
	if err != nil {
		if entry.Payload == nil {
			entry.Payload = map[string]any{}
		}
		entry.Payload["error"] = err.Error()
	} else if status >= 200 && status < 300 {
		entry.Outcome = postgres.AuditOutcomeSuccess
	}
	if aerr := h.audit.RecordAdminWrite(ctx, entry); aerr != nil {
		// The downstream call has already happened, so the response still
		// reports it; the lost audit row must at least reach the logs.
		slog.ErrorContext(ctx, "admin audit write failed", "error", aerr,
			"actor", entry.Actor, "app", entry.App, "operation", entry.Operation,
			"target_type", entry.TargetType, "target_id", entry.TargetID,
			"request_id", entry.RequestID, "outcome", entry.Outcome, "status_code", entry.StatusCode)
	}

	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil)
		return
	}
	if len(data) == 0 {
		c.Status(status)
		return
	}
	c.Data(status, "application/json", data)
}

func requestIDFrom(c *gin.Context) string {
	if id := trace.RequestIDFrom(c.Request.Context()); id != "" {
		return id
	}
	return c.GetHeader(trace.HeaderRequestID)
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
