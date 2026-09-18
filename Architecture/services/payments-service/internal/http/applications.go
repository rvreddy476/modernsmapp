package http

// Applications on the /v1/payments/internal family (migration 010).
//
//	GET /v1/payments/internal/applications
//	GET /v1/payments/internal/applications/:applicationId
//	PUT /v1/payments/internal/applications/:applicationId
//	GET /v1/payments/internal/applications/:applicationId/transactions?type=&status=&cursor=&limit=
//
// Reads use the family gate and payments:intent.read. A service token sees only
// the applications its SERVICE_CALLER_<NAME>_APPLICATIONS allows, and its
// transactions only for intents its own domain owns; the legacy internal key
// sees every application.
//
// The PUT is an operator route like the refund resolve: a token must carry
// payments:application.admin, the legacy key is refused in production, the
// operator is named in X-User-Id, and every write that changes something leaves
// one row in payments.application_audit_log. Disabling an application refuses
// its new payments; nothing existing is touched.
//
// And the application resolution every money request goes through: which
// applications a caller may name, and the one-release fallback for a request
// that names none.

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/atpost/payments-service/internal/config"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
)

// OpApplicationAdmin is the service-token operation for registry writes.
const OpApplicationAdmin = "payments:application.admin"

// Stable error codes for application scoping.
const (
	CodeApplicationRequired   = "APPLICATION_REQUIRED"
	CodeApplicationUnknown    = "APPLICATION_UNKNOWN"
	CodeApplicationNotAllowed = "APPLICATION_NOT_ALLOWED"
	CodeApplicationDisabled   = "APPLICATION_DISABLED"
	CodeApplicationMismatch   = "APPLICATION_MISMATCH"
	CodeMethodNotEnabled      = "METHOD_NOT_ENABLED_FOR_APPLICATION"
)

const (
	maxApplicationNameRunes = 100
	maxApplicationSettings  = 16 << 10
)

var paymentStatuses = map[string]bool{
	"pending": true, "processing": true, "succeeded": true, "failed": true,
	"refunded": true, "partially_refunded": true, "disputed": true, "cancelled": true,
}

var refundStatuses = map[string]bool{
	"pending": true, "submitted": true, "succeeded": true, "failed": true,
	"needs_attention": true, "resolved": true,
}

// legacyApplications is the reference-type fallback for the user-facing family,
// whose callers present no identity to hold an allowlist. It is the same mapping
// migration 010's backfill uses (payments.legacy_application_for, extended
// with dating by migration 011, mopedu by migration 012 and mopedu_subscription
// by migration 013).
var legacyApplications = map[string]string{
	servicetoken.RefOrder:              "mstore",
	servicetoken.RefFoodOrder:          "feast",
	servicetoken.RefDatingPremium:      "dating",
	servicetoken.RefMopeduRide:         "mopedu",
	servicetoken.RefMopeduSubscription: "mopedu",
}

// WithCallerApplications installs the caller → application allowlist that
// main.go parsed from SERVICE_CALLER_<NAME>_APPLICATIONS and validated against
// the registry.
func (h *Handler) WithCallerApplications(allow map[string][]string) *Handler {
	h.callerApps = allow
	return h
}

// allowedApplications is what this request's caller may name. A legacy
// internal-key caller has no identity, so it may name any active application
// (anyActive); the registry check still applies.
func (h *Handler) allowedApplications(c *gin.Context) (apps []string, anyActive bool) {
	if isLegacyCaller(c) {
		return nil, true
	}
	return h.callerApps[callerDomain(c)], false
}

// callerMayRead reports whether the caller may read one application's data.
func (h *Handler) callerMayRead(c *gin.Context, key string) bool {
	apps, anyActive := h.allowedApplications(c)
	return anyActive || config.Allows(apps, key)
}

// defaultApplication is the ONE-RELEASE backward-compatibility rule: a request
// that names no application_id is attributed to the caller's only allowed
// application, with a WARN. A caller with several (or a legacy caller, which
// may name any) is refused, because there is nothing to choose between them by.
// Remove once commerce and food have shipped the shared client that always
// sends application_id.
func (h *Handler) defaultApplication(c *gin.Context) (string, bool) {
	apps, anyActive := h.allowedApplications(c)
	if !anyActive && len(apps) == 1 {
		slog.Warn("payments: request named no application_id; attributed to the caller's only allowed application "+
			"(deprecated fallback, removed next release)",
			"caller", callerDomain(c), "application_id", apps[0], "method", c.Request.Method, "route", c.FullPath())
		return apps[0], true
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, CodeApplicationRequired,
		"application_id is required", nil)
	return "", false
}

