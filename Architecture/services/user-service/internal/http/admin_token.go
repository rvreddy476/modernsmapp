// Business pages moderation on behalf of a human admin, arriving from
// admin-service (admin console, Wave 2 — Content apps: business pages). Same
// pattern as food-service and dating-service.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls user-service with a service token it
// signs for THIS call:
//
//	iss   admin-service
//	aud   social
//	scope [the one permission admin-service checked, e.g. social:pages.suspend]
//	act   the admin's user id
//	exp   60 s
//
// user-service trusts none of that because of where the request came from.
// X-User-Id is no evidence (user-service's /v1 routes trust it on the direct
// port) and neither is the internal key. The token is: only admin-service
// holds the private key, the scope names what it checked, and the actor is
// signed.
//
// The legacy routes (/v1/pages/:id/{approve,reject,suspend,disable} and
// document review) keep the PAGES_ADMIN_USER_IDS allowlist unchanged and never
// look at a token. Both paths write page_admin_audit in the decision's
// transaction (store/page_admin.go).
package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/user-service/internal/pages"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AudienceSocial is the audience admin-service tokens for user-service carry.
const AudienceSocial = "social"

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// InternalAdminPrefix is the token-only admin family. It is registered outside
// the /internal key group, and the gateway refuses any path with an
// `internal` segment from the edge.
const InternalAdminPrefix = "/v1/users/internal/admin"

// Business pages permissions. Names identity's catalogue already has
// (identity-platform/services/auth-service/internal/permissions) are reused;
// the ones marked NEW are not in the catalogue yet.
const (
	PermStatsRead       = "social:stats.read" // NEW
	PermPagesModerate   = "social:pages.moderate"
	PermPagesSuspend    = "social:pages.suspend"    // NEW
	PermPagesDisable    = "social:pages.disable"    // NEW
	PermDocumentsReview = "social:documents.review" // NEW
)

// AdminPermissions lists every permission an admin-service token may carry to
// user-service. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermStatsRead, PermPagesModerate, PermPagesSuspend, PermPagesDisable, PermDocumentsReview,
}

// Error codes for the token path.
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const (
	ctxAdminTokenPerms = "user_admin_token_perms"
	ctxAdminActor      = "user_admin_actor"
)

// pageAdminStore is the slice of the store page decisions need, so both
// admin paths can be exercised without Postgres.
type pageAdminStore interface {
	GetBusinessPageByID(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*store.BusinessPage, error)
	GetBusinessPageByHandle(ctx context.Context, handle string, viewerID *uuid.UUID) (*store.BusinessPage, error)
	ListPageDocuments(ctx context.Context, pageID uuid.UUID) ([]store.PageDocument, error)
	AdminListPendingPages(ctx context.Context, limit, offset int) ([]store.BusinessPage, error)
	AdminSetPageStatus(ctx context.Context, d store.PageStatusDecision) error
	AdminDecidePageDocument(ctx context.Context, d store.PageDocumentDecision) error
	PageAdminStats(ctx context.Context) (*store.PageAdminStats, error)
}

// AdminServiceCallersFromEnv builds the verifier for the admin family from the
// same SERVICE_CALLERS registry as ServiceCallersFromEnv, for audience
// "social":
//
//	SERVICE_CALLERS=admin-service
//	SERVICE_CALLER_ADMIN_SERVICE_KID=a1
//	SERVICE_CALLER_ADMIN_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_ADMIN_SERVICE_OPS=social:stats.read,social:pages.moderate,...
//
// Blank SERVICE_CALLERS → (nil, nil): the admin family answers 401.
func AdminServiceCallersFromEnv(getenv func(string) string) (*servicetoken.Verifier, error) {
	return serviceCallersForAudience(getenv, AudienceSocial)
}

