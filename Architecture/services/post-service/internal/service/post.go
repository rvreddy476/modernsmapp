// post.go is the core of the post service: the Service type, its
// constructor and setters, the engagement counters and their adjusters,
// the package-level errors, and the feed hydration cache invalidation.
// Each concern of the service lives in its own post_*.go file beside it
// (create, reads, visibility, comments, engagement, reactions, reposts,
// review, video tools, content types, hashtags and mentions).

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/spam"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/counters"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	hashtagRegex = regexp.MustCompile(`#([\p{L}\p{M}\p{N}_]{2,50})`)
	// mentionRegex is the caption parser. Same alphabet as dbMentionRegex and
	// explicitMentionPattern: handles like `call.usera` are real usernames, so
	// `.` is allowed; a trailing `.` (sentence end) is trimmed in extractMentions.
	mentionRegex = regexp.MustCompile(`@([A-Za-z0-9_.]{1,30})`)
	// dbMentionRegex is the persistence-side mention pattern — 3+
	// chars and `.` allowed (handles handles like `john.doe`).
	// Compiled once at startup; previously this re-compiled on every
	// post create from inside DetectAndStoreMentions.
	dbMentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_.]{3,30})`)

	// ErrPostNotVisible is returned when a viewer tries to react /
	// bookmark / engage with a post whose visibility excludes them
	// (private, or followers-only when the viewer doesn't follow).
	// Audit C5 — engagement endpoints used to skip this entirely
	// and leak engagement counts for restricted-visibility posts.
	// (ErrPostNotFound is declared in my_uploads.go and reused here.)
	ErrPostNotVisible = errors.New("post not visible to this user")

	// ErrLikesDisabled / ErrCommentsDisabled surface the per-post
	// engagement flags. Pushed into the service layer (was: handler-
	// layer GetPost round trip) per audit H2 so engagement no longer
	// double-fetches the post.
	ErrLikesDisabled = errors.New("likes are disabled on this post")

	// ErrCreateKeyReused: this actor already used this Idempotency-Key for a
	// DIFFERENT payload (C-LB-3.5). Distinct from a replay, which succeeds.
	ErrCreateKeyReused  = errors.New("idempotency key already used with different content")
	ErrCommentsDisabled = errors.New("comments are disabled on this post")

	// ErrCommentsRestricted: the author's comments audience
	// (allow_comments_from = friends) excludes this viewer. Distinct from
	// ErrCommentsDisabled — the post accepts comments, just not from
	// strangers — so the client can render "Only friends can comment".
	ErrCommentsRestricted = errors.New("only friends can comment on this post")
)

type Service struct {
	// outboxNudge asks the outbox worker to drain immediately instead of
	// waiting out its 5s tick. Buffered at one: a pending drain makes a second
	// signal redundant. See NudgeOutbox in outbox.go.
	outboxNudge       chan struct{}
	pgStore           *postgres.Store
	scyllaStore       *scylla.InteractionStore
	scyllaSession     *gocql.Session
	rdb               *redis.Client
	producer          *postEvents.Producer // legacy producer, optional
	engProducer       *engagement.Producer // new engagement event producer
	rateLimiter       *engagement.RateLimiter
	spam              *spam.Detector
	userServiceURL    string
	profileServiceURL string
	mediaServiceURL   string
	// draftAltTexts writes a draft's alt_texts onto its media at publish
	// (draft_alt_texts.go); nil = media-service over HTTP.
	draftAltTexts          draftAltTextWriter
	graphServiceURL        string
	monetizationServiceURL string
	reviewerServiceURL     string
	// standing is the trust-safety standing client consulted on every
	// publication path (publish_standing.go). nil = unconfigured, which
	// fails closed unless requireStandingCheck is false.
	standing            StandingChecker
	standingBackoff     *standingBackoff
	standingBackoffOnce sync.Once
	// analyticsServiceURL serves the visible view count on a post
	// (view_counts.go). Empty means every count is 0, with a warning.
	analyticsServiceURL string
	viewCountMu         sync.Mutex
	viewCountCache      map[uuid.UUID]viewCountEntry
	// requireStandingCheck makes a MISSING standing client block every
	// publication instead of letting it through (Codex P2-1, founder F-7).
	// It is the dev-only opt-out; a wired client is always consulted.
	requireStandingCheck bool
	reviewAllVideos      bool
	internalServiceKey   string
	httpClient           *http.Client

	// claims registers the media a non-FK column holds (post covers, reel
	// drafts, event covers) with media-service, so the uploader cannot
	// delete it from under the row (media_claims.go). Nil refuses every
	// write that ADDS such media: fail closed.
	claims MediaClaims
	// mediaHolders locks and re-reads a holder row for the claim protocol;
	// ownMedia is the ownership read that gates a claim. *postgres.Store.
	mediaHolders mediaHolderStore
	ownMedia     mediaOwnershipReader
	// postDraftRows is the composer draft rows the claimed writers write
	// (post_draft_cover_claims.go). *postgres.Store; nil falls back to
	// pgStore.
	postDraftRows postDraftRows
	// The scheduled-draft workers' seams (post_draft_queue.go).
	draftQueueSeams
	// The tube audit's seams (tube_audit.go, 2026-10-30).
	tubeAuditSeams
	// The 31 Oct founder decisions' seams (tube_cover_claims.go).
	tubeDecisionSeams

	// purgeAfter is the "Recently deleted" window: a soft-deleted post can
	// be restored until deleted_at + purgeAfter, after which the purge
	// worker hard-deletes it. Set from POST_PURGE_AFTER (default 30 days).
	purgeAfter time.Duration

	// storyAudience resolves story audiences server-side (M4-P0-1). A nil
	// value fails closed with an unresolved error rather than degrading to an
	// empty relationship set, so an unwired deployment denies rather than
	// leaks.
	storyAudience *StoryAudience

	// commentMentionPublisher replaces the Kafka producer for comment
	// @mentions in tests (comment_mentions.go). Nil in production.
	commentMentionPublisher commentMentionPublisher
	// postMentionPublisher does the same for a post's @mentions at publish
	// (mention_tag_privacy.go). Nil in production.
	postMentionPublisher postMentionPublisher
	// savedCollections stands in for the Postgres store on the saved-
	// collection routes in tests (saved_collections.go). Nil = pgStore.
	savedCollections savedCollectionStore
	// hashtagFollows / followedPostsDecorate stand in for the Postgres
	// store and the per-row counts + media state on the hashtag-follow
	// routes in tests (hashtag_follows.go). Nil = the real ones.
	hashtagFollows        hashtagFollowStore
	followedPostsDecorate func(ctx context.Context, details []PostDetail) ([]PostDetail, error)

	// Sharded post_engagement_counts counters. Each replaces a hot-row
	// UPDATE on post_engagement_counts.<col> = <col> + 1 — at celebrity-
	// post scale a single row was bottlenecking every like/share/etc.
	// Nil-safe: when Redis is nil the service falls back to the legacy
	// per-event PG UPDATE so the dev loop still works.
	likeCounter     *counters.Counter
	commentCounter  *counters.Counter
	shareCounter    *counters.Counter
	bookmarkCounter *counters.Counter
	repostCounter   *counters.Counter

	// Aggregate use-count counters. Same nil-safe pattern as the
	// engagement counters — each replaces a hot-row UPDATE on a
	// singleton aggregate row.
	hashtagCounter *counters.Counter
	audioCounter   *counters.Counter

	// Story view counter. Stories are short-lived but a viral
	// story can take 1M+ views in 24h, all UPDATE-ing the same row.
	// Sharded counter pattern matches the other use-counts.
	storyViewCounter *counters.Counter

	// Private-account / comments-audience gate state (privacy_gate.go).
	privacyGateState
	// Hashtag SSE gate state (hashtag_stream_gate.go).
	hashtagStreamGateState

	// channels is the Tube channel store (channels.go). Nil when there is no
	// Postgres store; every channel flow then fails closed.
	channels channelStore
	// graphFollows writes the follow edge behind a channel subscription
	// (channel_subscriptions.go). Built lazily from graphServiceURL; tests
	// inject one.
	graphFollows graphFollowClient

	// Ownership for cards / end screens / chapters / playlist items
	// (video_authoring_authz.go).
	videoAuthoringAuthz

	// MTube contracts (2026-09-27), each a narrow store slice so its guard
	// is testable without a database: owner edits (post_edit.go), the
	// server-owned collections (system_playlists.go), the creator's comment
	// tools (comment_creator.go) and live -> video (live_vod.go).
	// audioTracks and audioAudience back AttachAudioToPost (audio.go); nil
	// means the Postgres store and ViewerMayAccessMedia.
	audioTracks   audioTrackStore
	audioAudience func(ctx context.Context, authorID, mediaID uuid.UUID) (bool, error)
	// Original sounds (sounds.go, 2026-09-29), each nil-safe: the reads
	// (nil = the Postgres store), the viewer's audience over a page of
	// sounds (nil = ViewerMayAccessMediaBatch), the counts of a listed page
	// (nil = Scylla + PostgreSQL), media-service's sound maker (nil = the
	// HTTP client against mediaServiceURL) and the per-user limit on making
	// one (nil = the Redis limiter, or none without Redis). mediaStates is
	// the store slice behind attachMediaState (processing.go).
	soundReads    soundStore
	soundAudience func(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]bool, error)
	soundCounts   func(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error)
	soundMaker    soundSource
	soundLimiter  func(ctx context.Context, userID uuid.UUID) bool
	mediaStates   mediaStateStore
	// soundUses is the store slice behind GET /v1/internal/sound-uses
	// (sound_uses.go, trending sounds); nil = the Postgres store.
	soundUses soundUsesStore
	// soundExtrasSeam stands in for media-service's cover-and-badge read on
	// the sound page (sounds.go soundPageExtras); nil = the HTTP client.
	soundExtrasSeam func(ctx context.Context, soundID uuid.UUID) (*SoundExtras, error)

	postEdits       postEditStore
	systemPlaylists systemPlaylistStore
	// likedReels is the per-viewer like record behind GET /v1/reels/liked
	// and every like path's index write (liked_reels.go). Nil = no index.
	likedReels      likedReelsStore
	creatorComments creatorCommentStore
	liveVOD         liveVODStore
	// liveServiceURL is live-service-v2, asked for a stream's title when the
	// vod_ready event carries none.
	liveServiceURL string
	// channelCounts is the 60 s in-process cache behind the channel page's
	// public tally (channel_branding.go), the viewCountCache pattern.
	channelCountsMu sync.Mutex
	channelCounts   map[uuid.UUID]channelCountsEntry

	// videoSeries backs the video-series visibility, ownership and removal
	// rules (video_series.go). Nil when there is no Postgres store; every
	// series flow then fails closed.
	videoSeries videoSeriesStore

	// Creator Hub (2026-09-28): the viewer's date of birth behind the 18+
	// gate (age_gate.go; nil = nobody's age is known, so an age-restricted
	// post opens to its owner only) and the private share list
	// (private_shares.go; nil = no private post is shared with anyone).
	birthDates   birthDateSource
	privateShare privateShareStore
	// mediaAccess is the storage slice behind the media byte gate
	// (media_access.go). Nil = unwired, which answers unresolved (503),
	// never allowed.
	mediaAccess mediaAccessStore
	// now is the clock seam for the recording-date and age rules.
	now func() time.Time
	// readGate stands in for singlePostRead in tests (comment_counts.go);
	// bulkDelete for DeletePost in the bulk-delete route (post_edit.go).
	readGate   func(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) error
	bulkDelete func(ctx context.Context, postID, callerID uuid.UUID) error

	// End screens and cards (end_screens.go, 2026-09-29): the store slice,
	// the one-per-viewer-per-day dedupe behind the impression / click
	// counters (Redis SETNX; nil = count every event), and the target-post
	// resolver seam tests use in place of viewablePostCard. flickSeries is
	// the reels series read the episode filter needs (series_visibility.go).
	endScreens   endScreenStore
	statDedupe   statDeduper
	postCardSeam func(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) *RelatedPostCard
	flickSeries  flickSeriesStore

	// Channel feed (channel_feed.go, 2026-09-29): the store slice, the
	// media-record source (nil = the HTTP client against mediaServiceURL)
	// and the 10-minute in-process cache of resolved enclosures.
	channelFeed     channelFeedStore
	feedMedia       feedMediaSource
	feedEnclosureMu sync.Mutex
	feedEnclosures  map[uuid.UUID]feedEnclosureEntry

	// Offline copies (offline_copies.go, 2026-10-02), each nil-safe: the
	// store slice (nil = the Postgres store), the caption-track list (nil =
	// media-service asked as the viewer) and the members-only answer (nil =
	// CheckEntitlement). The media record comes from feedMedia above.
	offline              offlineStore
	offlineCaptionTracks offlineCaptionSource
	offlineEntitlement   func(ctx context.Context, viewerID uuid.UUID, p *postgres.Post) (bool, error)

	// Video credits (credits.go, 2026-10-23), each nil-safe: the store slice
	// (nil = the credit routes answer unavailable and reads carry none), the
	// link checks (nil = every tag is stored as plain text), and seams tests
	// use in place of identity-profile (creditProfiles), the batch read
	// (appearancePosts) and the profile gate (appearanceProfileGate).
	credits               creditStore
	creditLinks           creditLinker
	creditProfiles        func(ctx context.Context, viewerID *uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]commentProfile, error)
	appearancePosts       func(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error)
	appearanceProfileGate func(ctx context.Context, viewerID *uuid.UUID, userID uuid.UUID) bool

	// A creator's Reposts tab (creator_reposts.go, 2026-10-24), each nil-safe
	// and set only by tests: the store page (nil = the Postgres store), the
	// reposter's profile gate (nil = canViewAuthor) and the viewer's follow
	// edges with the reposter (nil = followEdgesOfRestrictedPosts).
	userRepostsSeam   userRepostsPage
	repostProfileGate func(ctx context.Context, viewerID *uuid.UUID, userID uuid.UUID) bool
	repostEdgesSeam   func(ctx context.Context, viewerID *uuid.UUID, reposterID uuid.UUID) map[uuid.UUID]followEdges
	// pins stands in for the Postgres store on PUT /v1/posts/:postId/pin
	// (TogglePin); nil = pgStore.
	pins pinStore

	// Members-only reads (members_read.go, 2026-10-23), each nil-safe and
	// set only by tests: batchPostsSeam stands in for the batch read's
	// loading (the members-only redaction still runs after it),
	// postGateSeam for the (author, tier) lookup behind the comment gate,
	// and membershipGates for the PUT /membership store.
	batchPostsSeam  func(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error)
	postGateSeam    func(ctx context.Context, postID uuid.UUID) (uuid.UUID, *uuid.UUID, error)
	membershipGates membershipGateStore
	// actionLinks / actionCfg are the Action button (action_links.go).
	actionLinks actionLinkStore
	actionCfg   *actionConfig
	// creatorTiersSeam stands in for monetization-service's public tier list
	// (members_tiers.go): the gate validation on every write and the
	// required-price lookup on gated rows.
	creatorTiersSeam func(ctx context.Context, creatorID uuid.UUID) ([]CreatorTierRef, error)
	// createPostWriteSeam stands in for CreatePostWithEventIdempotent: the
	// one write that commits a new post row and its PostCreated together.
	createPostWriteSeam func(ctx context.Context, p *postgres.Post, eventType string, payload interface{}, idem *postgres.CreateIdempotency) error
	// createIdemLookupSeam stands in for LookupCreateIdempotency, the create
	// route's replay read (found=false: a new intent).
	createIdemLookupSeam func(ctx context.Context, actorID uuid.UUID, key string) (uuid.UUID, string, bool, error)
	// engagementPostSeam stands in for loadPostForEngagement (the post and
	// its visibility decision) and commentLookupSeam for the visible-comment
	// read, so the comment routes' members gate can be driven without a
	// database.
	engagementPostSeam func(ctx context.Context, postID, viewerID uuid.UUID) (*postgres.Post, error)
	commentLookupSeam  func(ctx context.Context, commentID uuid.UUID) (*postgres.Comment, error)
	// repostTargetSeam stands in for the post read the repost routes make
	// (heart_only.go: loadRepostTarget).
	repostTargetSeam func(ctx context.Context, postID uuid.UUID) (*postgres.Post, error)

	// Copyright title review (copyright_title_review.go, migration 073):
	// the store slice (nil = the Postgres store), this replica's catalogue
	// snapshot, and the kill switch (COPYRIGHT_TITLE_HOLD_ENABLED=false).
	titleReviews      titleReviewStore
	titleCatalogue    titleCatalogueCache
	titleHoldDisabled bool
	// autoBlockDisabled is the owner-registered auto-block kill switch
	// (COPYRIGHT_AUTO_BLOCK_ENABLED=false, migration 075): matches go to a
	// reviewer instead of being blocked by the system.
	autoBlockDisabled bool

	// Feed read paths (list_reads.go, 2026-10-30), each nil-safe and set only
	// by tests: listReadSeam stands in for the Postgres store on the thread,
	// trending and saved reads; counterReadSeam for Scylla's count and
	// reaction reads; engagementRowSeam for the PostgreSQL engagement rows;
	// pollSeam for the batched poll reads.
	listReadSeam      listReadStore
	counterReadSeam   counterReadStore
	engagementRowSeam engagementRowStore
	pollSeam          pollBatchStore
	// postCacheRDB is the body cache's own bounded Redis client, built from
	// rdb's options on first use (post_cache.go postCacheRedis).
	postCacheOnce sync.Once
	postCacheRDB  *redis.Client
}

func New(pg *postgres.Store, scylla *scylla.InteractionStore, rdb *redis.Client) *Service {
	svc := &Service{
		pgStore:     pg,
		scyllaStore: scylla,
		rdb:         rdb,
		rateLimiter: engagement.NewRateLimiter(rdb),
		spam:        spam.New(rdb),
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		outboxNudge: make(chan struct{}, 1),
	}
	if pg != nil {
		svc.hiddenAuthors = pg
		svc.channels = pg
		svc.authoringOwners = pg
		svc.videoSeries = pg
		svc.postEdits = pg
		svc.systemPlaylists = pg
		svc.likedReels = pg
		svc.creatorComments = pg
		svc.liveVOD = pg
		svc.privateShare = pg
		svc.mediaAccess = pg
		svc.endScreens = pg
		svc.flickSeries = pg
		svc.channelFeed = pg
		svc.soundReads = pg
		svc.mediaStates = pg
		svc.credits = pg
		svc.actionLinks = pg
		svc.mediaHolders = pg
		svc.ownMedia = pg
		svc.postDraftRows = pg
	}
	svc.creditLinks = defaultCreditLinker{s: svc}
	if rdb != nil {
		svc.statDedupe = redisStatDeduper{rdb: rdb}
		svc.likeCounter = counters.New(rdb, counters.Config{EntityKind: "post_like_count", Shards: 32})
		// comment_count is not sharded: PostgreSQL post_engagement_counts is
		// written directly, once per transition (store/postgres/comment_counts.go).
		svc.shareCounter = counters.New(rdb, counters.Config{EntityKind: "post_share_count", Shards: 32})
		svc.bookmarkCounter = counters.New(rdb, counters.Config{EntityKind: "post_bookmark_count", Shards: 32})
		svc.repostCounter = counters.New(rdb, counters.Config{EntityKind: "post_repost_count", Shards: 32})
		svc.hashtagCounter = counters.New(rdb, counters.Config{EntityKind: "hashtag_use_count", Shards: 32})
		svc.audioCounter = counters.New(rdb, counters.Config{EntityKind: "audio_use_count", Shards: 32})
		svc.storyViewCounter = counters.New(rdb, counters.Config{EntityKind: "story_view_count", Shards: 32})
	}
	return svc
}

// LikeCounter / CommentCounter / ShareCounter / BookmarkCounter /
// RepostCounter / HashtagCounter / AudioCounter expose the sharded
// counters so cmd/server can attach flush workers. Returns nil when
// Redis isn't configured.
func (s *Service) LikeCounter() *counters.Counter      { return s.likeCounter }
func (s *Service) CommentCounter() *counters.Counter   { return s.commentCounter }
func (s *Service) ShareCounter() *counters.Counter     { return s.shareCounter }
func (s *Service) BookmarkCounter() *counters.Counter  { return s.bookmarkCounter }
func (s *Service) RepostCounter() *counters.Counter    { return s.repostCounter }
func (s *Service) HashtagCounter() *counters.Counter   { return s.hashtagCounter }
func (s *Service) AudioCounter() *counters.Counter     { return s.audioCounter }
func (s *Service) StoryViewCounter() *counters.Counter { return s.storyViewCounter }

// adjustStoryViewCount routes a +1 view increment through the sharded
// counter. Falls back to a direct PG UPDATE when Redis is nil so the
// dev loop still works.
func (s *Service) adjustStoryViewCount(ctx context.Context, storyID uuid.UUID) error {
	if s.storyViewCounter != nil {
		if err := s.storyViewCounter.Inc(ctx, storyID.String(), 1); err == nil {
			return nil
		}
	}
	return s.pgStore.IncrementStoryViewCount(ctx, storyID)
}

// adjustHashtagUseCount routes a +1 increment through the sharded
// counter when available, otherwise falls back to the legacy per-event
// PG UPSERT (Redis-less dev loops + degraded-mode operation).
func (s *Service) adjustHashtagUseCount(ctx context.Context, tag string) error {
	if s.hashtagCounter != nil {
		if err := s.hashtagCounter.Inc(ctx, tag, 1); err != nil {
			slog.Warn("sharded hashtag counter inc failed; falling back to PG",
				"tag", tag, "err", err)
			return s.pgStore.IncrementHashtagUseCount(ctx, tag)
		}
		return nil
	}
	return s.pgStore.IncrementHashtagUseCount(ctx, tag)
}

// adjustAudioUseCount routes a +1 increment through the sharded counter
// when available, otherwise falls back to the per-event PG UPDATE.
func (s *Service) adjustAudioUseCount(ctx context.Context, audioTrackID uuid.UUID) error {
	if s.audioCounter != nil {
		if err := s.audioCounter.Inc(ctx, audioTrackID.String(), 1); err != nil {
			slog.Warn("sharded audio counter inc failed; falling back to PG",
				"audio_track_id", audioTrackID, "err", err)
			return s.audioStore().IncrementAudioUseCount(ctx, audioTrackID)
		}
		return nil
	}
	return s.audioStore().IncrementAudioUseCount(ctx, audioTrackID)
}

// adjustEngagementCount fans an increment/decrement to the sharded
// counter when available, otherwise falls back to the legacy per-event
// PG UPDATE. Failure inside the Redis path is logged but not fatal —
// the hourly reconciler (internal/reconcile) backfills any drift the
// next tick.
func (s *Service) adjustEngagementCount(ctx context.Context, c *counters.Counter, postID uuid.UUID, column string, delta int64) error {
	if c != nil {
		if err := c.Inc(ctx, postID.String(), delta); err != nil {
			slog.Warn("sharded engagement counter inc failed; falling back to PG",
				"column", column, "post_id", postID, "delta", delta, "err", err)
			return s.pgStore.IncrementEngagementCount(ctx, postID, column, delta)
		}
		return nil
	}
	return s.pgStore.IncrementEngagementCount(ctx, postID, column, delta)
}

// SetUserServiceURL configures the user-service base URL for mention resolution.
func (s *Service) SetUserServiceURL(url string) {
	s.userServiceURL = url
}

// SetGraphServiceURL configures the graph-service base URL for following/follower lookups.
func (s *Service) SetGraphServiceURL(url string) {
	s.graphServiceURL = url
}

// SetMonetizationServiceURL configures the monetization-service base
// URL used by the membership-gating entitlement check.
func (s *Service) SetMonetizationServiceURL(url string) {
	s.monetizationServiceURL = url
}

// SetInternalServiceKey stores the X-Internal-Service-Key value used
// when calling other services. Empty means no header set.
func (s *Service) SetInternalServiceKey(key string) {
	s.internalServiceKey = key
}

// SetReviewerServiceURL configures the reviewer-service base URL used to
// enqueue flagged video content for human review. Empty disables enqueue.
func (s *Service) SetReviewerServiceURL(url string) {
	s.reviewerServiceURL = url
}

// SetReviewAllVideos, when true, routes EVERY new video to human review (marks
// it 'flagged' so it enqueues), not just spam-flagged ones. Off by default;
// intended for staged rollout / testing of the reviewer pipeline.
func (s *Service) SetReviewAllVideos(v bool) {
	s.reviewAllVideos = v
}

// SetProducer sets the legacy Kafka producer for engagement events.
func (s *Service) SetProducer(p *postEvents.Producer) {
	s.producer = p
}

// SetEngagementProducer sets the new engagement event producer.
func (s *Service) SetEngagementProducer(p *engagement.Producer) {
	s.engProducer = p
}

// SetScyllaSession sets the raw ScyllaDB session for bookmark fallback.
func (s *Service) SetScyllaSession(session *gocql.Session) {
	s.scyllaSession = session
}

// invalidateFeedHydration removes the per-viewer feed cache entry after a
// viewer mutation. Without this, a successful like/bookmark/repost could be
// followed by five minutes of a false action bar on another device.
func (s *Service) invalidateFeedHydration(ctx context.Context, userID, postID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	if err := s.rdb.Del(ctx, fmt.Sprintf("feed:hydrate:%s:%s", userID, postID)).Err(); err != nil {
		slog.Warn("failed to invalidate feed hydration", "error", err,
			"user_id", userID, "post_id", postID)
	}
}
