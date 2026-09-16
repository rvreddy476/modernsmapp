// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Content: Social and Tube). Same pattern as
// food-service and dating-service.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls post-service with a service token it
// signs for THIS call:
//
//	iss   admin-service
//	aud   post
//	scope [the permission admin-service checked, e.g. social:posts.remove]
//	act   the admin's user id
//	exp   60 s
//
// post-service trusts none of that because of where the request came from.
// The internal key is no evidence (the gateway stamps it on edge traffic) and
// neither is X-User-Id or X-Scopes (anyone holding the key can set them). The
// token is: only admin-service holds the private key, the scope names what it
// checked, and the actor is signed. Identity headers are stripped from an
// admitted request so no reused handler can read them.
//
// Social covers posts, reels and comments; Tube covers long videos and
// channels. A post's kind comes from its content_type (postgres.ContentKindOf),
// so a route that acts on "a post" narrows to the kind's permission, and within
// a kind a takedown (reject, hide, remove) needs the narrower .remove
// permission while approve / restore / needs_changes need .moderate.
package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Content admin permissions. Names identity's catalogue already has
// (identity-platform/services/auth-service/internal/permissions) are reused;
// the ones marked NEW are not in the catalogue yet.
const (
	PermSocialStatsRead      = "social:stats.read" // NEW
	PermPostsModerate        = "social:posts.moderate"
	PermPostsRemove          = "social:posts.remove" // NEW
	PermReelsModerate        = "social:reels.moderate"
	PermReelsRemove          = "social:reels.remove" // NEW
	PermCommentsModerate     = "social:comments.moderate"
	PermCommentsRemove       = "social:comments.remove" // NEW
	PermSocialReportsAct     = "social:reports.act"
	PermSocialUsersRead      = "social:users.read"
	PermTubeStatsRead        = "tube:stats.read" // NEW
	PermVideosModerate       = "tube:videos.moderate"
	PermVideosRemove         = "tube:videos.remove" // NEW
	PermTubeChannelsModerate = "tube:channels.moderate"
	PermTubeReportsAct       = "tube:reports.act"
)

// AdminPermissions lists every permission an admin-service token may carry
// to post-service. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermSocialStatsRead, PermPostsModerate, PermPostsRemove, PermReelsModerate, PermReelsRemove,
	PermCommentsModerate, PermCommentsRemove, PermSocialReportsAct, PermSocialUsersRead,
	PermTubeStatsRead, PermVideosModerate, PermVideosRemove, PermTubeChannelsModerate, PermTubeReportsAct,
}

// Per-kind permissions for routes that act on a post of any kind.
var (
	kindModeratePerm = map[string]string{
		postgres.ContentKindPost: PermPostsModerate, postgres.ContentKindReel: PermReelsModerate, postgres.ContentKindVideo: PermVideosModerate,
	}
	kindRemovePerm = map[string]string{
		postgres.ContentKindPost: PermPostsRemove, postgres.ContentKindReel: PermReelsRemove, postgres.ContentKindVideo: PermVideosRemove,
	}
	// anyPostPerm admits a token to a post route; the handler then narrows to
	// the post's kind and the action's severity.
	anyPostPerm = []string{PermPostsModerate, PermReelsModerate, PermVideosModerate, PermPostsRemove, PermReelsRemove, PermVideosRemove}
)

// Error codes for the token path.
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const (
	ctxAdminTokenPerms = "post_admin_token_perms"
	ctxAdminActor      = "post_admin_actor"
)

// InternalAdminPrefix is the token-only admin family. The engine-wide internal
// key gate skips it (handler.go RegisterRoutes); the gateway refuses
// /internal/ from the edge.
const InternalAdminPrefix = "/v1/posts/internal/admin"

func isAdminTokenRoute(fullPath string) bool {
	return strings.HasPrefix(fullPath, InternalAdminPrefix+"/")
}

