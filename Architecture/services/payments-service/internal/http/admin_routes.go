package http

// Admin console routes (admin_token.go has the gate).
//
//	GET   /v1/payments/internal/admin/stats                         payments:stats.read
//	GET   /v1/payments/internal/admin/refunds/needs-attention       payments:refunds.read
//	GET   /v1/payments/internal/admin/refunds/:commandId            payments:refunds.read
//	POST  /v1/payments/internal/admin/refunds/:commandId/resolve    payments:refund.issue
//	GET   /v1/payments/internal/admin/intents                       payments:intents.read
//	GET   /v1/payments/internal/admin/intents/:id                   payments:intents.read
//	GET   /v1/payments/internal/admin/reconciliation                payments:reconciliation.read
//	GET   /v1/payments/internal/admin/applications                  payments:applications.read
//	PATCH /v1/payments/internal/admin/applications/:applicationId   payments:applications.manage
//	GET   /v1/payments/internal/admin/audit/payments                payments:audit.read
//	GET   /v1/payments/internal/admin/audit/applications            payments:audit.read
//
// Every route takes an optional ?application_id=. Absent, the admin sees every
// application; present, only rows of that application, and a row of another
// reads as absent (404). admin-service sets it for an admin whose role is
// scoped to one application.
//
// The admin family sees every owning domain (commerce, food, dating): it is
// the operator view, confined by application instead of by caller.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/paymentmethod"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AdminService is what the admin family needs from *service.Service.
type AdminService interface {
	AdminStats(ctx context.Context, applicationID string, pendingAge time.Duration) (*postgres.AdminStats, error)
	ListRefundsNeedingAttention(ctx context.Context, f postgres.NeedsAttentionFilter) ([]postgres.NeedsAttentionRefund, *postgres.RefundCursor, error)
	AdminGetRefund(ctx context.Context, id uuid.UUID, applicationID string) (*postgres.AdminRefund, error)
	ResolveRefundCommand(ctx context.Context, in postgres.ResolveRefundInput) (*postgres.RefundResolution, error)
	AdminGetIntent(ctx context.Context, id uuid.UUID, applicationID string) (*postgres.AdminIntentDetail, error)
	AdminListIntents(ctx context.Context, f postgres.AdminIntentFilter) ([]postgres.AdminIntent, *postgres.RefundCursor, error)
	AdminReconciliation(ctx context.Context, applicationID string, pendingAge time.Duration, limit int) (*postgres.Reconciliation, error)
	ListApplications(ctx context.Context) ([]postgres.Application, error)
	UpdateApplicationPresentation(ctx context.Context, in postgres.ApplicationPresentationInput) (*postgres.ApplicationWrite, error)
	AdminPaymentAudit(ctx context.Context, f postgres.PaymentAuditFilter) ([]postgres.PaymentAuditEntry, int64, error)
	AdminApplicationAudit(ctx context.Context, applicationID string, beforeID int64, limit int) ([]postgres.ApplicationAuditEntry, int64, error)
}

// adminCredential is recorded on every admin write's audit row.
const adminCredential = "service_token:" + IssuerAdminService

func adminError(c *gin.Context, status int, code, msg string) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, msg, nil)
}

// adminApplication reads the optional ?application_id= confinement.
func adminApplication(c *gin.Context) (string, bool) {
	key := strings.TrimSpace(c.Query("application_id"))
	if key != "" && !postgres.ValidApplicationKey(key) {
		adminError(c, http.StatusBadRequest, "INVALID_APPLICATION_ID", "application_id is not well formed")
		return "", false
	}
	return key, true
}

func adminLimit(c *gin.Context) (int, bool) {
	v := c.Query("limit")
	if v == "" {
		return 50, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 200 {
		adminError(c, http.StatusBadRequest, "INVALID_LIMIT", "limit must be 1..200")
		return 0, false
	}
	return n, true
}

func adminCursor(c *gin.Context) (*postgres.RefundCursor, bool) {
	v := c.Query("cursor")
	if v == "" {
		return nil, true
	}
	cur, err := decodeRefundCursor(v)
	if err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not valid")
		return nil, false
	}
	return cur, true
}

