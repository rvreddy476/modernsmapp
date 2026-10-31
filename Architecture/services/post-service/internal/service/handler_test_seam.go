package service

import (
	"context"
	"net/http"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// HandlerTestDeps are the in-memory stores and seams the Creator Hub
// handler tests (internal/http) drive the real router with. Every field is
// optional; an unset store leaves its flows failing closed exactly as an
// unwired production Service would.
type HandlerTestDeps struct {
	PostEdits         postEditStore
	PrivateShares     privateShareStore
	Authoring         videoAuthoringStore
	BirthDates        birthDateSource
	ReadGate          func(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) error
	BulkDelete        func(ctx context.Context, postID, callerID uuid.UUID) error
	ProfileServiceURL string
	Now               func() time.Time

	// End screens and cards (2026-09-29): the store, the channel store the
	// subscribe / channel elements read, the per-day dedupe, and PostCard in
	// place of viewablePostCard (nil = not this viewer's to open). The
	// series stores back the episode-list drops.
	EndScreens  endScreenStore
	Channels    channelStore
	StatDedupe  statDeduper
	PostCard    func(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) *RelatedPostCard
	VideoSeries videoSeriesStore
	FlickSeries flickSeriesStore
	// GraphServiceURL / AnalyticsServiceURL point the privacy gate and the
	// view counts at httptest fakes.
	GraphServiceURL     string
	AnalyticsServiceURL string
	// MediaAccess backs /v1/internal/media-access (media_access.go); nil
	// leaves the gate unresolved (503), never allowed.
	MediaAccess mediaAccessStore
	// Channel feed (channel_feed.go): the store, the media-record source in
	// place of media-service, and the hidden-author list the account gate
	// reads.
	ChannelFeed   channelFeedStore
	FeedMedia     feedMediaSource
	HiddenAuthors hiddenAuthorsStore
	// Original sounds (sounds.go): the reads, the media state of a listed
	// page, its counts in place of Scylla, and media-service itself, which
	// the tests stand up as an httptest server (MediaServiceURL, reached
	// with InternalServiceKey).
	SoundReads         soundStore
	SoundAudience      func(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]bool, error)
	MediaStates        mediaStateStore
	SoundCounts        func(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error)
	MediaServiceURL    string
	InternalServiceKey string
	// SoundUses backs GET /v1/internal/sound-uses (sound_uses.go).
	SoundUses soundUsesStore
	// SoundExtras stands in for media-service's cover and trending badge on
	// the sound page; nil asks media-service at MediaServiceURL.
	SoundExtras func(ctx context.Context, soundID uuid.UUID) (*SoundExtras, error)
	// Offline copies (offline_copies.go): the store, the relationship graph
	// the playback decision reads, the caption-track list in place of
	// media-service and the members-only answer in place of monetization.
	Offline            offlineStore
	Relationships      GraphRelationships
	OfflineCaptions    offlineCaptionSource
	OfflineEntitlement func(ctx context.Context, viewerID uuid.UUID, p *postgres.Post) (bool, error)
	// LikedReels backs GET /v1/reels/liked (liked_reels.go).
	LikedReels likedReelsStore
	// Video credits (credits.go): the store, the link checks in place of
	// identity-profile + graph-service, the accounts the read names, the
	// batch read behind "Appears in" and its profile gate.
	Credits               creditStore
	CreditLinks           creditLinker
	CreditProfiles        func(ctx context.Context, viewerID *uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]CreditProfile, error)
	AppearancePosts       func(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error)
	AppearanceProfileGate func(ctx context.Context, viewerID *uuid.UUID, userID uuid.UUID) bool
	// Members-only reads (members_read.go): the batch read's loading (the
	// redaction still runs after it), the (author, tier) lookup behind the
	// comment gate, the PUT /membership store, and monetization-service
	// itself (an httptest fake) for the real entitlement path.
	BatchPosts             func(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error)
	PostGate               func(ctx context.Context, postID uuid.UUID) (uuid.UUID, *uuid.UUID, error)
	MembershipGates        membershipGateStore
	MonetizationServiceURL string
	// CreatorTiers stands in for monetization-service's public tier list
	// (members_tiers.go): the gate check on writes and the required price on
	// gated rows. nil reads monetization-service at MonetizationServiceURL.
	CreatorTiers func(ctx context.Context, creatorID uuid.UUID) ([]CreatorTierRef, error)
	// CreateIdempotencyLookup stands in for the create route's replay read,
	// so POST /v1/posts reaches its guards without a database.
	CreateIdempotencyLookup func(ctx context.Context, actorID uuid.UUID, key string) (uuid.UUID, string, bool, error)
	// EngagementPost stands in for loadPostForEngagement and CommentLookup
	// for the visible-comment read (the comment routes' members gate).
	EngagementPost func(ctx context.Context, postID, viewerID uuid.UUID) (*postgres.Post, error)
	CommentLookup  func(ctx context.Context, commentID uuid.UUID) (*postgres.Comment, error)
	// ActionLinks backs the Action button (action_links.go); the rest of its
	// wiring (checker, eligibility sources, tap dedupe) is SetActionLinks.
	ActionLinks actionLinkStore
	// TitleReviews backs the copyright title review routes
	// (copyright_title_review.go).
	TitleReviews titleReviewStore
	// A creator's Reposts tab (creator_reposts.go): the store page and the
	// reposter's profile gate (nil = canViewAuthor against GraphServiceURL).
	UserReposts       func(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]postgres.Repost, string, error)
	RepostProfileGate func(ctx context.Context, viewerID *uuid.UUID, userID uuid.UUID) bool
	// Pins backs PUT /v1/posts/:postId/pin (TogglePin).
	Pins pinStore
	// SavedCollections backs the saved-collection routes
	// (saved_collections.go); HashtagFollows the hashtag-follow routes
	// (hashtag_follows.go).
	SavedCollections savedCollectionStore
	HashtagFollows   hashtagFollowStore
	// Heart-only and no reel repost (heart_only.go): the post the repost
	// routes read, and the engagement rate limiter (nil leaves the routes
	// that rate-limit unusable, as before).
	RepostTarget func(ctx context.Context, postID uuid.UUID) (*postgres.Post, error)
	RateLimiter  *engagement.RateLimiter
	// The tube audit (tube_audit.go): the playlist add's store and the
	// chapters store.
	PlaylistItems playlistItemWriter
	Chapters      chapterStore
	// The 31 Oct founder decisions: the product-tag read behind
	// GET /v1/posts/:postId/product-tags (tube_members_extras.go).
	ProductTags func(ctx context.Context, postID uuid.UUID) ([]*postgres.PostProductTag, error)
}

