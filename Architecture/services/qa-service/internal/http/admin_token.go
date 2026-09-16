// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Content apps, Q&A). Same pattern as food and
// dating.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls qa with a service token it signs
// for THIS call:
//
//	iss   admin-service
//	aud   qa
//	scope [the one permission admin-service checked, e.g. qa:answers.moderate]
//	act   the admin's user id
//	exp   60 s
//
// qa trusts none of that because of where the request came from. The
// internal key is no evidence (the gateway stamps it on edge traffic to
// /v1/qa) and neither is X-User-ID (anyone holding the key can set one).
// The token is: only admin-service holds the private key, the scope names
// what it checked, and the actor is signed.
//
// The LEGACY /v1/qa/admin/* routes (QA_MODERATOR_USER_IDS + X-User-ID) are
// untouched and do not accept tokens.
package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Q&A admin permissions. Names that identity's catalogue already has
// (identity-platform/services/auth-service/internal/permissions) are reused;
// the ones marked NEW are not in the catalogue yet.
const (
	PermStatsRead         = "qa:stats.read"   // NEW
	PermReportsRead       = "qa:reports.read" // NEW
	PermReportsAct        = "qa:reports.act"
	PermQuestionsModerate = "qa:questions.moderate"
	PermQuestionsMerge    = "qa:questions.merge" // NEW: irreversible, narrower than hide/lock
	PermAnswersModerate   = "qa:answers.moderate"
	PermCommentsModerate  = "qa:comments.moderate" // NEW
	PermAuditRead         = "qa:audit.read"
)

// AdminPermissions lists every permission an admin-service token may carry
// to qa. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermStatsRead, PermReportsRead, PermReportsAct, PermQuestionsModerate, PermQuestionsMerge,
	PermAnswersModerate, PermCommentsModerate, PermAuditRead,
}

// Error codes for the token path.
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const ctxAdminActor = "qa_admin_actor"

// InternalAdminPrefix is the token-only admin family. It is registered
// outside the internal-key group; the gateway refuses /internal/ from the edge.
const InternalAdminPrefix = "/v1/qa/internal/admin"

// AdminStore is what the token path needs from the store (*store.Store).
type AdminStore interface {
	ListReports(ctx context.Context, status string, limit, offset int) ([]store.ModerationReport, error)
	GetReport(ctx context.Context, reportID uuid.UUID) (*store.ModerationReport, error)
	AdminDecideReport(ctx context.Context, actor, reportID uuid.UUID, status, reason string) (*store.ModerationAction, error)
	AdminHideContent(ctx context.Context, actor uuid.UUID, targetType string, targetID uuid.UUID, reportID *uuid.UUID, reason string) (*store.ModerationAction, error)
	AdminLockQuestion(ctx context.Context, actor, questionID uuid.UUID, reportID *uuid.UUID, reason string) (*store.ModerationAction, error)
	AdminMergeQuestion(ctx context.Context, actor, questionID, intoID uuid.UUID, reportID *uuid.UUID, reason string) (*store.ModerationAction, error)
	AdminMarkDuplicate(ctx context.Context, actor, questionID, duplicateOfID uuid.UUID, reportID *uuid.UUID, reason string) (*store.ModerationAction, error)
	ListModerationActionsFiltered(ctx context.Context, targetID, actorID *uuid.UUID, actionType string, limit, offset int) ([]store.ModerationAction, error)
	AdminStats(ctx context.Context) (*store.AdminStats, error)
}

// WithServiceAuth installs the service-token verifier (ServiceCallersFromEnv).
// nil means no token is accepted.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

// WithAdminStore replaces the token path's store (tests).
func (h *Handler) WithAdminStore(s AdminStore) *Handler {
	h.adminStore = s
	return h
}

func (h *Handler) admin() AdminStore {
	if h.adminStore != nil {
		return h.adminStore
	}
	return h.svc.Store()
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// adminActor is the signed act claim of the admitted token. Handlers on the
// token path take the actor ONLY from here, never from a header.
func adminActor(c *gin.Context) uuid.UUID {
	v, _ := c.Get(ctxAdminActor)
	id, _ := v.(uuid.UUID)
	return id
}

// requireAdminToken admits ONLY an admin-service token carrying perm with a
// valid act claim. No token → 401, whatever else the request carries.
//
// Refused (403 unless noted): no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens; a caller other
// than admin-service; a token whose scope lacks perm; and a missing, malformed
// or nil act claim. X-User-ID, X-Scopes and the internal key riding on the
// same request are ignored.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("qa: requireAdminToken needs the route's permission")
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		raw := rawServiceToken(c)
		if raw == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired,
				"an admin-service token is required", nil)
			c.Abort()
			return
		}
		if h.verifier == nil || h.verifier.Callers() == 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"service tokens are not accepted by this deployment", nil)
			c.Abort()
			return
		}
		verified, err := h.verifier.Verify(raw, perm, "")
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			slog.WarnContext(ctx, "qa: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
				"the token does not carry the permission this route requires", gin.H{"required": perm})
			c.Abort()
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "qa: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerAdminService {
			slog.WarnContext(ctx, "qa: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		actor, err := uuid.Parse(verified.Actor)
		if err != nil || actor == uuid.Nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
				"the token does not name the acting admin", nil)
			c.Abort()
			return
		}
		c.Set(ctxAdminActor, actor)
		c.Next()
	}
}

