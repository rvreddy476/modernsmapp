package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/identity-profile-service/internal/officialverify"
	"github.com/atpost/identity-shared/api"
	"github.com/atpost/identity-shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The admin console's Official and Government marks family
// (docs/designs/official-marks-console-contract.md section 2).
//
// admin-service is the console's one backend. For every call it has already
// checked the admin's permission against identity, required MFA and, for
// writes, a fresh step-up, run the two-person rule for Government
// invitations, and written its own audit row. It then calls THIS family with
// a token it signs for the call:
//
//	iss   admin-service
//	aud   profile
//	scope [the one permission it checked, e.g. platform:official_marks.review]
//	act   the acting admin's user id
//	exp   <= 60 s
//
// in X-Service-Authorization: Bearer <token>. The token is the only evidence:
// the family is registered outside the internal-key gate (the gateway stamps
// that key on edge traffic, so it proves nothing), a gateway identity header
// is refused outright, and the actor is the SIGNED act claim. The token's
// scope is the authority; there is no admin-role read here any more. A jti is
// required (the verifier refuses a token without one) but not single-use:
// reads repeat, and every write is idempotent by state (a decided
// application is not in review; a revoked invitation is not pending).
//
// Outside /v1/profiles, so the edge never forwards it; it also carries an
// "internal" segment the gateway refuses from the edge. Not registered
// without ADMIN_SERVICE_TOKEN_PUBKEY: every path under the prefix is 404.
//
//	GET  /marks/stats                                              official_marks.read
//	GET  /marks/organisation/applications?status=&limit=&cursor=   official_marks.read
//	GET  /marks/organisation/applications/:applicationId           official_marks.read
//	GET  /marks/organisation/applications/:applicationId/document  official_marks.review
//	POST /marks/organisation/applications/:applicationId/review    official_marks.review
//	GET  /marks/government/invitations?status=&limit=&cursor=      official_marks.read
//	POST /marks/government/invitations                             government_marks.invite
//	POST /marks/government/invitations/:invitationId/revoke        government_marks.invite

// AdminMarksPrefix is the console family's prefix.
const AdminMarksPrefix = "/internal/v1/profiles/admin"

// Token contract.
const (
	// ServiceAuthHeader carries the admin-service token.
	ServiceAuthHeader = "X-Service-Authorization"
	// IssuerAdminService is the only caller whose tokens reach this family.
	IssuerAdminService = "admin-service"
	// AudienceProfile is the audience admin-service must mint for.
	AudienceProfile = "profile"
	// DefaultAdminServiceKID is the kid when ADMIN_SERVICE_TOKEN_KID is unset.
	DefaultAdminServiceKID = "a1"
)

// Permissions, spelled as identity's catalogue spells them
// (auth-service internal/permissions, app platform).
const (
	PermOfficialMarksRead     = "platform:official_marks.read"
	PermOfficialMarksReview   = "platform:official_marks.review"
	PermGovernmentMarksInvite = "platform:government_marks.invite"
)

// AdminServicePermissions is every operation admin-service may name in a
// token to this service; the verifier registers it with exactly these.
var AdminServicePermissions = []string{PermOfficialMarksRead, PermOfficialMarksReview, PermGovernmentMarksInvite}

// Error codes for the token path (shared vocabulary with auth-service
// admin_console.go and dating admin_token.go).
const (
	CodeAdminTokenRequired   = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeServiceTokenRejected = "SERVICE_TOKEN_REJECTED"
	CodeAdminPermissionScope = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired   = "ADMIN_ACTOR_REQUIRED"
)

const ctxAdminActor = "profile_admin_actor"

// AdminServiceVerifier builds the family's verifier from the public key
// (base64) and kid. No key → (nil, nil): the family is not registered. An
// unreadable key is a configuration error; the service must not boot with
// the console silently disabled.
func AdminServiceVerifier(pubB64, kid string) (*servicetoken.Verifier, error) {
	pubB64, kid = strings.TrimSpace(pubB64), strings.TrimSpace(kid)
	if pubB64 == "" {
		return nil, nil
	}
	if kid == "" {
		kid = DefaultAdminServiceKID
	}
	v := servicetoken.NewVerifier(AudienceProfile)
	if err := v.RegisterBase64(IssuerAdminService, kid, pubB64, AdminServicePermissions); err != nil {
		return nil, errors.New("ADMIN_SERVICE_TOKEN_PUBKEY is not a valid Ed25519 public key")
	}
	return v, nil
}

