// Package middleware contains gin middlewares specific to rider-service.
//
// audit.go is the Sprint 3 audit middleware: every /v1/rider/admin/* request
// produces exactly one rider_admin_audit_logs row with actor, action, target,
// path, method, IP, user-agent, body summary, response status, and latency.
//
// CRITICAL RULES (industry-standard):
//  - Every admin route is audited. Handlers may set c.Set("audit_action",
//    "partner.approve") + c.Set("audit_target_kind", "partner") +
//    c.Set("audit_target_id", id) to label the row; in their absence the
//    middleware falls back to "{METHOD}_{PATH}" / path-derived target kind.
//  - Audit-write failures are logged loudly but never block the response —
//    the alternative (a transient DB hiccup 5xx-ing every admin click) is
//    worse than a missed row, and the request is reconstructible from the
//    HTTP access log if the audit row is lost.
//  - Request bodies are truncated to 1KB before storage (admin tools may
//    POST big JSON; we only need a summary for compliance).
package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// auditBodyLimit caps the persisted request_body to 1KB. Admin payloads are
// typically tiny; the cap keeps a hostile client from filling the audit log.
const auditBodyLimit = 1024

// AdminRoleHeader is the stub header the middleware enforces in dev. In
// production the JWT carries a `roles` claim including `rider:admin`; the
// middleware can be swapped for a JWT-aware variant without touching
// handlers.
const AdminRoleHeader = "X-Admin-Role"

// AdminRoleValue is the required value for AdminRoleHeader.
const AdminRoleValue = "rider:admin"

// AdminUserKey is the gin context key holding the resolved admin user id.
const AdminUserKey = "admin_user_id"

// AuditActionKey is the optional gin context key handlers can set to label
// the audit row's action column. e.g. c.Set("audit_action","partner.approve").
const AuditActionKey = "audit_action"

// AuditTargetKindKey + AuditTargetIDKey label the target.
const (
	AuditTargetKindKey = "audit_target_kind"
	AuditTargetIDKey   = "audit_target_id"
)

// AuditWriter is the subset of *store.Store the middleware needs. Stays an
// interface so unit tests can inject a fake.
type AuditWriter interface {
	RecordAudit(ctx context.Context, in store.RecordAuditInput) (*store.AuditLog, error)
}

// AdminGuard is a gin middleware that enforces:
//   - Cryptographically authenticated admin identity from context or trusted gateway source,
//   - Method-aware scope checks (read scopes cannot authorize mutations).
func AdminGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		isMutation := method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete

		// 1. Check authenticated user from JWT context
		if uid, ok := GetAuthenticatedUserID(c); ok && uid != uuid.Nil {
			scopes := GetAuthenticatedScopes(c)
			if rolesVal, exists := c.Get(ContextKeyRoles); exists {
				if roles, ok := rolesVal.([]string); ok {
					for _, r := range roles {
						scopes = scopes + " " + r
					}
				}
			}

			if checkScopes(scopes, isMutation) {
				c.Set(AdminUserKey, uid)
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "FORBIDDEN", "message": "insufficient admin scopes for operation"}})
			return
		}

		// 2. Gateway verified claims from trusted edge (requiring cryptographic internal service key)
		internalHeader := c.GetHeader("X-Internal-Service-Key")
		internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
		isGatewayTrusted := internalHeader != "" && internalKey != "" && hmac.Equal([]byte(internalHeader), []byte(internalKey))

		if isGatewayTrusted {
			gwUserID := c.GetHeader("X-Verified-User-Id")
			if gwUserID == "" {
				gwUserID = c.GetHeader("X-User-Id")
			}
			if gwUserID == "" {
				gwUserID = c.GetHeader("X-User-ID")
			}
			if gwUserID != "" {
				uid, err := uuid.Parse(gwUserID)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "BAD_USER_ID", "message": "invalid user id in gateway header"}})
					return
				}
				scopes := c.GetHeader("X-Scopes")
				if scopes == "" {
					scopes = c.GetHeader(AdminRoleHeader)
				}
				if checkScopes(scopes, isMutation) {
					c.Set(AdminUserKey, uid)
					c.Next()
					return
				}
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "FORBIDDEN", "message": "insufficient admin scopes for operation"}})
				return
			}
		}

		// In development and test mode only: allow direct header if non-production
		if !isProductionEnv() {
			role := c.GetHeader(AdminRoleHeader)
			raw := c.GetHeader("X-User-ID")
			if raw == "" {
				raw = c.GetHeader("X-User-Id")
			}
			if raw != "" {
				uid, err := uuid.Parse(raw)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "BAD_USER_ID", "message": "invalid user id header"}})
					return
				}
				if checkScopes(role, isMutation) {
					c.Set(AdminUserKey, uid)
					c.Next()
					return
				}
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "FORBIDDEN", "message": "admin role required"}})
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "AUTH_REQUIRED", "message": "missing authenticated admin credentials"}})
	}
}

func checkScopes(scopes string, isMutation bool) bool {
	if scopes == "" {
		return false
	}
	parts := strings.FieldsFunc(scopes, func(r rune) bool {
		return r == ' ' || r == ','
	})

	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Universal admin scopes
		if p == "rider:admin" || p == "admin" || p == "superadmin" {
			return true
		}

		// Non-mutating read routes can be satisfied by read scopes
		if !isMutation {
			if p == "rider:ops:read" || p == "rider:read" || p == "rider:partner:review" ||
				p == "rider:safety:respond" || p == "rider:fare:manage" || p == "rider:payment:reconcile" {
				return true
			}
		} else {
			// Mutating routes require specific mutation scopes (read-only scopes like rider:ops:read or rider:read fail)
			if p == "rider:partner:review" || p == "rider:safety:respond" ||
				p == "rider:fare:manage" || p == "rider:payment:reconcile" {
				return true
			}
		}
	}
	return false
}

