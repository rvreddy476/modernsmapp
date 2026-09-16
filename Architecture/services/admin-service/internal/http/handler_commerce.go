package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// auditRecorder appends one row to admin.audit_log.
type auditRecorder interface {
	RecordAdminWrite(ctx context.Context, entry postgres.AdminAuditEntry) error
}

// Commerce permissions, as identity's catalogue names them.
const (
	permSellerApprove    = "commerce:seller.approve"
	permSellerSuspend    = "commerce:seller.suspend"
	permProductsModerate = "commerce:products.moderate"
	permKYCVerify        = "commerce:kyc.verify"
	permPayoutsRead      = "commerce:payouts.read"
	permCODSettle        = "commerce:cod.settle"
	permCatalogueEdit    = "commerce:catalogue.edit"
)

// Commerce operations that run through two-person approval.
const opCODSettle = "cod.settle"

// RegisterCommerceRoutes adds the MStore admin routes under /v1/admin/commerce.
//
//	seller and product decisions   commerce:seller.approve / seller.suspend / products.moderate
//	KYC verify                     commerce:kyc.verify, step-up
//	pending payouts                commerce:payouts.read, step-up
//	COD remittance settle          commerce:cod.settle, step-up, two-person
func (h *Handler) RegisterCommerceRoutes(r *gin.Engine, cc *service.CommerceClient) {
	const p = "/v1/admin/commerce"
	g := h.gate

	read := func(c *gin.Context, targetType, targetID string, call func(ctx context.Context) ([]byte, int, error)) {
		info := auditFrom(c)
		if targetType != "" {
			info.targetType, info.targetID = targetType, targetID
		}
		data, status, err := call(c.Request.Context())
		writeUpstream(c, info, data, status, err)
	}

	// Sellers
	g.Handle(r, http.MethodGet, p+"/sellers/queue",
		Requirement{Operation: "sellers.queue", Permission: permSellerApprove},
		func(c *gin.Context) {
			limit, offset := queryInt(c, "limit", 20), queryInt(c, "offset", 0)
			read(c, "", "", func(ctx context.Context) ([]byte, int, error) { return cc.ListSellerQueue(ctx, limit, offset) })
		})
	g.Handle(r, http.MethodGet, p+"/sellers/:sellerId",
		Requirement{Operation: "seller.read", Permission: permSellerApprove},
		func(c *gin.Context) {
			read(c, "seller", c.Param("sellerId"), func(ctx context.Context) ([]byte, int, error) { return cc.GetSeller(ctx, c.Param("sellerId")) })
		})

	sellerWrite := func(op, perm string, stepUp bool, call func(ctx context.Context, sellerID, actor string, req adminCommerceActionReq) ([]byte, int, error)) {
		g.Handle(r, http.MethodPost, p+"/sellers/:sellerId/"+strings.ReplaceAll(strings.TrimPrefix(op, "seller."), "_", "-"),
			Requirement{Operation: op, Permission: perm, StepUp: stepUp},
			func(c *gin.Context) {
				var req adminCommerceActionReq
				_ = c.ShouldBindJSON(&req)
				id := c.Param("sellerId")
				h.forwardCommerceWrite(c, commerceWrite{
					targetType: "seller", targetID: id, reason: req.Reason, payload: notesPayload(req),
					call: func(ctx context.Context, actor string) ([]byte, int, error) { return call(ctx, id, actor, req) },
				})
			})
	}
	sellerWrite("seller.approve", permSellerApprove, false, func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		s, err := cc.ApproveSeller(ctx, id, actor, req.Notes)
		return nil, s, err
	})
	sellerWrite("seller.reject", permSellerApprove, false, func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		s, err := cc.RejectSeller(ctx, id, actor, req.Reason, req.Notes)
		return nil, s, err
	})
	sellerWrite("seller.request_changes", permSellerApprove, false, func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		s, err := cc.RequestSellerChanges(ctx, id, actor, req.Changes, req.Notes)
		return nil, s, err
	})
	sellerWrite("seller.suspend", permSellerSuspend, false, func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		s, err := cc.SuspendSeller(ctx, id, actor, req.Reason, req.Notes)
		return nil, s, err
	})
	sellerWrite("seller.unsuspend", permSellerSuspend, false, func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		return cc.UnsuspendSeller(ctx, id, actor, req.Reason, req.Notes)
	})

	// KYC: runs the verification adapter over the seller's documents.
	g.Handle(r, http.MethodPost, p+"/sellers/:sellerId/kyc/verify",
		Requirement{Operation: "seller.kyc_verify", Permission: permKYCVerify, StepUp: true},
		func(c *gin.Context) {
			var req adminCommerceActionReq
			_ = c.ShouldBindJSON(&req)
			id := c.Param("sellerId")
			h.forwardCommerceWrite(c, commerceWrite{
				targetType: "seller", targetID: id, reason: req.Reason,
				call: func(ctx context.Context, actor string) ([]byte, int, error) {
					return cc.VerifySellerKYC(ctx, id, actor)
				},
			})
		})

	// Products
	g.Handle(r, http.MethodGet, p+"/products/queue",
		Requirement{Operation: "products.queue", Permission: permProductsModerate},
		func(c *gin.Context) {
			limit, offset := queryInt(c, "limit", 20), queryInt(c, "offset", 0)
			read(c, "", "", func(ctx context.Context) ([]byte, int, error) { return cc.ListProductQueue(ctx, limit, offset) })
		})
	productWrite := func(op string, call func(ctx context.Context, productID, actor string, req adminCommerceActionReq) ([]byte, int, error)) {
		g.Handle(r, http.MethodPost, p+"/products/:productId/"+strings.ReplaceAll(strings.TrimPrefix(op, "product."), "_", "-"),
			Requirement{Operation: op, Permission: permProductsModerate},
			func(c *gin.Context) {
				var req adminCommerceActionReq
				_ = c.ShouldBindJSON(&req)
				id := c.Param("productId")
				h.forwardCommerceWrite(c, commerceWrite{
					targetType: "product", targetID: id, reason: req.Reason, payload: notesPayload(req),
					call: func(ctx context.Context, actor string) ([]byte, int, error) { return call(ctx, id, actor, req) },
				})
			})
	}
	productWrite("product.approve", func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		s, err := cc.ApproveProduct(ctx, id, actor, req.Notes)
		return nil, s, err
	})
	productWrite("product.reject", func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		s, err := cc.RejectProduct(ctx, id, actor, req.Reason)
		return nil, s, err
	})
	productWrite("product.request_changes", func(ctx context.Context, id, actor string, req adminCommerceActionReq) ([]byte, int, error) {
		return cc.RequestProductChanges(ctx, id, actor, req.Changes, req.Notes)
	})

	// Money
	g.Handle(r, http.MethodGet, p+"/payouts/pending",
		Requirement{Operation: "payouts.pending", Permission: permPayoutsRead, StepUp: true},
		func(c *gin.Context) {
			limit := queryInt(c, "limit", 100)
			read(c, "", "", func(ctx context.Context) ([]byte, int, error) { return cc.ListPendingPayouts(ctx, limit) })
		})

	h.approvals.Register("commerce", opCODSettle, func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
		var p codSettlePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return approvals.Result{Err: err}
		}
		data, status, err := cc.SettleCODRemittance(ctx, p.RemittanceID, p.PayoutBatchID, actor)
		return approvals.Result{Data: data, Status: status, Err: err}
	})
	g.Handle(r, http.MethodPost, p+"/cod-remittances/:remittanceId/settle",
		Requirement{Operation: opCODSettle, Permission: permCODSettle, StepUp: true, TwoPerson: true},
		func(c *gin.Context) {
			var body struct {
				PayoutBatchID string `json:"payout_batch_id"`
				Reason        string `json:"reason"`
			}
			_ = c.ShouldBindJSON(&body)
			id, err := uuid.Parse(c.Param("remittanceId"))
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REMITTANCE_ID", "Invalid remittance id", nil)
				return
			}
			payload := codSettlePayload{RemittanceID: id.String()}
			if body.PayoutBatchID != "" {
				b, err := uuid.Parse(body.PayoutBatchID)
				if err != nil {
					api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_PAYOUT_BATCH_ID", "Invalid payout batch id", nil)
					return
				}
				payload.PayoutBatchID = b.String()
			}
			h.submitTwoPerson(c, "cod_remittance", id.String(), body.Reason, payload)
		})
}