// WithAdminTokenVerifier installs the console family's verifier; nil leaves
// the family unregistered.
func (h *Handler) WithAdminTokenVerifier(v *servicetoken.Verifier) *Handler {
	h.adminTokens = v
	return h
}

func (h *Handler) registerAdminMarksRoutes(r *gin.Engine) {
	if h.adminTokens == nil || h.adminTokens.Callers() == 0 || h.official == nil {
		// Not registered. A real 404 here, rather than the engine's no-route
		// answer, which runs behind the internal-key gate and would say 401.
		r.Any(AdminMarksPrefix+"/*path", func(c *gin.Context) {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Not found", nil, nil)
		})
		return
	}
	g := r.Group(AdminMarksPrefix + "/marks")
	read := h.requireAdminMarksToken(PermOfficialMarksRead)
	review := h.requireAdminMarksToken(PermOfficialMarksReview)
	invite := h.requireAdminMarksToken(PermGovernmentMarksInvite)
	g.GET("/stats", read, h.AdminMarksStats)
	g.GET("/organisation/applications", read, h.AdminListApplications)
	g.GET("/organisation/applications/:applicationId", read, h.AdminGetApplication)
	g.GET("/organisation/applications/:applicationId/document", review, h.AdminApplicationDocument)
	g.POST("/organisation/applications/:applicationId/review", review, h.AdminReviewApplication)
	g.GET("/government/invitations", read, h.AdminListInvitations)
	g.POST("/government/invitations", invite, h.AdminCreateInvitation)
	g.POST("/government/invitations/:invitationId/revoke", invite, h.AdminRevokeInvitation)
}

// requireAdminMarksToken admits ONLY an admin-service token whose scope holds
// perm. In order:
//
//  1. Any gateway identity header (X-User-Id, X-Verified-User-Id, X-Scopes,
//     X-Admin-Role) → 403 USER_CALLER_REFUSED, whatever else it carries.
//  2. No token → 401 ADMIN_SERVICE_TOKEN_REQUIRED.
//  3. Bad signature, unknown caller or kid, wrong audience, expired,
//     over-long, no jti → 403 SERVICE_TOKEN_REJECTED; scope without perm →
//     403 ADMIN_PERMISSION_NOT_IN_TOKEN; act missing or not a uuid → 403
//     ADMIN_ACTOR_REQUIRED.
//
// On success the signed actor is on the context.
func (h *Handler) requireAdminMarksToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("profile: requireAdminMarksToken needs the route's permission")
	}
	return func(c *gin.Context) {
		for _, name := range gatewayIdentityHeaders {
			for _, v := range c.Request.Header.Values(name) {
				if strings.TrimSpace(v) != "" {
					api.Error(c.Writer, http.StatusForbidden, CodeUserCallerRefused,
						"service-only endpoint; user requests are not accepted", nil, nil)
					c.Abort()
					return
				}
			}
		}
		raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.GetHeader(ServiceAuthHeader)), "Bearer "))
		if raw == "" {
			api.Error(c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil, nil)
			c.Abort()
			return
		}
		log := h.logger().With("path", c.FullPath(), "request_id", RequestIDFromContext(c))
		verified, err := h.adminTokens.Verify(raw, perm)
		switch {
		case errors.Is(err, servicetoken.ErrScopeDenied):
			log.Warn("admin-service token lacks the route permission", "required", perm)
			api.Error(c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
				"the token does not carry the permission this route requires", map[string]any{"required": perm}, nil)
			c.Abort()
			return
		case err != nil:
			log.Warn("admin-service token refused", "reason", err.Error())
			api.Error(c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil, nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerAdminService {
			log.Warn("admin route called by a non-admin service", "issuer", verified.Issuer)
			api.Error(c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil, nil)
			c.Abort()
			return
		}
		actor, err := uuid.Parse(verified.Actor)
		if err != nil || actor == uuid.Nil {
			api.Error(c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil, nil)
			c.Abort()
			return
		}
		c.Set(ctxAdminActor, actor)
		c.Next()
	}
}