// WithAdminServiceAuth installs the admin-family verifier. Nil admits none.
func (h *Handler) WithAdminServiceAuth(v *servicetoken.Verifier) *Handler {
	h.adminVerifier = v
	return h
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// tokenActor is the signed act claim of an admitted admin-service token.
func tokenActor(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxAdminActor)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// requireAdminToken admits ONLY an admin-service token. perms[0] is the
// route's permission; further perms are ones the handler narrows to per
// action. No token → 401, whatever else the request carries.
func (h *Handler) requireAdminToken(perms ...string) gin.HandlerFunc {
	if len(perms) == 0 {
		panic("user-service: requireAdminToken needs the route's permission")
	}
	return func(c *gin.Context) {
		if rawServiceToken(c) == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired,
				"an admin-service token is required", nil)
			c.Abort()
			return
		}
		if !h.authorizeAdminToken(c, perms) {
			c.Abort()
			return
		}
		c.Next()
	}
}

// authorizeAdminToken verifies the request's token for one of perms and, on
// success, records the admitted permissions and the signed actor.
//
// Refused (403 unless noted): no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens; a caller other
// than admin-service; a token whose scope holds none of perms; and a missing
// or malformed act claim. X-User-Id, X-Scopes and the internal key riding on
// the same request are ignored.
func (h *Handler) authorizeAdminToken(c *gin.Context, perms []string) bool {
	ctx := c.Request.Context()
	if h.adminVerifier == nil || h.adminVerifier.Callers() == 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
			"service tokens are not accepted by this deployment", nil)
		return false
	}
	raw := rawServiceToken(c)
	granted := map[string]bool{}
	var verified *servicetoken.Verified
	for _, perm := range perms {
		v, err := h.adminVerifier.Verify(raw, perm, "")
		if err == nil {
			granted[perm] = true
			verified = v
			continue
		}
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			continue
		}
		slog.WarnContext(ctx, "user-service: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified == nil {
		slog.WarnContext(ctx, "user-service: admin token lacks the route permission", "required", perms[0], "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this route requires", gin.H{"required": perms[0]})
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "user-service: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	actor, err := uuid.Parse(verified.Actor)
	if err != nil || actor == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
			"the token does not name the acting admin", nil)
		return false
	}
	c.Set(ctxAdminTokenPerms, granted)
	c.Set(ctxAdminActor, actor)
	return true
}

// adminMay reports whether the admitted token carries perm, answering 403
// when not.
func adminMay(c *gin.Context, perm string) bool {
	v, _ := c.Get(ctxAdminTokenPerms)
	if granted, _ := v.(map[string]bool); granted[perm] {
		return true
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
		"the token does not carry the permission this action requires", gin.H{"required": perm})
	return false
}

// ApprovePermission is the permission approving a page in status from needs:
// reinstating a suspended page reverses a suspension, so it needs suspend;
// approving a submission needs moderate.
func ApprovePermission(from string) string {
	if from == pages.StatusSuspended {
		return PermPagesSuspend
	}
	return PermPagesModerate
}

// registerAdminTokenRoutes declares the token-only family with the permission
// each route needs.
func (h *Handler) registerAdminTokenRoutes(g *gin.RouterGroup) {
	gate := h.requireAdminToken
	g.GET("/stats", gate(PermStatsRead), h.AdminPageStats)

	g.GET("/pages/pending", gate(PermPagesModerate), h.AdminListPendingPages)
	g.GET("/pages/:id", gate(PermPagesModerate), h.AdminGetPage)
	g.POST("/pages/:id/approve", gate(PermPagesModerate, PermPagesSuspend), h.AdminTokenApprovePage)
	g.POST("/pages/:id/reject", gate(PermPagesModerate), h.AdminTokenRejectPage)
	g.POST("/pages/:id/suspend", gate(PermPagesSuspend), h.AdminTokenSuspendPage)
	g.POST("/pages/:id/disable", gate(PermPagesDisable), h.AdminTokenDisablePage)

	// Documents can be identity proofs (KYC): the URLs are served only here.
	g.GET("/pages/:id/documents", gate(PermDocumentsReview), h.AdminListPageDocuments)
	g.POST("/pages/:id/documents/:docId/approve", gate(PermDocumentsReview), h.AdminTokenApproveDocument)
	g.POST("/pages/:id/documents/:docId/reject", gate(PermDocumentsReview), h.AdminTokenRejectDocument)
}

// --- shared decision bodies (token and legacy paths) ---

func requestReason(c *gin.Context) string {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	return strings.TrimSpace(body.Reason)
}

