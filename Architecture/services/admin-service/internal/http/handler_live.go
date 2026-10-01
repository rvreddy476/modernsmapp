package http

import (
	"net/http"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/gin-gonic/gin"
)

// Live permissions, exactly as live-service-v2 checks them on its token-only
// admin family (/v1/livestream/internal/admin, audience "live"), and as
// identity's catalogue grants them: superadmin and admin hold all six,
// moderators hold streams.read, reports.read, reports.act and chat.moderate.
const (
	permLiveStreamsRead  = "live:streams.read"
	permLiveStreamsStop  = "live:streams.stop"
	permLiveReportsRead  = "live:reports.read"
	permLiveReportsAct   = "live:reports.act"
	permLiveChatModerate = "live:chat.moderate"
	permLiveUsersBan     = "live:users.ban"
)

const (
	liveAuditApp = "live"
	livePrefix   = "/v1/admin/live"

	opLiveStreamStop    = "live.stream.stop"
	opLiveReportResolve = "live.report.resolve"
	opLiveUserBan       = "live.user.ban"
	opLiveUserUnban     = "live.user.unban"
)

// liveReportActions is live-service-v2's resolve vocabulary, each with the
// permission it needs IN ADDITION to live:reports.act (none for dismiss): a
// resolution that removes a message or bans the user is that action, so the
// admin must hold its own permission too.
var liveReportActions = map[string]string{
	"dismiss":        "",
	"remove_message": permLiveChatModerate,
	"ban_user":       permLiveUsersBan,
}

// LiveRoutes is the route table under /v1/admin/live, one for one with
// live-service-v2's admin routes. Stopping a stream, resolving a report
// (which can remove a message or ban the user) and the platform live ban
// (both directions) need a fresh step-up and a reason. Removing a chat
// message is a moderator's everyday action: no step-up, reason optional.
// Reading the live-ban list needs the ban permission, no step-up.
var LiveRoutes = []productRoute{
	{method: http.MethodGet, path: "/streams", operation: "live.streams.list", permission: permLiveStreamsRead},
	{method: http.MethodPost, path: "/streams/:streamId/stop", operation: opLiveStreamStop, permission: permLiveStreamsStop, stepUp: true, targetType: "live_stream"},
	{method: http.MethodDelete, path: "/streams/:streamId/chat/:messageId", operation: "live.chat.remove", permission: permLiveChatModerate, targetType: "live_chat_message"},
	{method: http.MethodGet, path: "/reports", operation: "live.reports.list", permission: permLiveReportsRead},
	{method: http.MethodPost, path: "/reports/:reportId/resolve", operation: opLiveReportResolve, permission: permLiveReportsAct, stepUp: true, targetType: "live_report"},
	{method: http.MethodPost, path: "/users/:userId/live-ban", operation: opLiveUserBan, permission: permLiveUsersBan, stepUp: true, targetType: "user"},
	{method: http.MethodDelete, path: "/users/:userId/live-ban", operation: opLiveUserUnban, permission: permLiveUsersBan, stepUp: true, targetType: "user"},
	{method: http.MethodGet, path: "/bans", operation: "live.bans.list", permission: permLiveUsersBan},
}

// RegisterLiveRoutes adds the Live moderation page under /v1/admin/live.
//
//	step-up   stream stop, report resolve, live ban and unban
//	reason    required on those four, checked before the call
//	resolve   remove_message also needs live:chat.moderate, ban_user also
//	          live:users.ban (checked here and added to the token scope)
func (h *Handler) RegisterLiveRoutes(r *gin.Engine) {
	p := product{app: liveAuditApp, label: "Live", prefix: livePrefix, client: h.live}
	routes := make([]productRoute, len(LiveRoutes))
	copy(routes, LiveRoutes)
	for i := range routes {
		switch routes[i].operation {
		case opLiveReportResolve:
			routes[i].decide = requireLiveResolveDecision
		case opLiveStreamStop, opLiveUserBan, opLiveUserUnban:
			routes[i].decide = requireReasonDecision
		}
	}
	h.registerProduct(r, p, routes, nil)
}

// requireLiveResolveDecision refuses a report resolution without a reason or
// with an action live-service-v2 does not know, before any product call, and
// records the action on the audit row. An action with its own permission
// (remove_message, ban_user) is refused 403 unless the admin holds it too;
// when held it joins the token's scope so live-service-v2 can check it.
func requireLiveResolveDecision(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
	if _, err := requireReasonDecision(c, perms); err != nil {
		return Decision{}, err
	}
	_, fields, _ := jsonBody(c)
	action := strings.TrimSpace(stringField(fields, "action"))
	extra, known := liveReportActions[action]
	if !known {
		return Decision{}, badRequest(CodeInvalidBody, "action must be one of dismiss, remove_message, ban_user")
	}
	if extra != "" {
		if !perms.Has(extra) {
			info := auditFrom(c)
			info.set("action", action)
			info.set("required_permission", extra)
			return Decision{}, &DecisionError{Status: http.StatusForbidden, Code: CodePermissionDenied, Message: "Missing permission " + extra}
		}
		c.Set(ctxExtraScopes, []string{extra})
	}
	return Decision{Audit: map[string]any{"action": action}}, nil
}
