package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Official and Government marks in the console
// (docs/designs/official-marks-console-contract.md, sections 1 and 3).
//
// Every route forwards to profile-service's token-only console family under
// /internal/v1/profiles/admin/marks, with a token for audience "profile"
// scoped to the route's permission and acting as the admin. Audit app
// "platform".
//
//	audited read  the application detail (GSTIN/CIN, signatory e-mail):
//	              the gate's one row names the application; never cached
//	step-up       review, document view, invitation create and revoke
//	two-person    invitation create, ALWAYS, and never the sole-holder path:
//	              profile records two distinct approvers, so with no second
//	              holder the request is refused (409 SECOND_APPROVER_REQUIRED)
//
// The registration document is viewed, not downloaded: admin-service asks
// profile for the applicant and the document's media id, then streams the
// bytes from media-service's private-evidence route exactly as the Doorstep
// evidence views do (relayDocumentImage: type and size checked, one audit row
// before the first byte, the KYC view headers). The media id never reaches
// the console or the audit row.
//
// The invite code the profile service answers to the approver is relayed to
// that approver once. admin.approvals keeps only the result status and
// outcome (FinishApproval), and the audit row records the approval, never the
// response body, so the code is stored nowhere here.
const (
	permMarksRead   = "platform:official_marks.read"
	permMarksReview = "platform:official_marks.review"
	permGovInvite   = "platform:government_marks.invite"

	// permMediaEvidenceRead is media-service's private-evidence scope.
	permMediaEvidenceRead = "media:partner-evidence.read"

	marksAuditApp = "platform"
	marksPrefix   = "/v1/admin/marks"
	// marksNavApp is the /me navigation entry for the Marks page.
	marksNavApp = "marks"

	opMarksStats        = "marks.stats.read"
	opMarksAppsList     = "marks.applications.list"
	opMarksAppRead      = "marks.application.read"
	opMarksDocumentView = "marks.application.document_view"
	opMarksAppReview    = "marks.application.review"
	opMarksInvitesList  = "marks.invitations.list"
	opMarksInviteCreate = "marks.invitation.create"
	opMarksInviteRevoke = "marks.invitation.revoke"

	marksTargetApplication = "official_application"
	marksTargetInvitation  = "government_invitation"
	marksTargetUser        = "user"

	// marksDocumentKind is the kind sent to media-service's evidence route
	// (any kind but introduction_video is an image read there).
	marksDocumentKind = "registration_document"

	CodeSecondApproverRequired = "SECOND_APPROVER_REQUIRED"
	CodeInvalidOfficialName    = "INVALID_OFFICIAL_NAME"
	CodeApproverIsInvitee      = "APPROVER_IS_INVITEE"
)

// MarksRoutes is the Marks page's route table under /v1/admin/marks; each
// upstream is the same path under profile's /marks.
var MarksRoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", upstream: "/marks/stats", operation: opMarksStats, permission: permMarksRead},
	{method: http.MethodGet, path: "/organisation/applications", upstream: "/marks/organisation/applications", operation: opMarksAppsList, permission: permMarksRead},
	{method: http.MethodGet, path: "/organisation/applications/:applicationId", upstream: "/marks/organisation/applications/:applicationId", operation: opMarksAppRead, permission: permMarksRead, targetType: marksTargetApplication},
	{method: http.MethodGet, path: "/organisation/applications/:applicationId/document/view", operation: opMarksDocumentView, permission: permMarksReview, stepUp: true, targetType: marksTargetApplication},
	{method: http.MethodPost, path: "/organisation/applications/:applicationId/review", upstream: "/marks/organisation/applications/:applicationId/review", operation: opMarksAppReview, permission: permMarksReview, stepUp: true, targetType: marksTargetApplication},
	{method: http.MethodGet, path: "/government/invitations", upstream: "/marks/government/invitations", operation: opMarksInvitesList, permission: permMarksRead},
	{method: http.MethodPost, path: "/government/invitations", operation: opMarksInviteCreate, permission: permGovInvite, stepUp: true, twoPerson: true, targetType: marksTargetUser},
	{method: http.MethodPost, path: "/government/invitations/:invitationId/revoke", upstream: "/marks/government/invitations/:invitationId/revoke", operation: opMarksInviteRevoke, permission: permGovInvite, stepUp: true, targetType: marksTargetInvitation},
}

func init() {
	approvalLabels[opMarksInviteCreate] = "Invite government account"
}

// WithProfile installs the profile-service console client (Official marks).
func (h *Handler) WithProfile(pc *service.ProductClient) *Handler {
	h.profile = pc
	return h
}

// govInviteBody is the console's invitation request.
type govInviteBody struct {
	UserID       string `json:"user_id"`
	OfficialName string `json:"official_name"`
	Reason       string `json:"reason"`
}

// govInvitePayload is the stored two-person request: exactly what the
// executor sends profile. requested_by is the requester; the approver who
// executes it is the token's act.
type govInvitePayload struct {
	UserID       string `json:"user_id"`
	OfficialName string `json:"official_name"`
	Reason       string `json:"reason"`
	RequestedBy  string `json:"requested_by"`
}