// loadAdminPage resolves a page by id or handle through pageAdmin, answering
// 404/500 itself. uuidOnly refuses handles (token path).
func (h *Handler) loadAdminPage(c *gin.Context, ref string, uuidOnly bool) (*store.BusinessPage, bool) {
	ctx := c.Request.Context()
	var p *store.BusinessPage
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		p, err = h.pageAdmin.GetBusinessPageByID(ctx, id, nil)
	} else if uuidOnly {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "page id must be a uuid", nil)
		return nil, false
	} else {
		p, err = h.pageAdmin.GetBusinessPageByHandle(ctx, ref, nil)
	}
	if err != nil {
		if strings.Contains(err.Error(), "no rows") || errors.Is(err, store.ErrPageNotFound) {
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
			return nil, false
		}
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return nil, false
	}
	return p, true
}

// decidePage applies one page decision with its audit row and writes the
// response.
func (h *Handler) decidePage(c *gin.Context, p *store.BusinessPage, action, to string, actor uuid.UUID, via, reason string) {
	ctx := c.Request.Context()
	if !pages.CanTransition(p.Status, to) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "conflict", "Illegal status transition", nil)
		return
	}
	err := h.pageAdmin.AdminSetPageStatus(ctx, store.PageStatusDecision{
		PageID: p.ID, From: p.Status, To: to, Action: action,
		Actor: actor, Via: via, Reason: reason, RequestID: c.GetHeader("X-Request-Id"),
	})
	switch {
	case err == nil:
		api.JSON(c.Writer, http.StatusOK, map[string]string{"status": to}, nil)
	case errors.Is(err, store.ErrPageNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "not_found", "Page not found", nil)
	case errors.Is(err, store.ErrPageStatusChanged), errors.Is(err, store.ErrIllegalPageTransition):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "conflict", "Page status changed; reload and retry", nil)
	default:
		slog.ErrorContext(ctx, "user-service: page decision", "action", action, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "page decision failed", nil)
	}
}

// decideDocument applies one document decision with its audit row.
func (h *Handler) decideDocument(c *gin.Context, pageID, docID uuid.UUID, approve bool, actor uuid.UUID, via, reason string) {
	ctx := c.Request.Context()
	status, action := "approved", store.DocumentActionApprove
	if !approve {
		status, action = "rejected", store.DocumentActionReject
	}
	err := h.pageAdmin.AdminDecidePageDocument(ctx, store.PageDocumentDecision{
		DocumentID: docID, PageID: pageID, Status: status, Action: action,
		Actor: actor, Via: via, Reason: reason, RequestID: c.GetHeader("X-Request-Id"),
	})
	switch {
	case err == nil:
		api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
	case errors.Is(err, store.ErrPageDocumentNotFound), errors.Is(err, store.ErrPageNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "not_found", "Document not found", nil)
	default:
		slog.ErrorContext(ctx, "user-service: document decision", "action", action, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "document decision failed", nil)
	}
}

// --- token family handlers ---

// tokenPageDecision is the body shared by the four token lifecycle routes.
// Every decision on this path needs a reason except approve.
func (h *Handler) tokenPageDecision(c *gin.Context, action, to string) {
	actor, ok := tokenActor(c)
	if !ok { // unreachable behind requireAdminToken; never fall back to a header
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "no acting admin", nil)
		return
	}
	p, ok := h.loadAdminPage(c, c.Param("id"), true)
	if !ok {
		return
	}
	if action == store.PageActionApprove && !adminMay(c, ApprovePermission(p.Status)) {
		return
	}
	reason := requestReason(c)
	if action != store.PageActionApprove && reason == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", "reason is required", nil)
		return
	}
	h.decidePage(c, p, action, to, actor, store.ViaAdminService, reason)
}

// AdminTokenApprovePage — POST .../pages/:id/approve (social:pages.moderate;
// social:pages.suspend when reinstating a suspended page).
func (h *Handler) AdminTokenApprovePage(c *gin.Context) {
	h.tokenPageDecision(c, store.PageActionApprove, pages.StatusApproved)
}

// AdminTokenRejectPage — POST .../pages/:id/reject (social:pages.moderate, reason).
func (h *Handler) AdminTokenRejectPage(c *gin.Context) {
	h.tokenPageDecision(c, store.PageActionReject, pages.StatusRejected)
}

