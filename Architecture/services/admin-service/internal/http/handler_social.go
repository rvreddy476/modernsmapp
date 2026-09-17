package http

import (
	"net/http"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/gin-gonic/gin"
)

// Social permissions, exactly as post-service checks them
// (post-service/internal/http/admin_token.go) and as user-service checks
// business pages (user-service/internal/http/admin_token.go). Both products
// verify audience "post" / "social" respectively; the strings are the same
// catalogue app "social". Those marked NEW there are not yet in identity's
// catalogue; see the report.
const (
	permSocialStatsRead        = "social:stats.read" // NEW (post-service and user-service)
	permSocialPostsModerate    = "social:posts.moderate"
	permSocialPostsRemove      = "social:posts.remove" // NEW
	permSocialReelsModerate    = "social:reels.moderate"
	permSocialReelsRemove      = "social:reels.remove" // NEW
	permSocialCommentsModerate = "social:comments.moderate"
	permSocialCommentsRemove   = "social:comments.remove" // NEW
	permSocialReportsAct       = "social:reports.act"
	permSocialUsersRead        = "social:users.read"
	// Business pages (user-service).
	permSocialPagesModerate   = "social:pages.moderate"
	permSocialPagesSuspend    = "social:pages.suspend"    // NEW
	permSocialPagesDisable    = "social:pages.disable"    // NEW
	permSocialDocumentsReview = "social:documents.review" // NEW
)

const (
	socialAuditApp       = "social"
	socialPrefix         = "/v1/admin/social"
	opSocialStats        = "social.stats"
	opSocialCommentDecid = "social.comment.moderate"
)

// Comment moderation statuses post-service accepts; hidden and removed are
// takedowns (social:comments.remove + step-up).
var (
	socialCommentStatuses = map[string]bool{"visible": true, "hidden": true, "removed": true, "review": true}
	socialCommentTakedown = map[string]bool{"hidden": true, "removed": true}
)

// SocialContentRoutes is the post-service half of /v1/admin/social: posts and
// reels (per kind), the flagged-reel queue, content reports, comments and
// creators. Stats is merged with user-service's pages stats (see
// RegisterSocialRoutes).
var SocialContentRoutes = append(append([]productRoute{
	{method: http.MethodGet, path: "/stats", operation: opSocialStats, permission: permSocialStatsRead},
}, contentKindRoutes(kindPost)...), append(contentKindRoutes(kindReel), []productRoute{
	// Reel review queue (moderation_reviews).
	{method: http.MethodGet, path: "/reels/flagged", operation: "social.reels.flagged", permission: permSocialReelsModerate,
		alternatives: []string{permSocialReelsModerate, permSocialReelsRemove}, upstream: "/reels/flagged"},
	{method: http.MethodGet, path: "/reels/:postId/reviews", operation: "social.reels.reviews", permission: permSocialReelsModerate,
		alternatives: []string{permSocialReelsModerate, permSocialReelsRemove}, upstream: "/reels/:postId/moderation", targetType: "reel"},

	// Content reports on posts, reels and comments. post-service lists only
	// the target types the token's social:reports.act covers and refuses a
	// video report (Tube) under this scope.
	{method: http.MethodGet, path: "/reports", operation: "social.reports.list", permission: permSocialReportsAct},
	{method: http.MethodPatch, path: "/reports/:reportId", operation: "social.report.review", permission: permSocialReportsAct, targetType: "content_report"},

	// Comments: hiding or removing is a takedown, decided from the body.
	{method: http.MethodGet, path: "/comments/moderation", operation: "social.comments.list", permission: permSocialCommentsModerate,
		alternatives: []string{permSocialCommentsModerate, permSocialCommentsRemove}},
	{method: http.MethodGet, path: "/comments/:commentId/audit", operation: "social.comment.audit", permission: permSocialCommentsModerate,
		alternatives: []string{permSocialCommentsModerate, permSocialCommentsRemove}, targetType: "comment"},
	{method: http.MethodPatch, path: "/comments/:commentId/moderation", operation: opSocialCommentDecid, permission: permSocialCommentsModerate, targetType: "comment"},

	// Creators.
	{method: http.MethodGet, path: "/creators/:userId/counts", operation: "social.creator.counts", permission: permSocialUsersRead, targetType: "user"},
}...)...)

