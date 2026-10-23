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

	// Copyright title review (post-service copyright_review.go): the staff
	// catalogue of film / TV titles, the queue of long videos held for
	// review (oldest first) and the clear / block decision. Catalogue writes
	// need videos.remove; a block is a takedown (videos.remove + step-up,
	// decided from the body), a clear needs videos.moderate.
	{method: http.MethodGet, path: "/copyright/titles", operation: opTubeCopyrightTitles, permission: permTubeVideosModerate,
		alternatives: []string{permTubeVideosModerate, permTubeVideosRemove}},
	{method: http.MethodPost, path: "/copyright/titles", operation: opTubeCopyrightTitleAdd, permission: permTubeVideosRemove},
	{method: http.MethodPatch, path: "/copyright/titles/:titleId", operation: opTubeCopyrightTitleSet, permission: permTubeVideosRemove, targetType: "copyright_title"},
	{method: http.MethodGet, path: "/copyright/reviews", operation: opTubeCopyrightReviews, permission: permTubeVideosModerate,
		alternatives: []string{permTubeVideosModerate, permTubeVideosRemove}},
	{method: http.MethodPost, path: "/copyright/reviews/:reviewId/decision", operation: opTubeCopyrightDecide, permission: permTubeVideosModerate, targetType: "copyright_review"},
}...)...)

// Copyright title review operations.
const (
	opTubeCopyrightTitles   = "tube.copyright.titles.list"
	opTubeCopyrightTitleAdd = "tube.copyright.title.create"
	opTubeCopyrightTitleSet = "tube.copyright.title.update"
	opTubeCopyrightReviews  = "tube.copyright.reviews.list"
	opTubeCopyrightDecide   = "tube.copyright.review.decision"
)

// Copyright review decisions post-service accepts; a block is a takedown.
var (
	copyrightDecisions        = map[string]bool{"clear": true, "block": true}
	copyrightDecisionTakedown = map[string]bool{"block": true}
)

// RegisterTubeRoutes adds the Tube dashboard under /v1/admin/tube.
//
//	step-up   video reject (decision or review status), copyright block
func (h *Handler) RegisterTubeRoutes(r *gin.Engine) {
	p := product{app: tubeAuditApp, label: "Tube", prefix: tubePrefix, client: h.post}
	routes := make([]productRoute, len(TubeRoutes))
	copy(routes, TubeRoutes)
	special := map[string]gin.HandlerFunc{}
	h.attachContentKind(p, kindVideo, routes, special)
	for i := range routes {
		if routes[i].operation == opTubeCopyrightDecide {
			// block → tube:videos.remove + step-up; clear → videos.moderate.
			routes[i].decide = contentDecision(kindVideo, "decision", copyrightDecisions, copyrightDecisionTakedown)
		}
	}
	h.registerProduct(r, p, routes, special)
}