// identityHeaders are removed from an admitted token request.
var identityHeaders = []string{"X-User-Id", "X-Scopes", "X-Roles", "X-Session-Id", "X-Internal-Service-Key", "X-Admin-Id", "X-Actor-Id"}

// WithServiceAuth installs the service-token verifier (ServiceCallersFromEnv).
// nil means no token is accepted.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
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

// requireAdminToken admits ONLY an admin-service token carrying one of perms
// (perms[0] is the route's permission, named in refusals). No token → 401,
// whatever else the request carries.
func (h *Handler) requireAdminToken(perms ...string) gin.HandlerFunc {
	if len(perms) == 0 {
		panic("post: requireAdminToken needs the route's permission")
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
		for _, name := range identityHeaders {
			c.Request.Header.Del(name)
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
// or malformed act claim.
func (h *Handler) authorizeAdminToken(c *gin.Context, perms []string) bool {
	ctx := c.Request.Context()
	if h.verifier == nil || h.verifier.Callers() == 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
			"service tokens are not accepted by this deployment", nil)
		return false
	}
	raw := rawServiceToken(c)
	granted := map[string]bool{}
	var verified *servicetoken.Verified
	for _, perm := range perms {
		v, err := h.verifier.Verify(raw, perm, "")
		if err == nil {
			granted[perm] = true
			verified = v
			continue
		}
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			continue
		}
		slog.WarnContext(ctx, "post: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified == nil {
		slog.WarnContext(ctx, "post: admin token lacks the route permission", "required", perms[0], "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this route requires", gin.H{"required": perms[0]})
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "post: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
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

// adminGranted reports whether the admitted token carries perm.
func adminGranted(c *gin.Context, perm string) bool {
	v, ok := c.Get(ctxAdminTokenPerms)
	if !ok {
		return false
	}
	granted, _ := v.(map[string]bool)
	return granted[perm]
}

// adminMayAny answers 403 unless the admitted token carries one of perms
// (perms[0] is named in the refusal). Unlike food's LEGACY-aware adminMay,
// there is no fallback: these helpers run only behind requireAdminToken.
func adminMayAny(c *gin.Context, perms ...string) bool {
	for _, p := range perms {
		if adminGranted(c, p) {
			return true
		}
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
		"the token does not carry the permission this action requires", gin.H{"required": perms[0]})
	return false
}

// PostActionPermission is the permission one action on a post of kind needs:
// a takedown needs the kind's .remove, anything else its .moderate.
//
//	decision actions: reject → remove; approve, needs_changes → moderate
//	review-status:    rejected → remove; approved → moderate
func PostActionPermission(kind, action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "reject", "rejected":
		return kindRemovePerm[kind]
	}
	return kindModeratePerm[kind]
}

// CommentStatusPermission: hiding or removing a comment is a takedown;
// visible (restore) and review need moderate.
func CommentStatusPermission(status string) string {
	switch strings.TrimSpace(status) {
	case "hidden", "removed":
		return PermCommentsRemove
	}
	return PermCommentsModerate
}

// ReportPermission: reports on videos are Tube; posts, reels and comments are Social.
func ReportPermission(targetType string) string {
	if targetType == "video" {
		return PermTubeReportsAct
	}
	return PermSocialReportsAct
}

// registerAdminTokenRoutes declares the admin console routes with the
// permission each needs. Every route here is token-only.
func (h *Handler) registerAdminTokenRoutes(r *gin.Engine) {
	gate := h.requireAdminToken
	g := r.Group(InternalAdminPrefix)

	g.GET("/stats", gate(PermSocialStatsRead), h.AdminSocialStats)
	g.GET("/tube/stats", gate(PermTubeStatsRead), h.AdminTubeStats)

	// Posts of every kind: narrowed to the post's kind in the handler.
	g.GET("/posts/review-queue", gate(anyPostPerm...), h.AdminReviewQueue)
	g.GET("/posts/:postId", gate(anyPostPerm...), h.AdminGetPost)
	g.GET("/posts/:postId/moderation-history", gate(anyPostPerm...), h.AdminPostModerationHistory)
	g.POST("/posts/:postId/moderation", gate(anyPostPerm...), h.AdminModeratePost)
	g.POST("/posts/review-status", gate(anyPostPerm...), h.AdminSetReviewStatus)
	g.POST("/posts/visibility", gate(anyPostPerm...), h.AdminSetVisibility)

	// Reel review queue (moderation_reviews).
	g.GET("/reels/flagged", gate(PermReelsModerate, PermReelsRemove), h.GetFlaggedReels)
	g.GET("/reels/:reelId/moderation", gate(PermReelsModerate, PermReelsRemove), h.GetReelModerationReviews)

	// Content reports: Social or Tube by target type.
	g.GET("/reports", gate(PermSocialReportsAct, PermTubeReportsAct), h.AdminListReports)
	g.PATCH("/reports/:reportId", gate(PermSocialReportsAct, PermTubeReportsAct), h.AdminReviewReport)

	// Comments (Social).
	g.GET("/comments/moderation", gate(PermCommentsModerate, PermCommentsRemove), h.ListFlaggedComments)
	g.GET("/comments/:commentId/audit", gate(PermCommentsModerate, PermCommentsRemove), h.AdminCommentAudit)
	g.PATCH("/comments/:commentId/moderation", gate(PermCommentsModerate, PermCommentsRemove), h.AdminModerateComment)

	// Tube channels (read only: no channel moderation exists yet).
	g.GET("/channels/search", gate(PermTubeChannelsModerate), h.AdminSearchChannels)
	g.GET("/channels/:ref", gate(PermTubeChannelsModerate), h.AdminGetChannel)

	// Creators.
	g.GET("/creators/:userId/counts", gate(PermSocialUsersRead), h.AdminCreatorCounts)
	g.GET("/creators/:userId/video-series", gate(PermVideosModerate, PermVideosRemove), h.AdminCreatorVideoSeries)
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func adminInternalError(c *gin.Context, what string, err error) {
	slog.ErrorContext(c.Request.Context(), "post: admin "+what, "error", err)
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", what+" failed", nil)
}

// AdminSocialStats — GET .../stats (social:stats.read).
func (h *Handler) AdminSocialStats(c *gin.Context) {
	stats, err := h.svc.SocialAdminStats(c.Request.Context())
	if err != nil {
		adminInternalError(c, "social stats", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// AdminTubeStats — GET .../tube/stats (tube:stats.read).
func (h *Handler) AdminTubeStats(c *gin.Context) {
	stats, err := h.svc.TubeAdminStats(c.Request.Context())
	if err != nil {
		adminInternalError(c, "tube stats", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// postKindAllowed loads a post's kind and answers whether the token may take
// action on it (action "" = read: either the kind's moderate or remove).
// It writes 400/404/403/500 itself.
func (h *Handler) postKindAllowed(c *gin.Context, postID uuid.UUID, action string) (string, bool) {
	kind, err := h.svc.PostContentKind(c.Request.Context(), postID)
	if errors.Is(err, postgres.ErrPostNotFound) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		return "", false
	}
	if err != nil {
		adminInternalError(c, "post lookup", err)
		return "", false
	}
	if action == "" {
		return kind, adminMayAny(c, kindModeratePerm[kind], kindRemovePerm[kind])
	}
	return kind, adminMayAny(c, PostActionPermission(kind, action))
}

// AdminReviewQueue — GET .../posts/review-queue?queue=flagged|staged&kind=post|reel|video&cursor&limit.
func (h *Handler) AdminReviewQueue(c *gin.Context) {
	queue := c.DefaultQuery("queue", "flagged")
	kind := c.DefaultQuery("kind", postgres.ContentKindPost)
	if queue != "flagged" && queue != "staged" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "queue must be flagged or staged", nil)
		return
	}
	if _, ok := kindModeratePerm[kind]; !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "kind must be post, reel or video", nil)
		return
	}
	if !adminMayAny(c, kindModeratePerm[kind], kindRemovePerm[kind]) {
		return
	}
	limit := 50
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	var before time.Time
	if v := c.Query("cursor"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "cursor must be RFC3339", nil)
			return
		}
		before = t
	}
	items, err := h.svc.ListReviewQueue(c.Request.Context(), queue, kind, before, limit)
	if err != nil {
		adminInternalError(c, "review queue", err)
		return
	}
	var meta *api.Meta
	if len(items) == limit {
		meta = &api.Meta{NextCursor: items[len(items)-1].CreatedAt.Format(time.RFC3339Nano)}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

func adminPostID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

// AdminGetPost — GET .../posts/:postId. The stored post, whatever its
// visibility or review status (soft-deleted posts are 404).
func (h *Handler) AdminGetPost(c *gin.Context) {
	postID, ok := adminPostID(c)
	if !ok {
		return
	}
	if _, ok := h.postKindAllowed(c, postID, ""); !ok {
		return
	}
	post, err := h.svc.AdminGetPost(c.Request.Context(), postID)
	if errors.Is(err, postgres.ErrPostNotFound) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		return
	}
	if err != nil {
		adminInternalError(c, "get post", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, post, nil)
}

// AdminPostModerationHistory — GET .../posts/:postId/moderation-history.
func (h *Handler) AdminPostModerationHistory(c *gin.Context) {
	postID, ok := adminPostID(c)
	if !ok {
		return
	}
	kind, ok := h.postKindAllowed(c, postID, "")
	if !ok {
		return
	}
	history, err := h.svc.GetPostModerationHistory(c.Request.Context(), postID, kind)
	if errors.Is(err, pgx.ErrNoRows) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		return
	}
	if err != nil {
		adminInternalError(c, "moderation history", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, history, nil)
}

// AdminModeratePost — POST .../posts/:postId/moderation. Same body and
// post_moderation_decisions row as POST /v1/posts/:postId/moderation, with
// actor_id the signed act claim.
func (h *Handler) AdminModeratePost(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
		return
	}
	h.moderatePost(c, actor, func(c *gin.Context, postID uuid.UUID, action string) bool {
		_, ok := h.postKindAllowed(c, postID, action)
		return ok
	})
}

// AdminSetReviewStatus — POST .../posts/review-status {post_id, status, reason}.
// Flagged → approved|rejected, one post_review_audit row with the act user.
func (h *Handler) AdminSetReviewStatus(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
		return
	}
	h.setReviewStatus(c, postgres.ReviewAuditActor{UserID: &actor}, func(c *gin.Context, postID uuid.UUID, status string) bool {
		_, ok := h.postKindAllowed(c, postID, status)
		return ok
	})
}

// AdminSetVisibility — POST .../posts/visibility {post_id, visibility, reason}.
// Staged → visibility, one post_review_audit row with the act user.
func (h *Handler) AdminSetVisibility(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
		return
	}
	h.setVisibility(c, postgres.ReviewAuditActor{UserID: &actor}, func(c *gin.Context, postID uuid.UUID, _ string) bool {
		_, ok := h.postKindAllowed(c, postID, "promote")
		return ok
	})
}

// allowedReportTypes is the report target types the token may see.
func allowedReportTypes(c *gin.Context) []string {
	types := []string{}
	if adminGranted(c, PermSocialReportsAct) {
		types = append(types, "post", "comment", "reel")
	}
	if adminGranted(c, PermTubeReportsAct) {
		types = append(types, "video")
	}
	return types
}

// AdminListReports — GET .../reports?status&target_type&limit&offset. Only the
// target types the token's reports.act covers are listed.
func (h *Handler) AdminListReports(c *gin.Context) {
	types := allowedReportTypes(c)
	if tt := c.Query("target_type"); tt != "" {
		switch tt {
		case "post", "comment", "reel", "video":
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid target type", nil)
			return
		}
		if !adminMayAny(c, ReportPermission(tt)) {
			return
		}
		types = []string{tt}
	}
	limit, offset := 50, 0
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	if n, err := strconv.Atoi(c.Query("offset")); err == nil && n >= 0 {
		offset = n
	}
	reports, err := h.svc.ListReportsOfTypes(c.Request.Context(), c.Query("status"), types, limit, offset)
	if err != nil {
		adminInternalError(c, "list reports", err)
		return
	}
	if reports == nil {
		reports = []postgres.ContentReport{}
	}
	api.JSON(c.Writer, http.StatusOK, reports, nil)
}

// AdminReviewReport — PATCH .../reports/:reportId {status, review_note}.
// Permission by the report's target; one post_admin_audit row with act.
func (h *Handler) AdminReviewReport(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
		return
	}
	h.reviewReport(c, actor, func(c *gin.Context, targetType string) bool {
		return adminMayAny(c, ReportPermission(targetType))
	})
}

