package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
)

// Error codes shared by the product routes.
const (
	CodeInvalidBody            = "INVALID_BODY"
	CodeIdempotencyKeyRequired = "IDEMPOTENCY_KEY_REQUIRED"
	CodeInvalidPathParam       = "INVALID_PATH_PARAM"
)

// productRoute is one declared product admin route: the console path under
// /v1/admin/<app>, the product path it calls, and what the gate requires.
type productRoute struct {
	method     string
	path       string // under /v1/admin/<app>, gin syntax
	operation  string
	permission string
	stepUp     bool
	twoPerson  bool
	// upstream is the product path relative to its admin prefix, with the same
	// :params; empty means the same as path.
	upstream string
	// alternatives lists every permission the product admits for a read. The
	// gate scopes the token to the first one the admin holds (permission is
	// alternatives[0]).
	alternatives []string
	// targetType names the audit target; the route's last :param is the id.
	targetType string
	// statusOnly answers with the product status and no body (console routes
	// that always answered that way keep doing so).
	statusOnly bool
	// idempotent routes need an Idempotency-Key from the console, forwarded.
	idempotent bool
	// decide and mayTwoPerson feed Requirement.Decide / MayTwoPerson.
	decide       func(*gin.Context, adminauth.Permissions) (Decision, error)
	mayTwoPerson bool
}

func (rt productRoute) requirement() Requirement {
	req := Requirement{
		Operation: rt.operation, Permission: rt.permission, StepUp: rt.stepUp, TwoPerson: rt.twoPerson,
		Decide: rt.decide, MayTwoPerson: rt.mayTwoPerson,
	}
	if len(rt.alternatives) > 0 {
		if rt.decide != nil || rt.permission != rt.alternatives[0] {
			panic("admin product route " + rt.operation + ": alternatives need permission = alternatives[0] and no decide")
		}
		req.Decide = heldAlternative(rt.alternatives)
	}
	return req
}

// heldAlternative scopes a multi-permission read to the first permission the
// admin holds; holding none leaves the declared one, which the gate refuses.
func heldAlternative(alts []string) func(*gin.Context, adminauth.Permissions) (Decision, error) {
	return func(_ *gin.Context, p adminauth.Permissions) (Decision, error) {
		for _, a := range alts {
			if p.Has(a) {
				return Decision{Permission: a}, nil
			}
		}
		return Decision{}, nil
	}
}

// product is one product's wiring for registration.
type product struct {
	app    string // audit app and URL segment owner, e.g. "food"
	label  string // "Feast"
	prefix string // "/v1/admin/food"
	client *service.ProductClient
}

// registerProduct declares every route; special supplies handlers by
// operation, everything else is forwarded as-is by forwardProduct.
func (h *Handler) registerProduct(r *gin.Engine, p product, routes []productRoute, special map[string]gin.HandlerFunc) {
	for _, rt := range routes {
		if adminauth.AppOf(rt.permission) != p.app {
			panic("admin " + p.app + ": " + rt.operation + " carries another app's permission " + rt.permission)
		}
		hf, ok := special[rt.operation]
		if !ok {
			hf = h.forwardProduct(p, rt)
		}
		h.gate.Handle(r, rt.method, p.prefix+rt.path, rt.requirement(), withAuditTarget(rt, hf))
	}
}

// withAuditTarget points the audit row at the route's target (its type and
// the value of its last :param) before any handler runs.
func withAuditTarget(rt productRoute, hf gin.HandlerFunc) gin.HandlerFunc {
	if rt.targetType == "" {
		return hf
	}
	param := ""
	for _, s := range strings.Split(rt.path, "/") {
		if strings.HasPrefix(s, ":") {
			param = s[1:]
		}
	}
	return func(c *gin.Context) {
		info := auditFrom(c)
		info.targetType = rt.targetType
		if param != "" {
			info.targetID = c.Param(param)
		}
		hf(c)
	}
}

// productPath fills the upstream template's :params from the request. A
// parameter that is empty or could climb out of its segment is refused.
func productPath(c *gin.Context, template string) (path, lastParam string, err error) {
	segs := strings.Split(template, "/")
	for i, s := range segs {
		if !strings.HasPrefix(s, ":") {
			continue
		}
		v := c.Param(s[1:])
		if v == "" || v == "." || strings.Contains(v, "..") || strings.ContainsAny(v, "/\\?#%") {
			return "", "", errInvalidID
		}
		segs[i] = url.PathEscape(v)
		lastParam = v
	}
	return strings.Join(segs, "/"), lastParam, nil
}

