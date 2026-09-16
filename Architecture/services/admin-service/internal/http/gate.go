package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Access says how a declared admin route is authorised.
type Access int

const (
	// AccessPermission (the default) requires Permission.
	AccessPermission Access = iota
	// AccessAnyAdmin admits any caller the gate can identify; the handler
	// decides from the resolved permissions (GET /me, the approvals inbox, and
	// approve/reject, whose permission is the one stored on the approval).
	AccessAnyAdmin
	// AccessSelfService is not an admin action at all: a signed-in user acting
	// on their own data (data export). It bypasses the admin gate.
	AccessSelfService
	// AccessRetired answers 410 and does nothing else.
	AccessRetired
)

// Requirement is one admin route's declaration.
type Requirement struct {
	// Operation names the action in the audit trail, e.g. "seller.approve".
	Operation string
	// Permission is "<app>:<resource>.<action>"; its app is the audit app.
	Permission string
	// App overrides the audit app for routes without a permission.
	App    string
	Access Access
	// StepUp requires a step-up within adminauth.StepUpWindow.
	StepUp bool
	// TwoPerson routes submit through the approval service instead of
	// executing on the first admin's call.
	TwoPerson bool
	// AllowWithoutMFA admits a caller without X-Admin-MFA. Only GET /me, so the
	// console can tell an admin they must enrol.
	AllowWithoutMFA bool
	// Decide narrows the declaration from the request itself — a status or
	// outcome in the body, an amount — BEFORE the permission, step-up and
	// two-person checks and before any product call. It may swap Permission
	// for another permission of the same app and may add StepUp or TwoPerson;
	// it can never remove what the declaration requires.
	Decide func(c *gin.Context, perms adminauth.Permissions) (Decision, error)
	// MayTwoPerson declares that Decide can require two-person approval, so
	// boot checks the operation has an executor.
	MayTwoPerson bool
	// AdmitsHeldAs lets Decide admit an admin who lacks Permission but holds
	// another app's permission (Decision.HeldAs) — the payments views an
	// app-scoped admin reaches confined to their own application. The token
	// still carries Permission; the handler must apply the confinement. Only
	// routes that declare it may be decided this way.
	AdmitsHeldAs bool
}

// Decision is what Decide concluded for one request.
type Decision struct {
	// Permission replaces the declared permission when set (same app only).
	Permission string
	StepUp     bool
	TwoPerson  bool
	// HeldAs admits the admin by this permission of ANOTHER app instead of
	// Permission (routes declaring AdmitsHeldAs only). Two-person approval then
	// requires a second holder of HeldAs.
	HeldAs string
	// Audit adds fields to the request's audit payload.
	Audit map[string]any
}

// DecisionError refuses a request from Decide with a client error.
type DecisionError struct {
	Status  int
	Code    string
	Message string
}

func (e *DecisionError) Error() string { return e.Code + ": " + e.Message }

func badRequest(code, msg string) error {
	return &DecisionError{Status: http.StatusBadRequest, Code: code, Message: msg}
}

func (r Requirement) app() string {
	if r.App != "" {
		return r.App
	}
	return adminauth.AppOf(r.Permission)
}

func (r Requirement) validate() error {
	if r.Operation == "" {
		return errors.New("no operation")
	}
	switch r.Access {
	case AccessPermission:
		if !adminauth.ValidPermission(r.Permission) {
			return fmt.Errorf("permission %q is not <app>:<resource>.<action>", r.Permission)
		}
	case AccessAnyAdmin, AccessSelfService, AccessRetired:
		if r.Permission != "" || r.TwoPerson || r.MayTwoPerson || r.Decide != nil {
			return errors.New("only permission routes may carry a permission, a decision or be two-person")
		}
		if r.Access == AccessAnyAdmin && r.App == "" {
			return errors.New("an any-admin route needs an audit app")
		}
	default:
		return errors.New("unknown access")
	}
	if r.AllowWithoutMFA && r.Access != AccessAnyAdmin {
		return errors.New("only an any-admin route may skip MFA")
	}
	if r.MayTwoPerson && r.Decide == nil {
		return errors.New("may-two-person needs a decision")
	}
	if r.AdmitsHeldAs && (r.Decide == nil || r.Access != AccessPermission) {
		return errors.New("admits-held-as needs a permission route with a decision")
	}
	return nil
}