// resolveCreateApplication decides the application of a new intent: the named
// one if it is registered and this caller may use it, or the one-release
// default. Active status and the method are checked by the service. Writes the
// error response itself.
func (h *Handler) resolveCreateApplication(c *gin.Context, requested string) (string, bool) {
	if requested == "" {
		return h.defaultApplication(c)
	}
	ctx := c.Request.Context()
	if !postgres.ValidApplicationKey(requested) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeApplicationUnknown,
			"application_id is not a registered application", nil)
		return "", false
	}
	if _, err := h.svc.GetApplication(ctx, requested); err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeApplicationUnknown,
				"application_id is not a registered application", nil)
			return "", false
		}
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FETCH_FAILED", "could not read the application registry", nil)
		return "", false
	}
	if apps, anyActive := h.allowedApplications(c); !anyActive && !config.Allows(apps, requested) {
		slog.Warn("payments: caller named an application it is not allowed",
			"caller", callerDomain(c), "application_id", requested, "route", c.FullPath())
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeApplicationNotAllowed,
			"this caller may not create payments for application_id", nil)
		return "", false
	}
	return requested, true
}

// writeApplicationError maps the service and store application errors onto
// responses. Returns false when err is not one of them.
func writeApplicationError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	write := func(status int, code string) bool {
		api.ErrorWithContext(ctx, c.Writer, status, code, err.Error(), nil)
		return true
	}
	switch {
	case errors.Is(err, service.ErrApplicationUnknown), errors.Is(err, postgres.ErrApplicationNotFound):
		return write(http.StatusUnprocessableEntity, CodeApplicationUnknown)
	case errors.Is(err, service.ErrApplicationDisabled):
		return write(http.StatusUnprocessableEntity, CodeApplicationDisabled)
	case errors.Is(err, service.ErrMethodNotEnabledForApplication):
		return write(http.StatusUnprocessableEntity, CodeMethodNotEnabled)
	case errors.Is(err, postgres.ErrApplicationMismatch):
		return write(http.StatusUnprocessableEntity, CodeApplicationMismatch)
	case errors.Is(err, postgres.ErrApplicationRequired):
		return write(http.StatusUnprocessableEntity, CodeApplicationRequired)
	case errors.Is(err, postgres.ErrInvalidChannel):
		return write(http.StatusBadRequest, "INVALID_BODY")
	}
	return false
}

// applicationFilter reads an optional ?application_id= on a list route. A token
// caller may only filter to an application it is allowed. Writes the error.
func (h *Handler) applicationFilter(c *gin.Context) (string, bool) {
	key := c.Query("application_id")
	if key == "" {
		return "", true
	}
	ctx := c.Request.Context()
	if !postgres.ValidApplicationKey(key) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_APPLICATION_ID", "application_id is not well formed", nil)
		return "", false
	}
	if !h.callerMayRead(c, key) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeApplicationNotAllowed, "this caller may not read application_id", nil)
		return "", false
	}
	return key, true
}

// ─── Registry reads ──────────────────────────────────────────────────

// ListApplications GET /v1/payments/internal/applications
func (h *Handler) ListApplications(c *gin.Context) {
	ctx := c.Request.Context()
	apps, err := h.svc.ListApplications(ctx)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FETCH_FAILED", "could not read the application registry", nil)
		return
	}
	items := make([]postgres.Application, 0, len(apps))
	for _, a := range apps {
		if h.callerMayRead(c, a.Key) {
			items = append(items, a)
		}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// readableApplicationKey validates :applicationId and the caller's right to it.
func (h *Handler) readableApplicationKey(c *gin.Context) (string, bool) {
	ctx := c.Request.Context()
	key := c.Param("applicationId")
	if !postgres.ValidApplicationKey(key) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_APPLICATION_ID", "application id is not well formed", nil)
		return "", false
	}
	if !h.callerMayRead(c, key) {
		slog.Warn("payments: caller refused an application it is not allowed",
			"caller", callerDomain(c), "application_id", key, "route", c.FullPath())
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeApplicationNotAllowed, "this caller may not read this application", nil)
		return "", false
	}
	return key, true
}