// RegisterMarksRoutes adds the Marks page under /v1/admin/marks.
func (h *Handler) RegisterMarksRoutes(r *gin.Engine) {
	p := product{app: marksAuditApp, label: "Official marks", prefix: marksPrefix, client: h.profile}
	routes := make([]productRoute, len(MarksRoutes))
	copy(routes, MarksRoutes)
	special := map[string]gin.HandlerFunc{}
	for i := range routes {
		switch routes[i].operation {
		case opMarksAppReview:
			routes[i].decide = marksReviewDecision
		case opMarksAppRead:
			special[opMarksAppRead] = noStore(h.forwardProduct(p, routes[i]))
		}
	}
	special[opMarksDocumentView] = h.marksDocumentView(p)
	special[opMarksInviteCreate] = h.marksInviteCreate

	h.approvals.Register(marksAuditApp, opMarksInviteCreate, h.marksInviteExecutor)
	h.registerProduct(r, p, routes, special)
}

// noStore marks a PII answer as never cacheable.
func noStore(hf gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private, max-age=0")
		hf(c)
	}
}

// marksReviewDecision only records the decision on the audit row; profile
// validates it (INVALID_DECISION, REASON_REQUIRED).
func marksReviewDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	_, fields, err := jsonBody(c)
	if err != nil {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	d := stringField(fields, "decision")
	if d == "approve" || d == "reject" {
		return Decision{Audit: map[string]any{"decision": d}}, nil
	}
	return Decision{}, nil
}

// marksApplicationID reads :applicationId; it answers 400 when invalid.
func marksApplicationID(c *gin.Context) (string, bool) {
	id, err := uuid.Parse(c.Param("applicationId"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid application id", nil)
		return "", false
	}
	return id.String(), true
}

// marksDocumentView streams an application's registration document:
//
//  1. the applicant, from the detail (scope official_marks.read, which every
//     holder of official_marks.review also holds in the catalogue; an admin
//     without it is refused);
//  2. the document's media id (scope official_marks.review); none → 404;
//  3. the bytes from media-service's private-evidence route, owned by the
//     applicant, through relayDocumentImage (types jpeg/png/webp/pdf, size
//     cap, one audit row before the first byte, the KYC view headers).
func (h *Handler) marksDocumentView(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		info := auditFrom(c)
		id, ok := marksApplicationID(c)
		if !ok {
			return
		}
		info.targetType, info.targetID = marksTargetApplication, id
		notFound := func() {
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeDocumentNotFound, "Document not found", nil)
		}
		if !permsFrom(c).Has(permMarksRead) {
			info.outcome = postgres.AuditOutcomeDenied
			info.set("required_permission", permMarksRead)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodePermissionDenied, "Missing permission "+permMarksRead, nil)
			return
		}
		base := "/marks/organisation/applications/" + id

		// 1. The applicant, who owns the uploaded document.
		var app struct {
			Data struct {
				UserID string `json:"user_id"`
			} `json:"data"`
		}
		if !h.marksRead(c, p, info, base, permMarksRead, nil, &app) {
			return
		}
		owner, err := uuid.Parse(app.Data.UserID)
		if err != nil || owner == uuid.Nil {
			upstreamError(c, info, 0, "The owning service answered with an unreadable application")
			return
		}

		// 2. The document's media id (kept here: never sent on, never audited).
		var doc struct {
			Data struct {
				DocumentMediaID string `json:"document_media_id"`
			} `json:"data"`
		}
		if !h.marksRead(c, p, info, base+"/document", permMarksReview, notFound, &doc) {
			return
		}
		media, err := uuid.Parse(doc.Data.DocumentMediaID)
		if err != nil || media == uuid.Nil {
			upstreamError(c, info, 0, "The owning service answered with an unreadable document reference")
			return
		}

		// 3. The bytes.
		h.relayDocumentImage(c, product{client: h.evidence, label: "Registration document"}, info, service.ProductRequest{
			Method: http.MethodGet, Path: "/" + media.String(),
			Query:      url.Values{"owner_id": {owner.String()}, "kind": {marksDocumentKind}},
			Permission: permMediaEvidenceRead, Actor: actorFrom(c),
		}, kycSellerDocumentTypes, notFound, "official mark document stream ended early", "application_id", id)
	}
}

// marksRead performs one profile GET and decodes a 200 into out. A product
// refusal is relayed as answered (a 404 to notFound when set), a redirect or
// an unreadable 200 is a 502; false means the answer was written.
func (h *Handler) marksRead(c *gin.Context, p product, info *auditInfo, path, permission string, notFound func(), out any) bool {
	resp, err := p.client.Do(productContext(c), service.ProductRequest{
		Method: http.MethodGet, Path: path, Permission: permission, Actor: actorFrom(c),
	})
	switch {
	case err != nil:
		writeProduct(c, info, p.label, resp, err, false)
		return false
	case resp.Status >= 300 && resp.Status < 400:
		upstreamError(c, info, resp.Status, "The owning service answered with a redirect")
		return false
	case resp.Status == http.StatusNotFound && notFound != nil:
		info.set("upstream_status", resp.Status)
		notFound()
		return false
	case resp.Status != http.StatusOK:
		writeProduct(c, info, p.label, resp, nil, false)
		return false
	}
	if json.Unmarshal(resp.Body, out) != nil {
		upstreamError(c, info, resp.Status, "The owning service answered with an unreadable body")
		return false
	}
	return true
}