// AdminRoutePrefix is the namespace every route must be declared under.
const AdminRoutePrefix = "/v1/admin"

// Error codes the gate answers with, besides adminauth's MFA and step-up codes.
const (
	CodeActorRequired          = "ACTOR_REQUIRED"
	CodePermissionDenied       = "PERMISSION_DENIED"
	CodePermissionsUnavailable = "PERMISSIONS_UNAVAILABLE"
	CodeAuditUnavailable       = "AUDIT_UNAVAILABLE"
	CodeRetired                = "RETIRED_USE_APP_ROUTES"
	CodeDecisionUnavailable    = "DECISION_UNAVAILABLE"
)

// Gate enforces declarations and writes the audit trail.
type Gate struct {
	perms      adminauth.PermissionSource
	audit      auditRecorder
	requireMFA bool
	now        func() time.Time
	declared   map[string]Requirement
}

// NewGate builds a gate. requireMFA is ADMIN_REQUIRE_MFA.
func NewGate(perms adminauth.PermissionSource, audit auditRecorder, requireMFA bool) *Gate {
	return &Gate{perms: perms, audit: audit, requireMFA: requireMFA, now: time.Now, declared: map[string]Requirement{}}
}

func routeKey(method, path string) string { return method + " " + path }

// Handle declares and registers one admin route. A bad declaration panics at
// registration, which is boot.
func (g *Gate) Handle(r gin.IRoutes, method, path string, req Requirement, h gin.HandlerFunc) {
	if !strings.HasPrefix(path, AdminRoutePrefix+"/") {
		panic("admin gate: " + path + " is outside " + AdminRoutePrefix)
	}
	if err := req.validate(); err != nil {
		panic(fmt.Sprintf("admin gate: %s %s: %v", method, path, err))
	}
	k := routeKey(method, path)
	if _, dup := g.declared[k]; dup {
		panic("admin gate: declared twice: " + k)
	}
	g.declared[k] = req
	switch req.Access {
	case AccessRetired:
		r.Handle(method, path, retired)
	case AccessSelfService:
		r.Handle(method, path, h)
	default:
		r.Handle(method, path, g.enforce(req), h)
	}
}

// Requirement returns the declaration for a registered route.
func (g *Gate) Requirement(method, path string) (Requirement, bool) {
	req, ok := g.declared[routeKey(method, path)]
	return req, ok
}

// VerifyDeclared refuses a router carrying any /v1/admin route that did not go
// through Handle. main exits on the error.
func (g *Gate) VerifyDeclared(r *gin.Engine) error {
	var missing []string
	for _, ri := range r.Routes() {
		if ri.Path != AdminRoutePrefix && !strings.HasPrefix(ri.Path, AdminRoutePrefix+"/") {
			continue
		}
		if _, ok := g.declared[routeKey(ri.Method, ri.Path)]; !ok {
			missing = append(missing, routeKey(ri.Method, ri.Path))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("admin routes without a permission declaration: %s", strings.Join(missing, ", "))
	}
	return nil
}

func retired(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, CodeRetired,
		"This platform-wide action is retired. Use the owning application's admin routes under /v1/admin/<app>/ (Wave 2).", nil)
}

// auditInfo is what the handler adds to the gate's one audit row.
type auditInfo struct {
	actor      string
	app        string
	operation  string
	targetType string
	targetID   string
	reason     string
	outcome    string // overrides the status-derived outcome
	statusCode *int   // overrides the response status (0 = downstream never answered)
	payload    map[string]any
}

func (a *auditInfo) set(key string, v any) {
	if a.payload == nil {
		a.payload = map[string]any{}
	}
	a.payload[key] = v
}

const (
	ctxAudit     = "admin.audit"
	ctxPerms     = "admin.permissions"
	ctxActor     = "admin.actor"
	ctxEffective = "admin.requirement"
	ctxBody      = "admin.body"
	ctxHeldAs    = "admin.held_as"
)

// heldAsFrom is the other-app permission the gate admitted this request by,
// or "" when the admin holds the route's own permission.
func heldAsFrom(c *gin.Context) string { return c.GetString(ctxHeldAs) }