// AdminModerateComment — PATCH .../comments/:commentId/moderation {status}.
// hidden/removed need social:comments.remove; one post_admin_audit row with act.
func (h *Handler) AdminModerateComment(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
		return
	}
	h.moderateComment(c, actor, func(c *gin.Context, status string) bool {
		return adminMayAny(c, CommentStatusPermission(status))
	})
}

// AdminCommentAudit — GET .../comments/:commentId/audit.
func (h *Handler) AdminCommentAudit(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	rows, err := h.svc.ListCommentAudit(c.Request.Context(), commentID)
	if err != nil {
		adminInternalError(c, "comment audit", err)
		return
	}
	if rows == nil {
		rows = []postgres.AdminAuditEntry{}
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

// AdminSearchChannels — GET .../channels/search?q=&limit=.
func (h *Handler) AdminSearchChannels(c *gin.Context) {
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a non-negative integer", nil)
			return
		}
		limit = n
	}
	views, err := h.svc.SearchChannels(c.Request.Context(), uuid.Nil, c.Query("q"), limit)
	if errors.Is(err, service.ErrEmptyChannelQuery) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", err.Error(), nil)
		return
	}
	if err != nil {
		adminInternalError(c, "channel search", err)
		return
	}
	if views == nil {
		views = []*service.ChannelView{}
	}
	api.JSON(c.Writer, http.StatusOK, views, nil)
}