// AdminRoute is one row of the token family's route table.
type AdminRoute struct {
	Method, Path, Permission string
	// StepUp: admin-service must require a fresh 2FA step-up before calling.
	StepUp bool
	// TwoPerson: admin-service must require a second approver.
	TwoPerson bool
}

// AdminRoutes is the token family's route table: permission and the gates
// admin-service enforces before it signs the call. qa does not see step-up or
// approvals; it records them here so the two sides can be checked against
// one list.
var AdminRoutes = []AdminRoute{
	{Method: http.MethodGet, Path: "/stats", Permission: PermStatsRead},
	{Method: http.MethodGet, Path: "/reports", Permission: PermReportsRead},
	{Method: http.MethodGet, Path: "/reports/:reportId", Permission: PermReportsRead},
	{Method: http.MethodPost, Path: "/reports/:reportId/resolve", Permission: PermReportsAct},
	{Method: http.MethodPost, Path: "/reports/:reportId/dismiss", Permission: PermReportsAct},
	{Method: http.MethodPost, Path: "/questions/:questionId/hide", Permission: PermQuestionsModerate},
	{Method: http.MethodPost, Path: "/questions/:questionId/lock", Permission: PermQuestionsModerate},
	{Method: http.MethodPost, Path: "/questions/:questionId/duplicate", Permission: PermQuestionsModerate},
	{Method: http.MethodPost, Path: "/questions/:questionId/merge", Permission: PermQuestionsMerge, StepUp: true},
	{Method: http.MethodPost, Path: "/answers/:answerId/hide", Permission: PermAnswersModerate},
	{Method: http.MethodPost, Path: "/comments/:commentId/hide", Permission: PermCommentsModerate},
	{Method: http.MethodGet, Path: "/actions", Permission: PermAuditRead},
}

// registerAdminTokenRoutes mounts the token-only family on the engine,
// outside the internal-key group.
func (h *Handler) registerAdminTokenRoutes(r *gin.Engine) {
	handlers := map[string]gin.HandlerFunc{
		"GET /stats":                            h.AdminStats,
		"GET /reports":                          h.AdminListReports,
		"GET /reports/:reportId":                h.AdminGetReport,
		"POST /reports/:reportId/resolve":       h.adminDecideReport("resolved"),
		"POST /reports/:reportId/dismiss":       h.adminDecideReport("dismissed"),
		"POST /questions/:questionId/hide":      h.adminHide("question", "questionId"),
		"POST /questions/:questionId/lock":      h.AdminLockQuestion,
		"POST /questions/:questionId/duplicate": h.AdminMarkDuplicate,
		"POST /questions/:questionId/merge":     h.AdminMergeQuestion,
		"POST /answers/:answerId/hide":          h.adminHide("answer", "answerId"),
		"POST /comments/:commentId/hide":        h.adminHide("comment", "commentId"),
		"GET /actions":                          h.AdminListActions,
	}
	g := r.Group(InternalAdminPrefix)
	for _, rt := range AdminRoutes {
		fn, ok := handlers[rt.Method+" "+rt.Path]
		if !ok {
			panic("qa: admin route without a handler: " + rt.Method + " " + rt.Path)
		}
		g.Handle(rt.Method, rt.Path, h.requireAdminToken(rt.Permission), fn)
	}
	if len(handlers) != len(AdminRoutes) {
		panic("qa: admin handler without a route table entry")
	}
}

// --- handlers ---

// adminWriteBody is the common body of a token-path write. reason is required:
// it is the audit reason the console collected.
type adminWriteBody struct {
	Reason        string `json:"reason"`
	ReportID      string `json:"report_id"`
	MergeIntoID   string `json:"merge_into_id"`
	DuplicateOfID string `json:"duplicate_of_id"`
}

func bindAdminWrite(c *gin.Context) (*adminWriteBody, *uuid.UUID, bool) {
	var body adminWriteBody
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "body must be JSON", nil)
		return nil, nil, false
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REASON_REQUIRED", "reason is required", nil)
		return nil, nil, false
	}
	if len(body.Reason) > 2000 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "reason is too long", nil)
		return nil, nil, false
	}
	var reportID *uuid.UUID
	if strings.TrimSpace(body.ReportID) != "" {
		id, ok := parseUUIDString(c, body.ReportID, "report_id")
		if !ok {
			return nil, nil, false
		}
		reportID = &id
	}
	return &body, reportID, true
}

