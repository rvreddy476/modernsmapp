package http

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Sound library permissions, exactly as media-service checks them on its
// token-only admin family (/v1/media/internal/admin/sounds/library, audience
// "media") and as identity's catalogue grants them: admin only (superadmin,
// a platform-wide admin, or an admin scoped to media).
const (
	permSoundsRead   = "media:sounds.read"
	permSoundsManage = "media:sounds.manage"
)

const (
	soundsAuditApp = "media"
	soundsPrefix   = "/v1/admin/sounds/library"
	soundTarget    = "library_sound"

	opSoundsList     = "media.sounds.list"
	opSoundsRead     = "media.sounds.read"
	opSoundsPreview  = "media.sounds.preview"
	opSoundsUpload   = "media.sounds.upload"
	opSoundsCreate   = "media.sounds.create"
	opSoundsUpdate   = "media.sounds.update"
	opSoundsWithdraw = "media.sounds.withdraw"

	soundWithdrawReasonMin = 3
	soundWithdrawReasonMax = 500
)

// SoundRoutes is the route table under /v1/admin/sounds/library, one for one
// with media-service's admin sound routes. Reads need media:sounds.read,
// every write media:sounds.manage. Withdrawing a sound everywhere silences
// posts that already use it, so that one needs a fresh step-up (decided from
// the body); a withdraw always needs a reason (3 to 500 characters).
var SoundRoutes = []productRoute{
	{method: http.MethodGet, path: "", operation: opSoundsList, permission: permSoundsRead},
	{method: http.MethodGet, path: "/:soundId", operation: opSoundsRead, permission: permSoundsRead, targetType: soundTarget},
	{method: http.MethodGet, path: "/:soundId/preview", operation: opSoundsPreview, permission: permSoundsRead, targetType: soundTarget},
	{method: http.MethodPost, path: "/uploads", operation: opSoundsUpload, permission: permSoundsManage},
	{method: http.MethodPost, path: "", operation: opSoundsCreate, permission: permSoundsManage, targetType: soundTarget},
	{method: http.MethodPatch, path: "/:soundId", operation: opSoundsUpdate, permission: permSoundsManage, targetType: soundTarget},
	{method: http.MethodPost, path: "/:soundId/withdraw", operation: opSoundsWithdraw, permission: permSoundsManage, targetType: soundTarget},
}

// WithSounds installs the media-service sound library client.
func (h *Handler) WithSounds(sc *service.ProductClient) *Handler {
	h.sounds = sc
	return h
}

// RegisterSoundRoutes adds the Sound library page under /v1/admin/sounds/library.
//
//	audit     every request; writes carry the reason when given, the
//	          created sound's id on create, the fields an edit sent, the
//	          withdraw scope
//	step-up   withdraw with scope everywhere
//	preview   307 to media-service's short-lived signed URL; the audit row
//	          records only that a link was issued
func (h *Handler) RegisterSoundRoutes(r *gin.Engine) {
	p := product{app: soundsAuditApp, label: "Sound library", prefix: soundsPrefix, client: h.sounds}
	routes := make([]productRoute, len(SoundRoutes))
	copy(routes, SoundRoutes)
	for i := range routes {
		switch routes[i].operation {
		case opSoundsUpload:
			routes[i].decide = soundUploadDecision
		case opSoundsCreate:
			routes[i].decide = soundCreateDecision
		case opSoundsUpdate:
			routes[i].decide = soundUpdateDecision
		case opSoundsWithdraw:
			routes[i].decide = soundWithdrawDecision
		}
	}
	h.registerProduct(r, p, routes, map[string]gin.HandlerFunc{
		opSoundsPreview: h.previewSound(p),
		opSoundsCreate:  h.createSound(p),
	})
}

// soundObject is the console body as a JSON object, or a 400.
func soundObject(c *gin.Context) (map[string]any, error) {
	raw, fields, err := jsonBody(c)
	if err != nil || len(raw) == 0 {
		return nil, badRequest(CodeInvalidBody, "The request body must be a JSON object")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, badRequest(CodeInvalidBody, "The request body must be a JSON object")
	}
	return fields, nil
}

func soundUploadDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	fields, err := soundObject(c)
	if err != nil {
		return Decision{}, err
	}
	return Decision{Audit: map[string]any{"content_type": fields["content_type"], "size_bytes": fields["size_bytes"]}}, nil
}

func soundCreateDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	fields, err := soundObject(c)
	if err != nil {
		return Decision{}, err
	}
	audit := map[string]any{"license_type": fields["license_type"]}
	if v, ok := fields["is_active"]; ok {
		audit["is_active"] = v
	}
	return Decision{Audit: audit}, nil
}

// soundUpdateDecision records which fields an edit sent (and an enable or
// disable as such); media-service validates them.
func soundUpdateDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	fields, err := soundObject(c)
	if err != nil {
		return Decision{}, err
	}
	changed := make([]string, 0, len(fields))
	for k := range fields {
		if k != "reason" {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	audit := map[string]any{"fields": changed}
	if v, ok := fields["is_active"]; ok {
		audit["is_active"] = v
	}
	return Decision{Audit: audit}, nil
}

// soundWithdrawDecision refuses a withdraw without a known scope or a 3 to
// 500 character reason before any product call, and requires a step-up for
// scope everywhere (it silences posts already using the sound).
func soundWithdrawDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	fields, err := soundObject(c)
	if err != nil {
		return Decision{}, err
	}
	scope, _ := fields["scope"].(string)
	if scope != "new_uses" && scope != "everywhere" {
		return Decision{}, badRequest(CodeInvalidBody, "scope must be new_uses or everywhere")
	}
	reason, _ := fields["reason"].(string)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Decision{}, badRequest(CodeReasonRequired, "reason is required")
	}
	if n := utf8.RuneCountInString(reason); n < soundWithdrawReasonMin || n > soundWithdrawReasonMax {
		return Decision{}, badRequest(CodeInvalidBody, "reason must be 3 to 500 characters")
	}
	return Decision{StepUp: scope == "everywhere", Audit: map[string]any{"scope": scope}}, nil
}

// createSound forwards the create and points the audit row at the new
// sound's id.
func (h *Handler) createSound(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		info := auditFrom(c)
		raw, fields, err := jsonBody(c)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON", nil)
			return
		}
		info.reason = stringField(fields, "reason")
		req, ok := effectiveRequirement(c)
		if !ok {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
			return
		}
		resp, err := p.client.Do(productContext(c), service.ProductRequest{
			Method: http.MethodPost, Path: "", RawBody: raw, Permission: req.Permission, Actor: actorFrom(c),
		})
		if err == nil && resp.Status == http.StatusCreated {
			var env struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(resp.Body, &env) == nil && env.Data.ID != "" {
				info.targetID = env.Data.ID
			}
		}
		writeProduct(c, info, p.label, resp, err, false)
	}
}

// previewSound answers media-service's redirect to the signed preview URL
// as a 307 of its own (an <audio> element follows it). The URL is a
// credential: the audit row records only that a link was issued.
func (h *Handler) previewSound(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private")
		info := auditFrom(c)
		path, _, err := productPath(c, "/:soundId/preview")
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		req, ok := effectiveRequirement(c)
		if !ok {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
			return
		}
		resp, err := p.client.Do(productContext(c), service.ProductRequest{
			Method: http.MethodGet, Path: path, Permission: req.Permission, Actor: actorFrom(c),
		})
		if err != nil || resp.Status < 300 || resp.Status >= 400 {
			writeProduct(c, info, p.label, resp, err, false)
			return
		}
		u, perr := url.Parse(resp.Location)
		if perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			upstreamError(c, info, resp.Status, "The sound library answered with an unusable preview link")
			return
		}
		info.set("preview", "link_issued")
		info.outcome = postgres.AuditOutcomeSuccess
		c.Redirect(http.StatusTemporaryRedirect, u.String())
	}
}