// GetApplication GET /v1/payments/internal/applications/:applicationId
func (h *Handler) GetApplication(c *gin.Context) {
	ctx := c.Request.Context()
	key, ok := h.readableApplicationKey(c)
	if !ok {
		return
	}
	app, err := h.svc.GetApplication(ctx, key)
	switch {
	case errors.Is(err, postgres.ErrApplicationNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "application not found", nil)
		return
	case err != nil:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FETCH_FAILED", "could not read the application registry", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, app, nil)
}

// ─── Transactions ────────────────────────────────────────────────────

// ListApplicationTransactions GET /v1/payments/internal/applications/:applicationId/transactions
func (h *Handler) ListApplicationTransactions(c *gin.Context) {
	ctx := c.Request.Context()
	key, ok := h.readableApplicationKey(c)
	if !ok {
		return
	}
	f := postgres.TransactionFilter{ApplicationID: key, Limit: 50, Type: c.Query("type"), Status: c.Query("status")}
	switch f.Type {
	case "", postgres.TransactionPayment, postgres.TransactionRefund:
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_TYPE", "type must be payment or refund", nil)
		return
	}
	if f.Status != "" {
		valid := (f.Type != postgres.TransactionRefund && paymentStatuses[f.Status]) ||
			(f.Type != postgres.TransactionPayment && refundStatuses[f.Status])
		if !valid {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_STATUS", "status is not a status of this transaction type", nil)
			return
		}
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_LIMIT", "limit must be 1..200", nil)
			return
		}
		f.Limit = n
	}
	if v := c.Query("cursor"); v != "" {
		cur, err := decodeRefundCursor(v)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not valid", nil)
			return
		}
		f.After = cur
	}
	if !isLegacyCaller(c) {
		// Defence in depth: a token also sees only intents its own domain owns,
		// as on every other owner-scoped route.
		f.OwnerDomain = callerDomain(c)
		if f.OwnerDomain == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "caller identity is required", nil)
			return
		}
	}
	if _, err := h.svc.GetApplication(ctx, key); err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "application not found", nil)
			return
		}
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FETCH_FAILED", "could not read the application registry", nil)
		return
	}
	items, next, err := h.svc.ListApplicationTransactions(ctx, f)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FETCH_FAILED", "could not list transactions", nil)
		return
	}
	if items == nil {
		items = []postgres.Transaction{}
	}
	var nextCursor any
	if next != nil {
		nextCursor = encodeRefundCursor(*next)
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor}, nil)
}

// ─── Registry write ──────────────────────────────────────────────────

// PutApplication PUT /v1/payments/internal/applications/:applicationId
//
//	{"display_name":"Feast","status":"active","merchant_display_name":"Momentum Merchant",
//	 "enabled_methods":["upi","card"],"settings":{}}
//
// A full replace: every field but settings is required. 201 when it created the
// entry, 200 otherwise; `changed:false` on a replay that matched.
func (h *Handler) PutApplication(c *gin.Context) {
	ctx := c.Request.Context()
	key := c.Param("applicationId")
	if !postgres.ValidApplicationKey(key) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_APPLICATION_ID",
			"application key must match ^[a-z][a-z0-9_]{1,31}$", nil)
		return
	}
	operator := strings.TrimSpace(c.GetHeader("X-User-Id"))
	if operator == "" || len(operator) > maxOperatorIDLen || !utf8.ValidString(operator) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "OPERATOR_REQUIRED",
			"X-User-Id must name the operator changing the application", nil)
		return
	}
	var body struct {
		DisplayName         *string         `json:"display_name"`
		Status              *string         `json:"status"`
		MerchantDisplayName *string         `json:"merchant_display_name"`
		EnabledMethods      []string        `json:"enabled_methods"`
		Settings            json.RawMessage `json:"settings"`
	}
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"body must be JSON with display_name, status, merchant_display_name, enabled_methods and optional settings", nil)
		return
	}
	invalid := func(msg string) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_APPLICATION", msg, nil)
	}
	name := func(p *string) (string, bool) {
		if p == nil {
			return "", false
		}
		s := strings.TrimSpace(*p)
		n := utf8.RuneCountInString(s)
		return s, n >= 1 && n <= maxApplicationNameRunes
	}
	displayName, ok := name(body.DisplayName)
	if !ok {
		invalid("display_name is required and at most 100 characters")
		return
	}
	merchantName, ok := name(body.MerchantDisplayName)
	if !ok {
		invalid("merchant_display_name is required and at most 100 characters")
		return
	}
	if body.Status == nil || (*body.Status != postgres.ApplicationStatusActive && *body.Status != postgres.ApplicationStatusDisabled) {
		invalid("status must be active or disabled")
		return
	}
	if len(body.EnabledMethods) == 0 {
		invalid("enabled_methods must name at least one method")
		return
	}
	for _, m := range body.EnabledMethods {
		if err := paymentmethod.Validate(m); err != nil {
			invalid("enabled_methods: " + err.Error())
			return
		}
	}
	settings := json.RawMessage(`{}`)
	if trimmed := bytes.TrimSpace(body.Settings); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var obj map[string]any
		if len(trimmed) > maxApplicationSettings || json.Unmarshal(trimmed, &obj) != nil {
			invalid("settings must be a JSON object of at most 16 KB")
			return
		}
		settings = trimmed
	}

	in := postgres.PutApplicationInput{
		Key: key, DisplayName: displayName, Status: *body.Status, MerchantDisplayName: merchantName,
		EnabledMethods: body.EnabledMethods, Settings: settings,
		OperatorID: operator, Credential: "internal_key",
	}
	if !isLegacyCaller(c) {
		if callerDomain(c) == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "caller identity is required", nil)
			return
		}
		in.Credential = "service_token:" + callerDomain(c)
	}
	res, err := h.svc.PutApplication(ctx, in)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "WRITE_FAILED", "could not write the application", nil)
		return
	}
	if res.Changed {
		slog.Warn("payments: application registry changed",
			"application_id", key, "created", res.Created, "status", res.Application.Status,
			"operator_id", operator, "credential", in.Credential)
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	api.JSON(c.Writer, status, res, nil)
}