func adminBeforeID(c *gin.Context) (int64, bool) {
	v := c.Query("before_id")
	if v == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		adminError(c, http.StatusBadRequest, "INVALID_CURSOR", "before_id must be a positive integer")
		return 0, false
	}
	return n, true
}

func nextCursor(cur *postgres.RefundCursor) any {
	if cur == nil {
		return nil
	}
	return encodeRefundCursor(*cur)
}

// AdminStats GET /stats?application_id=
func (h *Handler) AdminStats(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	stats, err := h.admin.AdminStats(c.Request.Context(), app, h.pendingAge)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "payments: admin stats", "error", err)
		adminError(c, http.StatusInternalServerError, "PAYMENTS_ADMIN_STATS_FAILED", "stats unavailable")
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// AdminListRefundsNeedingAttention GET /refunds/needs-attention?application_id=&ref_type=&limit=&cursor=
func (h *Handler) AdminListRefundsNeedingAttention(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	limit, ok := adminLimit(c)
	if !ok {
		return
	}
	cur, ok := adminCursor(c)
	if !ok {
		return
	}
	items, next, err := h.admin.ListRefundsNeedingAttention(c.Request.Context(), postgres.NeedsAttentionFilter{
		Limit: limit, ReferenceType: c.Query("ref_type"), ApplicationID: app, After: cur,
	})
	if err != nil {
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not list parked refunds")
		return
	}
	if items == nil {
		items = []postgres.NeedsAttentionRefund{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor(next)}, nil)
}

// AdminGetRefund GET /refunds/:commandId?application_id=
func (h *Handler) AdminGetRefund(c *gin.Context) {
	id, err := uuid.Parse(c.Param("commandId"))
	if err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_ID", "invalid refund command id")
		return
	}
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	r, err := h.admin.AdminGetRefund(c.Request.Context(), id, app)
	switch {
	case errors.Is(err, postgres.ErrRefundCommandNotFound):
		adminError(c, http.StatusNotFound, "NOT_FOUND", "refund command not found")
	case err != nil:
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not read the refund")
	default:
		api.JSON(c.Writer, http.StatusOK, r, nil)
	}
}

