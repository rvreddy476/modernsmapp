package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/group-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc         *service.Service
	internalKey string
	// verifier admits admin-service tokens on InternalAdminPrefix
	// (admin_token.go); nil accepts none.
	verifier *servicetoken.Verifier
	// admin backs the token-only admin handlers; the store in production.
	admin GroupAdmin
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Admin console token family FIRST: gin fixes a route's handler chain at
	// registration, so the engine-wide key middleware below stays off it.
	// It is judged only by the admin-service token (admin_token.go).
	h.registerAdminTokenRoutes(r)

	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	// media-service's group content authority — see handler_media_access.go.
	internal := r.Group("/v1/internal/groups")
	{
		internal.POST("/media-access", h.MediaAccess)
		internal.POST("/media-access/batch", h.MediaAccessBatch)
	}
	v1 := r.Group("/v1/groups")
	{
		v1.POST("", h.CreateGroup)
		v1.GET("/my", h.GetMyGroups)
		v1.GET("/feed", h.GetMyGroupsFeed)
		v1.GET("/discover", h.DiscoverGroups)
		v1.GET("/search", h.SearchGroups)
		v1.POST("/handle/check", h.CheckHandle)
		v1.GET("/by-handle/:handle", h.GetGroupByHandle)

		// Invite actions (before :groupId to avoid conflict)
		v1.GET("/invites/my", h.ListMyInvites)
		v1.POST("/invites/:inviteId/accept", h.AcceptInvite)
		v1.POST("/invites/:inviteId/reject", h.RejectInvite)

		v1.GET("/:groupId", h.GetGroup)
		v1.PUT("/:groupId", h.UpdateGroup)
		v1.DELETE("/:groupId", h.DeleteGroup)
		v1.POST("/:groupId/join", h.JoinGroup)
		v1.POST("/:groupId/leave", h.LeaveGroup)
		v1.POST("/:groupId/archive", h.ArchiveGroup)
		v1.GET("/:groupId/members", h.ListMembers)
		v1.PUT("/:groupId/members/:userId/role", h.UpdateMemberRole)
		v1.DELETE("/:groupId/members/:userId", h.RemoveMember)
		v1.POST("/:groupId/members/:userId/ban", h.BanMember)
		v1.POST("/:groupId/invite", h.InviteUser)
		v1.GET("/:groupId/invites", h.ListGroupInvites)

		// Join requests
		v1.POST("/:groupId/join-requests", h.CreateJoinRequest)
		v1.GET("/:groupId/join-requests", h.ListJoinRequests)
		v1.POST("/:groupId/join-requests/:requestId/approve", h.ApproveJoinRequest)
		v1.POST("/:groupId/join-requests/:requestId/reject", h.RejectJoinRequest)

		// Rules
		v1.GET("/:groupId/rules", h.GetGroupRules)
		v1.PUT("/:groupId/rules", h.UpdateGroupRules)

		// Ban management
		v1.DELETE("/:groupId/members/:userId/ban", h.UnbanMember)
		v1.GET("/:groupId/members/banned", h.ListBannedMembers)

		// Member Stats
		v1.GET("/:groupId/stats/contributors", h.GetTopContributors)
		v1.GET("/:groupId/members/:userId/stats", h.GetMemberStats)

		// Media
		v1.GET("/:groupId/media", h.GetGroupMedia)

		// Word Blocklist
		v1.GET("/:groupId/word-blocklist", h.GetWordBlocklist)
		v1.POST("/:groupId/word-blocklist", h.AddWordToBlocklist)
		v1.DELETE("/:groupId/word-blocklist/:word", h.RemoveWordFromBlocklist)

		// Post Approval Queue
		/*
			Moderation by POST id, on group_posts.status. The old
			/approval-queue routes read post_approval_queue, which nothing ever
			wrote. They were retired with the v1 post routes (R-L1): GET
			/:groupId/feed, POST /:groupId/posts, DELETE /:groupId/posts/:postId
			and the /pin pair. No client called any of them (retired_routes_test.go).
		*/
		v1.GET("/:groupId/posts/v2/pending", h.ListPendingPosts)
		v1.POST("/:groupId/posts/v2/:postId/approve", h.ApprovePendingPost)
		v1.POST("/:groupId/posts/v2/:postId/reject", h.RejectPendingPost)

		// Group Channels
		v1.GET("/:groupId/channels", h.ListGroupChannels)
		v1.POST("/:groupId/channels", h.CreateGroupChannel)
		v1.DELETE("/:groupId/channels/:channelId", h.DeleteGroupChannel)

		// Wiki
		v1.GET("/:groupId/wiki", h.ListWikiPages)
		v1.POST("/:groupId/wiki", h.CreateWikiPage)
		v1.PUT("/:groupId/wiki/:pageId", h.UpdateWikiPage)
		v1.DELETE("/:groupId/wiki/:pageId", h.DeleteWikiPage)

		// V2 Group Posts (rich, native group posts with engagement)
		v1.POST("/:groupId/posts/v2", h.CreateGroupPostV2)
		v1.GET("/:groupId/feed/v2", h.GetGroupFeedV2)
		/*
			Search sits BEFORE the :postId route, alongside the same
			static-versus-parameter pairing that posts/v2/pending already
			relies on. gin's tree prefers the static segment, so "search" is
			never parsed as a post id — and a post id is a uuid, so there is
			no id this shadows.
		*/
		v1.GET("/:groupId/posts/v2/search", h.SearchGroupPostsV2)
		v1.GET("/:groupId/posts/v2/:postId", h.GetGroupPostV2)
		v1.DELETE("/:groupId/posts/v2/:postId", h.DeleteGroupPostV2)

		// V2 Post Engagement
		v1.POST("/:groupId/posts/v2/:postId/spark", h.SparkGroupPost)
		v1.DELETE("/:groupId/posts/v2/:postId/spark", h.UnsparkGroupPost)
		// One current emoji reaction per viewer per post; spark stays as the
		// legacy 'like'. See service/reactions.go for the contract.
		v1.PUT("/:groupId/posts/v2/:postId/reaction", h.SetGroupPostReaction)
		v1.DELETE("/:groupId/posts/v2/:postId/reaction", h.RemoveGroupPostReaction)
		// Who wrote an anonymous post — owner/admins/moderators only, audited.
		v1.GET("/:groupId/posts/v2/:postId/author", h.RevealPostAuthor)
		v1.POST("/:groupId/posts/v2/:postId/stash", h.StashGroupPost)
		v1.DELETE("/:groupId/posts/v2/:postId/stash", h.UnstashGroupPost)
		v1.POST("/:groupId/posts/v2/:postId/view", h.RecordGroupPostView)
		v1.POST("/:groupId/posts/v2/:postId/echo", h.EchoGroupPost)
		v1.DELETE("/:groupId/posts/v2/:postId/echo", h.UnechoGroupPost)

		// V2 Post Comments
		v1.GET("/:groupId/posts/v2/:postId/comments", h.ListGroupPostComments)
		v1.POST("/:groupId/posts/v2/:postId/comments", h.AddGroupPostComment)
		v1.DELETE("/:groupId/posts/v2/:postId/comments/:commentId", h.DeleteGroupPostComment)

		// Group Events
		v1.POST("/:groupId/events", h.CreateGroupEvent)
		v1.GET("/:groupId/events", h.ListGroupEvents)
		v1.GET("/:groupId/events/:eventId", h.GetGroupEvent)
		v1.DELETE("/:groupId/events/:eventId", h.DeleteGroupEvent)
		v1.POST("/:groupId/events/:eventId/rsvp", h.RSVPGroupEvent)
	}
}