// AdminGetChannel — GET .../channels/:ref (handle or owner user id).
func (h *Handler) AdminGetChannel(c *gin.Context) {
	view, err := h.svc.GetChannelByRef(c.Request.Context(), uuid.Nil, c.Param("ref"))
	if errors.Is(err, service.ErrChannelNotFound) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found", nil)
		return
	}
	if err != nil {
		adminInternalError(c, "get channel", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

func adminUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("userId"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

// AdminCreatorCounts — GET .../creators/:userId/counts.
func (h *Handler) AdminCreatorCounts(c *gin.Context) {
	userID, ok := adminUserID(c)
	if !ok {
		return
	}
	counts, err := h.svc.GetAuthorCounts(c.Request.Context(), userID)
	if err != nil {
		adminInternalError(c, "creator counts", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

// AdminCreatorVideoSeries — GET .../creators/:userId/video-series?limit&offset,
// private series included.
func (h *Handler) AdminCreatorVideoSeries(c *gin.Context) {
	userID, ok := adminUserID(c)
	if !ok {
		return
	}
	limit, offset := 50, 0
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	if n, err := strconv.Atoi(c.Query("offset")); err == nil && n >= 0 {
		offset = n
	}
	series, err := h.svc.AdminListVideoSeriesByCreator(c.Request.Context(), userID, limit, offset)
	if err != nil {
		adminInternalError(c, "creator video series", err)
		return
	}
	if series == nil {
		series = []postgres.VideoSeries{}
	}
	api.JSON(c.Writer, http.StatusOK, series, nil)
}