// CreditProfile is one identity-profile account as the credit reads name it.
type CreditProfile = commentProfile

// NewForHandlerTests builds a Service over HandlerTestDeps, with no
// Postgres, Scylla or Redis behind it. For tests only; main never calls it.
func NewForHandlerTests(d HandlerTestDeps) *Service {
	s := &Service{
		postEdits:           d.PostEdits,
		privateShare:        d.PrivateShares,
		birthDates:          d.BirthDates,
		readGate:            d.ReadGate,
		bulkDelete:          d.BulkDelete,
		profileServiceURL:   d.ProfileServiceURL,
		now:                 d.Now,
		httpClient:          &http.Client{Timeout: 5 * time.Second},
		endScreens:          d.EndScreens,
		channels:            d.Channels,
		statDedupe:          d.StatDedupe,
		postCardSeam:        d.PostCard,
		videoSeries:         d.VideoSeries,
		flickSeries:         d.FlickSeries,
		graphServiceURL:     d.GraphServiceURL,
		analyticsServiceURL: d.AnalyticsServiceURL,
		mediaAccess:         d.MediaAccess,
		channelFeed:         d.ChannelFeed,
		feedMedia:           d.FeedMedia,
		soundReads:          d.SoundReads,
		soundAudience:       d.SoundAudience,
		mediaStates:         d.MediaStates,
		soundCounts:         d.SoundCounts,
		mediaServiceURL:     d.MediaServiceURL,
		internalServiceKey:  d.InternalServiceKey,
		soundUses:           d.SoundUses,
		soundExtrasSeam:     d.SoundExtras,

		offline:              d.Offline,
		offlineCaptionTracks: d.OfflineCaptions,
		offlineEntitlement:   d.OfflineEntitlement,
		likedReels:           d.LikedReels,

		credits:               d.Credits,
		creditLinks:           d.CreditLinks,
		creditProfiles:        d.CreditProfiles,
		appearancePosts:       d.AppearancePosts,
		appearanceProfileGate: d.AppearanceProfileGate,

		batchPostsSeam:         d.BatchPosts,
		postGateSeam:           d.PostGate,
		membershipGates:        d.MembershipGates,
		monetizationServiceURL: d.MonetizationServiceURL,
		creatorTiersSeam:       d.CreatorTiers,
		createIdemLookupSeam:   d.CreateIdempotencyLookup,
		engagementPostSeam:     d.EngagementPost,
		commentLookupSeam:      d.CommentLookup,
		actionLinks:            d.ActionLinks,
		titleReviews:           d.TitleReviews,
		userRepostsSeam:        d.UserReposts,
		repostProfileGate:      d.RepostProfileGate,
		pins:                   d.Pins,
		savedCollections:       d.SavedCollections,
		hashtagFollows:         d.HashtagFollows,
		repostTargetSeam:       d.RepostTarget,
		rateLimiter:            d.RateLimiter,
	}
	if d.Relationships != nil {
		s.storyAudience = NewStoryAudience(d.Relationships)
	}
	s.hiddenAuthors = d.HiddenAuthors
	s.authoringOwners = d.Authoring
	s.playlistItemsSeam = d.PlaylistItems
	s.chapterStoreSeam = d.Chapters
	s.productTagListSeam = d.ProductTags
	return s
}
