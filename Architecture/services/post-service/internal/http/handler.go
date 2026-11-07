// handler.go holds the Handler type, its constructor and options, and
// RegisterRoutes, the one place every post route is registered (the order
// matters: see action_links_routes_test.go). The handlers themselves live
// in one file per route group beside it (create_post_handler.go,
// post_reads_handler.go, comments_handler.go, reactions_handler.go and so
// on).

package http

import (
	"github.com/atpost/post-service/internal/http/middleware"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/streamhub"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	svc                *service.Service
	rdb                *redis.Client
	hub                *streamhub.Hub
	internalKey        string
	moderationVerifier *moderationcap.Verifier
	// verifier admits admin-service tokens on /v1/posts/internal/admin
	// (admin_token.go) and trust-safety tokens on the restrictions read
	// (post_restrictions_handler.go). nil: no token is accepted.
	verifier *servicetoken.Verifier
	// restrictionVerifier admits post_restriction capabilities on
	// POST /v1/posts/internal/restrictions. nil: every command is refused.
	restrictionVerifier *moderationcap.RestrictionVerifier
	// restrictions overrides the service as the restriction store (tests).
	restrictions restrictionStore
	// places is the location search (places_handler.go). nil: 503.
	places placeSearcher
}

func New(svc *service.Service, rdb *redis.Client) *Handler {
	return &Handler{svc: svc, rdb: rdb}
}

