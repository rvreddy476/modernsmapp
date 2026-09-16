package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

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

// MStore permissions, exactly as commerce-service checks them
// (commerce-service/internal/http/admin_token.go).
const (
	permCommerceStatsRead = "commerce:stats.read"
	permSellersRead       = "commerce:sellers.read"
	permSellerApprove     = "commerce:seller.approve"
	permSellerSuspend     = "commerce:seller.suspend"
	permProductsModerate  = "commerce:products.moderate"
	permKYCVerify         = "commerce:kyc.verify"
	permPayoutsRead       = "commerce:payouts.read"
	permCODSettle         = "commerce:cod.settle"
	permCatalogueEdit     = "commerce:catalogue.edit"
	permBannersEdit       = "commerce:banners.edit"
	permJobsRead          = "commerce:jobs.read"
	permComplianceRead    = "commerce:compliance.read"
	permComplianceSweep   = "commerce:compliance.sweep"
)

// Commerce operations that run through two-person approval.
const opCODSettle = "cod.settle"

// Commerce operations with their own handlers (whitelisted bodies, answers
// kept as the console has always received them).
const (
	opSellersQueue          = "sellers.queue"
	opSellerRead            = "seller.read"
	opSellerApprove         = "seller.approve"
	opSellerReject          = "seller.reject"
	opSellerRequestChanges  = "seller.request_changes"
	opSellerSuspend         = "seller.suspend"
	opSellerUnsuspend       = "seller.unsuspend"
	opSellerKYCVerify       = "seller.kyc_verify"
	opProductsQueue         = "products.queue"
	opProductApprove        = "product.approve"
	opProductReject         = "product.reject"
	opProductRequestChanges = "product.request_changes"
	opPayoutsPending        = "payouts.pending"
)

// CommerceRoutes is the MStore route table under /v1/admin/commerce (the
// catalogue authoring routes are in CatalogueRoutes). The console paths are
// the ones apps/admin already calls.
var CommerceRoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", operation: "stats", permission: permCommerceStatsRead},

	{method: http.MethodGet, path: "/sellers/queue", operation: opSellersQueue, permission: permSellersRead},
	{method: http.MethodGet, path: "/sellers/:sellerId", operation: opSellerRead, permission: permSellersRead, targetType: "seller"},
	{method: http.MethodPost, path: "/sellers/:sellerId/approve", operation: opSellerApprove, permission: permSellerApprove, targetType: "seller"},
	{method: http.MethodPost, path: "/sellers/:sellerId/reject", operation: opSellerReject, permission: permSellerApprove, targetType: "seller"},
	{method: http.MethodPost, path: "/sellers/:sellerId/request-changes", operation: opSellerRequestChanges, permission: permSellerApprove, targetType: "seller"},
	{method: http.MethodPost, path: "/sellers/:sellerId/suspend", operation: opSellerSuspend, permission: permSellerSuspend, stepUp: true, targetType: "seller"},
	{method: http.MethodPost, path: "/sellers/:sellerId/unsuspend", operation: opSellerUnsuspend, permission: permSellerSuspend, stepUp: true, targetType: "seller"},
	{method: http.MethodPost, path: "/sellers/:sellerId/kyc/verify", operation: opSellerKYCVerify, permission: permKYCVerify, stepUp: true, targetType: "seller"},

	{method: http.MethodGet, path: "/products/queue", operation: opProductsQueue, permission: permProductsModerate},
	{method: http.MethodGet, path: "/products/:productId/submissions", operation: "product.submissions", permission: permProductsModerate, targetType: "product"},
	{method: http.MethodPost, path: "/products/:productId/approve", operation: opProductApprove, permission: permProductsModerate, targetType: "product"},
	{method: http.MethodPost, path: "/products/:productId/reject", operation: opProductReject, permission: permProductsModerate, targetType: "product"},
	{method: http.MethodPost, path: "/products/:productId/request-changes", operation: opProductRequestChanges, permission: permProductsModerate, targetType: "product"},

	{method: http.MethodGet, path: "/payouts/pending", operation: opPayoutsPending, permission: permPayoutsRead, stepUp: true},
	{method: http.MethodPost, path: "/cod-remittances/:remittanceId/settle", operation: opCODSettle, permission: permCODSettle, stepUp: true, twoPerson: true, targetType: "cod_remittance"},

	{method: http.MethodGet, path: "/banners", operation: "banners.list", permission: permBannersEdit},
	{method: http.MethodPost, path: "/banners", operation: "banner.save", permission: permBannersEdit},
	{method: http.MethodPut, path: "/banners/:bannerId", operation: "banner.update", permission: permBannersEdit, targetType: "banner"},
	{method: http.MethodDelete, path: "/banners/:bannerId", operation: "banner.delete", permission: permBannersEdit, targetType: "banner"},
	{method: http.MethodGet, path: "/jobs/dead-letter", operation: "jobs.dead_letter", permission: permJobsRead},
	{method: http.MethodGet, path: "/compliance-gaps", operation: "compliance_gaps.list", permission: permComplianceRead},
	{method: http.MethodPost, path: "/compliance-gaps/sweep", operation: "compliance_gaps.sweep", permission: permComplianceSweep},
}

