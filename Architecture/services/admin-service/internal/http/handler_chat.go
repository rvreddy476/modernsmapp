package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Chat permissions, exactly as channel-service, group-service and
// community-service check them (each service's internal/http/admin_token.go;
// all three verify audience "chat"). Those marked NEW are not yet in
// identity's catalogue; see the report.
const (
	permChatStatsRead        = "chat:stats.read"   // NEW
	permChatReportsRead      = "chat:reports.read" // NEW
	permChatReportsAct       = "chat:reports.act"
	permChatChannelsModerate = "chat:channels.moderate"
)

const (
	chatAuditApp        = "chat"
	chatPrefix          = "/v1/admin/chat"
	chatChannelsPrefix  = chatPrefix + "/channels"
	chatGroupsPrefix    = chatPrefix + "/groups"
	chatCommunityPrefix = chatPrefix + "/communities"
	opChatStats         = "chat.stats"
)

// ChatChannelRoutes is the broadcast-channel table under
// /v1/admin/chat/channels (channel-service). Suspend and unsuspend need a
// step-up.
var ChatChannelRoutes = []productRoute{
	{method: http.MethodGet, path: "/reports", operation: "chat.channels.reports.list", permission: permChatReportsRead, upstream: "/channel-reports"},
	{method: http.MethodGet, path: "/reports/:reportId", operation: "chat.channels.report.read", permission: permChatReportsRead, upstream: "/channel-reports/:reportId", targetType: "channel_report"},
	{method: http.MethodPost, path: "/reports/:reportId/decision", operation: "chat.channels.report.decide", permission: permChatReportsAct, upstream: "/channel-reports/:reportId/decision", targetType: "channel_report"},
	{method: http.MethodPost, path: "/:channelId/suspend", operation: "chat.channel.suspend", permission: permChatChannelsModerate, stepUp: true, upstream: "/channels/:channelId/suspend", targetType: "broadcast_channel"},
	{method: http.MethodPost, path: "/:channelId/unsuspend", operation: "chat.channel.unsuspend", permission: permChatChannelsModerate, stepUp: true, upstream: "/channels/:channelId/unsuspend", targetType: "broadcast_channel"},
}

// ChatGroupRoutes is the group-report table under /v1/admin/chat/groups
// (group-service).
var ChatGroupRoutes = []productRoute{
	{method: http.MethodGet, path: "/reports", operation: "chat.groups.reports.list", permission: permChatReportsRead},
	{method: http.MethodGet, path: "/reports/:reportId", operation: "chat.groups.report.read", permission: permChatReportsRead, targetType: "group_report"},
	{method: http.MethodPost, path: "/reports/:reportId/decision", operation: "chat.groups.report.decide", permission: permChatReportsAct, targetType: "group_report"},
}

// ChatCommunityRoutes is the community-report table under
// /v1/admin/chat/communities (community-service).
var ChatCommunityRoutes = []productRoute{
	{method: http.MethodGet, path: "/reports", operation: "chat.communities.reports.list", permission: permChatReportsRead},
	{method: http.MethodGet, path: "/reports/:reportId", operation: "chat.communities.report.read", permission: permChatReportsRead, targetType: "community_report"},
	{method: http.MethodPost, path: "/reports/:reportId/decision", operation: "chat.communities.report.decide", permission: permChatReportsAct, targetType: "community_report"},
}

// RegisterChatRoutes adds the Chat dashboard under /v1/admin/chat: channel,
// group and community report queues, channel suspension, and stats merged
// from the three services (parts.channels, parts.groups, parts.communities;
// a source that fails is shown as unavailable, never as zeros).
//
//	step-up   channel suspend and unsuspend
func (h *Handler) RegisterChatRoutes(r *gin.Engine) {
	h.gate.Handle(r, http.MethodGet, chatPrefix+"/stats",
		Requirement{Operation: opChatStats, Permission: permChatStatsRead},
		h.mergedStats([]statsSource{
			{name: "channels", label: "channel-service", client: h.channel, path: "/stats"},
			{name: "groups", label: "group-service", client: h.group, path: "/stats"},
			{name: "communities", label: "community-service", client: h.community, path: "/stats"},
		}))
	h.registerProduct(r, product{app: chatAuditApp, label: "Chat channels", prefix: chatChannelsPrefix, client: h.channel}, ChatChannelRoutes, nil)
	h.registerProduct(r, product{app: chatAuditApp, label: "Chat groups", prefix: chatGroupsPrefix, client: h.group}, ChatGroupRoutes, nil)
	h.registerProduct(r, product{app: chatAuditApp, label: "Chat communities", prefix: chatCommunityPrefix, client: h.community}, ChatCommunityRoutes, nil)
}