// effectiveRequirement is the declaration as the gate enforced it for this
// request, after Decide. Handlers mint the product token with its Permission
// and submit two-person work when its TwoPerson is set.
func effectiveRequirement(c *gin.Context) (Requirement, bool) {
	v, ok := c.Get(ctxEffective)
	if !ok {
		return Requirement{}, false
	}
	r, ok := v.(Requirement)
	return r, ok
}

// maxAdminBody bounds what admin-service reads from the console.
const maxAdminBody = 1 << 20

var errBodyTooLarge = errors.New("request body too large")

// requestBody reads the request body once and keeps it for later readers
// (Decide, then the handler).
func requestBody(c *gin.Context) ([]byte, error) {
	if v, ok := c.Get(ctxBody); ok {
		return v.([]byte), nil
	}
	var b []byte
	if c.Request.Body != nil {
		var err error
		b, err = io.ReadAll(io.LimitReader(c.Request.Body, maxAdminBody+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxAdminBody {
			return nil, errBodyTooLarge
		}
	}
	c.Set(ctxBody, b)
	c.Request.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

func auditFrom(c *gin.Context) *auditInfo {
	if v, ok := c.Get(ctxAudit); ok {
		return v.(*auditInfo)
	}
	// Outside the gate (unit tests of a bare handler): a throwaway record.
	return &auditInfo{}
}

func permsFrom(c *gin.Context) adminauth.Permissions {
	if v, ok := c.Get(ctxPerms); ok {
		return v.(adminauth.Permissions)
	}
	return adminauth.Permissions{}
}

func actorFrom(c *gin.Context) string { return c.GetString(ctxActor) }

func (g *Gate) enforce(declared Requirement) gin.HandlerFunc {
	return func(c *gin.Context) {
		// A per-request copy: a decision narrows THIS request only, never the
		// declaration later requests start from.
		req := declared
		ctx := c.Request.Context()
		if g.audit == nil {
			slog.ErrorContext(ctx, "admin audit recorder not configured; refusing", "operation", req.Operation)
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeAuditUnavailable, "Admin actions are unavailable", nil)
			c.Abort()
			return
		}
		id, err := uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			// No actor to attribute a row to; the gateway always sets one for a
			// signed-in caller, so this is an unauthenticated or forged request.
			slog.WarnContext(ctx, "admin route called without a valid actor", "operation", req.Operation, "path", c.FullPath())
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeActorRequired,
				"A valid X-User-Id identifying the acting admin is required", nil)
			c.Abort()
			return
		}
		actor := id.String()
		info := &auditInfo{actor: actor, app: req.app(), operation: req.Operation, targetType: "route", targetID: c.Request.URL.Path}
		c.Set(ctxAudit, info)
		c.Set(ctxActor, actor)

		if g.requireMFA && !req.AllowWithoutMFA && !adminauth.MFAVerified(c.GetHeader(adminauth.HeaderAdminMFA)) {
			g.deny(c, info, http.StatusForbidden, adminauth.CodeMFARequired, "Two-factor authentication is required for admin actions")
			return
		}

		perms, err := g.perms.UserPermissions(ctx, actor)
		if err != nil {
			slog.ErrorContext(ctx, "admin permissions unavailable; refusing", "error", err, "operation", req.Operation)
			info.set("error", "permissions unavailable")
			g.deny(c, info, http.StatusServiceUnavailable, CodePermissionsUnavailable, "Permissions could not be resolved")
			return
		}
		c.Set(ctxPerms, perms)

		// The request's own decision comes first, so the permission, step-up
		// and two-person checks below judge what was actually asked for.
		heldAs := ""
		if req.Decide != nil {
			d, err := req.Decide(c, perms)
			if err != nil {
				var de *DecisionError
				if errors.As(err, &de) {
					g.deny(c, info, de.Status, de.Code, de.Message)
					return
				}
				slog.ErrorContext(ctx, "admin decision failed; refusing", "error", err, "operation", req.Operation)
				info.set("error", err.Error())
				g.deny(c, info, http.StatusServiceUnavailable, CodeDecisionUnavailable, "The rule for this action could not be applied")
				return
			}
			for k, v := range d.Audit {
				info.set(k, v)
			}
			if d.Permission != "" {
				if adminauth.AppOf(d.Permission) != adminauth.AppOf(req.Permission) || !adminauth.ValidPermission(d.Permission) {
					slog.ErrorContext(ctx, "admin decision crossed apps; refusing", "declared", req.Permission, "decided", d.Permission)
					g.deny(c, info, http.StatusInternalServerError, "INTERNAL_ERROR", "Invalid route decision")
					return
				}
				req.Permission = d.Permission
			}
			if d.HeldAs != "" {
				if !req.AdmitsHeldAs || !adminauth.ValidPermission(d.HeldAs) ||
					adminauth.AppOf(d.HeldAs) == adminauth.AppOf(req.Permission) || adminauth.AppOf(d.HeldAs) == "platform" {
					slog.ErrorContext(ctx, "admin decision admitted by an undeclared permission; refusing", "declared", req.Permission, "held_as", d.HeldAs)
					g.deny(c, info, http.StatusInternalServerError, "INTERNAL_ERROR", "Invalid route decision")
					return
				}
				heldAs = d.HeldAs
			}
			req.StepUp = req.StepUp || d.StepUp
			req.TwoPerson = req.TwoPerson || d.TwoPerson
		}

		if req.Access == AccessPermission {
			held := perms.Has(req.Permission)
			if !held && heldAs != "" && perms.Has(heldAs) {
				held = true
				info.set("held_as", heldAs)
				c.Set(ctxHeldAs, heldAs)
			}
			if !held {
				info.set("required_permission", req.Permission)
				g.deny(c, info, http.StatusForbidden, CodePermissionDenied, "Missing permission "+req.Permission)
				return
			}
		}
		c.Set(ctxEffective, req)
		if req.StepUp {
			if _, ok := adminauth.StepUpValidUntil(c.GetHeader(adminauth.HeaderStepUpAt), g.now()); !ok {
				g.deny(c, info, http.StatusForbidden, adminauth.CodeStepUpRequired, "A fresh two-factor check is required for this action")
				return
			}
		}

		c.Next()
		g.record(c, info)
	}
}