// RegisterCommerceRoutes adds the MStore admin routes under /v1/admin/commerce.
//
//	seller suspend / unsuspend     commerce:seller.suspend, step-up
//	KYC verify                     commerce:kyc.verify, step-up
//	pending payouts                commerce:payouts.read, step-up
//	COD remittance settle          commerce:cod.settle, step-up, two-person
//	attribute-schema publish       commerce:catalogue.edit, step-up (catalogue)
func (h *Handler) RegisterCommerceRoutes(r *gin.Engine) {
	p := h.commerceProduct()
	cc := h.commerce

	h.approvals.Register("commerce", opCODSettle, func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
		var pl codSettlePayload
		if err := json.Unmarshal(payload, &pl); err != nil {
			return approvals.Result{Err: err}
		}
		id, err := uuid.Parse(pl.RemittanceID)
		if err != nil {
			return approvals.Result{Err: err}
		}
		body := map[string]string{}
		if pl.PayoutBatchID != "" {
			body["payout_batch_id"] = pl.PayoutBatchID
		}
		resp, err := cc.Do(ctx, service.ProductRequest{
			Method: http.MethodPost, Path: "/cod-remittances/" + id.String() + "/settle",
			Permission: permCODSettle, Actor: actor, Body: body,
		})
		return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
	})

	queue := func(path string) gin.HandlerFunc {
		return func(c *gin.Context) {
			q := url.Values{"limit": {strconv.Itoa(queryInt(c, "limit", 20))}, "offset": {strconv.Itoa(queryInt(c, "offset", 0))}}
			h.productCall(c, p, service.ProductRequest{Method: http.MethodGet, Path: path, Query: q}, false)
		}
	}

	// action forwards a seller/product write with only the fields commerce
	// reads. statusOnly keeps the answer the console has always had.
	action := func(param, prefix, suffix string, statusOnly bool, body func(adminCommerceActionReq) any) gin.HandlerFunc {
		return func(c *gin.Context) {
			id, _, err := productPath(c, ":"+param)
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
				return
			}
			raw, _, _ := jsonBody(c)
			var req adminCommerceActionReq
			_ = json.Unmarshal(raw, &req)
			info := auditFrom(c)
			info.targetID, info.reason = c.Param(param), req.Reason
			for k, v := range notesPayload(req) {
				info.set(k, v)
			}
			h.productCall(c, p, service.ProductRequest{Method: http.MethodPost, Path: prefix + id + suffix, Body: body(req)}, statusOnly)
		}
	}
	notes := func(r adminCommerceActionReq) any { return adminActionPayload{Notes: r.Notes} }
	reasonNotes := func(r adminCommerceActionReq) any { return adminActionPayload{Reason: r.Reason, Notes: r.Notes} }
	changesNotes := func(r adminCommerceActionReq) any { return adminActionPayload{Changes: r.Changes, Notes: r.Notes} }

	special := map[string]gin.HandlerFunc{
		opSellersQueue:          queue("/sellers/queue"),
		opProductsQueue:         queue("/products/queue"),
		opSellerApprove:         action("sellerId", "/sellers/", "/approve", true, notes),
		opSellerReject:          action("sellerId", "/sellers/", "/reject", true, reasonNotes),
		opSellerRequestChanges:  action("sellerId", "/sellers/", "/request-changes", true, changesNotes),
		opSellerSuspend:         action("sellerId", "/sellers/", "/suspend", true, reasonNotes),
		opSellerUnsuspend:       action("sellerId", "/sellers/", "/unsuspend", false, reasonNotes),
		opSellerKYCVerify:       action("sellerId", "/sellers/", "/kyc/verify", false, func(adminCommerceActionReq) any { return struct{}{} }),
		opProductApprove:        action("productId", "/products/", "/approve", true, notes),
		opProductReject:         action("productId", "/products/", "/reject", true, func(r adminCommerceActionReq) any { return adminActionPayload{Reason: r.Reason} }),
		opProductRequestChanges: action("productId", "/products/", "/request-changes", false, changesNotes),
		opPayoutsPending: func(c *gin.Context) {
			q := url.Values{"limit": {strconv.Itoa(queryInt(c, "limit", 100))}}
			h.productCall(c, p, service.ProductRequest{Method: http.MethodGet, Path: "/payouts/pending", Query: q}, false)
		},
		opCODSettle: func(c *gin.Context) {
			raw, _, _ := jsonBody(c)
			var body struct {
				PayoutBatchID string `json:"payout_batch_id"`
				Reason        string `json:"reason"`
			}
			_ = json.Unmarshal(raw, &body)
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
		},
	}
	h.registerProduct(r, p, CommerceRoutes, special)
}

func (h *Handler) commerceProduct() product {
	return product{app: "commerce", label: "MStore", prefix: "/v1/admin/commerce", client: h.commerce}
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

// adminActionPayload is the body commerce's seller and product actions read.
type adminActionPayload struct {
	Reason  string `json:"reason,omitempty"`
	Notes   string `json:"notes,omitempty"`
	Changes string `json:"changes,omitempty"`
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

// writeUpstream answers with the downstream result and records it on the
// audit row: a transport error is a 502 to the client and status 0 in the row.
func writeUpstream(c *gin.Context, info *auditInfo, data []byte, status int, err error) {
	ctx := c.Request.Context()
	if errors.Is(err, service.ErrActorRequired) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeActorRequired,
			"A valid X-User-Id identifying the acting admin is required", nil)
		return
	}
	if errors.Is(err, service.ErrProductUnavailable) {
		zero := 0
		info.statusCode = &zero
		info.set("error", "service token key not configured")
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeProductUnavailable,
			"Admin actions for this application are not configured on this deployment", nil)
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