// SocialPagesRoutes is the user-service half of /v1/admin/social: business
// pages. Step-up on suspend, disable, reinstating a suspended page, and every
// document route (documents can be identity proofs).
//
// Reinstating is its own console route: it calls user-service's approve with
// social:pages.suspend, which user-service requires for a page in status
// suspended and refuses for any other status (a plain approve carries
// social:pages.moderate, which user-service refuses for a suspended page).
var SocialPagesRoutes = []productRoute{
	{method: http.MethodGet, path: "/pages/pending", operation: "social.pages.pending", permission: permSocialPagesModerate},
	{method: http.MethodGet, path: "/pages/:id", operation: "social.page.read", permission: permSocialPagesModerate, targetType: "business_page"},
	{method: http.MethodPost, path: "/pages/:id/approve", operation: "social.page.approve", permission: permSocialPagesModerate, targetType: "business_page"},
	{method: http.MethodPost, path: "/pages/:id/reinstate", operation: "social.page.reinstate", permission: permSocialPagesSuspend, stepUp: true, upstream: "/pages/:id/approve", targetType: "business_page"},
	{method: http.MethodPost, path: "/pages/:id/reject", operation: "social.page.reject", permission: permSocialPagesModerate, targetType: "business_page"},
	{method: http.MethodPost, path: "/pages/:id/suspend", operation: "social.page.suspend", permission: permSocialPagesSuspend, stepUp: true, targetType: "business_page"},
	{method: http.MethodPost, path: "/pages/:id/disable", operation: "social.page.disable", permission: permSocialPagesDisable, stepUp: true, targetType: "business_page"},
	{method: http.MethodGet, path: "/pages/:id/documents", operation: "social.page.documents.list", permission: permSocialDocumentsReview, stepUp: true, targetType: "business_page"},
	{method: http.MethodPost, path: "/pages/:id/documents/:docId/approve", operation: "social.page.document.approve", permission: permSocialDocumentsReview, stepUp: true, targetType: "page_document"},
	{method: http.MethodPost, path: "/pages/:id/documents/:docId/reject", operation: "social.page.document.reject", permission: permSocialDocumentsReview, stepUp: true, targetType: "page_document"},
}

// RegisterSocialRoutes adds the Social dashboard under /v1/admin/social:
// post-service content and user-service business pages.
//
//	step-up   post/reel reject (decision or review status), comment hide or
//	          remove, page suspend/disable/reinstate, document list and decisions
//
// Social stats merge post-service's /stats (parts.content) with user-
// service's pages /stats (parts.pages); a source that fails is shown as
// unavailable, never as zeros.
func (h *Handler) RegisterSocialRoutes(r *gin.Engine) {
	content := product{app: socialAuditApp, label: "Social", prefix: socialPrefix, client: h.post}
	routes := make([]productRoute, len(SocialContentRoutes))
	copy(routes, SocialContentRoutes)
	special := map[string]gin.HandlerFunc{
		opSocialStats: h.mergedStats([]statsSource{
			{name: "content", label: "post-service", client: h.post, path: "/stats"},
			{name: "pages", label: "user-service", client: h.pages, path: "/stats"},
		}),
	}
	h.attachContentKind(content, kindPost, routes, special)
	h.attachContentKind(content, kindReel, routes, special)
	for i := range routes {
		if routes[i].operation == opSocialCommentDecid {
			routes[i].decide = socialCommentDecision
		}
	}
	h.registerProduct(r, content, routes, special)

	pages := product{app: socialAuditApp, label: "Business pages", prefix: socialPrefix, client: h.pages}
	h.registerProduct(r, pages, SocialPagesRoutes, nil)
}

// socialCommentDecision: hidden or removed → social:comments.remove + step-up;
// visible or review → social:comments.moderate. Anything else is refused
// before post-service is called.
func socialCommentDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	raw, fields, err := jsonBody(c)
	if err != nil || len(raw) == 0 {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON with status")
	}
	status := normaliseOutcome(stringField(fields, "status"))
	if !socialCommentStatuses[status] {
		return Decision{}, badRequest(CodeInvalidAction, "status must be visible, hidden, removed or review")
	}
	d := Decision{Audit: map[string]any{"status": status}}
	if socialCommentTakedown[status] {
		d.Permission = permSocialCommentsRemove
		d.StepUp = true
	}
	return d, nil
}