type codSettlePayload struct {
	RemittanceID  string `json:"remittance_id"`
	PayoutBatchID string `json:"payout_batch_id,omitempty"`
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

func queryInt(c *gin.Context, key string, def int) int {
	if n, err := strconv.Atoi(c.Query(key)); err == nil && n >= 0 {
		return n
	}
	return def
}

// commerceWrite is one admin write that admin-service forwards to commerce.
type commerceWrite struct {
	targetType string
	targetID   string
	reason     string
	payload    map[string]any
	// call performs the downstream request as actor. data is written back to
	// the client when non-empty; otherwise only the status is.
	call func(ctx context.Context, actor string) (data []byte, status int, err error)
}

// forwardCommerceWrite performs one gated commerce write as the acting admin
// and fills in the gate's audit row. It must run behind the gate, which has
// already established the actor and written nothing yet.
func (h *Handler) forwardCommerceWrite(c *gin.Context, w commerceWrite) {
	info := auditFrom(c)
	info.targetType, info.targetID, info.reason = w.targetType, w.targetID, w.reason
	for k, v := range w.payload {
		info.set(k, v)
	}
	data, status, err := w.call(c.Request.Context(), actorFrom(c))
	writeUpstream(c, info, data, status, err)
}

// writeUpstream answers with the downstream result and records it on the
// audit row: a transport error is a 502 to the client and status 0 in the row.
func writeUpstream(c *gin.Context, info *auditInfo, data []byte, status int, err error) {
	ctx := c.Request.Context()
	if errors.Is(err, service.ErrActorRequired) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeActorRequired,
			"A valid X-User-Id identifying the acting admin is required", nil)
		return
	}
	if err != nil {
		zero := 0
		info.statusCode = &zero
		info.outcome = postgres.AuditOutcomeFailure
		info.set("error", err.Error())
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", "The owning service did not answer", nil)
		return
	}
	if len(data) == 0 {
		c.Status(status)
		c.Writer.WriteHeaderNow()
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