// WithStreamHub installs the shared Redis-pubsub fan-out hub. Both
// SSE handlers (hashtag per-tag stream, trending stream) attach to
// it instead of opening a Redis SUB per HTTP connection. Falling back
// gracefully — if no hub is set the old per-connection path runs.
func (h *Handler) WithStreamHub(hub *streamhub.Hub) *Handler {
	h.hub = hub
	return h
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) WithModerationVerifier(verifier *moderationcap.Verifier) *Handler {
	h.moderationVerifier = verifier
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	idempotent := middleware.Idempotency(h.rdb)

	// Apply internal service key enforcement to all /v1 routes.
	// Health and metrics endpoints registered outside this group remain public.
	//
	// The one exception is the admin-service token family (admin_token.go):
	// the key proves nothing there (the gateway stamps it on edge traffic), so
	// those routes are judged ONLY by the signed token. The exemption is keyed
	// on the MATCHED route (FullPath), never the raw URL, and every route
	// under the prefix is registered with requireAdminToken.
	if h.internalKey != "" {
		keyGate := sharedmiddleware.RequireInternalKey(h.internalKey)
		r.Use(func(c *gin.Context) {
			if isAdminTokenRoute(c.FullPath()) {
				c.Next()
				return
			}
			keyGate(c)
		})
	}
	h.registerAdminTokenRoutes(r)

	v1 := r.Group("/v1/posts")
	{
		v1.POST("", h.CreatePost)
		v1.POST("/batch", h.BatchGetPosts)
		v1.GET("/categories", h.ListCategories)
		v1.GET("/recent", h.GetRecentPosts)
		// MTube (2026-09-27): public listing of promoted live recordings
		// (source = 'live'); same hydrated rows as by-author.
		v1.GET("/live-recordings", h.GetLiveRecordings)
		// Original sounds (2026-09-29, sounds.go): the reels that play a
		// sound, and "use this sound" on a reel.
		v1.GET("/by-sound/:soundId", h.GetPostsBySound)
		v1.POST("/:postId/sound", h.UseSound)
		v1.GET("/bookmarks", h.GetBookmarks)
		v1.GET("/by-author/:authorId", h.GetPostsByAuthor)
		v1.GET("/by-author/:authorId/counts", h.GetAuthorCounts)
		// Offline copies inside the app (2026-10-02, offline_copies.go). The
		// two static routes sit beside /:postId and resolve as static:
		// "offline" is never read as a post id.
		v1.GET("/offline", h.ListOfflineCopies)
		v1.POST("/offline/check", h.CheckOfflineCopies)
		v1.POST("/:postId/offline", h.GrantOfflineCopy)
		v1.DELETE("/:postId/offline", h.RemoveOfflineCopy)
		// The Action button (action_links.go). action-labels is static and
		// registered before /:postId, so it is never read as a post id
		// (route_order test: action_links_routes_test.go).
		v1.GET("/action-labels", h.ListActionLabels)
		v1.GET("/:postId/action/stats", h.GetPostActionStats)
		v1.GET("/:postId", h.GetPost)
		// Soft delete → "Recently deleted" → restore (2026-09-04). DELETE
		// keeps rows and media for POST_PURGE_AFTER; the purge worker
		// (internal/postpurge) hard-deletes after that.
		v1.DELETE("/:postId", h.DeletePost)
		v1.POST("/:postId/restore", h.RestorePost)
		v1.GET("/me/deleted", h.ListMyDeletedPosts)
		// Scheduled publish (2026-09-05, schedule.go): the author's list and
		// reschedule / publish-now. DELETE above soft-deletes a scheduled post.
		v1.GET("/me/scheduled", h.ListMyScheduledPosts)
		v1.PATCH("/:postId/schedule", h.UpdateSchedule)
		v1.PUT("/:postId/pin", h.TogglePin)
		// MTube (2026-09-27, post_edit.go): edit after publish, the
		// creator summary, and the viewer's Queue.
		v1.PATCH("/:postId", h.UpdatePost)
		v1.GET("/me/summary", h.GetCreatorSummary)
		// Creator Hub private sharing (2026-09-28, post_edit.go): owner only.
		v1.GET("/:postId/private-shares", h.GetPrivateShares)
		v1.PUT("/:postId/private-shares", h.PutPrivateShares)
		v1.POST("/:postId/watch-later", h.AddWatchLater)
		v1.DELETE("/:postId/watch-later", h.RemoveWatchLater)

		// Legacy reaction routes (kept for backward compat)
		v1.POST("/:postId/reactions", h.React)
		v1.DELETE("/:postId/reactions", h.Unreact)
		v1.GET("/:postId/reactions/me", h.GetMyReaction)

		// New engagement routes
		v1.POST("/:postId/like", h.ToggleLike)
		v1.POST("/:postId/share", h.SharePost)
		v1.POST("/:postId/comments", idempotent, h.AddComment)
		v1.GET("/:postId/comments", h.ListComments)
		v1.GET("/:postId/comments/around/:commentId", h.ListCommentsAround)
		// PUT-like semantics on POST for mobile retries: repeated calls keep the
		// bookmark set. DELETE is the inverse. The former toggle made a network
		// retry silently undo the user's first successful tap.
		v1.POST("/:postId/bookmark", h.AddBookmark)
		v1.DELETE("/:postId/bookmark", h.RemoveBookmark)
		// Saved collections (saved_collections_handler.go): move a saved
		// post to a collection, addressed by the post.
		v1.PUT("/:postId/bookmark/collection", h.MoveBookmark)
		v1.POST("/:postId/resubmit", h.Resubmit)
		// RETIRED (security sweep #12, 21 Oct 2026): POST /:postId/moderation
		// granted moderator rights from the X-Scopes header alone. The admin
		// console's decision write is the token-only route
		// /v1/posts/internal/admin/posts/:postId/moderation (admin_token.go).
		v1.GET("/:postId/poll", h.GetPoll)
		v1.POST("/:postId/vote", h.CastVote)

		// Poll results (new)
		v1.POST("/:postId/poll/vote", h.CastPollVote)
		v1.GET("/:postId/poll/results", h.GetPollResults)

		// Tune (private negative signal)
		v1.POST("/:postId/tune", h.CreateTune)
		v1.DELETE("/:postId/tune", h.DeleteTune)
		v1.GET("/:postId/tune/me", h.GetTune)

		// Distribution policy (Module 1 P0-1) — owner-only.
		v1.PATCH("/:postId/distribution", h.UpdateDistribution)

		// Threads (Module 1 P0-8).
		v1.POST("/thread", h.CreateThread)
		v1.GET("/:postId/thread", h.GetThread)

		// The watch page's series strip: which series is this post in, and
		// what are its neighbours. Registered here rather than under
		// /v1/video-series because the caller holds a post id, not a series
		// id. See GetPostSeries for the 404-not-403 rule that follows.
		v1.GET("/:postId/series", h.GetPostSeries)
	}

	// Internal WRITES (security sweep #11, 21 Oct 2026). The gateway refuses
	// every /internal/ path at the edge, but it also stamps the shared
	// X-Internal-Service-Key on every proxied request, so the key alone is
	// no evidence of who is calling. Each write therefore needs a signed
	// service token from its one caller (requireServiceToken), the way the
	// admin family and the reconciliation read already did:
	//
	//   review-status, visibility   reviewer-service  (ML pre-filter, promotion worker)
	//   moderation                  trust-safety      (appeal overturn; plus the post_moderation capability)
	//   restrictions (POST)         trust-safety      (copyright holds; plus the post_restriction capability)
	r.POST("/v1/posts/internal/review-status", h.requireServiceToken(IssuerReviewerService, OpReviewStatusWrite), h.SetReviewStatusInternal)
	r.POST("/v1/posts/internal/visibility", h.requireServiceToken(IssuerReviewerService, OpVisibilityWrite), h.SetVisibilityInternal)
	r.GET("/v1/posts/internal/moderation-subject/:postId", h.GetModerationSubjectInternal)
	r.POST("/v1/posts/internal/moderation", h.requireTrustSafetyToken(OpModerationWrite), h.ModeratePostInternal)
	// Case-specific restrictions (post_restrictions_handler.go): the
	// signed command from trust-safety, and its reconciliation read.
	r.POST("/v1/posts/internal/restrictions", h.requireTrustSafetyToken(OpRestrictionsWrite), h.ApplyPostRestriction)
	r.GET("/v1/posts/internal/restrictions", h.requireTrustSafetyToken(OpRestrictionsRead), h.ListPostRestrictions)
	//   copyright/owner-match       trust-safety      (a fingerprint provider's owner match; copyright_owner_match_internal.go)
	h.registerCopyrightOwnerMatchInternalRoute(r)

	// The Action button's public redirect read (action_links.go): no
	// identity required; the gateway routes /v1/links here.
	r.GET("/v1/links/:id", h.ResolveActionLink)

	// Events
	r.POST("/v1/events", h.CreateEvent)
	r.GET("/v1/events/:eventId", h.GetEvent)
	r.POST("/v1/events/:eventId/rsvp", h.RSVPEvent)
	r.GET("/v1/events/:eventId/rsvps", h.GetEventRSVPs)

	// M4-P0-5 — the content-authority answer for byte delivery.
	//
	// Registered outside the /v1/stories group on purpose: it is not a story
	// surface (the story route inventory would otherwise demand a viewer
	// policy for it), and media-service is its only intended caller. The
	// internal-key middleware applied to all /v1 routes above is what gates it.
	r.POST("/v1/internal/media-access", h.MediaAccess)
	r.POST("/v1/internal/media-access/batch", h.MediaAccessBatch)
	r.GET("/v1/internal/media-download-allowed", h.MediaDownloadAllowed) // media-service download gate (media_download_internal.go)
	// ws-gateway asks this before admitting a socket to the post:<id> room.
	r.GET("/v1/internal/posts/:id/visibility", h.PostVisibility)
	r.GET("/v1/internal/posts/by-media/:mediaId", h.PostsByMediaInternal) // search-service has_subtitles fan-out (posts_by_media_internal.go)
	// media-service's trending-sounds worker: every sound's daily uses (sound_uses_internal.go).
	r.GET("/v1/internal/sound-uses", h.SoundUsesInternal)
	// live-service-v2 asks which video a recording became (post_by_live_stream_internal.go).
	r.GET("/v1/internal/posts/by-live-stream/:streamId", h.PostByLiveStreamInternal)

	// Tube channels (channels_handler.go): one per account, gate for long videos.
	h.registerChannelRoutes(r)
	h.registerTubeCatalogueInternalRoutes(r) // MTube search reindex source (channels_internal_list.go)

	// Stories
	stories := r.Group("/v1/stories")
	{
		stories.POST("", h.CreateStory)
		stories.GET("/feed", h.GetStoriesFeed)
		// Owner-only truthful state. Registered before /:storyId so "mine" is
		// not captured as a story id.
		stories.GET("/mine", h.GetMyStoryStatus)
		stories.GET("/author/:authorId", h.GetStoriesByAuthor)
		stories.GET("/:storyId", h.GetStory)
		stories.DELETE("/:storyId", h.DeleteStory)
		stories.POST("/:storyId/view", h.ViewStory)
	}

	// Multi-reactions (new)
	v1.POST("/:postId/react", h.ToggleReaction)
	v1.GET("/:postId/reactions/counts", h.GetReactionCounts)

	// Saved items
	saved := r.Group("/v1/saved")
	{
		saved.POST("", h.SaveItem)
		saved.GET("", h.ListSavedItems)
		saved.DELETE("/:savedId", h.UnsaveItem)
		// Saved collections (2026-10-24, saved_collections_handler.go).
		saved.GET("/collections", h.ListSavedCollections)
		saved.POST("/collections", h.CreateSavedCollection)
		saved.PATCH("/collections/:collectionId", h.RenameSavedCollection)
		saved.DELETE("/collections/:collectionId", h.DeleteSavedCollection)
		saved.PUT("/:savedId/collection", h.MoveSavedItem)
	}

	// Hashtag search
	// Note: /v1/feed routes to feed-service in the gateway, so the trending
	// hashtag endpoint lives under /v1/hashtags/trending to keep it in
	// post-service where the data lives.
	r.GET("/v1/hashtags/trending", h.GetTrendingHashtagsFeed)
	// Live top-N stream — debounced 30 s pushes from
	// internal/trending.Publisher; implementation in trending_stream.go.
	r.GET("/v1/hashtags/trending/stream", h.StreamTrendingHashtags)
	r.GET("/v1/posts/trending", h.GetTrendingPosts)
	// Spec §7.6: 30 req/min per user, burst small. ~0.5 req/sec sustained.
	hashtagSearchLimit := sharedmiddleware.RateLimit(sharedmiddleware.RateLimitConfig{
		IPRate: 0.5, IPBurst: 5,
		UserRate: 0.5, UserBurst: 5,
	})
	r.GET("/v1/hashtags/search", hashtagSearchLimit, h.SearchHashtags)
	// Location search (places_handler.go): every call is a billed Google
	// request, so the same small per-user budget as hashtag search.
	placesLimit := sharedmiddleware.RateLimit(sharedmiddleware.RateLimitConfig{
		IPRate: 1, IPBurst: 10,
		UserRate: 1, UserBurst: 10,
	})
	r.GET("/v1/posts/places/autocomplete", placesLimit, h.AutocompletePlaces)
	r.GET("/v1/hashtags/:tag/posts", h.GetPostsByHashtag)
	// Followed hashtags (2026-10-24, hashtag_follows_handler.go). The two
	// static "followed" routes resolve before :tag.
	r.GET("/v1/hashtags/followed", h.ListFollowedHashtags)
	r.GET("/v1/hashtags/followed/posts", h.GetFollowedHashtagPosts)
	r.GET("/v1/hashtags/:tag/follow", h.GetHashtagFollow)
	r.POST("/v1/hashtags/:tag/follow", h.FollowHashtag)
	r.DELETE("/v1/hashtags/:tag/follow", h.UnfollowHashtag)
	// Real-time hashtag-feed push. Holds an SSE connection open and
	// forwards every new post that includes :tag (via Redis pub/sub
	// channel `hashtag:<tag>:new_post`). Implementation lives in
	// hashtag_stream.go.
	r.GET("/v1/hashtags/:tag/stream", h.StreamHashtagPosts)

	// Video creator tools
	videos := r.Group("/v1/videos")
	{
		videos.GET("/:videoId", h.GetVideoDetail)
		videos.PATCH("/:videoId/trim", h.UpdateTrim)
		videos.PATCH("/:videoId/category", h.OverrideCategory)
		videos.POST("/:videoId/cover-frame", h.SetCoverFrame)
		videos.POST("/:videoId/publish", h.PublishVideo)
	}

	// Comment-level routes
	comments := r.Group("/v1/comments")
	{
		comments.POST("/:commentId/reply", idempotent, h.CreateReply)
		// Replies for everyone + emoji reactions (2026-09-27, comment_routes.go).
		comments.GET("/:commentId/replies", h.ListReplies)
		comments.PUT("/:commentId/reaction", h.SetCommentReaction)
		comments.DELETE("/:commentId/reaction", h.RemoveCommentReaction)
		// Legacy like: toggles the viewer's reaction between ❤️ and none on
		// the same comment_reactions table. Dislike keeps its own path.
		comments.POST("/:commentId/like", h.ToggleCommentLike)
		comments.POST("/:commentId/dislike", h.ToggleCommentDislike)
		comments.DELETE("/:commentId", h.DeleteComment)
		comments.PATCH("/:commentId", h.EditComment)
		// Creator tools (2026-09-27, comment_creator.go). "inbox" is static
		// and registered alongside :commentId; gin resolves the static
		// child first, so it is never read as a comment id.
		comments.GET("/inbox", h.ListCreatorInbox)
		comments.POST("/:commentId/heart", h.HeartComment)
		comments.DELETE("/:commentId/heart", h.UnheartComment)
		comments.PUT("/:commentId/pin", h.PinComment)
		comments.DELETE("/:commentId/pin", h.UnpinComment)
	}

	// Flick Series
	series := r.Group("/v1/series")
	{
		series.POST("", h.CreateFlickSeries)
		series.GET("/:seriesId", h.GetFlickSeries)
		series.GET("/:seriesId/episodes", h.GetSeriesEpisodes)
		series.POST("/:seriesId/episodes", h.AddEpisodeToSeries)
		series.POST("/:seriesId/follow", h.FollowSeries)
		series.DELETE("/:seriesId/follow", h.UnfollowSeries)
	}
	// Creator's series list
	r.GET("/v1/creators/:creatorId/series", h.ListCreatorSeries)

	// Remix token
	v1.GET("/:postId/remix-token", h.GetRemixToken)

	// Video Series
	vseries := r.Group("/v1/video-series")
	{
		vseries.POST("", h.CreateVideoSeries)
		vseries.GET("/:seriesId", h.GetVideoSeries)
		vseries.GET("/:seriesId/episodes", h.GetVideoSeriesEpisodes)
		vseries.POST("/:seriesId/episodes", h.AddVideoSeriesEpisode)
		// Retitle, describe, mark complete, make public: a series was
		// create-only before, so a typo in the title was permanent.
		vseries.PATCH("/:seriesId", h.UpdateVideoSeries)
		// A series has to be able to shrink. Without these two a series
		// created by mistake was permanent and an episode added by mistake
		// could only be overwritten — playlists, below, have had both deletes
		// all along. :episodeRef takes either an episode number or a post id;
		// see DeleteVideoSeriesEpisode.
		vseries.DELETE("/:seriesId", h.DeleteVideoSeries)
		vseries.DELETE("/:seriesId/episodes/:episodeRef", h.DeleteVideoSeriesEpisode)
	}
	r.GET("/v1/creators/:creatorId/video-series", h.ListCreatorVideoSeries)

	// Playlists
	playlists := r.Group("/v1/playlists")
	{
		playlists.POST("", h.CreatePlaylist)
		// The caller's server-owned collections (2026-09-27,
		// post_edit.go): static "system" beside :playlistId.
		playlists.GET("/system/:kind", h.GetSystemPlaylist)
		playlists.GET("/:playlistId", h.GetPlaylist)
		// A playlist could be created and added to but never edited: no
		// rename, no re-description, no cover, no reorder and no way to
		// make one public or private after the fact.
		playlists.PATCH("/:playlistId", h.UpdatePlaylist)
		playlists.DELETE("/:playlistId", h.DeletePlaylist)
		playlists.POST("/:playlistId/items", h.AddPlaylistItem)
		playlists.PATCH("/:playlistId/items/:postId", h.MovePlaylistItem)
		playlists.DELETE("/:playlistId/items/:postId", h.RemovePlaylistItem)
		playlists.GET("/:playlistId/items", h.GetPlaylistItems)
	}
	r.GET("/v1/creators/:creatorId/playlists", h.ListCreatorPlaylists)

	// Product feedback (distinct from trust-safety reports).
	r.POST("/v1/feedback", h.SubmitFeedback)

	// Chapters / end screens / cards
	r.POST("/v1/posts/:postId/chapters", h.SaveChapters)
	r.GET("/v1/posts/:postId/chapters", h.GetChapters)
	r.POST("/v1/posts/:postId/end-screens", h.SaveEndScreens)
	r.GET("/v1/posts/:postId/end-screens", h.GetEndScreens)
	r.POST("/v1/posts/:postId/cards", h.SaveVideoCards)
	r.GET("/v1/posts/:postId/cards", h.GetVideoCards)
	// Impressions and clicks (2026-09-29, end_screens_handler.go): signed-in
	// or anonymous, one of each per viewer per element per day. 204.
	r.POST("/v1/posts/:postId/end-screens/:elementId/impression", h.RecordEndScreenImpression)
	r.POST("/v1/posts/:postId/end-screens/:elementId/click", h.RecordEndScreenClick)
	r.POST("/v1/posts/:postId/cards/:cardId/impression", h.RecordCardImpression)
	r.POST("/v1/posts/:postId/cards/:cardId/click", h.RecordCardClick)
	r.PUT("/v1/posts/:postId/membership", h.SetPostMembershipGate)
	// Copyright title review (copyright_review.go): the author's
	// "I own the rights" note on their held video.
	r.POST("/v1/posts/:postId/copyright-review/request", h.RequestCopyrightReview)

	// Watch Progress. The static segments (continue-watching, history) are
	// registered ahead of the :videoId routes so neither is ever read as a
	// video id; gin matches static children first regardless, but the
	// order here says what is intended.
	r.GET("/v1/videos/continue-watching", h.GetContinueWatching)
	// History (2026-09-12): the full record, completed rows included,
	// paged, and clearable in one call. See GetWatchHistory.
	r.GET("/v1/videos/history", h.GetWatchHistory)
	r.DELETE("/v1/videos/history", h.ClearWatchHistory)
	r.POST("/v1/videos/:videoId/progress", h.SaveWatchProgress)
	r.GET("/v1/videos/:videoId/progress", h.GetWatchProgress)
	r.DELETE("/v1/videos/:videoId/progress", h.DeleteWatchProgress)

	// In-video product tags (affiliate overlays). See
	// internal/http/product_tags_handler.go for the contract.
	r.POST("/v1/posts/:postId/product-tags", h.CreateProductTag)
	r.GET("/v1/posts/:postId/product-tags", h.ListProductTagsByPost)
	r.DELETE("/v1/posts/:postId/product-tags/:tagId", h.DeleteProductTag)
	r.POST("/v1/posts/:postId/product-tags/:tagId/impression", h.RecordProductTagImpression)
	r.POST("/v1/posts/:postId/product-tags/:tagId/click", h.RecordProductTagClick)
	r.GET("/v1/creators/:creatorId/product-tags", h.ListProductTagsByCreator)

	// Video credits (2026-10-23, credits.go): the genre list, the tagged
	// person's requests and answers, and "Appears in".
	h.registerCreditRoutes(r)
}