func respondAdminWrite(c *gin.Context, action *store.ModerationAction, err error) {
	ctx := c.Request.Context()
	switch {
	case err == nil:
		api.JSONWithContext(ctx, c.Writer, http.StatusOK, action)
	case errors.Is(err, store.ErrAdminNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, store.ErrAdminConflict):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "CONFLICT", err.Error(), nil)
	case errors.Is(err, store.ErrAdminInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	default:
		slog.ErrorContext(ctx, "qa: admin write failed", "path", c.Request.URL.Path, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "QA_ADMIN_WRITE_FAILED", "the action could not be applied", nil)
	}
}

func (h *Handler) adminDecideReport(status string) gin.HandlerFunc {
	return func(c *gin.Context) {
		reportID, ok := parseUUID(c, "reportId")
		if !ok {
			return
		}
		body, _, ok := bindAdminWrite(c)
		if !ok {
			return
		}
		action, err := h.admin().AdminDecideReport(c.Request.Context(), adminActor(c), reportID, status, body.Reason)
		respondAdminWrite(c, action, err)
	}
}

func (h *Handler) adminHide(targetType, param string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseUUID(c, param)
		if !ok {
			return
		}
		body, reportID, ok := bindAdminWrite(c)
		if !ok {
			return
		}
		action, err := h.admin().AdminHideContent(c.Request.Context(), adminActor(c), targetType, id, reportID, body.Reason)
		respondAdminWrite(c, action, err)
	}
}

// AdminLockQuestion — POST .../questions/:questionId/lock (qa:questions.moderate).
func (h *Handler) AdminLockQuestion(c *gin.Context) {
	id, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}
	body, reportID, ok := bindAdminWrite(c)
	if !ok {
		return
	}
	action, err := h.admin().AdminLockQuestion(c.Request.Context(), adminActor(c), id, reportID, body.Reason)
	respondAdminWrite(c, action, err)
}

// AdminMergeQuestion — POST .../questions/:questionId/merge (qa:questions.merge).
func (h *Handler) AdminMergeQuestion(c *gin.Context) {
	id, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}
	body, reportID, ok := bindAdminWrite(c)
	if !ok {
		return
	}
	into, ok := parseUUIDString(c, body.MergeIntoID, "merge_into_id")
	if !ok {
		return
	}
	action, err := h.admin().AdminMergeQuestion(c.Request.Context(), adminActor(c), id, into, reportID, body.Reason)
	respondAdminWrite(c, action, err)
}

// AdminMarkDuplicate — POST .../questions/:questionId/duplicate (qa:questions.moderate).
func (h *Handler) AdminMarkDuplicate(c *gin.Context) {
	id, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}
	body, reportID, ok := bindAdminWrite(c)
	if !ok {
		return
	}
	dup, ok := parseUUIDString(c, body.DuplicateOfID, "duplicate_of_id")
	if !ok {
		return
	}
	action, err := h.admin().AdminMarkDuplicate(c.Request.Context(), adminActor(c), id, dup, reportID, body.Reason)
	respondAdminWrite(c, action, err)
}

// AdminListReports — GET .../reports?status=pending (qa:reports.read).
func (h *Handler) AdminListReports(c *gin.Context) {
	status := c.DefaultQuery("status", "pending")
	switch status {
	case "pending", "reviewed", "resolved", "dismissed":
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "unknown status", nil)
		return
	}
	limit, offset := parsePagination(c)
	reports, err := h.admin().ListReports(c.Request.Context(), status, limit, offset)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "qa: admin list reports", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", "reports unavailable", nil)
		return
	}
	if reports == nil {
		reports = []store.ModerationReport{}
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, reports)
}

// AdminGetReport — GET .../reports/:reportId (qa:reports.read).
func (h *Handler) AdminGetReport(c *gin.Context) {
	id, ok := parseUUID(c, "reportId")
	if !ok {
		return
	}
	report, err := h.admin().GetReport(c.Request.Context(), id)
	if err != nil || report == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "report not found", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, report)
}

// AdminListActions — GET .../actions?target_id=&actor_id=&action_type= (qa:audit.read).
func (h *Handler) AdminListActions(c *gin.Context) {
	optional := func(name string) (*uuid.UUID, bool) {
		raw := strings.TrimSpace(c.Query(name))
		if raw == "" {
			return nil, true
		}
		id, ok := parseUUIDString(c, raw, name)
		return &id, ok
	}
	targetID, ok := optional("target_id")
	if !ok {
		return
	}
	actorID, ok := optional("actor_id")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	actions, err := h.admin().ListModerationActionsFiltered(c.Request.Context(), targetID, actorID, strings.TrimSpace(c.Query("action_type")), limit, offset)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "qa: admin list actions", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", "history unavailable", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, actions)
}

// AdminStats — GET .../stats (qa:stats.read). Read-only dashboard counts.
func (h *Handler) AdminStats(c *gin.Context) {
	stats, err := h.admin().AdminStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "qa: admin stats", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QA_ADMIN_STATS_FAILED", "stats unavailable", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, stats)
}