// AdminTokenSuspendPage — POST .../pages/:id/suspend (social:pages.suspend, reason).
func (h *Handler) AdminTokenSuspendPage(c *gin.Context) {
	h.tokenPageDecision(c, store.PageActionSuspend, pages.StatusSuspended)
}

// AdminTokenDisablePage — POST .../pages/:id/disable (social:pages.disable, reason; terminal).
func (h *Handler) AdminTokenDisablePage(c *gin.Context) {
	h.tokenPageDecision(c, store.PageActionDisable, pages.StatusDisabled)
}

func (h *Handler) tokenDocumentDecision(c *gin.Context, approve bool) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "no acting admin", nil)
		return
	}
	pageID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "page id must be a uuid", nil)
		return
	}
	docID, err := uuid.Parse(c.Param("docId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "document id must be a uuid", nil)
		return
	}
	reason := requestReason(c)
	if !approve && reason == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "validation_error", "reason is required", nil)
		return
	}
	h.decideDocument(c, pageID, docID, approve, actor, store.ViaAdminService, reason)
}

// AdminTokenApproveDocument — POST .../pages/:id/documents/:docId/approve.
func (h *Handler) AdminTokenApproveDocument(c *gin.Context) { h.tokenDocumentDecision(c, true) }

// AdminTokenRejectDocument — POST .../pages/:id/documents/:docId/reject (reason).
func (h *Handler) AdminTokenRejectDocument(c *gin.Context) { h.tokenDocumentDecision(c, false) }

// AdminListPendingPages — GET .../pages/pending?limit=&offset= (social:pages.moderate).
func (h *Handler) AdminListPendingPages(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	list, err := h.pageAdmin.AdminListPendingPages(c.Request.Context(), limit, offset)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "user-service: pending pages", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "pending pages unavailable", nil)
		return
	}
	if list == nil {
		list = []store.BusinessPage{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": list, "count": len(list)}, nil)
}

// AdminPageDocumentSummary is a document without its URL: the page detail is
// for moderators, and a document can be an identity proof.
type AdminPageDocumentSummary struct {
	ID               uuid.UUID  `json:"id"`
	DocumentType     string     `json:"document_type"`
	Status           string     `json:"status"`
	ReviewedByUserID *uuid.UUID `json:"reviewed_by_user_id,omitempty"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	RejectionReason  string     `json:"rejection_reason,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// AdminGetPage — GET .../pages/:id (social:pages.moderate): the page in any
// status, with document metadata but no document URLs.
func (h *Handler) AdminGetPage(c *gin.Context) {
	p, ok := h.loadAdminPage(c, c.Param("id"), true)
	if !ok {
		return
	}
	docs, err := h.pageAdmin.ListPageDocuments(c.Request.Context(), p.ID)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "user-service: page documents", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "page documents unavailable", nil)
		return
	}
	summaries := make([]AdminPageDocumentSummary, 0, len(docs))
	for _, d := range docs {
		summaries = append(summaries, AdminPageDocumentSummary{
			ID: d.ID, DocumentType: d.DocumentType, Status: d.Status, ReviewedByUserID: d.ReviewedByUserID,
			ReviewedAt: d.ReviewedAt, RejectionReason: d.RejectionReason, CreatedAt: d.CreatedAt,
		})
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"page": p, "documents": summaries}, nil)
}

// AdminListPageDocuments — GET .../pages/:id/documents (social:documents.review):
// documents with their URLs.
func (h *Handler) AdminListPageDocuments(c *gin.Context) {
	p, ok := h.loadAdminPage(c, c.Param("id"), true)
	if !ok {
		return
	}
	docs, err := h.pageAdmin.ListPageDocuments(c.Request.Context(), p.ID)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "user-service: page documents", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "page documents unavailable", nil)
		return
	}
	if docs == nil {
		docs = []store.PageDocument{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": docs, "count": len(docs)}, nil)
}

// AdminPageStats — GET /v1/users/internal/admin/stats (social:stats.read).
func (h *Handler) AdminPageStats(c *gin.Context) {
	st, err := h.pageAdmin.PageAdminStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "user-service: page admin stats", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "USER_ADMIN_STATS_FAILED", "stats unavailable", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"pages": st}, nil)
}