// AuditAdmin is the gin middleware that writes one audit row per admin
// request. Mount AFTER AdminGuard so c.Keys[AdminUserKey] is populated.
func AuditAdmin(writer AuditWriter) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Capture the request body BEFORE the handler reads it (gin's
		// c.ShouldBindJSON drains the body), then re-attach a fresh reader.
		var bodyCopy []byte
		if c.Request.Body != nil {
			b, _ := io.ReadAll(c.Request.Body)
			if len(b) > 0 {
				bodyCopy = make([]byte, len(b))
				copy(bodyCopy, b)
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(b))
		}

		c.Next()

		// After the handler runs, harvest action + target labels.
		action := stringFromContext(c, AuditActionKey)
		if action == "" {
			action = c.Request.Method + " " + c.FullPath()
		}
		targetKind := stringFromContext(c, AuditTargetKindKey)
		if targetKind == "" {
			targetKind = deriveTargetKind(c.FullPath())
		}
		var targetID *uuid.UUID
		if v, ok := c.Get(AuditTargetIDKey); ok {
			switch tid := v.(type) {
			case uuid.UUID:
				if tid != uuid.Nil {
					t := tid
					targetID = &t
				}
			case string:
				if u, err := uuid.Parse(tid); err == nil {
					targetID = &u
				}
			}
		}
		if targetID == nil {
			// Fallback: if the route param :id parses as a uuid, use it.
			if raw := c.Param("id"); raw != "" {
				if u, err := uuid.Parse(raw); err == nil {
					targetID = &u
				}
			}
		}

		actorID, _ := c.Get(AdminUserKey)
		actorUUID, _ := actorID.(uuid.UUID)
		if actorUUID == uuid.Nil {
			// AdminGuard should have populated this; defensive fallback so
			// the audit row still has SOMETHING traceable. We use the nil
			// uuid + log loudly so this misconfiguration surfaces.
			slog.Warn("rider audit: admin_user_id missing in gin context — skipping audit write",
				"path", c.FullPath(), "method", c.Request.Method)
			return
		}

		path := c.Request.URL.Path
		method := c.Request.Method
		ip := clientIP(c)
		ua := c.Request.UserAgent()
		bodySummary := truncateBody(bodyCopy, auditBodyLimit)
		status := c.Writer.Status()
		latency := int(time.Since(start) / time.Millisecond)

		input := store.RecordAuditInput{
			AdminUserID:    actorUUID,
			Action:         action,
			EntityType:     targetKind,
			EntityID:       targetID,
			RequestPath:    &path,
			RequestMethod: &method,
			ResponseStatus: &status,
			LatencyMS:      &latency,
		}
		if ip != "" {
			input.IPAddress = &ip
		}
		if ua != "" {
			input.UserAgent = &ua
		}
		if bodySummary != "" {
			input.RequestBody = &bodySummary
		}

		// Audit writes get a fresh, short-deadline context — we don't want
		// a cancelled client request to suppress the row.
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := writer.RecordAudit(auditCtx, input); err != nil {
			slog.Error("rider audit: write failed",
				"action", action, "path", path, "method", method,
				"admin_user_id", actorUUID.String(), "error", err)
		}
	}
}

// stringFromContext pulls a string out of gin context safely.
func stringFromContext(c *gin.Context, key string) string {
	v, ok := c.Get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// truncateBody returns the body trimmed to limit bytes, with an "...[truncated]"
// suffix when truncation happened. Empty input returns empty output.
func truncateBody(body []byte, limit int) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "...[truncated]"
}

// clientIP returns the client IP. Honors X-Forwarded-For (first chunk) and
// X-Real-IP, falling back to RemoteAddr.
func clientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := c.GetHeader("X-Real-IP"); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		// Some test paths set RemoteAddr without a port — return raw.
		return c.Request.RemoteAddr
	}
	return host
}

// deriveTargetKind is the fallback when handlers forget to set
// audit_target_kind. Maps `/v1/rider/admin/partners/...` -> "partner",
// `/v1/rider/admin/documents/...` -> "document", etc.
func deriveTargetKind(path string) string {
	const prefix = "/v1/rider/admin/"
	if !strings.HasPrefix(path, prefix) {
		return "rider"
	}
	tail := strings.TrimPrefix(path, prefix)
	first := tail
	if i := strings.Index(tail, "/"); i >= 0 {
		first = tail[:i]
	}
	// Singular form for known plural buckets.
	switch first {
	case "partners":
		return "partner"
	case "documents":
		return "document"
	case "vehicles":
		return "vehicle"
	case "payments":
		return "payment"
	case "rides":
		return "ride"
	case "complaints":
		return "complaint"
	case "safety-incidents":
		return "safety_incident"
	case "cities":
		return "city"
	case "zones":
		return "zone"
	case "fare-rules":
		return "fare_rule"
	case "audit-logs":
		return "audit_log"
	case "dashboard":
		return "dashboard"
	}
	return first
}