// AdminResolveRefund POST /refunds/:commandId/resolve?application_id=
//
//	{"resolution":"refunded_manually"|"written_off"|"test_data","note":"…"}
//
// The operator is the token's act, never a header. The resolution, the ledger
// effect of refunded_manually and the payment_audit_log row (actor_id = act)
// commit in one transaction (postgres.ResolveRefundCommand), with every check
// that route already applies.
func (h *Handler) AdminResolveRefund(c *gin.Context) {
	actor, ok := adminActor(c)
	if !ok {
		adminError(c, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin")
		return
	}
	id, err := uuid.Parse(c.Param("commandId"))
	if err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_ID", "invalid refund command id")
		return
	}
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	var body struct {
		Resolution string `json:"resolution"`
		Note       string `json:"note"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_BODY", "body must be JSON with resolution and note")
		return
	}
	if !postgres.IsValidResolution(body.Resolution) {
		adminError(c, http.StatusBadRequest, "INVALID_RESOLUTION", "resolution must be refunded_manually, written_off or test_data")
		return
	}
	note := strings.TrimSpace(body.Note)
	if note == "" || utf8.RuneCountInString(note) > maxResolutionNoteRunes {
		adminError(c, http.StatusBadRequest, "INVALID_NOTE", "note is required and at most 1000 characters")
		return
	}
	res, err := h.admin.ResolveRefundCommand(c.Request.Context(), postgres.ResolveRefundInput{
		CommandID: id, Resolution: body.Resolution, Note: note,
		OperatorID: actor.String(), ActorID: &actor, Credential: adminCredential, ApplicationID: app,
	})
	switch {
	case errors.Is(err, postgres.ErrRefundCommandNotFound):
		adminError(c, http.StatusNotFound, "NOT_FOUND", "refund command not found")
	case errors.Is(err, postgres.ErrRefundCommandNotParked):
		adminError(c, http.StatusConflict, "REFUND_NOT_PARKED", "only a refund in needs_attention can be resolved")
	case errors.Is(err, postgres.ErrManualRefundRefused):
		adminError(c, http.StatusConflict, "MANUAL_REFUND_REFUSED", err.Error())
	case errors.Is(err, postgres.ErrInvalidResolution):
		adminError(c, http.StatusBadRequest, "INVALID_RESOLUTION", err.Error())
	case err != nil:
		adminError(c, http.StatusInternalServerError, "RESOLVE_FAILED", "could not resolve the refund")
	default:
		api.JSON(c.Writer, http.StatusOK, res, nil)
	}
}

// AdminListIntents GET /intents?application_id=&ref_type=&ref_id=&provider_ref=&status=&limit=&cursor=
func (h *Handler) AdminListIntents(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	limit, ok := adminLimit(c)
	if !ok {
		return
	}
	cur, ok := adminCursor(c)
	if !ok {
		return
	}
	f := postgres.AdminIntentFilter{
		ApplicationID: app, ReferenceType: c.Query("ref_type"), ProviderRef: strings.TrimSpace(c.Query("provider_ref")),
		Status: c.Query("status"), Limit: limit, After: cur,
	}
	if v := c.Query("ref_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			adminError(c, http.StatusBadRequest, "INVALID_REFERENCE_ID", "ref_id must be a UUID")
			return
		}
		f.ReferenceID = &id
	}
	if f.Status != "" && !paymentStatuses[f.Status] {
		adminError(c, http.StatusBadRequest, "INVALID_STATUS", "status is not a payment status")
		return
	}
	items, next, err := h.admin.AdminListIntents(c.Request.Context(), f)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not list payments")
		return
	}
	if items == nil {
		items = []postgres.AdminIntent{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor(next)}, nil)
}

// AdminGetIntent GET /intents/:id?application_id=
func (h *Handler) AdminGetIntent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_ID", "invalid intent id")
		return
	}
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	d, err := h.admin.AdminGetIntent(c.Request.Context(), id, app)
	switch {
	case errors.Is(err, postgres.ErrIntentNotFound):
		adminError(c, http.StatusNotFound, "NOT_FOUND", "intent not found")
	case err != nil:
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not read the intent")
	default:
		api.JSON(c.Writer, http.StatusOK, d, nil)
	}
}

// AdminReconciliation GET /reconciliation?application_id=&limit=
func (h *Handler) AdminReconciliation(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	limit, ok := adminLimit(c)
	if !ok {
		return
	}
	rec, err := h.admin.AdminReconciliation(c.Request.Context(), app, h.pendingAge, limit)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not read reconciliation status")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		// The reconciler runs only with a real provider; on the stub nothing is
		// reconciled and stuck intents stay stuck.
		"reconciler_running": h.provider != nil,
		"status":             rec,
	}, nil)
}

// AdminListApplications GET /applications?application_id=
func (h *Handler) AdminListApplications(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	apps, err := h.admin.ListApplications(c.Request.Context())
	if err != nil {
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not read the application registry")
		return
	}
	items := make([]postgres.Application, 0, len(apps))
	for _, a := range apps {
		if app == "" || a.Key == app {
			items = append(items, a)
		}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// AdminUpdateApplication PATCH /applications/:applicationId?application_id=
//
//	{"display_name":"Feast","merchant_display_name":"…","enabled_methods":["upi","card"]}
//
// Any subset of the three. Status (disable) and settings are not here. One
// application_audit_log row (operator_id = act) when something changed.
func (h *Handler) AdminUpdateApplication(c *gin.Context) {
	actor, ok := adminActor(c)
	if !ok {
		adminError(c, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin")
		return
	}
	key := c.Param("applicationId")
	if !postgres.ValidApplicationKey(key) {
		adminError(c, http.StatusBadRequest, "INVALID_APPLICATION_ID", "application id is not well formed")
		return
	}
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	if app != "" && app != key {
		adminError(c, http.StatusNotFound, "NOT_FOUND", "application not found")
		return
	}
	var body struct {
		DisplayName         *string  `json:"display_name"`
		MerchantDisplayName *string  `json:"merchant_display_name"`
		EnabledMethods      []string `json:"enabled_methods"`
	}
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_BODY",
			"body must be JSON with any of display_name, merchant_display_name and enabled_methods")
		return
	}
	in := postgres.ApplicationPresentationInput{Key: key, OperatorID: actor.String(), Credential: adminCredential}
	name := func(p *string, field string) (*string, bool) {
		if p == nil {
			return nil, true
		}
		s := strings.TrimSpace(*p)
		if n := utf8.RuneCountInString(s); n < 1 || n > maxApplicationNameRunes {
			adminError(c, http.StatusBadRequest, "INVALID_APPLICATION", field+" must be 1..100 characters")
			return nil, false
		}
		return &s, true
	}
	if in.DisplayName, ok = name(body.DisplayName, "display_name"); !ok {
		return
	}
	if in.MerchantDisplayName, ok = name(body.MerchantDisplayName, "merchant_display_name"); !ok {
		return
	}
	if body.EnabledMethods != nil {
		if len(body.EnabledMethods) == 0 {
			adminError(c, http.StatusBadRequest, "INVALID_APPLICATION", "enabled_methods must name at least one method")
			return
		}
		for _, m := range body.EnabledMethods {
			if err := paymentmethod.Validate(m); err != nil {
				adminError(c, http.StatusBadRequest, "INVALID_APPLICATION", "enabled_methods: "+err.Error())
				return
			}
		}
		in.EnabledMethods = body.EnabledMethods
	}
	if in.DisplayName == nil && in.MerchantDisplayName == nil && in.EnabledMethods == nil {
		adminError(c, http.StatusBadRequest, "INVALID_BODY", "nothing to change")
		return
	}
	res, err := h.admin.UpdateApplicationPresentation(c.Request.Context(), in)
	switch {
	case errors.Is(err, postgres.ErrApplicationNotFound):
		adminError(c, http.StatusNotFound, "NOT_FOUND", "application not found")
	case err != nil:
		adminError(c, http.StatusInternalServerError, "WRITE_FAILED", "could not write the application")
	default:
		api.JSON(c.Writer, http.StatusOK, res, nil)
	}
}

// AdminPaymentAudit GET /audit/payments?application_id=&intent_id=&event=&before_id=&limit=
func (h *Handler) AdminPaymentAudit(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	limit, ok := adminLimit(c)
	if !ok {
		return
	}
	before, ok := adminBeforeID(c)
	if !ok {
		return
	}
	f := postgres.PaymentAuditFilter{ApplicationID: app, Event: c.Query("event"), BeforeID: before, Limit: limit}
	if v := c.Query("intent_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			adminError(c, http.StatusBadRequest, "INVALID_ID", "intent_id must be a UUID")
			return
		}
		f.IntentID = &id
	}
	items, next, err := h.admin.AdminPaymentAudit(c.Request.Context(), f)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not read the payment audit log")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "next_before_id": nextBefore(next)}, nil)
}

// AdminApplicationAudit GET /audit/applications?application_id=&before_id=&limit=
func (h *Handler) AdminApplicationAudit(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	limit, ok := adminLimit(c)
	if !ok {
		return
	}
	before, ok := adminBeforeID(c)
	if !ok {
		return
	}
	items, next, err := h.admin.AdminApplicationAudit(c.Request.Context(), app, before, limit)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not read the application audit log")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "next_before_id": nextBefore(next)}, nil)
}

func nextBefore(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