// marksInviteCreate validates the console's request and stores it for a
// second holder of government_marks.invite. It never executes on this call.
func (h *Handler) marksInviteCreate(c *gin.Context) {
	ctx := c.Request.Context()
	info := auditFrom(c)
	raw, _, err := jsonBody(c)
	var b govInviteBody
	if err == nil && raw != nil {
		err = json.Unmarshal(raw, &b)
	}
	if err != nil || raw == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be a JSON object", nil)
		return
	}
	info.reason = b.Reason
	user, err := uuid.Parse(strings.TrimSpace(b.UserID))
	if err != nil || user == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "user_id must be a user id", nil)
		return
	}
	info.targetType, info.targetID = marksTargetUser, user.String()
	name := strings.TrimSpace(b.OfficialName)
	if name == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidOfficialName, "An official name is required", nil)
		return
	}
	requester := actorFrom(c)
	if requester == user.String() {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeApproverIsInvitee, "You cannot invite your own account", nil)
		return
	}
	h.submitPendingTwoPerson(c, marksTargetUser, user.String(), b.Reason, govInvitePayload{
		UserID: user.String(), OfficialName: name, Reason: b.Reason, RequestedBy: requester,
	})
}

// submitPendingTwoPerson is submitTwoPerson without the sole-holder path:
// 202 pending, or 409 SECOND_APPROVER_REQUIRED when nobody else could approve.
func (h *Handler) submitPendingTwoPerson(c *gin.Context, targetType, targetID, reason string, payload any) {
	ctx := c.Request.Context()
	info := auditFrom(c)
	info.targetType, info.targetID, info.reason = targetType, targetID, reason
	req, ok := effectiveRequirement(c)
	if !ok || !req.TwoPerson {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared two-person", nil)
		return
	}
	if strings.TrimSpace(reason) == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeReasonRequired, "A reason is required for this action", nil)
		return
	}
	sub, err := h.approvals.SubmitPending(ctx, approvals.Request{
		App: info.app, Operation: req.Operation, TargetType: targetType, TargetID: targetID,
		RequiredPermission: req.Permission, Requester: actorFrom(c), Reason: reason, Payload: payload,
	})
	if errors.Is(err, approvals.ErrSecondHolderRequired) {
		info.outcome = postgres.AuditOutcomeDenied
		info.set("code", CodeSecondApproverRequired)
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeSecondApproverRequired,
			"A second admin holding "+req.Permission+" with two-factor enrolled must approve this, and there is none", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "two-person submit failed; refusing", "error", err, "operation", req.Operation)
		info.outcome = postgres.AuditOutcomeDenied
		info.set("error", err.Error())
		code := "INTERNAL_ERROR"
		if errors.Is(err, approvals.ErrHoldersUnavailable) {
			code = CodeApprovalUnavailable
		}
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, code, "The approval rule could not be applied", nil)
		return
	}
	info.outcome = postgres.AuditOutcomePending
	info.set(approvalFieldApproval, approvalValueRequested)
	info.set(approvalFieldApprovalID, sub.Approval.ID)
	api.JSON(c.Writer, http.StatusAccepted, gin.H{"approval": approvalView(*sub.Approval)}, nil)
}

// marksInviteExecutor runs a stored invitation as the approver (act), naming
// the requester as requested_by. Executing as the requester (which only the
// sole-holder path could do) is refused here too, before profile is called.
func (h *Handler) marksInviteExecutor(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
	var pl govInvitePayload
	if err := json.Unmarshal(payload, &pl); err != nil {
		return approvals.Result{Err: err}
	}
	user, err1 := uuid.Parse(pl.UserID)
	requester, err2 := uuid.Parse(pl.RequestedBy)
	if err1 != nil || err2 != nil || user == uuid.Nil || requester == uuid.Nil {
		return approvals.Result{Err: errInvalidID}
	}
	if requester.String() == actor {
		body, _ := json.Marshal(gin.H{"error": gin.H{"code": CodeSecondApproverRequired,
			"message": "A Government invitation needs a second admin to approve it"}})
		return approvals.Result{Data: body, Status: http.StatusConflict}
	}
	pl.UserID, pl.RequestedBy = user.String(), requester.String()
	resp, err := h.profile.Do(ctx, service.ProductRequest{
		Method: http.MethodPost, Path: "/marks/government/invitations", Body: pl,
		Permission: permGovInvite, Actor: actor,
	})
	return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
}