// adminActor is the signed act claim the guard put on the context. A handler
// reached without the guard has no actor and refuses (fail closed).
func adminActor(c *gin.Context) (uuid.UUID, bool) {
	v, _ := c.Get(ctxAdminActor)
	a, ok := v.(uuid.UUID)
	if !ok || a == uuid.Nil {
		api.Error(c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil, nil)
		return uuid.Nil, false
	}
	return a, true
}

func pathUUID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil || id == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid "+name, nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func listQuery(c *gin.Context) (officialverify.ListQuery, bool) {
	q := officialverify.ListQuery{Status: c.Query("status"), Cursor: c.Query("cursor")}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > officialverify.MaxPageSize {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be 1 to 50", nil, nil)
			return q, false
		}
		q.Limit = n
	}
	return q, true
}

// AdminMarksStats — GET .../marks/stats.
func (h *Handler) AdminMarksStats(c *gin.Context) {
	if _, ok := adminActor(c); !ok {
		return
	}
	out, err := h.official.Stats(c.Request.Context())
	h.writeOfficial(c, http.StatusOK, out, err)
}

// AdminListApplications — GET .../marks/organisation/applications.
func (h *Handler) AdminListApplications(c *gin.Context) {
	if _, ok := adminActor(c); !ok {
		return
	}
	q, ok := listQuery(c)
	if !ok {
		return
	}
	out, err := h.official.ListApplications(c.Request.Context(), q)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// AdminGetApplication — GET .../marks/organisation/applications/:applicationId.
func (h *Handler) AdminGetApplication(c *gin.Context) {
	if _, ok := adminActor(c); !ok {
		return
	}
	id, ok := pathUUID(c, "applicationId")
	if !ok {
		return
	}
	out, err := h.official.GetApplication(c.Request.Context(), id)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// AdminApplicationDocument — GET .../applications/:applicationId/document.
// The media id is for admin-service, which streams the bytes; the console
// never sees it.
func (h *Handler) AdminApplicationDocument(c *gin.Context) {
	actor, ok := adminActor(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "applicationId")
	if !ok {
		return
	}
	mediaID, err := h.official.ApplicationDocument(c.Request.Context(), id)
	if err == nil {
		h.logger().Info("official mark document requested", "application_id", id, "actor", actor)
		api.JSON(c.Writer, http.StatusOK, gin.H{"document_media_id": mediaID}, nil)
		return
	}
	h.writeOfficial(c, http.StatusOK, nil, err)
}

// AdminReviewApplication — POST .../applications/:applicationId/review.
func (h *Handler) AdminReviewApplication(c *gin.Context) {
	actor, ok := adminActor(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "applicationId")
	if !ok {
		return
	}
	var d officialverify.ReviewDecision
	if err := c.ShouldBindJSON(&d); err != nil {
		h.writeBindError(c, err)
		return
	}
	d.ReviewerID = actor
	h.logger().Info("official mark review requested", "application_id", id, "actor", actor, "decision", d.Decision)
	out, err := h.official.ReviewOrganisation(c.Request.Context(), id, d)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// AdminListInvitations — GET .../marks/government/invitations.
func (h *Handler) AdminListInvitations(c *gin.Context) {
	if _, ok := adminActor(c); !ok {
		return
	}
	q, ok := listQuery(c)
	if !ok {
		return
	}
	out, err := h.official.ListInvitations(c.Request.Context(), q)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// AdminCreateInvitation — POST .../marks/government/invitations. The
// approvers are requested_by (body) and the signed actor.
func (h *Handler) AdminCreateInvitation(c *gin.Context) {
	actor, ok := adminActor(c)
	if !ok {
		return
	}
	var req officialverify.GovInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeBindError(c, err)
		return
	}
	if req.UserID == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "user_id is required", nil, nil)
		return
	}
	req.ApproverID = actor
	h.logger().Info("government invitation requested", "user_id", req.UserID, "actor", actor, "requested_by", req.RequestedBy)
	out, err := h.official.CreateGovInvitation(c.Request.Context(), req)
	h.writeOfficial(c, http.StatusCreated, out, err)
}

// AdminRevokeInvitation — POST .../government/invitations/:invitationId/revoke.
func (h *Handler) AdminRevokeInvitation(c *gin.Context) {
	actor, ok := adminActor(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "invitationId")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeBindError(c, err)
		return
	}
	out, err := h.official.RevokeInvitation(c.Request.Context(), id, actor, req.Reason)
	h.writeOfficial(c, http.StatusOK, out, err)
}