// deny answers, audits the refusal, and stops the chain.
func (g *Gate) deny(c *gin.Context, info *auditInfo, status int, code, message string) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, message, nil)
	c.Abort()
	info.outcome = postgres.AuditOutcomeDenied
	info.set("code", code)
	g.record(c, info)
}

// record appends the request's one audit row. A lost row cannot undo what
// already happened, so it is logged loudly instead.
func (g *Gate) record(c *gin.Context, info *auditInfo) {
	status := c.Writer.Status()
	if info.statusCode != nil {
		status = *info.statusCode
	}
	outcome := info.outcome
	if outcome == "" {
		outcome = postgres.AuditOutcomeFailure
		if status >= 200 && status < 300 {
			outcome = postgres.AuditOutcomeSuccess
		}
	}
	entry := postgres.AdminAuditEntry{
		Actor:      info.actor,
		App:        info.app,
		Operation:  info.operation,
		TargetType: info.targetType,
		TargetID:   info.targetID,
		Reason:     info.reason,
		RequestID:  requestIDFrom(c),
		Outcome:    outcome,
		StatusCode: status,
		Payload:    info.payload,
	}
	// Detached from the request context: a client hanging up must not cost the row.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	if err := g.audit.RecordAdminWrite(ctx, entry); err != nil {
		slog.ErrorContext(ctx, "admin audit write failed", "error", err,
			"actor", entry.Actor, "app", entry.App, "operation", entry.Operation,
			"target_type", entry.TargetType, "target_id", entry.TargetID,
			"request_id", entry.RequestID, "outcome", entry.Outcome, "status_code", entry.StatusCode)
	}
}

// VerifyExecutors refuses a two-person route whose operation has no executor:
// its first call would create an approval nobody could ever run.
func (g *Gate) VerifyExecutors(has func(app, operation string) bool) error {
	var missing []string
	for k, req := range g.declared {
		if req.TwoPerson && !has(req.app(), req.Operation) {
			missing = append(missing, k)
		}
		if req.MayTwoPerson && !has(req.app(), req.Operation) {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("two-person routes without an executor: %s", strings.Join(missing, ", "))
	}
	return nil
}
