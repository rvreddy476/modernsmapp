package http

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Audit read permissions: "*:audit.read" reads every app's rows;
// "<app>:audit.read" reads only that app's rows.
const (
	permAuditReadAll  = "*:audit.read"
	auditReadSuffix   = ":audit.read"
	CodeInvalidFilter = "INVALID_FILTER"
)

var auditAppPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// RegisterAuditRoute adds GET /v1/admin/audit, the unified admin trail.
//
//	?app=food&actor=<uuid>&operation=food.order.cancel&outcome=denied
//	&from=2026-09-01T00:00:00Z&to=2026-09-17T00:00:00Z&limit=50&cursor=<next_cursor>
//
// Newest first, keyset-paged by next_cursor. Rows carry actor ids only.
// The route admits any identified admin; the handler restricts the rows to
// the apps the caller may audit and refuses a caller who may audit none.
func (h *Handler) RegisterAuditRoute(r *gin.Engine) {
	h.gate.Handle(r, http.MethodGet, "/v1/admin/audit",
		Requirement{Operation: "audit.trail.read", Access: AccessAnyAdmin, App: "platform"}, h.auditTrail)
}

// auditableApps is nil for a holder of *:audit.read (every app) and otherwise
// the sorted apps the caller holds <app>:audit.read for.
func auditableApps(p adminauth.Permissions) []string {
	if p.Has(permAuditReadAll) {
		return nil
	}
	apps := []string{}
	if p.Has("platform" + auditReadSuffix) {
		apps = append(apps, "platform")
	}
	for app := range p.Apps {
		if p.Has(app + auditReadSuffix) {
			apps = append(apps, app)
		}
	}
	sort.Strings(apps)
	return apps
}

func (h *Handler) auditTrail(c *gin.Context) {
	ctx := c.Request.Context()
	info := auditFrom(c)
	deny := func(status int, code, msg string) {
		info.outcome = postgres.AuditOutcomeDenied
		info.set("code", code)
		api.ErrorWithContext(ctx, c.Writer, status, code, msg, nil)
	}
	bad := func(msg string) { api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidFilter, msg, nil) }

	apps := auditableApps(permsFrom(c))
	if apps != nil && len(apps) == 0 {
		deny(http.StatusForbidden, CodePermissionDenied, "Missing permission *:audit.read or <app>:audit.read")
		return
	}

	f := postgres.AuditTrailFilter{Apps: apps, Cursor: c.Query("cursor")}
	if app := c.Query("app"); app != "" {
		if !auditAppPattern.MatchString(app) {
			bad("app is not an application id")
			return
		}
		if apps != nil && !contains(apps, app) {
			deny(http.StatusForbidden, CodePermissionDenied, "Missing permission "+app+auditReadSuffix)
			return
		}
		f.App = app
		info.set("app", app)
	}
	if actor := c.Query("actor"); actor != "" {
		id, err := uuid.Parse(actor)
		if err != nil {
			bad("actor must be a user id")
			return
		}
		f.Actor = id.String()
		info.set("actor", f.Actor)
	}
	if op := c.Query("operation"); op != "" {
		if len(op) > 120 {
			bad("operation is too long")
			return
		}
		f.Operation = op
		info.set("operation", op)
	}
	if outcome := c.Query("outcome"); outcome != "" {
		switch outcome {
		case postgres.AuditOutcomeSuccess, postgres.AuditOutcomeFailure, postgres.AuditOutcomeDenied,
			postgres.AuditOutcomePending, postgres.AuditOutcomeRejected:
		default:
			bad("outcome must be success, failure, denied, pending or rejected")
			return
		}
		f.Outcome = outcome
		info.set("outcome", outcome)
	}
	for key, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := c.Query(key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				bad(key + " must be an RFC 3339 time")
				return
			}
			*dst = &t
		}
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			bad("limit must be a positive number")
			return
		}
		f.Limit = n
	}
	if apps != nil {
		info.set("restricted_to", apps)
	}

	page, err := h.svc.ListAuditTrail(ctx, f)
	if errors.Is(err, postgres.ErrInvalidAuditCursor) {
		bad("cursor is not valid")
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "audit trail read failed", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load the audit trail", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