// --- Helpers ---

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

// optionalUserID reads the caller for a read that a signed-out viewer may also
// make (a public group's rules, channels, wiki). No header means anonymous
// (uuid.Nil, which the access gate admits to public groups only); a header that
// is present but not a uuid is refused, never read as anonymous.
func optionalUserID(c *gin.Context) (uuid.UUID, bool) {
	if strings.TrimSpace(c.GetHeader("X-User-Id")) == "" {
		return uuid.Nil, true
	}
	return getUserID(c)
}

func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(c.DefaultQuery("offset", "0")); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}

// resolveIdempotencyKey picks the caller's idempotency key: the snake_case
// idempotency_key body field wins, and the Idempotency-Key HTTP header is the
// fallback. Both are trimmed; an all-whitespace value counts as absent so a
// client cannot satisfy the guard with " ". Returns "" when neither is usable,
// which the caller turns into IDEMPOTENCY_KEY_REQUIRED.
func resolveIdempotencyKey(c *gin.Context, bodyKey string) string {
	if k := strings.TrimSpace(bodyKey); k != "" {
		return k
	}
	return strings.TrimSpace(c.GetHeader("Idempotency-Key"))
}

func handleServiceError(c *gin.Context, err error) {
	if writeMediaError(c, err) {
		return
	}
	msg := err.Error()
	switch {
	case contains(msg, "forbidden"), contains(msg, "only admins"), contains(msg, "only the group"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", msg, nil)
	case contains(msg, "not found"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil)
	case contains(msg, "not a member"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil)
	case contains(msg, "rate_limited"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", msg, nil)
	case contains(msg, "already"), contains(msg, "is already"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "CONFLICT", msg, nil)
	case contains(msg, "must be between"), contains(msg, "must contain"), contains(msg, "reserved"),
		contains(msg, "invalid"), contains(msg, "not allowed"), contains(msg, "maximum"),
		contains(msg, "no users"), contains(msg, "no longer pending"),
		contains(msg, "does not accept"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", msg, nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", msg, nil)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
