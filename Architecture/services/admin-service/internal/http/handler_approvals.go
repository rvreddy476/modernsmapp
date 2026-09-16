package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Approval error codes.
const (
	CodeReasonRequired       = "REASON_REQUIRED"
	CodeApprovalUnavailable  = "APPROVAL_UNAVAILABLE"
	CodeApprovalNotFound     = "APPROVAL_NOT_FOUND"
	CodeSelfApproval         = "SELF_APPROVAL_FORBIDDEN"
	CodeApprovalExpired      = "APPROVAL_EXPIRED"
	CodeApprovalDecided      = "APPROVAL_ALREADY_DECIDED"
	CodeApprovalInProgress   = "APPROVAL_IN_PROGRESS"
	CodePayloadHashMismatch  = "PAYLOAD_HASH_MISMATCH"
	approvalAuditApp         = "platform"
	approvalFieldApproval    = "approval"
	approvalFieldApprovalID  = "approval_id"
	approvalValueRequested   = "requested"
	approvalValueSoleHolder  = "sole_holder"
	approvalValueApproved    = "approved"
	approvalValueRejected    = "rejected"
	approvalValueRequestedBy = "requester"
)

// RegisterApprovalRoutes adds the two-person inbox and decisions. The
// permission for a decision is the one stored on the approval, so these routes
// admit any identified admin and the approval service checks the rest.
func (h *Handler) RegisterApprovalRoutes(r *gin.Engine) {
	g := h.gate
	g.Handle(r, http.MethodGet, "/v1/admin/approvals",
		Requirement{Operation: "approvals.list", Access: AccessAnyAdmin, App: approvalAuditApp},
		h.listApprovals)
	g.Handle(r, http.MethodPost, "/v1/admin/approvals/:id/approve",
		Requirement{Operation: "approvals.approve", Access: AccessAnyAdmin, App: approvalAuditApp, StepUp: true},
		h.approveApproval)
	g.Handle(r, http.MethodPost, "/v1/admin/approvals/:id/reject",
		Requirement{Operation: "approvals.reject", Access: AccessAnyAdmin, App: approvalAuditApp},
		h.rejectApproval)
}

// submitTwoPerson runs a two_person route's first call: pending (202) when a
// second holder exists, immediate execution when the caller is the only one.
func (h *Handler) submitTwoPerson(c *gin.Context, targetType, targetID, reason string, payload any) {
	ctx := c.Request.Context()
	info := auditFrom(c)
	info.targetType, info.targetID, info.reason = targetType, targetID, reason

	req, ok := effectiveRequirement(c)
	if !ok || !req.TwoPerson {
		// Programming error: VerifyDeclared and the route table disagree.
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared two-person", nil)
		return
	}
	if strings.TrimSpace(reason) == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeReasonRequired, "A reason is required for this action", nil)
		return
	}

	// An admin admitted by another app's permission (payments confined to one
	// application) needs a second holder of THAT permission, never a count of
	// holders of a permission the requester does not hold.
	required := req.Permission
	if held := heldAsFrom(c); held != "" {
		required = held
	}
	sub, err := h.approvals.Submit(ctx, approvals.Request{
		App: info.app, Operation: req.Operation, TargetType: targetType, TargetID: targetID,
		RequiredPermission: required, Requester: actorFrom(c), Reason: reason, Payload: payload,
	})
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

	switch sub.Mode {
	case approvals.ModeSoleHolder:
		info.set(approvalFieldApproval, approvalValueSoleHolder)
		writeUpstream(c, info, sub.Result.Data, sub.Result.Status, sub.Result.Err)
	default:
		info.outcome = postgres.AuditOutcomePending
		info.set(approvalFieldApproval, approvalValueRequested)
		info.set(approvalFieldApprovalID, sub.Approval.ID)
		api.JSON(c.Writer, http.StatusAccepted, gin.H{"approval": approvalView(*sub.Approval)}, nil)
	}
}

// ApprovalView is one approval as GET /v1/admin/approvals (and a two-person
// route's 202) returns it: every stored field, plus the names the console
// reads —
//
//	id, status, app, operation   as stored
//	summary       short description built from operation, target and payload
//	requested_by  the requester's user id
//	requested_at  when it was requested (created_at)
//	expires_at    as stored
//	reason        the requester's reason
type ApprovalView struct {
	approvals.Approval
	Summary     string    `json:"summary"`
	RequestedBy string    `json:"requested_by"`
	RequestedAt time.Time `json:"requested_at"`
	Reason      string    `json:"reason"`
}

