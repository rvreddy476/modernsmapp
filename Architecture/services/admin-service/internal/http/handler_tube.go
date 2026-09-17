package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Tube permissions, exactly as post-service checks them
// (post-service/internal/http/admin_token.go). Those marked NEW are not yet
// in identity's catalogue; see the report.
const (
	permTubeStatsRead        = "tube:stats.read" // NEW
	permTubeVideosModerate   = "tube:videos.moderate"
	permTubeVideosRemove     = "tube:videos.remove" // NEW
	permTubeChannelsModerate = "tube:channels.moderate"
	permTubeReportsAct       = "tube:reports.act"
)

const (
	tubeAuditApp = "tube"
	tubePrefix   = "/v1/admin/tube"
)

// TubeRoutes is the route table under /v1/admin/tube: long videos (per kind,
// on post-service's post routes), video reports, channels (read only: no
// channel moderation exists yet) and a creator's series.
var TubeRoutes = append([]productRoute{
	{method: http.MethodGet, path: "/stats", operation: "tube.stats", permission: permTubeStatsRead, upstream: "/tube/stats"},
}, append(contentKindRoutes(kindVideo), []productRoute{
	// Video reports. post-service lists only video reports under
	// tube:reports.act and refuses a post, reel or comment report (Social).
	{method: http.MethodGet, path: "/reports", operation: "tube.reports.list", permission: permTubeReportsAct},
	{method: http.MethodPatch, path: "/reports/:reportId", operation: "tube.report.review", permission: permTubeReportsAct, targetType: "content_report"},

	// Channels (handle or owner user id).
	{method: http.MethodGet, path: "/channels/search", operation: "tube.channels.search", permission: permTubeChannelsModerate},
	{method: http.MethodGet, path: "/channels/:ref", operation: "tube.channel.read", permission: permTubeChannelsModerate, targetType: "tube_channel"},

	// A creator's video series, private ones included.
	{method: http.MethodGet, path: "/creators/:userId/series", operation: "tube.creator.series", permission: permTubeVideosModerate,
		alternatives: []string{permTubeVideosModerate, permTubeVideosRemove}, upstream: "/creators/:userId/video-series", targetType: "user"},
}...)...)

// RegisterTubeRoutes adds the Tube dashboard under /v1/admin/tube.
//
//	step-up   video reject (decision or review status)
func (h *Handler) RegisterTubeRoutes(r *gin.Engine) {
	p := product{app: tubeAuditApp, label: "Tube", prefix: tubePrefix, client: h.post}
	routes := make([]productRoute, len(TubeRoutes))
	copy(routes, TubeRoutes)
	special := map[string]gin.HandlerFunc{}
	h.attachContentKind(p, kindVideo, routes, special)
	h.registerProduct(r, p, routes, special)
}