// jsonBody returns the console body (validated as JSON when present) and its
// top-level object fields, if it is an object.
func jsonBody(c *gin.Context) ([]byte, map[string]any, error) {
	b, err := requestBody(c)
	if err != nil {
		return nil, nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, map[string]any{}, nil
	}
	if !json.Valid(b) {
		return nil, nil, errors.New("body is not JSON")
	}
	fields := map[string]any{}
	_ = json.Unmarshal(b, &fields)
	return b, fields, nil
}

func stringField(fields map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := fields[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// forwardProduct calls the product with the console's body and query as sent:
// the product validates them, and the token's one scope bounds what they can do.
func (h *Handler) forwardProduct(p product, rt productRoute) gin.HandlerFunc {
	upstream := rt.upstream
	if upstream == "" {
		upstream = rt.path
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		path, last, err := productPath(c, upstream)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		info := auditFrom(c)
		if rt.targetType != "" {
			info.targetType, info.targetID = rt.targetType, last
		}
		pr := service.ProductRequest{Method: rt.method, Path: path, RawQuery: c.Request.URL.RawQuery}
		if rt.method != http.MethodGet {
			raw, fields, err := jsonBody(c)
			if err != nil {
				api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON", nil)
				return
			}
			pr.RawBody = raw
			info.reason = stringField(fields, "reason", "note", "resolution_notes")
		}
		key, ok := idempotencyKey(c, rt.idempotent)
		if !ok {
			return
		}
		pr.IdempotencyKey = key
		h.productCall(c, p, pr, rt.statusOnly)
	}
}

// idempotencyKey reads the console's Idempotency-Key; when required and
// missing (or unreasonably long) it answers 400 and returns ok=false.
func idempotencyKey(c *gin.Context, required bool) (string, bool) {
	key := strings.TrimSpace(c.GetHeader(service.IdempotencyKeyHeader))
	if len(key) > 255 || (required && key == "") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeIdempotencyKeyRequired,
			"An Idempotency-Key header (at most 255 characters) is required for this action", nil)
		return "", false
	}
	return key, true
}

// productCall runs one product call as the gate's admin with the gate's
// effective permission as the token scope, and writes the answer.
func (h *Handler) productCall(c *gin.Context, p product, pr service.ProductRequest, statusOnly bool) {
	info := auditFrom(c)
	req, ok := effectiveRequirement(c)
	if !ok || req.Permission == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
		return
	}
	pr.Permission, pr.Actor = req.Permission, actorFrom(c)
	resp, err := p.client.Do(productContext(c), pr)
	writeProduct(c, info, p.label, resp, err, statusOnly)
}

// productContext carries the request id to the product.
func productContext(c *gin.Context) context.Context {
	base := c.Request.Context()
	if rid := requestIDFrom(c); rid != "" && trace.RequestIDFrom(base) == "" {
		return trace.WithRequestID(base, rid)
	}
	return base
}

// writeProduct answers with a product's result and records it on the audit row.
//
// A redirect (the Feast settlement file lives in object storage) is not
// followed: the console receives 200 {"download_url": <location>} and fetches
// the file itself. The presigned URL is a credential, so the audit row records
// only that a link was issued.
func writeProduct(c *gin.Context, info *auditInfo, label string, resp service.ProductResponse, err error, statusOnly bool) {
	ctx := c.Request.Context()
	if errors.Is(err, service.ErrProductUnavailable) {
		zero := 0
		info.statusCode = &zero
		info.set("error", "service token key not configured")
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeProductUnavailable,
			label+" admin actions are not configured on this deployment", nil)
		return
	}
	if err != nil {
		writeUpstream(c, info, nil, resp.Status, err)
		return
	}
	if resp.Status >= 300 && resp.Status < 400 {
		info.set("upstream_status", resp.Status)
		u, perr := url.Parse(resp.Location)
		if perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			info.outcome = "failure"
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, "UPSTREAM_ERROR", "The owning service answered with an unusable redirect", nil)
			return
		}
		info.set("download", "link_issued")
		api.JSON(c.Writer, http.StatusOK, gin.H{"download_url": u.String()}, nil)
		return
	}
	if statusOnly || len(resp.Body) == 0 {
		c.Status(resp.Status)
		c.Writer.WriteHeaderNow()
		return
	}
	ct := resp.ContentType
	if ct == "" || strings.HasPrefix(ct, "application/json") {
		c.Data(resp.Status, "application/json", resp.Body)
		return
	}
	if resp.ContentDisposition != "" {
		c.Header("Content-Disposition", resp.ContentDisposition)
	}
	c.Data(resp.Status, ct, resp.Body)
}