func approvalView(a approvals.Approval) ApprovalView {
	return ApprovalView{Approval: a, Summary: approvalSummary(a), RequestedBy: a.Requester, RequestedAt: a.CreatedAt, Reason: a.RequesterReason}
}

var approvalLabels = map[string]string{
	opCODSettle:              "Settle COD remittance",
	opFoodRefundIssue:        "Refund Feast order",
	opFoodRefundDecide:       "Decide Feast refund request",
	opFoodRestaurantMarkPaid: "Mark restaurant settlement paid",
	opFoodDeliveryMarkPaid:   "Mark delivery partner settlement paid",
	opMonRatesSet:            "Set creator fund rate",
	opMonBandsSet:            "Set creator fund quality band",
	opMonBudgetSet:           "Set creator fund budget",
	opMonSettleDay:           "Settle creator fund day",
	opMonSettlePeriod:        "Settle creator fund period",
	opMonSettleCreator:       "Settle creator fund period for creator",
	opMonEarningReverse:      "Reverse creator fund earning",
	opMonRefundIssue:         "Refund monetization transaction",
	opPayRefundResolve:       "Resolve payments refund",
}

// approvalSummary is a one-line description: what, on which target, and the
// amount or decision when the stored request carries one. Ids only.
func approvalSummary(a approvals.Approval) string {
	label, ok := approvalLabels[a.Operation]
	if !ok {
		label = strings.ReplaceAll(a.Operation, ".", " ")
	}
	target := a.TargetID
	if len(target) > 8 {
		target = target[:8]
	}
	s := label
	if target != "" {
		s += " " + target
	}
	var p struct {
		AmountPaise   int64  `json:"amount_paise"`
		Status        string `json:"status"`
		Resolution    string `json:"resolution"`
		ApplicationID string `json:"application_id"`
		Query         string `json:"query"`
	}
	_ = json.Unmarshal(a.Payload, &p)
	if p.AmountPaise > 0 {
		s += " for " + formatRupees(p.AmountPaise)
	} else if a.Operation == opFoodRefundIssue {
		s += " (full refund)"
	}
	if p.Status != "" {
		s += " (" + p.Status + ")"
	}
	if p.Resolution != "" {
		s += " as " + strings.ReplaceAll(p.Resolution, "_", " ")
	}
	if p.ApplicationID != "" {
		s += " in " + p.ApplicationID
	}
	if a.App == monetizationAuditApp && p.Query != "" {
		// settle ?day= / settle-period ?period=
		if q, err := url.ParseQuery(p.Query); err == nil {
			for _, k := range []string{"day", "period"} {
				if v := q.Get(k); v != "" && len(v) <= 20 {
					s += " " + k + " " + v
				}
			}
		}
	}
	return s
}

// formatRupees renders paise as ₹1,23,456.78 (Indian grouping).
func formatRupees(paise int64) string {
	rupees, frac := paise/100, paise%100
	digits := strconv.FormatInt(rupees, 10)
	if len(digits) > 3 {
		head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
		var groups []string
		for len(head) > 2 {
			groups = append([]string{head[len(head)-2:]}, groups...)
			head = head[:len(head)-2]
		}
		if head != "" {
			groups = append([]string{head}, groups...)
		}
		digits = strings.Join(groups, ",") + "," + tail
	}
	return fmt.Sprintf("₹%s.%02d", digits, frac)
}

func (h *Handler) listApprovals(c *gin.Context) {
	perms := permsFrom(c)
	list, err := h.approvals.ListDecidable(c.Request.Context(), actorFrom(c), perms.All(), queryInt(c, "limit", 50))
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "list approvals failed", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list approvals", nil)
		return
	}
	items := make([]ApprovalView, 0, len(list))
	for _, a := range list {
		items = append(items, approvalView(a))
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

type decisionReq struct {
	Reason string `json:"reason"`
}

// decisionContext binds the body and points the audit row at the approval's
// real target once the approval is known.
func decisionAudit(info *auditInfo, a *approvals.Approval, id string) {
	info.targetType, info.targetID = "approval", id
	if a == nil {
		return
	}
	info.app, info.operation = a.App, a.Operation
	info.targetType, info.targetID = a.TargetType, a.TargetID
	info.set(approvalFieldApprovalID, a.ID)
	info.set(approvalValueRequestedBy, a.Requester)
	info.set("required_permission", a.RequiredPermission)
}

func (h *Handler) approveApproval(c *gin.Context) {
	ctx := c.Request.Context()
	info := auditFrom(c)
	id := c.Param("id")
	var body decisionReq
	_ = c.ShouldBindJSON(&body)
	info.reason = body.Reason
	decisionAudit(info, nil, id)
	if strings.TrimSpace(body.Reason) == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeReasonRequired, "A reason is required to approve", nil)
		return
	}

	perms := permsFrom(c)
	d, err := h.approvals.Approve(ctx, id, actorFrom(c), body.Reason, perms.Has)
	decisionAudit(info, d.Approval, id)
	info.set(approvalFieldApproval, approvalValueApproved)
	if errors.Is(err, approvals.ErrRecordExecution) {
		// It ran; say so, and leave the stuck approval for an operator.
		slog.ErrorContext(ctx, "approval executed but not marked executed", "error", err, "approval_id", id)
		info.set("record_error", err.Error())
		writeUpstream(c, info, d.Result.Data, d.Result.Status, d.Result.Err)
		return
	}
	if err != nil {
		h.decisionError(c, info, err)
		return
	}
	if d.Replayed {
		// Already executed: answer with the stored outcome and run nothing.
		info.set("replayed", true)
		status := d.Result.Status
		info.statusCode = &status
		if d.Approval.ResultOutcome != nil {
			info.outcome = *d.Approval.ResultOutcome
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{"approval": d.Approval, "replayed": true}, nil)
		return
	}
	writeUpstream(c, info, d.Result.Data, d.Result.Status, d.Result.Err)
}

func (h *Handler) rejectApproval(c *gin.Context) {
	ctx := c.Request.Context()
	info := auditFrom(c)
	id := c.Param("id")
	var body decisionReq
	_ = c.ShouldBindJSON(&body)
	info.reason = body.Reason
	decisionAudit(info, nil, id)
	if strings.TrimSpace(body.Reason) == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeReasonRequired, "A reason is required to reject", nil)
		return
	}
	perms := permsFrom(c)
	a, err := h.approvals.Reject(ctx, id, actorFrom(c), body.Reason, perms.Has)
	decisionAudit(info, a, id)
	info.set(approvalFieldApproval, approvalValueRejected)
	if err != nil {
		h.decisionError(c, info, err)
		return
	}
	info.outcome = postgres.AuditOutcomeRejected
	api.JSON(c.Writer, http.StatusOK, gin.H{"approval": a}, nil)
}

func (h *Handler) decisionError(c *gin.Context, info *auditInfo, err error) {
	status, code, msg := http.StatusInternalServerError, "INTERNAL_ERROR", "The decision could not be recorded"
	switch {
	case errors.Is(err, approvals.ErrNotFound):
		status, code, msg = http.StatusNotFound, CodeApprovalNotFound, "Approval not found"
	case errors.Is(err, approvals.ErrSelfApproval):
		status, code, msg = http.StatusForbidden, CodeSelfApproval, "You cannot decide your own request"
	case errors.Is(err, approvals.ErrForbidden):
		status, code, msg = http.StatusForbidden, CodePermissionDenied, "Missing the permission this approval requires"
	case errors.Is(err, approvals.ErrExpired):
		status, code, msg = http.StatusGone, CodeApprovalExpired, "This approval has expired"
	case errors.Is(err, approvals.ErrAlreadyDecided), errors.Is(err, approvals.ErrNotClaimable):
		status, code, msg = http.StatusConflict, CodeApprovalDecided, "This approval has already been decided"
	case errors.Is(err, approvals.ErrInProgress):
		status, code, msg = http.StatusConflict, CodeApprovalInProgress, "This approval is being executed"
	case errors.Is(err, approvals.ErrPayloadTampered):
		status, code, msg = http.StatusConflict, CodePayloadHashMismatch, "The stored request no longer matches what was requested; it has been closed"
		slog.ErrorContext(c.Request.Context(), "approval payload hash mismatch", "approval_id", c.Param("id"))
	default:
		slog.ErrorContext(c.Request.Context(), "approval decision failed", "error", err, "approval_id", c.Param("id"))
	}
	info.outcome = postgres.AuditOutcomeDenied
	info.set("code", code)
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, msg, nil)
}
