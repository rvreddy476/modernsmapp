package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/spam"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/postclassify"
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

// AutoResolveFlagged sets a FLAGGED post to a terminal review_status
// (approved|rejected). Used by reviewer-service's ML pre-filter to clear
// content without a human. Scoped to flagged rows so it can't override a
// human/pending verdict. Busts the cached post body on success.
// The actor is written to post_review_audit with the change.
func (s *Service) AutoResolveFlagged(ctx context.Context, postID uuid.UUID, status string, actor postgres.ReviewAuditActor) (bool, error) {
	ok, err := s.pgStore.SetReviewStatusFromFlagged(ctx, postID, status, actor)
	if err == nil && ok && s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+postID.String()).Err()
	}
	return ok, err
}

func (s *Service) GetModerationSubject(ctx context.Context, postID uuid.UUID) (*postgres.ModerationSubject, error) {
	return s.pgStore.GetModerationSubject(ctx, postID)
}

func (s *Service) ModeratePost(ctx context.Context, in postgres.ModeratePostInput) (*postgres.ModerationDecision, error) {
	decision, err := s.pgStore.ModeratePost(ctx, in)
	if err == nil && decision.Changed && s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+in.PostID.String()).Err()
	}
	return decision, err
}

// Resubmit lets the creator send an edited post (in 'needs_changes' after a
// super-admin requested edits) back into human review. Owner-gated; re-enqueues
// to reviewer-service so the loop continues.
func (s *Service) Resubmit(ctx context.Context, postID, actorID uuid.UUID) (bool, error) {
	owner, err := s.IsPostAuthor(ctx, postID, actorID)
	if err != nil {
		return false, err
	}
	if !owner {
		return false, fmt.Errorf("forbidden: not the author")
	}
	changed, err := s.pgStore.ResubmitFromNeedsChanges(ctx, postID)
	if err != nil || !changed {
		return false, err
	}
	if s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+postID.String()).Err()
	}
	// Re-enqueue for human review (fresh content → let the pre-filter/human decide).
	if posts, err := s.pgStore.GetPostsByIDs(ctx, []uuid.UUID{postID}); err == nil && len(posts) > 0 {
		s.enqueueForReview(&posts[0], 0)
	}
	return true, nil
}

// PromoteStaged finalizes a test-audience rollout by moving a STAGED post to a
// new visibility (typically 'public'). Used by the reviewer promotion worker.
// The actor is written to post_review_audit with the change.
func (s *Service) PromoteStaged(ctx context.Context, postID uuid.UUID, visibility string, actor postgres.ReviewAuditActor) (bool, error) {
	ok, err := s.pgStore.SetVisibilityFromStaged(ctx, postID, visibility, actor)
	if err == nil && ok && s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+postID.String()).Err()
	}
	return ok, err
}

// enqueueForReview best-effort notifies reviewer-service that a piece of video
// content needs human review (review_status='flagged'). Fire-and-forget: never
// blocks or fails the post-create/transcode path. No-op when the URL is unset
// or the content isn't video.
func (s *Service) enqueueForReview(p *postgres.Post, spamScore float64) {
	if s.reviewerServiceURL == "" || !isVideoContentType(p.ContentType) {
		return
	}
	langs := []string{}
	if p.Language != "" {
		langs = []string{p.Language}
	}
	body, _ := json.Marshal(map[string]any{
		"content_id":      p.ID.String(),
		"creator_id":      p.AuthorID.String(),
		"content_type":    p.ContentType,
		"languages":       langs,
		"content_seconds": 0, // duration may not be known yet; reviewer caps anyway
		"spam_score":      spamScore,
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			s.reviewerServiceURL+"/v1/reviewer/internal/enqueue", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if s.internalServiceKey != "" {
			req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			slog.Warn("reviewer enqueue failed (best-effort)", "post", p.ID, "err", err)
			return
		}
		_ = resp.Body.Close()
	}()
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

type PostDetail struct {
	*postgres.Post
	Counts         *scylla.Counts     `json:"counts"`
	ViewCount      int64              `json:"view_count"`
	ViewerReaction *string            `json:"viewer_reaction,omitempty"`
	HasReacted     bool               `json:"has_reacted"`
	IsBookmarked   bool               `json:"is_bookmarked"`
	RepostCount    int                `json:"repost_count"`
	ViewerRepost   *RepostStateResult `json:"viewer_repost,omitempty"`
	HasReposted    bool               `json:"has_reposted"`
	IsRepostable   bool               `json:"is_repostable"`
	// Channel is the author's Tube channel, attached to long_video posts
	// whose author has one (channels.go); omitted otherwise.
	Channel *ChannelRef `json:"channel,omitempty"`
	// Author is the same block the feed carries, from the same source
	// (identity-profile). Absent when the profile lookup is unavailable.
	Author *PostAuthor `json:"author,omitempty"`
	// MTube (2026-09-27), never omitempty so a client cannot confuse
	// "false" with "unknown": ViewerDisliked is the viewer's private tune
	// (no count exists anywhere); ViewerQueued is "in the viewer's Queue"
	// (watch_later system collection).
	ViewerDisliked bool `json:"viewer_disliked"`
	ViewerQueued   bool `json:"viewer_queued"`
	// Chapters: saved media_chapters, or for a long video the chapters
	// derived from description timestamps (chapters.go). Always an array
	// on the direct read (GET /v1/posts/:id); null on list surfaces, which
	// do not load chapters.
	Chapters []ChapterRef `json:"chapters"`

	// Creator Hub (2026-09-28). LikeCount is the like number when the
	// author hid it (hide_like_count): null for everyone but the owner, and
	// counts.likes is 0 for them too. The direct read always sets it; list
	// surfaces set it only when hidden (absent otherwise). RelatedPost is
	// the owner-picked related post as a card, on the direct read only and
	// only when this viewer may see it (else null, and related_post_id is
	// null too). NotifySubscribers is the owner's (resolved distribution)
	// and absent for everyone else.
	LikeCount         *LikeCount        `json:"like_count,omitempty"`
	RelatedPost       *RelatedPostField `json:"related_post,omitempty"`
	NotifySubscribers *bool             `json:"notify_subscribers,omitempty"`
	// Restrictions (migration 056) is the owner's view of every active
	// case-specific restriction on the direct read: present (possibly
	// empty) for the owner, absent for everyone else — a viewer never
	// reaches this struct for a restricted post at all.
	Restrictions []RestrictionNotice `json:"restrictions,omitempty"`
	// CopyrightReview is the author's view of a copyright title review
	// (copyright_title_review.go): on the owner's direct read and uploads
	// rows of a long video that was flagged, absent otherwise and for every
	// other viewer. Per viewer, so never in the cached body.
	CopyrightReview *CopyrightReviewView `json:"copyright_review,omitempty"`
	// Sound is the added sound the post plays (audio_track_id), for a
	// viewer who may hear it; absent when there is none, when this viewer
	// may not, and when that could not be decided (sounds.go). It is on the
	// detail and not on the post because the post body is what the cache
	// holds (post_cache.go), and a cached body carries nothing per viewer.
	Sound *PostSound `json:"sound,omitempty"`
	// Credits is a long video's cast & crew as this viewer may see them
	// (credits.go): an array on the direct read of a long video (possibly
	// empty), absent for every other kind and on list surfaces. Per viewer,
	// so on the detail and never in the cached post body.
	Credits *[]CreditView `json:"credits,omitempty"`
	// CreditTagsSkipped is how many tags the write behind this detail stored
	// as plain text over the uploader's daily limit; the handler puts it in
	// meta.credit_tags_skipped. Never in the body.
	CreditTagsSkipped int `json:"-"`
}

// CreatePostInput holds all fields for creating a new post.
type CreatePostInput struct {
	// PostID, when set, is used as the new post's id instead of a random
	// UUID. Draft publishing passes the draft id here so a crash-retry
	// hits the posts PK instead of double-publishing (P0-5 idempotency).
	PostID          *uuid.UUID
	AuthorID        uuid.UUID
	Text            string
	Visibility      string
	ContentType     string
	MediaIDs        []uuid.UUID
	Feeling         *string
	Activity        *string
	ActivityDetail  *string
	RichText        json.RawMessage
	Poll            *CreatePollInput
	NoComments      bool
	NoLikes         bool
	LocationName    *string
	LocationLat     *float64
	LocationLng     *float64
	PostType        string
	AppOrigin       string
	ShareToPostbook bool
	// Reel metadata
	Title             string
	Tags              []string
	Category          string
	Language          string
	SEOTitle          string
	PaidPromotion     bool
	AlteredContent    bool
	IsMadeForKids     bool
	License           string
	AllowEmbedding    bool
	PublishToFeed     bool
	RemixSetting      string
	CommentModeration string
	CommentAccess     string
	RecordingDate     *time.Time
	RecordingLocation string
	CoverMediaID      *uuid.UUID
	OriginalAudioVol  float32
	OverlayAudioVol   float32
	// Per-reel controls (2026-09-04). AllowDownload is a plain bool like
	// ShareToPostbook: the HANDLER resolves an omitted field to true, so
	// every other constructor of this input must set it deliberately.
	HideShare     bool
	AllowDownload bool
	// TaggedUserIDs is already parsed; NormalizeTaggedUsers decides what
	// is stored.
	TaggedUserIDs []uuid.UUID
	// PublishAt schedules the post (schedule.go): nil publishes now; a
	// time in [now+5m, now+30d] stores it author-only until the worker
	// publishes it. Validated by ValidatePublishAt before anything is written.
	PublishAt *time.Time
	// Hashtags and Mentions are the studio's separate fields
	// (explicit_tags.go), merged with what the caption parser extracts.
	Hashtags []string
	Mentions []string
	// Distribution is the raw policy document from the client (P0-1).
	// nil = no policy = legacy behavior. Validated by ParseDistributionPolicy.
	Distribution json.RawMessage
	// LegacyDistribution carries explicitly-supplied pre-policy fields
	// from old clients. When no typed policy is present these express the
	// creator's real intent and are honored (Codex P1-1).
	LegacyDistribution LegacyDistributionFields

	// VisibilityExplicit records that the CALLER chose this audience, as
	// opposed to inheriting a default (Slice C, C-LB-2).
	//
	// The after-hours Trusted Circle rule below could not tell the two apart:
	// it matched on the VALUE "public", which a defaulting client and a
	// deliberate client produce identically. Its own comment says a manually
	// selected wider audience should win, and the implementation rewrote both.
	// A user who explicitly published to Public at 23:00 got a trusted-circle
	// post and was told it was public.
	//
	// The HTTP handler sets this because `visibility` is `binding:"required"`
	// there — every request over the wire is an explicit choice. Internal
	// callers that genuinely default leave it false and keep the old behaviour.
	VisibilityExplicit bool

	// CreateKey and CreateFingerprint carry the durable idempotency claim
	// (C-LB-3). Empty CreateKey disables the claim, which is what internal
	// callers (draft publish, thread entries) do — they have their own
	// exactly-once mechanism through PostID.
	CreateKey         string
	CreateFingerprint string

	// Post screens parity (2026-10-18, post_screen_fields.go). CoverIndex
	// picks the cover of a photo post by position in MediaIDs; LinkURL and
	// LocationPlaceID are raw ("" = none) and normalized by CreatePost.
	CoverIndex      *int
	IsBranded       bool
	AIGenerated     bool
	LinkURL         string
	LocationPlaceID *string

	// Video credits (2026-10-23, credits.go), long videos only. Validated
	// and linked by CreatePost; written in the post's transaction.
	VideoCredits VideoCreditFields

	// TierRequiredID gates the post on one of the author's membership tiers
	// from birth (members_tiers.go). nil = open to everyone. CreatePost
	// checks it against monetization-service before anything is written and
	// stores it on the row in the post's own transaction, so the post is
	// never public for a moment and its PostCreated already carries the gate.
	TierRequiredID *uuid.UUID

	// Action is the Action button (action_links.go); nil = none. CreatePost
	// runs the label / URL / eligibility / safety checks before anything is
	// written and inserts the link in the post's own transaction.
	Action *ActionInput
}

// CreatePollInput holds poll creation data.
type CreatePollInput struct {
	Question       string
	Options        []string
	AllowsMultiple bool
	DurationHours  *int
}

// extractHashtags parses #hashtag patterns from text.
func extractHashtags(text string) []string {
	matches := hashtagRegex.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var tags []string
	for _, match := range matches {
		tag := strings.ToLower(match[1])
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

// filterBlockedHashtags removes any tags that are marked is_blocked=true in
// the hashtags table. Fails open: if the DB check errors, all tags are kept.
func (s *Service) filterBlockedHashtags(ctx context.Context, tags []string) []string {
	if len(tags) == 0 {
		return tags
	}
	blocked, err := s.pgStore.GetBlockedHashtags(ctx, tags)
	if err != nil || len(blocked) == 0 {
		return tags
	}
	blockedSet := make(map[string]bool, len(blocked))
	for _, t := range blocked {
		blockedSet[t] = true
	}
	out := tags[:0]
	for _, t := range tags {
		if !blockedSet[t] {
			out = append(out, t)
		}
	}
	return out
}

// extractMentions parses @username patterns from text.
// maxMentionsPerPost caps the number of unique @-mentions extracted
// from a single post. Audit H3: the per-mention resolver fans out one
// goroutine + one HTTP call to user-service per mention. Without a
// cap, a post containing 100 `@x` tokens spawns 100 in-flight
// requests — a DoS amplifier dressed as a feature. Anything beyond
// this cap is silently dropped; the same cap is used by the
// `user.mentioned` event fan-out below.
const maxMentionsPerPost = 10

func extractMentions(text string) []string {
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var usernames []string
	for _, match := range matches {
		username := strings.TrimRight(match[1], ".")
		if username == "" {
			continue
		}
		if !seen[username] {
			seen[username] = true
			usernames = append(usernames, username)
			if len(usernames) >= maxMentionsPerPost {
				break
			}
		}
	}
	return usernames
}

// DetectAndStoreMentions parses @username patterns from body text and inserts
// them into the post_mentions table. Each unique username is stored with the
// post ID and post type. Resolution from username to user_id happens at
// notification time.
func DetectAndStoreMentions(ctx context.Context, postID uuid.UUID, postType string, body string, store *postgres.Store) {
	// Use the package-level compiled regex (audit H6: was being
	// recompiled per call) and cap at maxMentionsPerPost (audit H3:
	// was unbounded, allowing a post with 100 @-tokens to fire 100
	// INSERTs).
	matches := dbMentionRegex.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool)
	inserted := 0
	for _, match := range matches {
		if inserted >= maxMentionsPerPost {
			break
		}
		username := match[1]
		if seen[username] {
			continue
		}
		seen[username] = true
		inserted++
		if err := store.InsertMention(ctx, postID, postType, username); err != nil {
			log.Printf("Warning: failed to insert mention for @%s on post %s: %v", username, postID, err)
		}
	}
}

// flickMaxDurationSeconds is the maximum duration (inclusive) for a video to
// be auto-classified as a "reel" (Flick). Videos longer than this are "video" (Long Video).
// Flick = up to 10 minutes, Long Video = more than 10 minutes (founder,
// 2026-10-21; was 5 minutes from 2026-09-05). It IS
// postclassify.FlickMaxDurationSeconds — change it there, never here.
const flickMaxDurationSeconds = postclassify.FlickMaxDurationSeconds

// validContentTypes is the allowed set for content_type.
// "voice" is Module 1 P0-6: a voice-only post (audio media + optional
// text). Mixed voice carousels are deferred.
var validContentTypes = map[string]bool{
	"post": true, "poll": true, "reel": true, "video": true,
	"flick": true, "long_video": true, "voice": true,
}

// isVoiceContentType reports whether a post is a voice post.
func isVoiceContentType(ct string) bool { return ct == "voice" }

// gateVoiceReviewStatus holds a voice post out of public surfaces until
// media-service reports the baseline audio-safety pass complete
// (Codex P0-6). A failed/unknown check holds 'pending' rather than
// auto-approving; a rejected asset rejects the post.
func (s *Service) gateVoiceReviewStatus(ctx context.Context, mediaID uuid.UUID) string {
	procStatus, modStatus, err := s.getMediaModeration(ctx, mediaID)
	if err != nil {
		log.Printf("Warning: voice media safety check failed for %s, holding pending: %v", mediaID, err)
		return "pending"
	}
	if procStatus == "rejected" || modStatus == "rejected" {
		return "rejected"
	}
	if procStatus != "ready" {
		return "pending"
	}
	switch modStatus {
	case "approved":
		return "approved"
	case "failed":
		// Safety pipeline failed → human review, never auto-approve.
		return "flagged"
	default:
		return "pending"
	}
}

// classifyVideoContentType returns "flick" or "long_video" based on duration and dimensions.
// Legacy callers: "reel" maps to "flick", "video" maps to "long_video".
func classifyVideoContentType(durationSeconds int) string {
	if durationSeconds > 0 && durationSeconds <= flickMaxDurationSeconds {
		return "flick"
	}
	return "long_video"
}

// ClassifyVideo returns the computed category and orientation based on
// duration and dimensions. This is the *measurement*, recorded on
// video_metadata.computed_category for analytics. It decides a post's
// content_type only when the author expressed no kind — see
// resolveVideoContentType for the rule.
func ClassifyVideo(durationSeconds float64, width, height int) (category, orientation string) {
	orientation = deriveOrientation(width, height)
	if durationSeconds <= float64(flickMaxDurationSeconds) && (orientation == "portrait" || orientation == "square") {
		return "flick", orientation
	}
	return "long_video", orientation
}

// deriveOrientation returns "portrait", "landscape", or "square" from dimensions.
func deriveOrientation(width, height int) string {
	if width <= 0 || height <= 0 {
		return "landscape"
	}
	ratio := float64(width) / float64(height)
	if ratio > 1.05 {
		return "landscape"
	}
	if ratio < 0.95 {
		return "portrait"
	}
	return "square"
}

// resolveVideoContentType decides the content_type of a post that carries a
// video, from the caller's (already-normalized) content_type intent and the
// measured duration + dimensions when transcode has produced them
// (durationSeconds <= 0 means "not yet").
//
// The rule (founder, 2026-09-04/05): a reel is what the author posted as a
// reel; a video is what the author posted as a video. An explicit "flick"
// stays a flick even when the frame is landscape or the clip runs long, and
// an explicit "long_video" stays a long video even when the clip is portrait
// and short — a vertical clip posted from Tube belongs in Tube, not Reels.
// Legacy "reel"/"video" spellings were folded into these before this runs.
//
// And a post is a post (founder, 2026-09-25, superseding the earlier rule's
// last branch). A plain "post" that attaches a video used to be classified
// from the measurement — a short portrait clip became a flick and landed on
// the Reels page, anything else became a long_video and landed on Tube and
// in the creator fund — although the author chose neither; the web composer
// had inferred "video" from a MIME type. Now it stays a post: it shows in
// the feed, not in Reels or Tube, and CanonicalMonetizationType("post") is
// not ok, so it is never priced. Android's feed composer already sent
// "post"; the web one now does too, so both clients mean the same thing.
//
// The measurement branch below is kept for any other intent that reaches
// here with a video attached (a poll, for instance) — unchanged, and out of
// scope of this rule. `explicit` reports whether the answer was the author's
// choice, so the MediaTranscodeConsumer knows to leave it alone; a post is
// reported explicit for exactly that reason.
func resolveVideoContentType(intent string, durationSeconds int, width, height int) (contentType string, explicit bool) {
	switch intent {
	case "flick", "reel":
		return "flick", true
	case "long_video", "video":
		return "long_video", true
	case "post":
		return "post", true
	}
	if durationSeconds <= 0 {
		return "long_video", false
	}
	cat, _ := ClassifyVideo(float64(durationSeconds), width, height)
	return cat, false
}

// ValidateCategoryOverride checks if a category override request is valid.
// This guards the PATCH category endpoint only: an author may always move a
// video to long_video, but may only call it a flick when the measurement
// allows (≤ flickMaxDurationSeconds, not landscape).
func ValidateCategoryOverride(vm *postgres.VideoMetadata, requested string) error {
	if requested == "flick" {
		if vm.DurationSeconds > float64(flickMaxDurationSeconds) {
			return fmt.Errorf("cannot classify as flick: duration exceeds %ds", flickMaxDurationSeconds)
		}
		if vm.Orientation == "landscape" {
			return fmt.Errorf("cannot classify as flick: landscape orientation")
		}
	}
	return nil // long_video is always valid
}

// CanonicalContentType normalizes a client-supplied content type (legacy
// spellings included) and reports whether it is one this service accepts.
func CanonicalContentType(ct string) (string, bool) {
	if ct == "" {
		return "", false
	}
	ct = normalizeLegacyContentType(ct)
	return ct, validContentTypes[ct]
}

// normalizeLegacyContentType maps old content types to new ones.
func normalizeLegacyContentType(contentType string) string {
	switch contentType {
	case "reel":
		return "flick"
	case "video":
		return "long_video"
	default:
		return contentType
	}
}

func (s *Service) CreatePost(ctx context.Context, input *CreatePostInput) (*postgres.Post, error) {
	// Non-empty and length ceiling, before anything is written (C-LB-1.3).
	// Server-side because a hostile or older client will not enforce it.
	if err := ValidatePostContent(input.Text, len(input.MediaIDs)); err != nil {
		return nil, err
	}
	// Scheduling window, before anything is written (schedule.go).
	if err := ValidatePublishAt(input.PublishAt, time.Now()); err != nil {
		return nil, err
	}
	// The details screen's fields, before anything is written
	// (post_screen_fields.go): every one is a statement about the request.
	coverMediaID, err := ResolvePostCover(input.PostType, input.MediaIDs, input.CoverMediaID, input.CoverIndex)
	if err != nil {
		return nil, err
	}
	remixSetting, err := normalizeCreateEnum(input.RemixSetting, PostRemixSettings, "allow", ErrInvalidRemixSetting)
	if err != nil {
		return nil, err
	}
	commentAcc, err := normalizeCreateEnum(input.CommentAccess, PostCommentAccesses, "everyone", ErrInvalidCommentAccess)
	if err != nil {
		return nil, err
	}
	linkURL, err := NormalizeLinkURL(input.LinkURL)
	if err != nil {
		return nil, err
	}
	locationPlaceID, err := NormalizeLocationPlaceID(input.LocationPlaceID)
	if err != nil {
		return nil, err
	}
	// Author standing, before anything is written (publish_standing.go):
	// a suspended author gets 403 AUTHOR_SUSPENDED, an unanswerable
	// trust-safety 503 — never a post.
	if err := s.requirePublishStanding(ctx, input.AuthorID); err != nil {
		return nil, err
	}

	// DURABLE IDEMPOTENCY — fast path (C-LB-3.3).
	//
	// An ordinary retry is answered here without redoing validation, media
	// authority, spam scoring and event construction. This read is only an
	// optimisation: the authority is the unique index inside
	// CreatePostWithEventIdempotent, because another request can claim the key
	// between this lookup and that insert.
	if input.CreateKey != "" {
		postID, fingerprint, found, lookupErr := s.lookupCreateIdempotency(
			ctx, input.AuthorID, input.CreateKey)
		if lookupErr != nil {
			// Fail closed: an unreadable authority must not become "assume new".
			return nil, lookupErr
		}
		if found {
			if fingerprint != input.CreateFingerprint {
				return nil, ErrCreateKeyReused
			}
			existing, getErr := s.pgStore.GetPost(ctx, postID)
			if getErr != nil {
				return nil, fmt.Errorf("replay created post: %w", getErr)
			}
			if existing != nil {
				s.AttachAuthorActions(ctx, input.AuthorID, []*postgres.Post{existing})
			}
			return existing, nil
		}
	}

	// Validate the distribution policy up front so a malformed/unsupported
	// policy fails the whole request (400) before any row is written —
	// never silently ignored (Codex P0-1).
	distPolicy, err := ParseDistributionPolicy(input.Distribution)
	if err != nil {
		return nil, err
	}

	contentType := input.ContentType
	if contentType == "" {
		contentType = "post"
	}

	// Normalize legacy content types from old clients
	contentType = normalizeLegacyContentType(contentType)

	// Validate content_type
	if !validContentTypes[contentType] {
		return nil, fmt.Errorf("%w %q: must be post, poll, flick, or long_video", ErrInvalidContentType, contentType)
	}

	// Long-video uploads paused (copyright repeat-uploader rule,
	// copyright_title_review.go): one more field of the standing answer
	// requirePublishStanding already cached. Before anything is written;
	// reels and posts are never paused by it.
	if err := s.requireLongVideoUploads(ctx, input.AuthorID, contentType); err != nil {
		return nil, err
	}

	// Video credits (credits.go): every value, then the kind — the INTENT,
	// so only what the author posted as a long video may carry them (a
	// plain post with a video attached stays a post). Before anything is
	// written.
	videoCredits, productionHouse, err := validatedVideoCredits(contentType, input.VideoCredits, s.clock())
	if err != nil {
		return nil, err
	}

	// Tube: a long video is listed under a channel, so the account must have
	// created one first (founder rule, 2026-09-05). Reels/flicks are not gated.
	if err := s.gateVideoBehindChannel(ctx, input.AuthorID, contentType); err != nil {
		return nil, err
	}

	// Tube: a long video is listed by its title, so it must have one; the
	// ceiling applies to every kind of post. Stored trimmed.
	title := strings.TrimSpace(input.Title)
	if err := ValidateTitle(contentType, title); err != nil {
		return nil, err
	}

	// Category is stored in its slug form for every content type, and flicks
	// are additionally held to the closed taxonomy (categories.go). This is
	// the single write site for posts.category: the create route and all
	// three draft-publish paths build a CreatePostInput and land here, so
	// feed-service's exact-match category pages see one spelling.
	category, catErr := resolveCreateCategory(contentType, input.Category)
	if catErr != nil {
		return nil, catErr
	}

	taggedUsers, tagErr := NormalizeTaggedUsers(input.AuthorID, input.TaggedUserIDs)
	if tagErr != nil {
		return nil, tagErr
	}

	// Members-only from birth (members_tiers.go): the tier must be one of
	// the author's active tiers. A tier list monetization-service cannot
	// answer refuses the create (503) — never a post created open.
	if err := s.ValidateTierForAuthor(ctx, input.AuthorID, input.TierRequiredID); err != nil {
		return nil, err
	}

	// The Action button (action_links.go): label, URL rules, eligibility
	// and the safety check, before anything is written; the link is
	// inserted in the post's own transaction (insertPostTx).
	var actionWrite *postgres.ActionLinkWrite
	if input.Action != nil {
		w, err := s.prepareAction(ctx, input.AuthorID, input.Action)
		if err != nil {
			return nil, err
		}
		w.ID = uuid.New()
		actionWrite = w
	}

	// REMOVED 21 Sep: "Trusted Circle after-hours protection", which
	// auto-rewrote a defaulted visibility to `trusted` between 22:00 and
	// 06:00 when the author had tc_after_hours_posts on.
	//
	// The trusted/close-friends audience was retired with the Trusted
	// Circle tier (graph-service migration 012). Leaving this in would
	// have been worse than removing it: a late-night post would still be
	// rewritten to an audience that now resolves to NOBODY, so the author
	// would silently publish to themselves. The tc_* settings it read are
	// gone too.

	postType := input.PostType
	if postType == "" {
		postType = "text"
	}
	appOrigin := input.AppOrigin
	if appOrigin == "" {
		appOrigin = "postbook"
	}

	// Extract hashtags from text (cap at 20 per design spec)
	hashtags := extractHashtags(input.Text)
	if len(hashtags) > 20 {
		hashtags = hashtags[:20]
	}
	// Merge the studio's explicit `hashtags` field (explicit_tags.go):
	// validated here so a bad tag fails the request before any write.
	explicitTags, err := NormalizeExplicitHashtags(input.Hashtags)
	if err != nil {
		return nil, err
	}
	hashtags = mergeTags(hashtags, explicitTags, maxMergedHashtags)
	hashtags = s.filterBlockedHashtags(ctx, hashtags)

	// Extract @mentions from text and merge the explicit `mentions` field.
	mentions := extractMentions(input.Text)
	explicitMentions, err := NormalizeExplicitMentions(input.Mentions)
	if err != nil {
		return nil, err
	}
	mentions = mergeTags(mentions, explicitMentions, maxMergedMentions)

	// "Who can mention or tag me" (mention_tag_privacy.go): a mention or tag
	// of someone who does not allow it is dropped here, silently — the
	// mention stays in the text as plain text — so it is never stored,
	// linked or notified, and the create never fails over it.
	mentions = s.allowedMentions(ctx, input.AuthorID, mentions)
	taggedUsers = s.allowedTags(ctx, input.AuthorID, taggedUsers)

	// Default reel metadata values
	lang := input.Language
	if lang == "" {
		lang = "en"
	}
	license := input.License
	if license == "" {
		license = "standard"
	}
	// remix_setting and comment_access were validated and defaulted at the
	// top (normalizeCreateEnum): they used to be stored as sent.
	commentMod := input.CommentModeration
	if commentMod == "" {
		commentMod = "none"
	}
	origVol := input.OriginalAudioVol
	if origVol == 0 {
		origVol = 1.0
	}
	overlayVol := input.OverlayAudioVol
	if overlayVol == 0 {
		overlayVol = 1.0
	}

	newPostID := uuid.New()
	if input.PostID != nil && *input.PostID != uuid.Nil {
		newPostID = *input.PostID
	}
	p := &postgres.Post{
		ID:                newPostID,
		AuthorID:          input.AuthorID,
		Text:              input.Text,
		Visibility:        input.Visibility,
		ContentType:       contentType,
		Feeling:           input.Feeling,
		Activity:          input.Activity,
		ActivityDetail:    input.ActivityDetail,
		RichText:          input.RichText,
		NoComments:        input.NoComments,
		NoLikes:           input.NoLikes,
		Hashtags:          hashtags,
		LocationName:      input.LocationName,
		LocationLat:       input.LocationLat,
		LocationLng:       input.LocationLng,
		PostType:          postType,
		AppOrigin:         appOrigin,
		ShareToPostbook:   input.ShareToPostbook,
		Title:             title,
		Tags:              input.Tags,
		Category:          category,
		Language:          lang,
		SEOTitle:          input.SEOTitle,
		PaidPromotion:     input.PaidPromotion,
		AlteredContent:    input.AlteredContent,
		IsMadeForKids:     input.IsMadeForKids,
		License:           license,
		AllowEmbedding:    input.AllowEmbedding,
		PublishToFeed:     input.PublishToFeed,
		RemixSetting:      remixSetting,
		CommentModeration: commentMod,
		CommentAccess:     commentAcc,
		RecordingDate:     input.RecordingDate,
		RecordingLocation: input.RecordingLocation,
		CoverMediaID:      coverMediaID,
		OriginalAudioVol:  origVol,
		OverlayAudioVol:   overlayVol,
		HideShare:         input.HideShare,
		AllowDownload:     input.AllowDownload,
		TaggedUserIDs:     taggedUsers,
		MentionUsernames:  mentions,
		PublishAt:         input.PublishAt,
		CreatedAt:         time.Now(),
		IsBranded:         input.IsBranded,
		AIGenerated:       input.AIGenerated,
		LinkURL:           linkURL,
		LocationPlaceID:   locationPlaceID,
		Genres:            videoCredits.Genres,
		ReleaseYear:       videoCredits.ReleaseYear,
		ProductionHouse:   productionHouse,
		// Written by the insert itself (posts.tier_required_id), in the
		// same transaction as the row and its PostCreated outbox event.
		TierRequiredID: copyTierID(input.TierRequiredID),
		ActionWrite:    actionWrite,
	}

	// Persist the validated policy verbatim; rev 1 marks "has a policy",
	// rev 0 marks legacy rows that never carried one.
	//
	// P1-1: when there is no typed policy but the old client explicitly
	// asked for a distribution outcome, materialize that intent into the
	// canonical policy. Without this the row and the event would both
	// fall back to "main_feed=true" and silently override the creator.
	effectivePolicy := distPolicy
	if effectivePolicy == nil {
		effectivePolicy = PolicyFromLegacy(input.LegacyDistribution)
	}
	if effectivePolicy != nil {
		stored, mErr := MarshalPolicy(effectivePolicy)
		if mErr != nil {
			return nil, mErr
		}
		p.Distribution = stored
		p.DistributionRev = 1
	}

	// AUTHORITY BEFORE ATTACHMENT (Slice C, C-LB-4).
	//
	// Ordinary create-post used to look up media KIND only, and silently
	// treated a missing row as an image. It never established that the caller
	// uploaded the asset, that processing had finished, or that moderation had
	// passed. Any authenticated user could therefore attach another user's
	// media by id, or publish an asset that was still processing or had already
	// been rejected by safety review.
	//
	// Thread creation already got this right (`threads.go`, Codex P1-6) using
	// the same batched query. The ordinary path is the one people actually use.
	//
	// The FK added by migration 030 prevents a DANGLING row; it says nothing
	// about who owns the media or whether it is safe to publish. Those are
	// different questions and only this check answers them.
	if err := s.verifyMediaAuthority(
		ctx, input.AuthorID, input.MediaIDs, contentType, postType,
	); err != nil {
		return nil, err
	}
	// The cover answers the same questions as an attachment (cover_guard.go):
	// it used to be stored as sent, a stranger's asset included.
	if coverMediaID != nil {
		if err := s.verifyCoverOnWrite(ctx, input.AuthorID, *coverMediaID); err != nil {
			return nil, err
		}
	}

	// Attach media in a single round trip — audit H1.
	// Previously this loop did 1 SELECT per media-id (kind), plus a
	// second SELECT per video (duration), plus a third SELECT for
	// dimensions of the first video. For N media that's ~2N+1
	// queries. BatchGetMediaMetadata folds it into one.
	mediaMeta, mediaErr := s.pgStore.BatchGetMediaMetadata(ctx, input.MediaIDs)
	if mediaErr != nil {
		// Fall back to the per-row helpers below if the batch query
		// fails — keeps post creation working through a transient
		// DB hiccup, just at the old query cost.
		log.Printf("Warning: batch media metadata lookup failed; falling back to per-row: %v", mediaErr)
		mediaMeta = nil
	}

	var maxDuration int
	for _, mediaID := range input.MediaIDs {
		var kind string
		var dur int
		if meta, ok := mediaMeta[mediaID]; ok {
			kind = meta.Kind
			dur = meta.DurationSeconds
		} else {
			// Either the batch failed entirely or the row didn't
			// exist in media_assets. Preserve the legacy
			// "default to image" behaviour from ResolveMediaKind.
			if mediaMeta == nil {
				kind = s.pgStore.ResolveMediaKind(ctx, mediaID)
				if kind == "video" {
					dur = s.pgStore.ResolveMediaDuration(ctx, mediaID)
				}
			} else {
				kind = "image"
			}
		}
		// Slice C / C-CLB-3: the create response carries the accessibility
		// decision the author just made. The composer navigates straight to
		// the post it created, so without this the first render of a brand-new
		// image post is the one render guaranteed to be unlabelled.
		//
		// Absent metadata leaves both zero: no description and not decorative,
		// which is "nobody said" — the state a renderer must treat as missing
		// rather than as an explicit decorative mark.
		meta := mediaMeta[mediaID]
		p.Media = append(p.Media, postgres.PostMedia{
			MediaID:       mediaID,
			Kind:          kind,
			AltText:       meta.AltText,
			AltDecorative: meta.AltDecorative,
		})
		if kind == "video" && dur > maxDuration {
			maxDuration = dur
		}
	}

	// Decide the content_type of a post that carries a video. A reel is what
	// the author posted as a reel; a video is what the author posted as a
	// video (founder, 2026-09-04/05) — and a post is a post (founder,
	// 2026-09-25): a plain "post" with a video stays in the feed rather than
	// being classified into Reels or Tube from the measurement. See
	// resolveVideoContentType.
	var videoMediaID uuid.UUID
	hasVideo := false
	for _, m := range p.Media {
		if m.Kind == "video" {
			videoMediaID = m.MediaID
			hasVideo = true
			break
		}
	}
	// P0-6: locate the voice attachment (media_assets.file_type='audio'
	// surfaces as kind "audio") and classify the post as a voice post.
	var voiceMediaID uuid.UUID
	for _, m := range p.Media {
		if m.Kind == "audio" {
			voiceMediaID = m.MediaID
			break
		}
	}
	if voiceMediaID != uuid.Nil && !hasVideo {
		p.ContentType = "voice"
		contentType = "voice"
	}
	// videoW/videoH are the dimensions transcode measured (0 when pending).
	// Reuse the batch when available; fall back to the per-row helper for
	// the unlikely batch-failed-but-loop-succeeded path.
	var videoW, videoH int
	if hasVideo {
		if meta, ok := mediaMeta[videoMediaID]; ok {
			videoW, videoH = meta.Width, meta.Height
		} else {
			videoW, videoH, _ = s.pgStore.ResolveMediaDimensions(ctx, videoMediaID)
		}
		// Persisting `explicit` is what tells the MediaTranscodeConsumer
		// to leave the kind alone once the measurement lands. A post is
		// reported explicit for that reason: it must not be rewritten into
		// a flick or long_video by a duration and a frame size.
		p.ContentType, p.ContentTypeExplicit = resolveVideoContentType(contentType, maxDuration, videoW, videoH)
	}

	// Attach poll
	if input.Poll != nil {
		var endsAt *time.Time
		if input.Poll.DurationHours != nil && *input.Poll.DurationHours > 0 {
			t := time.Now().Add(time.Duration(*input.Poll.DurationHours) * time.Hour)
			endsAt = &t
		}
		opts := make([]postgres.PollOption, len(input.Poll.Options))
		for i, label := range input.Poll.Options {
			opts[i] = postgres.PollOption{Label: label}
		}
		p.Poll = &postgres.PollData{
			Question:       input.Poll.Question,
			AllowsMultiple: input.Poll.AllowsMultiple,
			EndsAt:         endsAt,
			Options:        opts,
		}
	}

	// Spam detection
	spamResult := s.spam.Check(ctx, input.AuthorID.String(), input.Text, len(input.MediaIDs))
	if spamResult.Score > 0.95 {
		return nil, fmt.Errorf("content rejected: %s", spamResult.Reason)
	}
	reviewStatus := "approved"
	if spamResult.Score > 0.7 {
		reviewStatus = "flagged"
		// Emit spam detection event (best-effort)
		if s.producer != nil {
			go s.producer.PublishSpamDetected(context.Background(), input.AuthorID, spamResult.Reason, spamResult.Score)
		}
	}

	// reels/posttube items 2+3 — video publish gate: a video post is not
	// publicly visible until its media is transcoded AND content-scanned.
	// If the media is already ready, finalize the verdict now; otherwise
	// hold it 'pending' and the MediaTranscodeConsumer flips it when
	// transcode completes. The chunk-1 read-filters already hide every
	// non-'approved' post, so no event-flow change is needed.
	if reviewStatus == "approved" && isVideoContentType(p.ContentType) && videoMediaID != uuid.Nil {
		reviewStatus = s.gateVideoReviewStatus(ctx, videoMediaID)
	}
	// P0-6: voice posts stay out of public surfaces until baseline audio
	// safety completes. Same 'pending' mechanism as the video gate — the
	// read filters already hide every non-approved post.
	if reviewStatus == "approved" && isVoiceContentType(p.ContentType) && voiceMediaID != uuid.Nil {
		reviewStatus = s.gateVoiceReviewStatus(ctx, voiceMediaID)
	}
	// Optional: send every video to human review, not just spam-flagged. Covers
	// 'pending' (still transcoding) too — otherwise the transcode consumer would
	// later flip pending→approved and publish it without review. The reviewer
	// watches the uploaded media directly.
	if s.reviewAllVideos && isVideoContentType(p.ContentType) &&
		(reviewStatus == "approved" || reviewStatus == "pending") {
		reviewStatus = "flagged"
	}
	p.ReviewStatus = reviewStatus

	// Video credits: which tags may link an account, decided before the
	// transaction (it asks identity-profile and graph-service); the insert
	// writes them, spends the tagging limit and announces the requests in
	// the post's own transaction (store/postgres/credits.go).
	// Every long video being created carries a write (possibly empty), which
	// is also how its PostCreated knows the list is this one.
	if isLongVideoContentType(contentType) {
		w := &postgres.CreditsWrite{}
		if len(videoCredits.Credits) > 0 {
			w = s.resolveCreditCandidates(ctx, input.AuthorID, videoCredits.Credits)
		}
		w.PostTitle = title
		p.CreditsWrite = w
	}

	// Build the PostCreated event BEFORE the insert so the outbox row
	// commits in the same transaction as the post (Codex P0-1: event and
	// row are atomic; closes the old commit→outbox dual-write window).
	//
	// A SCHEDULED post (publish_at set) emits nothing here: no PostCreated,
	// so no feed fan-out, no search index, no follower or subscriber
	// notification. The schedule worker emits the identical event — built by
	// the same buildPostCreatedPayload — when it publishes (schedule.go).
	scheduled := p.PublishAt != nil
	p.IsScheduled = scheduled

	// Copyright title review (copyright_title_review.go): a long video over
	// 20 minutes naming a catalogue title, by a non-owner, is published
	// HELD — the hold commits with the post and PostCreated is withheld, so
	// nothing fans out, indexes or notifies until a reviewer clears it. A
	// scheduled post is judged when it goes live (PublishScheduled). A held
	// post may not be members-only (409 COPYRIGHT_REVIEW).
	if !scheduled {
		if err := s.applyCreateTitleHold(ctx, p, maxDuration, hasVideo); err != nil {
			return nil, err
		}
	}
	held := p.TitleReviewWrite != nil

	// Creation is always revision 1; later transitions increment.
	createEventType, createPayload := s.createdEventFor(ctx, p, effectivePolicy, maxDuration, videoH)

	// The post, its outbox event and the durable idempotency claim commit
	// together or not at all (C-LB-3.2).
	var idem *postgres.CreateIdempotency
	if input.CreateKey != "" {
		idem = &postgres.CreateIdempotency{
			ActorID:     input.AuthorID,
			ClientKey:   input.CreateKey,
			Fingerprint: input.CreateFingerprint,
		}
	}
	if err := s.writeCreatedPost(ctx, p, createEventType, createPayload, idem); err != nil {
		// A concurrent request won the key. Whether it was the same intent
		// decides between replaying its post and refusing the reuse.
		var replay postgres.ErrCreateKeyReplay
		if errors.As(err, &replay) {
			existing, getErr := s.pgStore.GetPost(ctx, replay.PostID)
			if getErr != nil {
				return nil, fmt.Errorf("replay created post: %w", getErr)
			}
			if err := s.attachMediaState(ctx, []*postgres.Post{existing}); err != nil {
				return nil, err
			}
			s.AttachAuthorActions(ctx, input.AuthorID, []*postgres.Post{existing})
			return existing, nil
		}
		if errors.Is(err, postgres.ErrCreateKeyConflict) {
			return nil, ErrCreateKeyReused
		}
		return nil, err
	}

	// Record the auto-moderation verdict for video content (audit trail).
	// Skipped while 'pending' — the transcode consumer records the
	// terminal verdict when it finalizes the gate.
	if isVideoContentType(p.ContentType) && reviewStatus != "pending" {
		s.RecordVideoModeration(ctx, p.ID, reviewStatus, spamResult.Score)
	}

	// Route flagged video content to the human-review queue (best-effort).
	if reviewStatus == "flagged" {
		s.enqueueForReview(p, spamResult.Score)
	}

	// Persist the merged @mentions (caption + explicit field) to
	// post_mentions. Scheduled posts too: the rows are a join, not a
	// notification — user.mentioned is only emitted at publish time.
	s.storeMentions(ctx, p.ID, p.ContentType, mentions)

	// Create video_metadata for video content types.
	//
	// ALWAYS for a video attachment, even before transcode has measured it
	// (instant publish, 2026-09-04). The row is the join the
	// MediaTranscodeConsumer uses to find this post when the transcode
	// completes — to flip the `pending` review gate, wire the HLS URL and
	// reclassify. A post created while its media was still processing used
	// to get no row at all, so it could never be released.
	if videoMediaID != uuid.Nil {
		// upload_status starts at "pending" and is flipped to "ready" by
		// the MediaTranscodeCompleted consumer — which is the ONLY other
		// writer of the column. That made the ordering load-bearing in a
		// way no client was told about: a browser that uploaded, waited
		// for the transcode (the natural thing to do, since you want a
		// preview and a duration before filling in the details), and only
		// then created the post, produced a row the completion event had
		// already flown past. Nothing would ever set it, so
		// GET /v1/videos/:id reported a fully-transcoded video as
		// "pending" forever and POST /v1/videos/:id/publish answered 409
		// NOT_READY for the life of the post.
		//
		// The transcode having already finished is not a missed event, it
		// is a fact we can read: seed the column from the live
		// media_assets row instead of asserting a state we haven't
		// checked. A still-processing asset is unchanged — "pending", and
		// the consumer flips it as before.
		uploadStatus := "pending"
		if meta, ok := mediaMeta[videoMediaID]; ok && meta.ProcessingStatus == mediaReady {
			uploadStatus = mediaReady
		}
		vm := &postgres.VideoMetadata{
			PostID:       p.ID,
			UploadStatus: uploadStatus,
			MediaAssetID: &videoMediaID,
		}
		// final_category is the kind the post *is* — what the author chose,
		// or the measurement when they chose nothing (resolveVideoContentType
		// already settled that on p.ContentType). computed_category is the
		// measurement alone, kept for analytics: how many author-flicks are
		// landscape, how many author-videos are short verticals.
		vm.FinalCategory = p.ContentType
		if maxDuration > 0 {
			category, orientation := ClassifyVideo(float64(maxDuration), videoW, videoH)
			vm.DurationSeconds = float64(maxDuration)
			vm.Width = &videoW
			vm.Height = &videoH
			vm.Orientation = orientation
			vm.ComputedCategory = category
		} else {
			// Duration unknown: record the provisional answer; the
			// MediaTranscodeConsumer rewrites the measurement once the
			// transcode reports duration + dimensions.
			vm.ComputedCategory = p.ContentType
			vm.Orientation = deriveOrientation(0, 0)
			if p.ContentType == "flick" {
				vm.Orientation = "portrait"
			}
		}
		if err := s.pgStore.CreateVideoMetadata(ctx, vm); err != nil {
			log.Printf("Warning: failed to create video_metadata for post %s: %v", p.ID, err)
		}
	}

	// Invalidate author content counts cache
	if s.rdb != nil {
		s.rdb.Del(ctx, fmt.Sprintf("post:author-counts:%s", input.AuthorID))
	}

	// PostCreated rides the same transaction as the post row (see
	// CreatePostWithEventIdempotent above) — the outbox worker publishes it
	// to Kafka with retry until success. Everything else a public post does
	// at birth — user.mentioned, trending and hashtag counters, the live
	// pub/sub — is announcePublishedPost, and a scheduled post does none of
	// it until the worker publishes it.
	//
	// The worker's 5s tick stays the floor and the safety net, but waiting
	// it out delayed every post before fan-out had even begun, so we tell it
	// there is work now. Non-blocking, and a no-op if a drain is pending.
	s.NudgeOutbox()
	if held {
		// The hold committed with the row; the author's own reads see it.
		p.ActiveRestrictionCount = 1
		slog.Info("copyright title review: publication held", "post_id", p.ID, "author_id", p.AuthorID,
			"matched_title", p.TitleReviewWrite.MatchedTitle, "awaiting_duration", p.TitleReviewWrite.AwaitingDuration)
	}
	if !scheduled && !held {
		s.announcePublishedPost(p, mentions)
	}

	// The create response is the first render of the post the composer
	// navigates to. It carries the per-media pipeline state and
	// is_processing so the client can show "uploading… improving quality"
	// instead of a broken player.
	if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
		return nil, err
	}
	// The author's view of the action just written (url and status
	// included), built from the write: no read back.
	if actionWrite != nil {
		p.Action = ActionViewFor(&postgres.ActionLink{
			ID: actionWrite.ID, AuthorID: p.AuthorID, URL: actionWrite.URL, Domain: actionWrite.Domain,
			Label: actionWrite.Label, Status: postgres.ActionStatusActive, SafetyStatus: actionWrite.SafetyStatus,
		}, true)
	}
	return p, nil
}

// isVideoContentType reports whether a post content_type carries video
// (short-form reel / flick or long-form). Used to scope auto-moderation.
func isVideoContentType(ct string) bool {
	switch ct {
	case "reel", "flick", "long_video", "video":
		return true
	}
	return false
}

// gateVideoReviewStatus decides a fresh video post's review_status from
// its media's processing + moderation state:
//   - media still processing  → "pending" (transcode consumer flips it)
//   - ready + scan rejected    → "rejected"
//   - ready + scan clean       → "approved"
//
// On a media-service error it fails safe to "pending" — the post stays
// hidden rather than risking an unprocessed or unscanned video going live.
func (s *Service) gateVideoReviewStatus(ctx context.Context, mediaID uuid.UUID) string {
	procStatus, modStatus, err := s.getMediaModeration(ctx, mediaID)
	if err != nil {
		log.Printf("Warning: media moderation check failed for %s, holding pending: %v", mediaID, err)
		return "pending"
	}
	if procStatus != "ready" {
		return "pending"
	}
	if modStatus == "rejected" {
		return "rejected"
	}
	return "approved"
}

// getMediaModeration fetches a media asset's processing_status and
// moderation_status from media-service (GET /v1/media/:id).
func (s *Service) getMediaModeration(ctx context.Context, mediaID uuid.UUID) (processingStatus, moderationStatus string, err error) {
	base := os.Getenv("MEDIA_SERVICE_URL")
	if base == "" {
		base = "http://media-service:8087"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(base, "/")+"/v1/media/"+mediaID.String(), nil)
	if err != nil {
		return "", "", err
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("media-service returned %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			ProcessingStatus string `json:"processing_status"`
			ModerationStatus string `json:"moderation_status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return "", "", err
	}
	return env.Data.ProcessingStatus, env.Data.ModerationStatus, nil
}

func (s *Service) GetPost(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*PostDetail, error) {
	// Tier 1b: cached read. Falls through to pgStore on miss; nil
	// rdb is supported.
	p, err := s.getCachedPostBody(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}

	// reels/posttube item 5: a post the spam detector or auto-moderation
	// flagged/rejected — or one still pending a verdict — must not be
	// reachable by direct link. Feeds already filter on review_status;
	// this closes the GetPost hole. The author still sees their own.
	if p.EffectiveReviewStatus() != "" && p.EffectiveReviewStatus() != "approved" {
		if viewerID == nil || *viewerID != p.AuthorID {
			return nil, nil
		}
	}

	// Instant publish (processing.go): overlay the live media pipeline
	// state — AFTER the cache, never from it — and hide a still-processing
	// post from everyone but its author. Same 404 as the other gates.
	if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
		return nil, err
	}
	if hiddenFromViewer(p, viewerID) {
		return nil, nil
	}

	// Visibility gate on the direct-link read. The engagement endpoints,
	// feed batch and repost paths were already gated; this was the one
	// door left open — anyone with the id could read a private or
	// followers-only post. Renders as 404, not 403: a denial that
	// confirms existence is itself a leak.
	if !s.viewerMayViewPost(ctx, p, viewerID) {
		return nil, nil
	}
	// Age-restricted (age_gate.go): only after the viewer may see the post
	// at all, so the refusal never confirms a post they could not open.
	if err := s.checkAgeGate(ctx, p, viewerID); err != nil {
		return nil, err
	}

	counts, err := s.countsForPost(ctx, id)
	if err != nil {
		return nil, err
	}

	// Load poll if present
	hasPoll, _ := s.pgStore.HasPoll(ctx, id)
	if hasPoll {
		poll, err := s.pgStore.GetPoll(ctx, id)
		if err == nil && poll != nil {
			if viewerID != nil {
				votes, _ := s.pgStore.GetUserPollVotes(ctx, id, *viewerID)
				poll.ViewerVotes = votes
			}
			p.Poll = poll
		}
	}

	detail := &PostDetail{Post: p, Counts: counts}
	detail.ViewCount = s.getViewCount(ctx, id)
	detail.Author = s.fetchPostAuthor(ctx, viewerID, p.AuthorID)

	// Repost count from PG
	repostCount, _ := s.pgStore.GetRepostCount(ctx, id)
	detail.RepostCount = repostCount

	// A post is repostable if it's public (non-private) and not a reel
	// (heart_only.go).
	detail.IsRepostable = repostableContent(p.ContentType, p.Visibility)

	// Enrich with viewer-specific state
	if viewerID != nil {
		stored, err := s.scyllaStore.GetReaction(ctx, id, *viewerID)
		if err != nil {
			return nil, fmt.Errorf("load viewer reaction: %w", err)
		}
		// An old emoji reads as the heart (heart_only.go).
		applyViewerReaction(detail, stored)
		bookmarked, err := s.pgStore.IsBookmarked(ctx, *viewerID, id)
		if err != nil {
			return nil, fmt.Errorf("load viewer bookmark: %w", err)
		}
		detail.IsBookmarked = bookmarked

		// Repost state
		repostState, err := s.GetRepostState(ctx, *viewerID, id)
		if err != nil {
			return nil, fmt.Errorf("load viewer repost: %w", err)
		}
		if repostState != nil && repostState.HasReposted {
			detail.ViewerRepost = repostState
			detail.HasReposted = true
		}
		// MTube (2026-09-27): the viewer's private dislike and Queue state.
		detail.ViewerDisliked = s.viewerDisliked(ctx, viewerID, id)
		detail.ViewerQueued = s.viewerQueued(ctx, *viewerID, id)
	}
	detail.Chapters = s.chaptersForDetail(ctx, p)

	// Tube: the author's channel card on a long video (channels.go).
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, []*PostDetail{detail})
	// Creator Hub (hub_detail.go): like_count / hide_like_count, the
	// related post card, the owner's notify_subscribers.
	s.applyHubDetail(ctx, detail, viewerID)
	// The sound the post plays, for this viewer (sounds.go). After the
	// cache read, like everything else that depends on who is asking.
	s.attachSounds(ctx, viewerID, []*PostDetail{detail})
	// A long video's credits, as this viewer may see them (credits.go).
	s.attachCredits(ctx, detail, viewerID)

	return detail, nil
}

// GetPostsByAuthor returns paginated posts by a specific author. The author
// sees their own posts regardless of review status (a flagged reel still
// shows in their own profile grid); every other viewer sees only approved.
//
// sort is latest (pinned first, newest), popular or oldest (T-21,
// postgres/author_sort.go). A cursor minted for another sort, or one that does
// not decode, is postgres.ErrInvalidAuthorCursor.
func (s *Service) GetPostsByAuthor(ctx context.Context, authorID uuid.UUID, contentType string, sort postgres.AuthorPostSort, limit int, cursor string, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	return s.GetPostsByAuthorFiltered(ctx, AuthorListFilter{
		AuthorID: authorID, ContentType: contentType, Sort: sort, Limit: limit, Cursor: cursor,
	}, viewerID)
}

// AuthorListFilter is one page of GET /v1/posts/by-author/:id. MembersOnly
// (?members_only=true, 2026-10-23) keeps only the author's members-only
// posts; every row still goes through the members-only rule, so a
// non-member gets them redacted (the "members" shelf of a profile shows the
// lock cards).
type AuthorListFilter struct {
	AuthorID    uuid.UUID
	ContentType string
	Sort        postgres.AuthorPostSort
	Limit       int
	Cursor      string
	MembersOnly bool
}

// authorPostsQuery is the store query for one page of f.
func authorPostsQuery(f AuthorListFilter, isAuthor bool) postgres.AuthorPostsQuery {
	return postgres.AuthorPostsQuery{
		AuthorID: f.AuthorID, ContentType: f.ContentType, Sort: f.Sort, Limit: f.Limit, Cursor: f.Cursor,
		IncludeNonApproved: isAuthor, MembersOnly: f.MembersOnly,
	}
}

// GetPostsByAuthorFiltered is GetPostsByAuthor with the list's filters.
func (s *Service) GetPostsByAuthorFiltered(ctx context.Context, f AuthorListFilter, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	authorID := f.AuthorID
	isAuthor := viewerID != nil && *viewerID == authorID
	// Private account: the whole grid is follower-only. An empty page — not
	// an error — is the honest answer for a stranger: the profile itself
	// stays reachable (with is_private=true) and the client renders the
	// "This account is private" state. Fail-closed on an unresolved graph.
	if !isAuthor && !s.canViewAuthor(ctx, viewerID, authorID) {
		return []PostDetail{}, "", nil
	}
	posts, nextCursor, err := s.pgStore.GetPostsByAuthorQuery(ctx, authorPostsQuery(f, isAuthor))
	if err != nil {
		return nil, "", err
	}

	// Visibility filter for the profile grid. ONE graph lookup covers the
	// whole page — every row shares the author — and it runs only when a
	// followers-only post is actually present.
	viewerFollows := func() func() bool {
		checked, follows := false, false
		return func() bool {
			if !checked {
				checked = true
				if viewerID != nil {
					f, err := s.checkViewerFollowsAuthor(ctx, *viewerID, authorID)
					if err != nil {
						log.Printf("Warning: profile visibility graph lookup failed; hiding restricted posts: %v", err)
					} else {
						follows = f
					}
				}
			}
			return follows
		}
	}()
	// The friends audience needs the other edge too: one more lookup, made
	// only when a friends-only post is on the page. Fail closed.
	viewerMutual := func() func() bool {
		checked, mutual := false, false
		return func() bool {
			if !checked {
				checked = true
				if viewerID != nil {
					m, err := s.checkViewerMutualWithAuthor(ctx, *viewerID, authorID)
					if err != nil {
						log.Printf("Warning: profile friends visibility graph lookup failed; hiding friends posts: %v", err)
					} else {
						mutual = m
					}
				}
			}
			return mutual
		}
	}()

	// Merge counts from Scylla for each visible post
	details := make([]PostDetail, 0, len(posts))
	for _, p := range posts {
		post := p // copy to avoid pointer reuse
		if !isAuthor && !profileGridAllows(post.Visibility, viewerFollows, viewerMutual) {
			continue
		}
		// The store filters on the gate; an open row is never on the
		// members-only list whatever it answered.
		if f.MembersOnly && post.TierRequiredID == nil {
			continue
		}
		counts, _ := s.countsForPost(ctx, p.ID)
		if post.ContentType == "poll" {
			poll, err := s.pgStore.GetPoll(ctx, post.ID)
			if err == nil && poll != nil {
				post.Poll = poll
			}
		}
		details = append(details, PostDetail{Post: &post, Counts: counts})
	}

	// Instant publish (processing.go): overlay live media state, drop what
	// this viewer may not see while it processes.
	details, err = s.attachMediaStateToDetails(ctx, details, viewerID)
	if err != nil {
		return nil, "", err
	}
	// Members-only rows: redacted for this viewer (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	return details, nextCursor, nil
}

// GetRecentPosts returns recent public posts from all users with engagement counts.
//
// Audit: this explore/recent surface previously went straight from the store
// query to hydration, never calling canViewPosts — the one privacy_gate.go
// choke point every other read path (GetPostsByIDs, GetPostsByAuthor,
// comments, ...) funnels through. That let a private author's posts, and a
// deactivated/pending-delete author's posts (post_hidden_authors), surface here
// even though every other surface correctly hid them. viewerID is now
// threaded through so the same fail-closed gate applies.
// contentTypes narrows the page (legacy spellings normalized); empty = all.
func (s *Service) GetRecentPosts(ctx context.Context, viewerID *uuid.UUID, excludeAuthor *uuid.UUID, contentTypes []string, category string, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetRecentPosts(ctx, excludeAuthor, contentTypes, category, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	authorSet := make(map[uuid.UUID]struct{}, len(posts))
	authorIDs := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if viewerID != nil && *viewerID == p.AuthorID {
			continue
		}
		if _, ok := authorSet[p.AuthorID]; !ok {
			authorSet[p.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, p.AuthorID)
		}
	}
	viewableAuthor := s.canViewPosts(ctx, viewerID, authorIDs)

	details := make([]PostDetail, 0, len(posts))
	for _, p := range posts {
		if viewerID == nil || *viewerID != p.AuthorID {
			if !viewableAuthor[p.AuthorID] {
				continue
			}
		}
		post := p
		counts, _ := s.countsForPost(ctx, p.ID)
		if post.ContentType == "poll" {
			poll, err := s.pgStore.GetPoll(ctx, post.ID)
			if err == nil && poll != nil {
				post.Poll = poll
			}
		}
		details = append(details, PostDetail{Post: &post, Counts: counts})
	}

	// Instant publish (processing.go): overlay live media state, drop what
	// this viewer may not see while it processes.
	details, err = s.attachMediaStateToDetails(ctx, details, viewerID)
	if err != nil {
		return nil, "", err
	}
	// Members-only rows: redacted for this viewer (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	return details, nextCursor, nil
}

// GetPostsByIDs returns a map of post_id → PostDetail for the given IDs.
// If viewerID is provided, viewer-specific state (reaction, bookmark) is included.
//
// It is POST /v1/posts/batch, which feed-service hydrates every feed from,
// and the read behind playlists, watch later, continue watching and
// history. Members-only rows are redacted for this viewer on the way out
// (members_read.go), whatever loaded them.
func (s *Service) GetPostsByIDs(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
	load := s.loadPostsByIDs
	if s.batchPostsSeam != nil {
		load = s.batchPostsSeam
	}
	result, err := load(ctx, ids, viewerID)
	if err != nil {
		return nil, err
	}
	page := make([]*PostDetail, 0, len(result))
	for _, detail := range result {
		page = append(page, detail)
	}
	s.redactDetailPtrsForViewer(ctx, viewerID, page)
	return result, nil
}

// loadPostsByIDs is GetPostsByIDs before the members-only redaction: every
// visibility drop and the viewer-specific state.
func (s *Service) loadPostsByIDs(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
	posts, err := s.pgStore.GetPostsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Instant publish (processing.go): one media_assets round trip for the
	// whole page, overlaid onto each post before the visibility loop.
	postPtrs := make([]*postgres.Post, len(posts))
	for i := range posts {
		postPtrs[i] = &posts[i]
	}
	if err := s.attachMediaState(ctx, postPtrs); err != nil {
		return nil, err
	}

	// Page hydration must stay batch-shaped. The previous loop issued one
	// PostgreSQL query for every bookmark and repost count/state, which merely
	// moved the client's N+1 problem behind the gateway. PostgreSQL-owned state
	// is loaded in a constant number of round trips for the whole page.
	repostCounts, err := s.pgStore.BatchGetRepostCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load repost counts: %w", err)
	}
	bookmarks := map[uuid.UUID]bool{}
	activeReposts := map[uuid.UUID]*postgres.Repost{}
	if viewerID != nil {
		bookmarks, err = s.pgStore.BatchIsBookmarked(ctx, *viewerID, ids)
		if err != nil {
			return nil, fmt.Errorf("load viewer bookmarks: %w", err)
		}
		activeReposts, err = s.pgStore.BatchGetActiveReposts(ctx, *viewerID, ids)
		if err != nil {
			return nil, fmt.Errorf("load viewer reposts: %w", err)
		}
	}

	// Scylla partitions reactions and counters by post. The store helper runs
	// those independent point reads with bounded concurrency, so one slow
	// partition cannot turn this into a serial page-length latency chain.
	countsByPost, err := s.scyllaStore.BatchGetCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load post counts: %w", err)
	}
	if err := s.overlayCommentCounts(ctx, ids, countsByPost); err != nil {
		return nil, err
	}
	reactions := map[uuid.UUID]string{}
	if viewerID != nil {
		reactions, err = s.scyllaStore.BatchGetReactions(ctx, ids, *viewerID)
		if err != nil {
			return nil, fmt.Errorf("load viewer reactions: %w", err)
		}
	}

	// Private accounts: ONE graph round trip for the page's distinct authors
	// (≤100 per call, 3s cache). Fanout wrote these timeline rows without
	// consulting the author's account_visibility, and a follower who was
	// removed — or a viewer who never followed a since-private author —
	// must not read them here. Fail-closed: an unresolved author is dropped.
	authorSet := make(map[uuid.UUID]struct{}, len(posts))
	authorIDs := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if viewerID != nil && *viewerID == p.AuthorID {
			continue
		}
		if _, ok := authorSet[p.AuthorID]; !ok {
			authorSet[p.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, p.AuthorID)
		}
	}
	viewableAuthor := s.canViewPosts(ctx, viewerID, authorIDs)
	ageOK := s.ageAllowance(ctx, viewerID)
	edges := s.followEdgesOfRestrictedPosts(ctx, posts, viewerID)

	result := make(map[uuid.UUID]*PostDetail, len(posts))
	for _, p := range posts {
		post := p // copy to avoid pointer reuse

		if viewerID == nil || *viewerID != post.AuthorID {
			if !viewableAuthor[post.AuthorID] {
				continue
			}
			// Per-post visibility: the same rule as the direct read
			// (viewerMayViewPost), so the batch is never a way round it.
			// This used to drop only `private` and trust the fan-out
			// recipient set for `followers` — but this route is reachable
			// by any signed-in user with a post id, and a timeline row
			// outlives an unfollow whose purge failed. Private sharing
			// stays a single-post gate: a share list is never listed.
			if !batchVisibilityAllowsEdges(post.Visibility, edges[post.AuthorID]) {
				continue
			}
		}

		// reels/posttube item 5: hide non-approved posts (flagged /
		// rejected / pending) from everyone but the author — mirrors the
		// GetPost gate so feed hydration never surfaces moderated-out
		// content even when fanout already wrote a recipient timeline row.
		if post.EffectiveReviewStatus() != "" && !strings.EqualFold(post.EffectiveReviewStatus(), "approved") {
			if viewerID == nil || *viewerID != post.AuthorID {
				continue
			}
		}

		// Instant publish: a post whose media is not yet ready+passed is
		// the author's alone. Feed fanout wrote the follower timeline rows
		// at create time; this is where they stay hidden until the media
		// lands (feed-service mirrors it at its hydration tail).
		if hiddenFromViewer(&post, viewerID) {
			continue
		}
		// Creator Hub: an age-restricted post leaves feed hydration for
		// anonymous / under-18 / unknown-age viewers (age_gate.go).
		if !ageOK(&post) {
			continue
		}

		counts := countsByPost[post.ID]
		if counts == nil {
			counts = &scylla.Counts{}
		}

		detail := &PostDetail{
			Post:         &post,
			Counts:       counts,
			RepostCount:  repostCounts[post.ID],
			IsRepostable: repostableContent(post.ContentType, post.Visibility),
		}

		// Enrich with viewer-specific state
		if viewerID != nil {
			// An old emoji reads as the heart (heart_only.go).
			applyViewerReaction(detail, reactions[post.ID])
			detail.IsBookmarked = bookmarks[post.ID]
			if repost := activeReposts[post.ID]; repost != nil {
				detail.HasReposted = true
				detail.ViewerRepost = &RepostStateResult{
					HasReposted: true,
					RepostID:    &repost.ID,
					Type:        repost.RepostType,
					QuoteText:   repost.QuoteText,
					CreatedAt:   repost.CreatedAt.Format(time.RFC3339),
				}
			}
		}

		// Enrich with poll data if post is a poll
		if post.ContentType == "poll" {
			poll, err := s.pgStore.GetPoll(ctx, post.ID)
			if err == nil && poll != nil {
				if viewerID != nil {
					votes, _ := s.pgStore.GetUserPollVotes(ctx, post.ID, *viewerID)
					poll.ViewerVotes = votes
				}
				post.Poll = poll
				detail.Post = &post
			}
		}

		applyLikeCountPrivacy(detail, viewerID, false)
		result[post.ID] = detail
	}

	// The sounds the page plays: one read and one audience decision for
	// every row (sounds.go). Never a reason to fail the page.
	page := make([]*PostDetail, 0, len(result))
	for _, detail := range result {
		page = append(page, detail)
	}
	s.attachSounds(ctx, viewerID, page)

	return result, nil
}

// GetAuthorCounts returns post counts grouped by content type.
// GetAuthorCounts is the ungated count (the admin console's creator view,
// AdminCreatorCounts). Viewer-facing reads go through
// GetAuthorCountsForViewer.
func (s *Service) GetAuthorCounts(ctx context.Context, authorID uuid.UUID) (map[string]int64, error) {
	return s.pgStore.GetPostCountsByAuthor(ctx, authorID)
}

// GetAuthorCountsForViewer is GetAuthorCounts behind the profile's own
// audience rule (security sweep #24): the author always; anyone else only
// if canViewAuthor says so — the same fail-closed answer GetPostsByAuthor
// gives, so a private account's post counts are not readable by a stranger
// whose grid is empty, and a blocked viewer learns nothing. A refused
// viewer gets the empty map (every count zero), never an error.
func (s *Service) GetAuthorCountsForViewer(ctx context.Context, authorID uuid.UUID, viewerID *uuid.UUID) (map[string]int64, error) {
	isAuthor := viewerID != nil && *viewerID == authorID
	if !isAuthor && !s.canViewAuthor(ctx, viewerID, authorID) {
		return map[string]int64{}, nil
	}
	return s.pgStore.GetPostCountsByAuthor(ctx, authorID)
}

// TogglePin sets or unsets pinned status, enforcing max 3 pinned per author.
// MaxPinnedPosts is how many posts one author may pin to their profile.
const MaxPinnedPosts = 3

// ErrPinLimit refuses a pin past MaxPinnedPosts (409 PIN_LIMIT, details
// {max: 3}). Pinning a post that is already pinned is not counted.
var ErrPinLimit = errors.New("PIN_LIMIT")

// pinStore is the pin read and write; the Postgres store satisfies it.
type pinStore interface {
	CountPinnedByAuthor(ctx context.Context, authorID, exceptPostID uuid.UUID) (int, error)
	SetPinned(ctx context.Context, postID, authorID uuid.UUID, pinned bool) error
}

func (s *Service) pinReads() pinStore {
	if s.pins != nil {
		return s.pins
	}
	return s.pgStore
}

func (s *Service) TogglePin(ctx context.Context, postID, authorID uuid.UUID, pinned bool) error {
	pins := s.pinReads()
	if pinned {
		count, err := pins.CountPinnedByAuthor(ctx, authorID, postID)
		if err != nil {
			return err
		}
		if count >= MaxPinnedPosts {
			return ErrPinLimit
		}
	}
	return pins.SetPinned(ctx, postID, authorID, pinned)
}

func (s *Service) React(ctx context.Context, postID, userID uuid.UUID, reaction string) error {
	// Heart only (heart_only.go): before anything is read or written.
	if err := checkPostReaction(reaction); err != nil {
		return err
	}
	// Audit C5 + H2: one fetch covers visibility + author-id for
	// the PostReacted event. Was: visibility check did a GetPost,
	// the goroutine below did *another* GetPost just for AuthorID.
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return err
	}
	// The liked list's record first (liked_reels.go): idempotent, so a
	// failure here or below is an error the client retries.
	if err := s.indexReaction(ctx, postID, userID, reaction, true); err != nil {
		return err
	}
	added, err := s.scyllaStore.React(ctx, postID, userID, reaction)
	if err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	// The "Liked videos" collection follows a like made through the
	// reactions route too (Android likes this way; the web uses /like), and a
	// like changed to another reaction leaves it.
	s.syncLikedCollection(ctx, userID, post, reaction == "like")

	// Audit H4: PostReacted via outbox. Synchronous insert so a
	// process crash in the React goroutine window doesn't drop the
	// notification trigger. Only for a NEW reaction: a repeat tap or a
	// change of type adds no reaction to announce (see reactionEvent).
	if s.producer != nil && added {
		payload := events.PostReactedPayload{
			PostID:       postID.String(),
			PostAuthorID: post.AuthorID.String(),
			ReactorID:    userID.String(),
			ReactType:    reaction,
			CreatedAt:    time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostReacted, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue PostReacted to outbox: %v", err)
		}
	}

	// Ephemeral Redis pub/sub for live feed viewers — best-effort.
	go func() {
		bgCtx := context.Background()
		counts, _ := s.countsForPost(bgCtx, postID)
		if counts != nil {
			signal, _ := json.Marshal(map[string]any{
				"type": "post_update",
				"payload": map[string]any{
					"post_id":     postID.String(),
					"update_type": "reaction",
					"actor_id":    userID.String(),
					"likes":       counts.Likes,
					"comments":    counts.Comments,
				},
			})
			s.rdb.Publish(bgCtx, "feed:post_update", signal)
		}
	}()

	return nil
}

func (s *Service) Unreact(ctx context.Context, postID, userID uuid.UUID) error {
	if err := s.checkEngagementVisibility(ctx, postID, userID); err != nil {
		return err
	}
	if err := s.indexReaction(ctx, postID, userID, "", false); err != nil {
		return err
	}
	removed, err := s.scyllaStore.Unreact(ctx, postID, userID)
	if err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	// An unlike through the reactions route leaves "Liked videos" too.
	if removed {
		if post, getErr := s.pgStore.GetPost(ctx, postID); getErr == nil {
			s.syncLikedCollection(ctx, userID, post, false)
		}
	}

	// PostUnreacted, so feed-service's velocity counter comes back down
	// (it used to only ever go up). Only when a reaction was actually
	// removed. Author id is not loaded on this path; the consumers key on
	// post and reactor.
	if s.producer != nil && removed {
		payload := events.PostReactedPayload{
			PostID:    postID.String(),
			ReactorID: userID.String(),
			CreatedAt: time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostUnreacted, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue PostUnreacted to outbox: %v", err)
		}
	}

	// Fire-and-forget: Redis publish in background
	go func() {
		bgCtx := context.Background()
		counts, _ := s.countsForPost(bgCtx, postID)
		if counts != nil {
			signal, _ := json.Marshal(map[string]any{
				"type": "post_update",
				"payload": map[string]any{
					"post_id":     postID.String(),
					"update_type": "reaction",
					"actor_id":    userID.String(),
					"likes":       counts.Likes,
					"comments":    counts.Comments,
				},
			})
			s.rdb.Publish(bgCtx, "feed:post_update", signal)
		}
	}()

	return nil
}

func (s *Service) GetMyReaction(ctx context.Context, postID, userID uuid.UUID) (string, error) {
	stored, err := s.scyllaStore.GetReaction(ctx, postID, userID)
	if err != nil {
		return "", err
	}
	// An old emoji reads as the heart (heart_only.go).
	return heartViewerReaction(stored), nil
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

func (s *Service) AddComment(ctx context.Context, postID, userID uuid.UUID, text string) (uuid.UUID, error) {
	commentID, err := s.scyllaStore.AddComment(ctx, postID, userID, text)
	if err != nil {
		return uuid.Nil, err
	}

	// Fire-and-forget: Kafka + Redis publish in background
	go func() {
		bgCtx := context.Background()

		// Emit Kafka event
		if s.producer != nil {
			post, err := s.pgStore.GetPost(bgCtx, postID)
			if err == nil && post != nil {
				if err := s.producer.PublishCommentCreated(bgCtx, commentID, postID, post.AuthorID, userID, text); err != nil {
					log.Printf("Warning: failed to publish CommentCreated event: %v", err)
				}
			}
		}

		// Publish real-time update for live feed viewers
		counts, _ := s.countsForPost(bgCtx, postID)
		if counts != nil {
			signal, _ := json.Marshal(map[string]any{
				"type": "post_update",
				"payload": map[string]any{
					"post_id":     postID.String(),
					"update_type": "comment",
					"actor_id":    userID.String(),
					"comment_id":  commentID.String(),
					"likes":       counts.Likes,
					"comments":    counts.Comments,
				},
			})
			s.rdb.Publish(bgCtx, "feed:post_update", signal)
		}
	}()

	return commentID, nil
}

func (s *Service) ListComments(ctx context.Context, postID uuid.UUID, limit int) ([]scylla.Comment, error) {
	return s.scyllaStore.ListComments(ctx, postID, limit)
}

// --- Bookmark methods ---

func (s *Service) AddBookmark(ctx context.Context, userID, postID uuid.UUID) error {
	if err := s.checkEngagementVisibility(ctx, postID, userID); err != nil {
		return err
	}
	if err := s.pgStore.AddBookmark(ctx, userID, postID); err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	return nil
}

func (s *Service) RemoveBookmark(ctx context.Context, userID, postID uuid.UUID) error {
	// No visibility gate on remove — a user who already bookmarked
	// must always be able to clean up their own row even if the
	// post's visibility tightened (author switched to followers-only).
	if err := s.pgStore.RemoveBookmark(ctx, userID, postID); err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	return nil
}

// GetBookmarks is the viewer's saved list. contentTypes is the canonical
// filter from the handler (nil means every type); it is applied in SQL so
// a page of long videos is a full page, not a page of everything with the
// flicks removed afterwards. scope narrows it to one saved collection or the
// default one (saved_collections.go; the zero scope is every saved post).
func (s *Service) GetBookmarks(ctx context.Context, userID uuid.UUID, contentTypes []string, limit int, cursor string, scope postgres.SavedScope) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetBookmarks(ctx, userID, contentTypes, limit, cursor, scope)
	if err != nil {
		return nil, "", err
	}

	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.countsForPost(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts, IsBookmarked: true}
	}

	// Instant publish (processing.go): the bookmarker is the viewer.
	details, err = s.attachMediaStateToDetails(ctx, details, &userID)
	if err != nil {
		return nil, "", err
	}
	// Members-only rows: a bookmark outlives a membership (members_read.go).
	s.redactDetailsForViewer(ctx, &userID, details)
	ptrs := make([]*PostDetail, len(details))
	for i := range details {
		ptrs[i] = &details[i]
	}
	s.attachChannelRefs(ctx, userID, ptrs)
	return details, nextCursor, nil
}

// --- Poll methods ---

// GetPoll returns poll data with vote counts and optionally the viewer's votes.
func (s *Service) GetPoll(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (*postgres.PollData, error) {
	poll, err := s.pgStore.GetPoll(ctx, postID)
	if err != nil {
		return nil, err
	}
	if poll == nil {
		return nil, nil
	}

	if viewerID != nil {
		votes, _ := s.pgStore.GetUserPollVotes(ctx, postID, *viewerID)
		poll.ViewerVotes = votes
	}

	return poll, nil
}

// CastVote and CastPollVote both live in polls.go now — one implementation
// serving both vote routes, returning the typed errors the handlers map to
// stable codes.

// ============================================================
// New Engagement System (dual-write: Redis hot path + async consumers)
// ============================================================

// LikeToggleResult is the response shape for the like toggle API.
type LikeToggleResult struct {
	Liked bool  `json:"liked"`
	Count int64 `json:"count"`
}

// ToggleLike executes the atomic Lua toggle and publishes an engagement event.
func (s *Service) ToggleLike(ctx context.Context, postID, userID uuid.UUID) (*LikeToggleResult, error) {
	// Audit C5 + H2: one fetch covers visibility, NoLikes flag, and
	// the author-id used for the PostReacted event below.
	// Previously the handler did a GetPost just to check NoLikes,
	// then the service did another GetPost just to read AuthorID —
	// two full Postgres+Scylla fetches per like toggle.
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return nil, err
	}
	if post.NoLikes {
		return nil, ErrLikesDisabled
	}

	// Rate limit
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:like:%s", userID), engagement.LikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// Lua atomic toggle
	result, err := engagement.ToggleLike(ctx, s.rdb, userID, postID)
	if err != nil {
		return nil, err
	}

	// Sync ScyllaDB reactions_by_post so feed hydration sees the change immediately
	if result.IsSet {
		if _, err := s.scyllaStore.React(ctx, postID, userID, "like"); err != nil {
			log.Printf("Warning: failed to write reaction to ScyllaDB: %v", err)
		}
		// Like and tune are mutually exclusive (tune.go): a like being
		// SET clears the viewer's private dislike.
		if err := s.pgStore.DeleteTune(ctx, userID, postID); err != nil {
			log.Printf("Warning: failed to clear tune on like: %v", err)
		}
	} else {
		if _, err := s.scyllaStore.Unreact(ctx, postID, userID); err != nil {
			log.Printf("Warning: failed to remove reaction from ScyllaDB: %v", err)
		}
	}
	// The liked list's record (liked_reels.go). Best-effort: the toggle has
	// already flipped, and a retried toggle would flip it back.
	s.indexReactionBestEffort(ctx, postID, userID, "like", result.IsSet)
	// A liked long video joins the viewer's "Loved" collection; an unliked
	// one leaves it (system_playlists.go; reels are never written there).
	s.syncLikedCollection(ctx, userID, post, result.IsSet)
	s.invalidateFeedHydration(ctx, userID, postID)

	// Author already loaded above — no second GetPost needed.
	authorID := post.AuthorID

	// Self-engagement check (return early but don't error, Lua already toggled)
	// We do the check here for the event publishing. The handler should block self-likes before calling this.

	// Build and publish engagement event async
	eventType := engagement.EventPostLiked
	if !result.IsSet {
		eventType = engagement.EventPostUnliked
	}

	if s.engProducer != nil {
		event := engagement.BuildEvent(eventType, postID, userID, authorID, postID, "post", "like", result.IsSet, result.Seq, result.ActionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish engagement event: %v", err)
			}
		}()
	}

	// Audit H4: route the legacy PostReacted notification trigger
	// through the outbox. Was fire-and-forget Kafka in a goroutine;
	// a crash window dropped the notification.
	if s.producer != nil && result.IsSet {
		payload := events.PostReactedPayload{
			PostID:       postID.String(),
			PostAuthorID: authorID.String(),
			ReactorID:    userID.String(),
			ReactType:    "like",
			CreatedAt:    time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostReacted, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue PostReacted (ToggleLike) to outbox: %v", err)
		}
	}

	return &LikeToggleResult{Liked: result.IsSet, Count: result.Count}, nil
}

// BookmarkToggleResult is the response shape for the bookmark toggle API.
type BookmarkToggleResult struct {
	Bookmarked bool `json:"bookmarked"`
}

// ToggleBookmarkNew executes the atomic Lua toggle for bookmarks.
// NO notification, NO WebSocket — bookmarks are completely private.
func (s *Service) ToggleBookmarkNew(ctx context.Context, postID, userID uuid.UUID) (*BookmarkToggleResult, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:bookmark:%s", userID), engagement.BookmarkLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	result, err := engagement.ToggleBookmark(ctx, s.rdb, userID, postID)
	if err != nil {
		return nil, err
	}

	// Keep the Postgres saved_items row in sync so the /v1/saved page mirrors
	// the post bookmark icon. Best-effort: failures here don't unwind the
	// Redis/Scylla bookmark — the icon's source of truth is still the
	// engagement layer.
	if result.IsSet {
		if _, err := s.pgStore.SaveItem(ctx, userID, "post", postID, nil); err != nil {
			log.Printf("Warning: failed to mirror bookmark into saved_items: %v", err)
		}
	} else {
		if err := s.pgStore.UnsaveItemByTarget(ctx, userID, "post", postID); err != nil {
			log.Printf("Warning: failed to remove bookmark from saved_items: %v", err)
		}
	}

	// Publish engagement event for durable write (ScyllaDB consumer only — no notification, no WS)
	eventType := engagement.EventPostBookmarked
	if !result.IsSet {
		eventType = engagement.EventPostUnbookmarked
	}

	if s.engProducer != nil {
		event := engagement.BuildEvent(eventType, postID, userID, uuid.Nil, postID, "post", "bookmark", result.IsSet, result.Seq, result.ActionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish bookmark event: %v", err)
			}
		}()
	}

	return &BookmarkToggleResult{Bookmarked: result.IsSet}, nil
}

// CommentLikeToggleResult is the response shape for the comment like toggle API.
type CommentLikeToggleResult struct {
	Liked        bool  `json:"liked"`
	Count        int64 `json:"count"`
	DislikeCount int64 `json:"dislike_count"`
}

// ToggleCommentLike is the legacy like route, now a toggle of the viewer's
// reaction between postgres.LikeEmoji and none on comment_reactions (2026-09-27),
// so old clients and the emoji UI count the same rows. Count is
// reaction_count. The Redis Lua toggle and the like_count column are no
// longer written for likes; dislikes keep their own path.
func (s *Service) ToggleCommentLike(ctx context.Context, commentID, userID uuid.UUID) (*CommentLikeToggleResult, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment_like:%s", userID), engagement.CommentLikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}
	comment, err := s.visibleCommentForViewer(ctx, commentID, userID)
	if err != nil {
		return nil, err
	}

	liked, err := s.pgStore.ToggleCommentHeart(ctx, commentID, userID)
	if err != nil {
		return nil, err
	}
	summary, err := s.pgStore.GetCommentReactionSummary(ctx, commentID, &userID)
	if err != nil {
		return nil, err
	}
	dislikes, err := s.pgStore.GetCommentDislikeCount(ctx, commentID)
	if err != nil {
		slog.Warn("comment dislike count read failed", "comment_id", commentID, "err", err)
	}

	s.publishCommentReactionEvents(ctx, comment, userID, postgres.LikeEmoji, liked)
	s.publishCommentChange(comment.PostID, commentID, comment.ParentID, CommentChangeReaction, userID)
	return &CommentLikeToggleResult{Liked: liked, Count: int64(summary.ReactionCount), DislikeCount: dislikes}, nil
}

// CommentDislikeToggleResult is the response shape for the comment dislike toggle API.
type CommentDislikeToggleResult struct {
	Disliked     bool  `json:"disliked"`
	DislikeCount int64 `json:"dislike_count"`
	LikeCount    int64 `json:"like_count"`
}

// ToggleCommentDislike executes the atomic Lua toggle for comment dislikes with mutual exclusion.
func (s *Service) ToggleCommentDislike(ctx context.Context, commentID, userID uuid.UUID) (*CommentDislikeToggleResult, error) {
	// The post's read gate first (2026-09-29): the like toggle already
	// refused comments under a post the viewer may not open; the dislike
	// did not, and its counts answered for private and 18+ posts.
	if err := s.gateCommentPost(ctx, commentID, &userID); err != nil {
		return nil, err
	}
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment_like:%s", userID), engagement.CommentLikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	result, err := engagement.ToggleCommentDislike(ctx, s.rdb, userID, commentID)
	if err != nil {
		return nil, err
	}

	// Update PostgreSQL dislike_count
	dislikeDelta := 1
	if !result.IsSet {
		dislikeDelta = -1
	}
	if err := s.pgStore.IncrementCommentDislikeCount(ctx, commentID, dislikeDelta); err != nil {
		log.Printf("Warning: failed to update comment dislike_count: %v", err)
	}

	// If a like was removed by mutual exclusion, decrement like_count in PG
	if result.OppositeRemoved {
		if err := s.pgStore.IncrementCommentLikeCount(ctx, commentID, -1); err != nil {
			log.Printf("Warning: failed to update comment like_count: %v", err)
		}
	}

	eventType := engagement.EventCommentDisliked
	if !result.IsSet {
		eventType = engagement.EventCommentUndisliked
	}

	if s.engProducer != nil {
		event := engagement.BuildEvent(eventType, uuid.Nil, userID, uuid.Nil, commentID, "comment", "dislike", result.IsSet, result.Seq, result.ActionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment dislike event: %v", err)
			}
		}()
		// If like was removed, also publish that event
		if result.OppositeRemoved {
			likeEvent := engagement.BuildEvent(engagement.EventCommentUnliked, uuid.Nil, userID, uuid.Nil, commentID, "comment", "like", false, result.Seq, result.ActionTS)
			go func() {
				if err := s.engProducer.Publish(context.Background(), likeEvent); err != nil {
					log.Printf("Warning: failed to publish comment unlike event: %v", err)
				}
			}()
		}
	}

	// No notifications for dislikes

	if c, err := s.pgStore.GetCommentByID(ctx, commentID); err == nil && c != nil {
		s.publishCommentChange(c.PostID, commentID, c.ParentID, CommentChangeReaction, userID)
	}
	return &CommentDislikeToggleResult{Disliked: result.IsSet, DislikeCount: result.DislikeCount, LikeCount: result.LikeCount}, nil
}

// ShareResult is the response shape for the share API.
type ShareResult struct {
	Shared bool  `json:"shared"`
	Count  int64 `json:"count"`
}

// SharePost creates a share record. Reposts are idempotent (409 on duplicate).
func (s *Service) SharePost(ctx context.Context, postID, userID uuid.UUID, shareType, quoteText string) (*ShareResult, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:share:%s", userID), engagement.ShareLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// Check circle restriction
	post, err := s.loadRepostTarget(ctx, postID)
	if err != nil || post == nil {
		return nil, fmt.Errorf("POST_NOT_FOUND")
	}
	// A reel is never reposted (heart_only.go); an external share is not a
	// repost and is unaffected.
	if isRepostShareType(shareType) {
		if err := checkRepostContentType(post.ContentType); err != nil {
			return nil, err
		}
	}
	if shareRestricted(post.Visibility, shareType) {
		return nil, fmt.Errorf("CIRCLE_SHARE_RESTRICTED")
	}

	// Repost idempotency check
	if shareType == "repost" {
		shareKey := fmt.Sprintf("shared:%s:%s", userID, postID)
		exists, _ := s.rdb.Exists(ctx, shareKey).Result()
		if exists > 0 {
			return nil, fmt.Errorf("ALREADY_SHARED")
		}
	}

	// Update Redis counter + membership
	shareKey := fmt.Sprintf("shared:%s:%s", userID, postID)
	s.rdb.Set(ctx, shareKey, "1", 7*24*time.Hour)
	engKey := fmt.Sprintf("post:eng:%s", postID)
	newCount, _ := s.rdb.HIncrBy(ctx, engKey, "shares", 1).Result()

	// Get sequence for event
	seqKey := fmt.Sprintf("eng:seq:%s", userID)
	seq, _ := s.rdb.Incr(ctx, seqKey).Result()
	s.rdb.Expire(ctx, seqKey, 24*time.Hour)

	if s.engProducer != nil {
		event := engagement.BuildEvent(engagement.EventPostShared, postID, userID, post.AuthorID, postID, "post", "share", true, seq, time.Now().UnixMicro())
		event.ShareType = shareType
		event.QuoteText = quoteText
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				slog.Warn("failed to publish share event", "error", err)
			}
		}()
	}

	return &ShareResult{Shared: true, Count: newCount}, nil
}

// IsBookmarkedWithFallback checks Redis first, falls back to ScyllaDB.
func (s *Service) IsBookmarkedWithFallback(ctx context.Context, userID, postID uuid.UUID) bool {
	bmKey := fmt.Sprintf("bookmarked:%s:%s", userID, postID)
	val, err := s.rdb.Get(ctx, bmKey).Result()
	if err == nil {
		return val == "1"
	}

	// Cache miss → ScyllaDB fallback
	if s.scyllaSession != nil {
		var collection string
		if err := s.scyllaSession.Query(`
			SELECT collection FROM bookmark_check WHERE user_id = ? AND post_id = ?`,
			userID, postID,
		).WithContext(ctx).Scan(&collection); err == nil {
			s.rdb.Set(ctx, bmKey, "1", 24*time.Hour)
			return true
		}
	}

	// Negative cache (shorter TTL)
	s.rdb.Set(ctx, bmKey, "0", time.Hour)
	return false
}

// IsLikedFromRedis checks if the viewer liked a post via Redis.
func (s *Service) IsLikedFromRedis(ctx context.Context, userID, postID uuid.UUID) bool {
	key := fmt.Sprintf("liked:%s:%s", userID, postID)
	exists, _ := s.rdb.Exists(ctx, key).Result()
	return exists > 0
}

// IsSharedFromRedis checks if the viewer shared a post via Redis.
func (s *Service) IsSharedFromRedis(ctx context.Context, userID, postID uuid.UUID) bool {
	key := fmt.Sprintf("shared:%s:%s", userID, postID)
	exists, _ := s.rdb.Exists(ctx, key).Result()
	return exists > 0
}

// CreateCommentPG creates a comment in PostgreSQL with counter update.
// CreateCommentPG creates a comment.
//
// clientKey/fingerprint make the insert durably idempotent (see
// postgres.CreateCommentIdempotent). Both may be empty, in which case the
// caller made no idempotency promise and none is enforced.
func (s *Service) CreateCommentPG(ctx context.Context, postID, authorID uuid.UUID, body, clientKey, fingerprint string) (*postgres.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("INVALID_REQUEST: comment body cannot be blank")
	}
	// Audit C5 + H2: one fetch covers visibility, NoComments flag,
	// and the post-author-id used for the CommentCreated event below.
	// Previously the handler did a GetPost just to check NoComments,
	// then this service did another GetPost just for AuthorID — two
	// full Postgres+Scylla fetches per comment.
	post, err := s.loadPostForEngagement(ctx, postID, authorID)
	if err != nil {
		return nil, err
	}
	// Members-only post: members and the author only (members_read.go).
	if err := s.requireMemberOf(ctx, post, &authorID); err != nil {
		return nil, err
	}
	if post.NoComments {
		return nil, ErrCommentsDisabled
	}
	if err := s.commentAccessAllows(ctx, post, authorID); err != nil {
		return nil, err
	}
	// Comments audience (allow_comments_from: everyone | friends). Resolved
	// by graph-service against the post author's CURRENT setting and the
	// live connection/mutual-follow graph; fail-closed (privacy_gate.go).
	if !s.canComment(ctx, authorID, post.AuthorID) {
		return nil, ErrCommentsRestricted
	}

	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment:%s", authorID), engagement.CommentLimitPerMin, time.Minute) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	comment, replayed, err := s.pgStore.CreateCommentIdempotent(ctx, postID, authorID, body, clientKey, fingerprint)
	if err != nil {
		return nil, err
	}
	if replayed {
		// The intent was already recorded. Return the original comment and do
		// NOT re-run any of the side effects below — bumping trending scores,
		// counters or notifications a second time is exactly the duplication
		// this path exists to prevent.
		return comment, nil
	}

	// Bump trending scores for hashtags in the comment body (max 5 per design spec).
	// Fire-and-forget: comment hashtags influence trending but are not stored per-comment.
	if commentTags := extractHashtags(body); len(commentTags) > 0 {
		if len(commentTags) > 5 {
			commentTags = commentTags[:5]
		}
		go func() {
			bgCtx := context.Background()
			today := time.Now().UTC().Format("2006-01-02")
			key := "trending:hashtags:" + today
			for _, tag := range commentTags {
				if err := s.rdb.ZIncrBy(bgCtx, key, 0.5, tag).Err(); err != nil {
					log.Printf("Warning: failed to bump comment hashtag trending for %s: %v", tag, err)
				}
			}
			s.rdb.Expire(bgCtx, key, 48*time.Hour)
		}()
	}

	// The one writer of comment_count: a visible comment came into being.
	if err := s.pgStore.AdjustCommentCount(ctx, postID, 1); err != nil {
		slog.Warn("failed to increment comment_count", "post_id", postID, "error", err)
	}
	s.publishCommentChange(postID, comment.ID, nil, CommentChangeCreated, authorID)

	// Author already loaded above.
	postAuthorID := post.AuthorID

	// Publish engagement event
	if s.engProducer != nil {
		seqKey := fmt.Sprintf("eng:seq:%s", authorID)
		seq, _ := s.rdb.Incr(ctx, seqKey).Result()
		s.rdb.Expire(ctx, seqKey, 24*time.Hour)

		event := engagement.BuildEvent(engagement.EventCommentCreated, postID, authorID, postAuthorID, comment.ID, "post", "comment", true, seq, time.Now().UnixMicro())
		event.CommentBody = body
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment event: %v", err)
			}
		}()
	}

	// Audit H4: route CommentCreated through the outbox so a
	// crash in the previous goroutine window can't silently drop
	// the notification trigger. The synchronous INSERT runs after
	// the comment row is committed; the outbox worker publishes
	// to Kafka with at-least-once delivery.
	if s.producer != nil {
		payload := events.CommentCreatedPayload{
			CommentID:    comment.ID.String(),
			PostID:       postID.String(),
			PostAuthorID: postAuthorID.String(),
			AuthorID:     authorID.String(),
			Text:         body,
			CreatedAt:    time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.CommentCreated, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue CommentCreated to outbox: %v", err)
		}
	}

	// @mentions in the comment (T-12): blocks, self and the post's audience
	// are applied per mentioned user (comment_mentions.go).
	s.announceCommentMentions(post, authorID, comment.ID, body)

	return comment, nil
}

// CreateReply creates a reply under a comment (2026-09-27: for everyone).
//
// The gates are EXACTLY AddComment's (CreateCommentPG): the post must be
// visible to the viewer (private accounts, blocks both ways, followers-only
// — loadPostForEngagement, fail-closed), comments not disabled, the comments
// audience (canComment), and the rate limit. The former post-owner rule and
// one-reply cap are gone. Replying to a reply attaches to the reply's parent
// (store.CreateReply), so the frame's parent_id is the top-level comment.
func (s *Service) CreateReply(ctx context.Context, commentID, userID uuid.UUID, body string) (*postgres.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("INVALID_REQUEST: reply body cannot be blank")
	}
	target, err := s.visibleComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	post, err := s.loadPostForEngagement(ctx, target.PostID, userID)
	if err != nil {
		return nil, err
	}
	// Members-only post: members and the author only (members_read.go).
	if err := s.requireMemberOf(ctx, post, &userID); err != nil {
		return nil, err
	}
	if post.NoComments {
		return nil, ErrCommentsDisabled
	}
	if err := s.commentAccessAllows(ctx, post, userID); err != nil {
		return nil, err
	}
	if !s.canComment(ctx, userID, post.AuthorID) {
		return nil, ErrCommentsRestricted
	}
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:reply:%s", userID), engagement.ReplyLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	reply, parentAuthorID, err := s.pgStore.CreateReply(ctx, commentID, userID, body)
	if err != nil {
		return nil, err
	}
	// A reply is a comment: it counts, and the thread's viewers hear about it.
	if err := s.pgStore.AdjustCommentCount(ctx, reply.PostID, 1); err != nil {
		slog.Warn("failed to increment comment_count for reply", "post_id", reply.PostID, "error", err)
	}
	s.publishCommentChange(reply.PostID, reply.ID, reply.ParentID, CommentChangeReplied, userID)

	// Publish engagement event
	if s.engProducer != nil {
		seqKey := fmt.Sprintf("eng:seq:%s", userID)
		seq, _ := s.rdb.Incr(ctx, seqKey).Result()
		s.rdb.Expire(ctx, seqKey, 24*time.Hour)

		event := engagement.BuildEvent(engagement.EventReplyCreated, reply.PostID, userID, uuid.Nil, reply.ID, "comment", "reply", true, seq, time.Now().UnixMicro())
		event.CommentBody = body
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish reply event: %v", err)
			}
		}()
	}

	// Publish legacy event so notification-service sends a notification to
	// the author of the comment replied to (not to yourself).
	if s.producer != nil && parentAuthorID != userID {
		go func() {
			if err := s.producer.PublishCommentCreated(context.Background(), reply.ID, reply.PostID, parentAuthorID, userID, body); err != nil {
				log.Printf("Warning: failed to publish legacy reply notification event: %v", err)
			}
		}()
	}

	// @mentions in the reply (T-12), same rules as a top-level comment.
	s.announceCommentMentions(post, userID, reply.ID, body)

	return reply, nil
}

// SoftDeleteComment marks a comment as deleted and decrements counter.
func (s *Service) SoftDeleteComment(ctx context.Context, commentID, userID uuid.UUID) error {
	postID, counted, err := s.pgStore.SoftDeleteComment(ctx, commentID, userID)
	if err != nil {
		return err
	}
	// Only a comment that was in the visible set leaves the count; a hidden
	// or held one was never in it. A retried delete is COMMENT_NOT_FOUND
	// above and never reaches here.
	if counted {
		if err := s.pgStore.AdjustCommentCount(ctx, postID, -1); err != nil {
			slog.Warn("failed to decrement comment_count", "post_id", postID, "error", err)
		}
	}
	s.publishCommentChange(postID, commentID, nil, CommentChangeDeleted, userID)

	if s.engProducer != nil {
		seqKey := fmt.Sprintf("eng:seq:%s", userID)
		seq, _ := s.rdb.Incr(ctx, seqKey).Result()
		s.rdb.Expire(ctx, seqKey, 24*time.Hour)

		event := engagement.BuildEvent(engagement.EventCommentDeleted, postID, userID, uuid.Nil, commentID, "post", "comment", false, seq, time.Now().UnixMicro())
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment delete event: %v", err)
			}
		}()
	}

	return nil
}

// EditComment edits a comment within 15 minutes of creation.
func (s *Service) EditComment(ctx context.Context, commentID, userID uuid.UUID, body string) error {
	postID, err := s.pgStore.EditComment(ctx, commentID, userID, body)
	if err != nil {
		return err
	}
	s.publishCommentChange(postID, commentID, nil, CommentChangeEdited, userID)
	return nil
}

// ListCommentsPG returns paginated threaded comments from PostgreSQL.
// viewerID drives moderation visibility: held-for-review comments are
// only shown to their own author. Pass nil for anonymous viewers.
func (s *Service) ListCommentsPG(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID, cursor string, limit int) ([]postgres.Comment, string, error) {
	return s.pgStore.ListComments(ctx, postID, viewerID, cursor, limit)
}

// GetCommentsAroundPG returns comments surrounding a target comment
// for deep-link navigation. viewerID drives moderation visibility:
// held-for-review comments are only shown to their own author.
// The post's read decision comes first, as in ListCommentsSortedPG.
func (s *Service) GetCommentsAroundPG(ctx context.Context, postID, commentID uuid.UUID, viewerID *uuid.UUID, limit int) ([]postgres.Comment, error) {
	if err := s.singlePostRead(ctx, postID, viewerID); err != nil {
		return nil, err
	}
	if err := s.postMembersGate(ctx, postID, viewerID); err != nil {
		return nil, err
	}
	return s.pgStore.GetCommentsAround(ctx, postID, commentID, viewerID, limit)
}

// ============================================================
// Stories
// ============================================================

// CreateStoryInput holds fields for creating a story.
type CreateStoryInput struct {
	AuthorID uuid.UUID
	// MediaID is the canonical asset. M4-P0-4 removed the MediaURL field
	// entirely rather than deprecating it: a field that still exists is a
	// field a caller can still populate.
	MediaID        uuid.UUID
	MediaType      string
	Caption        string
	Visibility     string
	IsHighlight    bool
	HighlightGroup *string
	IdempotencyKey string
}

// CreateStory creates a new ephemeral story with 24h expiry.

// GetStory returns a single story by ID.
func (s *Service) GetStory(ctx context.Context, storyID uuid.UUID) (*postgres.Story, error) {
	return s.pgStore.GetStory(ctx, storyID)
}

// GetStoriesFeed returns stories from followed users. Caller provides followed user IDs.
func (s *Service) GetStoriesFeed(ctx context.Context, followedUserIDs []uuid.UUID) ([]postgres.Story, error) {
	return s.pgStore.GetStoriesFeed(ctx, followedUserIDs)
}

// GetStoriesFeedForUser resolves the user's following graph and returns active stories.
func (s *Service) GetStoriesFeedForUser(ctx context.Context, userID uuid.UUID) ([]postgres.Story, error) {
	following, err := s.fetchFollowing(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.pgStore.GetStoriesFeed(ctx, following)
}

// GetStoriesByAuthor returns active stories for a specific author.
func (s *Service) GetStoriesByAuthor(ctx context.Context, authorID uuid.UUID) ([]postgres.Story, error) {
	return s.pgStore.GetStoriesByAuthor(ctx, authorID)
}

// DeleteStory removes a story.
func (s *Service) DeleteStory(ctx context.Context, storyID, authorID uuid.UUID) error {
	return s.pgStore.DeleteStory(ctx, storyID, authorID)
}

// ViewStory increments the view count of a story via the sharded
// Redis counter; PG snapshot is materialised every ~10s by the flush
// worker in cmd/server/main.go. Hot at viral scale (1M+ views/24h on
// one row) — without sharding every viewer write contended on the
// same UPDATE.
func (s *Service) ViewStory(ctx context.Context, storyID uuid.UUID) error {
	return s.adjustStoryViewCount(ctx, storyID)
}

// CleanupExpiredStories removes stories past their expiry. Called by cron.
func (s *Service) CleanupExpiredStories(ctx context.Context) (int64, error) {
	return s.pgStore.CleanupExpiredStories(ctx)
}

// checkEngagementVisibility enforces the post's visibility scope on
// engagement mutations (react / unreact / bookmark). Returns nil
// when the viewer is allowed to engage:
//
//   - the viewer is the author (always allowed)
//   - visibility == "public" (everyone)
//   - visibility == "followers" AND the viewer follows the author
//     (the retired "circle" is author-only)
//
// All other cases return ErrPostNotVisible. Graph errors fail closed:
// without a working relationship check we can't distinguish a
// follower from a stranger, so the engagement is rejected — the
// alternative (fail-open) was the exploit path called out by audit
// C5 ("engagement on private posts leaks counts via React").
func (s *Service) checkEngagementVisibility(ctx context.Context, postID, viewerID uuid.UUID) error {
	_, err := s.loadPostForEngagement(ctx, postID, viewerID)
	return err
}

// loadPostForEngagement is the shared "fetch + visibility-gate" helper
// behind every engagement endpoint. Audit H2: handlers used to
// double-fetch the post (handler did a GetPost to read NoComments /
// NoLikes flags, then the service called GetPost again inside
// checkEngagementVisibility). Now they share one fetch through this
// helper; callers can read the returned Post's NoComments / NoLikes
// without an extra DB round trip.
//
// Hot path; uses pgStore.GetPost which already has a Redis-cached
// path for repeat reads of the same post.
func (s *Service) loadPostForEngagement(ctx context.Context, postID, viewerID uuid.UUID) (*postgres.Post, error) {
	if s.engagementPostSeam != nil {
		return s.engagementPostSeam(ctx, postID, viewerID)
	}
	post, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, ErrPostNotFound
	}
	// One visibility rule (2026-09-29): this used to restate
	// viewerMayViewPost without 'unlisted' and without the private share
	// list, so a viewer the detail let in (an unlisted link, a user a
	// private post is shared with) was refused every engagement — while
	// the routes gated by the detail's gate let them through. Same fail-
	// closed answers: an unknown value or a graph outage denies.
	if !s.viewerMayViewPost(ctx, post, &viewerID) {
		return nil, ErrPostNotVisible
	}
	return post, nil
}

// viewerMayViewPost applies the visibility policy to an ALREADY-LOADED post
// — the allocation-free twin of loadPostForEngagement's gate, for callers
// that hold the row. Same rules: authors always see their own, unknown
// values fail closed, a graph outage denies rather than leaks.
func (s *Service) viewerMayViewPost(ctx context.Context, post *postgres.Post, viewerID *uuid.UUID) bool {
	if viewerID != nil && *viewerID == post.AuthorID {
		return true
	}
	// Account-level gate first: a private account's posts are follower-only
	// whatever the per-post visibility says, and an anonymous viewer is a
	// stranger to every private account (privacy_gate.go, fail-closed).
	if !s.canViewAuthor(ctx, viewerID, post.AuthorID) {
		return false
	}
	switch strings.ToLower(post.Visibility) {
	case "", "public", "unlisted":
		return true
	case "private":
		// Private sharing (2026-09-28): a user on the post's share list.
		// This is a single-post gate only; no listing calls it.
		return s.sharedWithViewer(ctx, post.ID, viewerID)
	case "followers":
		if viewerID == nil {
			return false
		}
		follows, err := s.checkViewerFollowsAuthor(ctx, *viewerID, post.AuthorID)
		if err != nil {
			log.Printf("Warning: visibility check graph lookup failed; rejecting: %v", err)
			return false
		}
		return follows
	case PostVisibilityFriends:
		// Friends (2026-10-18): mutual follows only. Same fail-closed
		// answer as followers on a graph outage or an anonymous viewer.
		if viewerID == nil {
			return false
		}
		mutual, err := s.checkViewerMutualWithAuthor(ctx, *viewerID, post.AuthorID)
		if err != nil {
			log.Printf("Warning: friends visibility graph lookup failed; rejecting: %v", err)
			return false
		}
		return mutual
	default:
		// "circle" (and "trusted" / "close_friends") is author-only here as
		// on the media gate (evaluatePostMediaVisibility) since 2026-09-29:
		// the close-friends audience was retired on 21 Sep (graph-service
		// migration 012), no writer can set it (PostVisibilities), and the
		// old "treat it as followers" widened a close-friends post to every
		// follower. "staged", "private" without a share and unknown values
		// fail closed as before.
		return false
	}
}

// checkViewerFollowsAuthor does a single graph-service relationship
// lookup. Returns (follows=true) when viewer→author edge exists.
// Empty graphServiceURL is treated as "no policy" — same as the
// existing fetchFollowing helper — so unit tests + dev rigs without
// graph-service skip the gate cleanly.
func (s *Service) checkViewerFollowsAuthor(ctx context.Context, viewerID, authorID uuid.UUID) (bool, error) {
	follows, _, err := s.fetchFollowEdges(ctx, viewerID, authorID)
	return follows, err
}

// checkViewerMutualWithAuthor answers the `friends` audience: the viewer
// follows the author AND the author follows the viewer, from the same one
// graph lookup checkViewerFollowsAuthor makes. Empty graphServiceURL is
// "no policy" exactly as there (unit tests and dev rigs).
func (s *Service) checkViewerMutualWithAuthor(ctx context.Context, viewerID, authorID uuid.UUID) (bool, error) {
	follows, followedBy, err := s.fetchFollowEdges(ctx, viewerID, authorID)
	if err != nil {
		return false, err
	}
	return follows && followedBy, nil
}

// fetchFollowEdges is the one GET /v1/graph/relationship call behind both
// audience checks: follows = viewer→author, followedBy = author→viewer.
func (s *Service) fetchFollowEdges(ctx context.Context, viewerID, authorID uuid.UUID) (follows, followedBy bool, err error) {
	if s.graphServiceURL == "" {
		return true, true, nil
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf(
		"%s/v1/graph/relationship?user_id=%s&other_id=%s",
		s.graphServiceURL, viewerID, authorID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false, fmt.Errorf("build relationship request: %w", err)
	}
	// graph-service gates /v1/graph/* behind the internal service key.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, false, fmt.Errorf("relationship request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false, fmt.Errorf("graph-service status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Follows    bool `json:"follows"`
			FollowedBy bool `json:"followed_by"`
		} `json:"data"`
		Follows    bool `json:"follows"`     // legacy un-wrapped shape tolerated
		FollowedBy bool `json:"followed_by"` // legacy un-wrapped shape tolerated
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, false, fmt.Errorf("decode relationship: %w", err)
	}
	return envelope.Data.Follows || envelope.Follows, envelope.Data.FollowedBy || envelope.FollowedBy, nil
}

func (s *Service) fetchFollowing(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if s.graphServiceURL == "" {
		return nil, nil
	}

	var allFollowing []uuid.UUID
	offset := 0
	limit := 100

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	for {
		url := fmt.Sprintf(
			"%s/v1/graph/following/%s?limit=%d&offset=%d",
			s.graphServiceURL,
			userID.String(),
			limit,
			offset,
		)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create following request: %w", err)
		}
		// graph-service gates /v1/graph/* behind the internal service key.
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}

		var envelope struct {
			Data []uuid.UUID `json:"data"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&envelope)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode following response: %w", decodeErr)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d", resp.StatusCode)
		}

		allFollowing = append(allFollowing, envelope.Data...)
		if len(envelope.Data) < limit {
			break
		}
		offset += limit
	}

	return allFollowing, nil
}

// ============================================================
// Multi-Reactions
// ============================================================

// ReactionToggleResult is the response for the multi-reaction toggle API.
type ReactionToggleResult struct {
	ReactionType string                   `json:"reaction_type"`
	IsSet        bool                     `json:"is_set"`
	Counts       *postgres.ReactionCounts `json:"counts"`
}

// ToggleReaction sets, changes, or removes a reaction on a post.
func (s *Service) ToggleReaction(ctx context.Context, postID, userID uuid.UUID, reactionType string) (*ReactionToggleResult, error) {
	// Heart only (heart_only.go). The store toggles any existing reaction
	// off for a post, so a viewer whose old emoji reads as the heart
	// un-hearts on the next tap rather than being switched to "like".
	if err := checkPostReaction(reactionType); err != nil {
		return nil, err
	}

	// Audit C5 + H2 combined: one fetch checks visibility *and*
	// reads the per-post NoLikes flag — handler used to do its own
	// GetPost just for that, then the service called GetPost again
	// inside the visibility gate.
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return nil, err
	}
	if post.NoLikes {
		return nil, ErrLikesDisabled
	}

	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:react:%s", userID), engagement.LikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// The reactions row this writes is also the liked list's record
	// (liked_reels.go); no separate index write.
	// Audit H4: the reaction event rides the reaction's own transaction
	// (store/postgres/reactions.go). Self-reactions announce nothing, as
	// before. `post` is already in scope from the visibility check above.
	toggle, err := s.pgStore.ToggleReaction(ctx, "post", postID, userID, reactionType, func(t postgres.ReactionToggle) (string, interface{}, bool) {
		if s.producer == nil || post.AuthorID == userID {
			return "", nil, false
		}
		return reactionEvent(t, postID, post.AuthorID, userID)
	})
	if err != nil {
		return nil, err
	}
	newType, isSet := toggle.NewType, toggle.IsSet

	// Also sync to ScyllaDB for feed hydration compatibility
	if isSet {
		if _, err := s.scyllaStore.React(ctx, postID, userID, newType); err != nil {
			log.Printf("Warning: failed to sync reaction to ScyllaDB: %v", err)
		}
	} else {
		if _, err := s.scyllaStore.Unreact(ctx, postID, userID); err != nil {
			log.Printf("Warning: failed to remove reaction from ScyllaDB: %v", err)
		}
	}

	// Get updated counts
	counts, err := s.pgStore.GetReactionCounts(ctx, "post", postID)
	if err != nil {
		log.Printf("Warning: failed to get reaction counts: %v", err)
	}

	return &ReactionToggleResult{
		ReactionType: newType,
		IsSet:        isSet,
		Counts:       foldPostReactionCounts(counts),
	}, nil
}

// reactionEvent is the event a reaction toggle announces: PostReacted for a
// NEW reaction, PostUnreacted for a removed one, and nothing for a change of
// type — the post gained no reaction, so neither the author's notification
// nor feed-service's velocity counter should move. A type switch used to be
// announced as a fresh PostReacted, so flipping like → love → like inflated
// a post's velocity and re-notified its author.
func reactionEvent(t postgres.ReactionToggle, postID, authorID, reactorID uuid.UUID) (string, interface{}, bool) {
	payload := events.PostReactedPayload{
		PostID:       postID.String(),
		PostAuthorID: authorID.String(),
		ReactorID:    reactorID.String(),
		CreatedAt:    time.Now(),
	}
	switch {
	case t.IsSet && t.PrevType == "":
		payload.ReactType = t.NewType
		return events.PostReacted, payload, true
	case !t.IsSet && t.PrevType != "":
		payload.ReactType = t.PrevType
		return events.PostUnreacted, payload, true
	}
	return "", nil, false
}

// ── Video Creator Tools ────────────────────────────────────────

// GetVideoDetail returns the video metadata for a post. INTERNAL USE ONLY —
// it returns storage_video_url, the object-storage path of the source file,
// which no viewer may see. Handlers must call GetVideoDetailForCaller.
func (s *Service) GetVideoDetail(ctx context.Context, postID uuid.UUID) (*postgres.VideoMetadata, error) {
	return s.pgStore.GetVideoMetadata(ctx, postID)
}

// GetVideoDetailForCaller is the read behind GET /v1/videos/:videoId. It
// returns the same metadata as GetVideoDetail with the owner-only fields
// redacted for anyone who is not the post's author.
//
// storage_video_url is the internal object-storage location of the source
// upload — not the CDN playback URL a player needs. The endpoint is
// unauthenticated at the edge (the gateway forwards tokenless requests and
// leaves the 401 to us), so before this it handed that path to anyone who
// could guess or scrape a post id. Redacting rather than 401-ing keeps the
// duration / dimensions / playback_url that a viewer legitimately reads
// available, and closes the leak for every caller who is not the owner.
//
// Fail closed: a nil caller, a failed author lookup, or an unwired store all
// redact. The only branch that keeps the field is a positive match between
// the caller and the post's author.
func (s *Service) GetVideoDetailForCaller(ctx context.Context, postID uuid.UUID, callerID *uuid.UUID) (*postgres.VideoMetadata, error) {
	if s.authoringOwners == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	// The post's own read decision first (2026-09-28): this route answered
	// playback_url for any post id, private and age-restricted ones
	// included. Same gate as GET /v1/posts/:id, private shares honoured.
	if err := s.singlePostRead(ctx, postID, callerID); err != nil {
		return nil, err
	}
	vm, err := s.authoringOwners.GetVideoMetadata(ctx, postID)
	if err != nil {
		return nil, err
	}
	if vm == nil {
		return nil, ErrPostNotFound
	}
	if callerID == nil || s.requirePostAuthor(ctx, *callerID, postID) != nil {
		redacted := *vm
		redacted.StorageVideoURL = nil
		// Members-only video (2026-10-23): a non-member gets the metadata —
		// duration, dimensions, category — but no URL to play it from. The
		// one membership rule (members_read.go): members and the author keep
		// playback_url; a signed-out caller, a non-member and an unresolved
		// gate or lookup lose it.
		if s.postMembersGate(ctx, postID, callerID) != nil {
			redacted.PlaybackURL = nil
		}
		return &redacted, nil
	}
	return vm, nil
}

// UpdateVideoTrim updates trim points for a video.
func (s *Service) UpdateVideoTrim(ctx context.Context, postID, userID uuid.UUID, startMs int, endMs *int) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
	if err != nil {
		return fmt.Errorf("video metadata not found")
	}

	// Validate: 0 <= start < end <= duration*1000
	maxMs := int(vm.DurationSeconds * 1000)
	if startMs < 0 {
		return fmt.Errorf("trim_start_ms must be >= 0")
	}
	effectiveEnd := maxMs
	if endMs != nil {
		effectiveEnd = *endMs
	}
	if startMs >= effectiveEnd {
		return fmt.Errorf("trim_start_ms must be less than trim_end_ms")
	}
	if effectiveEnd > maxMs {
		return fmt.Errorf("trim_end_ms exceeds video duration")
	}

	return s.pgStore.UpdateTrim(ctx, postID, startMs, endMs)
}

// OverrideCategory overrides the video category classification.
func (s *Service) OverrideCategory(ctx context.Context, postID, userID uuid.UUID, category string) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	if category != "flick" && category != "long_video" {
		return fmt.Errorf("invalid category: must be flick or long_video")
	}

	vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
	if err != nil {
		return fmt.Errorf("video metadata not found")
	}

	if err := ValidateCategoryOverride(vm, category); err != nil {
		return err
	}

	return s.pgStore.UpdateFinalCategory(ctx, postID, category)
}

// SetCoverFrame sets the cover frame for a video.
func (s *Service) SetCoverFrame(ctx context.Context, postID, userID uuid.UUID, coverMediaID *uuid.UUID, thumbnailURL *string) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	// Update cover_media_id on the post
	if coverMediaID != nil {
		// Authority before the write (cover_guard.go); nothing is written,
		// thumbnail included, when it fails.
		if err := s.verifyCoverOnWrite(ctx, authorID, *coverMediaID); err != nil {
			return err
		}
		if err := s.pgStore.UpdatePostCoverMedia(ctx, postID, coverMediaID); err != nil {
			return err
		}
		// Tier 1b: cover_media_id is in the cached body.
		s.InvalidatePostBodyCache(ctx, postID)
	}

	// Update thumbnail_url on video_metadata
	if thumbnailURL != nil {
		vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
		if err != nil {
			return fmt.Errorf("video metadata not found")
		}
		vm.ThumbnailURL = thumbnailURL
		return s.pgStore.UpdateVideoMetadata(ctx, vm)
	}

	return nil
}

// PublishVideo is POST /v1/videos/:videoId/publish: a SCHEDULED video goes
// live now, keeping the visibility its creator chose (PublishScheduled, the
// same flip "publish now" and the schedule worker make).
//
// It used to UPDATE visibility = 'public' on any post — a private or
// unlisted video, or one staged for review, went public on one call
// (2026-09-29 audit). A post that is already live is left exactly as it is
// and the call answers 200, as before. No client calls this route today: the
// upload studio publishes through the create path and PATCH
// /v1/posts/:id/schedule, so nothing relied on the old flip.
func (s *Service) PublishVideo(ctx context.Context, postID, userID uuid.UUID) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
	if err != nil {
		return fmt.Errorf("video metadata not found")
	}

	if vm.UploadStatus != "ready" {
		return fmt.Errorf("video not ready: current status is %s", vm.UploadStatus)
	}

	if _, err := s.PublishScheduled(ctx, postID, &userID, false); err != nil {
		return err
	}
	return nil
}

// GetReactionCounts returns the breakdown of reaction counts for a post.
// Every emoji folds into "like" (heart_only.go).
func (s *Service) GetReactionCounts(ctx context.Context, postID uuid.UUID) (*postgres.ReactionCounts, error) {
	counts, err := s.pgStore.GetReactionCounts(ctx, "post", postID)
	if err != nil {
		return nil, err
	}
	return foldPostReactionCounts(counts), nil
}

// ============================================================
// Saved Items / Collections
// ============================================================

// SaveItem, ListSavedItems and the collection routes are in
// saved_collections.go (migration 074).

// UnsaveItem removes a saved item.
func (s *Service) UnsaveItem(ctx context.Context, savedID, userID uuid.UUID) error {
	return s.pgStore.UnsaveItem(ctx, savedID, userID)
}

// ============================================================
// Hashtag Search
// ============================================================

// GetPostsByHashtag returns posts with a specific hashtag.
// sort accepts "top" or "recent" (default).
// contentTypes filters by content_type (e.g. ["post"], ["flick"], ["long_video"]); nil = all.
//
// The store lists public, viewer-eligible (effectively approved, live)
// posts; the author gate — private accounts the viewer does not follow,
// blocks, deactivated authors — is canViewPosts, the same fail-closed
// choke point GetRecentPosts and the sound page use. viewerID nil is the
// signed-out stranger. HashtagTotal counts exactly this set.
func (s *Service) GetPostsByHashtag(ctx context.Context, viewerID *uuid.UUID, hashtag string, limit int, cursor, sort string, contentTypes []string) ([]PostDetail, string, error) {
	mode := postgres.HashtagSortRecent
	if sort == "top" {
		mode = postgres.HashtagSortTop
	}
	posts, nextCursor, err := s.pgStore.GetPostsByHashtag(ctx, hashtag, limit, cursor, mode, contentTypes)
	if err != nil {
		return nil, "", err
	}

	posts = s.hashtagPostsVisibleTo(ctx, viewerID, posts)

	details := make([]PostDetail, 0, len(posts))
	for _, p := range posts {
		post := p
		counts, _ := s.countsForPost(ctx, p.ID)
		details = append(details, PostDetail{Post: &post, Counts: counts})
	}

	// Instant publish (processing.go): public discovery surface with no
	// viewer — a still-processing post is nobody's to see here.
	details, err = s.attachMediaStateToDetails(ctx, details, nil)
	if err != nil {
		return nil, "", err
	}
	// Members-only rows, for the real viewer: members and the author read
	// them in full here too (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	return details, nextCursor, nil
}

// HashtagTotal is in hashtag_total.go.

// GetTrendingPosts returns trending posts globally, optionally scoped to one
// or more content types. Used by the Posttube/Reels "Trending" tabs and the
// general discover surface. cursor is the same opaque base64 string used by
// the hashtag top sort.
// viewerID is used only to label the channel card (its subscribed state);
// it does NOT widen what the page returns — trending is public rows only,
// selected in SQL.
func (s *Service) GetTrendingPosts(ctx context.Context, contentTypes []string, limit int, cursor string, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetTrendingPosts(ctx, contentTypes, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.countsForPost(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts}
	}
	// Instant publish (processing.go): public discovery surface with no
	// viewer — a still-processing post is nobody's to see here.
	details, err = s.attachMediaStateToDetails(ctx, details, nil)
	if err != nil {
		return nil, "", err
	}
	// Members-only rows, for the real viewer: members and the author read
	// them in full here too (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	// A trending long video arrived with no channel, so the watch card had
	// no name or avatar to draw a poster with. Same best-effort attach
	// GetBookmarks and the watch-progress list do — one batched lookup for
	// the page's distinct authors, and the page reads the same without it.
	ptrs := make([]*PostDetail, len(details))
	for i := range details {
		ptrs[i] = &details[i]
	}
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, ptrs)
	return details, nextCursor, nil
}

// SearchHashtags returns hashtag suggestions matching a prefix query.
// Reads directly from posts.hashtags via the store; no Redis index is wired.
func (s *Service) SearchHashtags(ctx context.Context, query string, limit int) ([]postgres.HashtagSuggestion, error) {
	return s.pgStore.SearchHashtags(ctx, query, limit)
}

// GetTrendingHashtags24h returns the most-used hashtags in the last 24 hours.
// SQL fallback used until the Redis trending writer ships.
func (s *Service) GetTrendingHashtags24h(ctx context.Context, limit int) ([]postgres.HashtagTrending24h, error) {
	return s.pgStore.GetTrendingHashtags24h(ctx, limit)
}

// lookupUserByUsername resolves a username to a user ID via the app
// user-service (`GET /v1/users/by-username/{username}`), the same service the
// api-gateway routes `/v1/users` to. app.users.username is the authority for
// handles; identity-profile is a different store and 404s for app handles.
//
// The internal key is sent so the call keeps working if the route is ever
// gated. The reply is the standard envelope `{"data":{"id":"<uuid>",...}}`.
// Returns ("", nil) when the URL is unset or the user does not exist, so
// callers skip the mention without treating it as an error.
func (s *Service) lookupUserByUsername(ctx context.Context, username string) (string, error) {
	if s.userServiceURL == "" {
		return "", nil
	}
	reqURL := fmt.Sprintf("%s/v1/users/by-username/%s",
		strings.TrimRight(s.userServiceURL, "/"), url.PathEscape(username))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", nil
	default:
		return "", fmt.Errorf("user-service by-username returned %d", resp.StatusCode)
	}
	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode user-service by-username: %w", err)
	}
	return result.Data.ID, nil
}

// UpdateDistribution replaces a post's distribution policy (owner-only).
// The row update, the monotonic rev bump, and the PostDistributionUpdated
// outbox event commit in one transaction. Returns the updated post.
func (s *Service) UpdateDistribution(ctx context.Context, postID, actorID uuid.UUID, raw json.RawMessage) (*postgres.Post, error) {
	policy, err := ParseDistributionPolicy(raw)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		// Explicitly clearing a policy back to legacy behavior is not a
		// supported operation — reject rather than silently accept.
		return nil, fmt.Errorf("%w: policy document required", ErrInvalidDistribution)
	}
	stored, err := MarshalPolicy(policy)
	if err != nil {
		return nil, err
	}

	// Fetch first for the event's content_type + existence/ownership
	// pre-check (the UPDATE re-checks ownership atomically).
	existing, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrPostNotFound
	}
	if existing.AuthorID != actorID {
		return nil, ErrNotPostAuthor
	}

	resolved := ResolveDistribution(policy)
	_, err = s.pgStore.UpdateDistribution(ctx, postID, actorID, stored,
		func(rev int64) (string, interface{}) {
			return events.PostDistributionUpdated, events.PostDistributionUpdatedPayload{
				PostID:            postID.String(),
				AuthorID:          actorID.String(),
				ContentType:       existing.ContentType,
				MainFeed:          resolved.MainFeed,
				NotifySubscribers: resolved.NotifySubscribers,
				DistributionRev:   rev,
				UpdatedAt:         time.Now().UTC(),
			}
		})
	if err != nil {
		return nil, err
	}
	return s.pgStore.GetPost(ctx, postID)
}

// ---------------------------------------------------------------------------
// Repost (Echo) Service Methods
// ---------------------------------------------------------------------------

// RepostResult is the response shape for repost create APIs.
type RepostResult struct {
	ID             uuid.UUID `json:"id"`
	OriginalPostID uuid.UUID `json:"original_post_id"`
	Type           string    `json:"type"`
	QuoteText      string    `json:"quote_text,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      string    `json:"created_at"`
}

// CreateRepostInput holds all parameters for creating a repost.
type CreateRepostInput struct {
	UserID            uuid.UUID
	PostID            uuid.UUID
	Type              string // "plain" or "quote"
	QuoteText         string
	SourceContextType string
	SourceContextID   *uuid.UUID
}

// CreateRepost creates a plain or quote repost per the spec.
func (s *Service) CreateRepost(ctx context.Context, input CreateRepostInput) (*RepostResult, error) {
	// A repost publishes on the reposter's behalf: author standing first
	// (publish_standing.go). Cached per user, so it is cheap before the
	// rate limit.
	if err := s.requirePublishStanding(ctx, input.UserID); err != nil {
		return nil, err
	}
	// Rate limit
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:repost:%s", input.UserID), 30, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// 1. Verify original post exists and is not deleted (GetPost filters deleted_at IS NULL)
	post, err := s.loadRepostTarget(ctx, input.PostID)
	if err != nil || post == nil {
		return nil, fmt.Errorf("POST_NOT_FOUND")
	}

	// 1b. A reel is never reposted, plain or quote (heart_only.go). An
	// existing reel repost stays; switching its type is a new repost and is
	// refused here too, while undo (UndoRepost) still works.
	if err := checkRepostContentType(post.ContentType); err != nil {
		return nil, err
	}

	// 2. Visibility check — private or followers-only posts cannot be reposted
	if !repostableVisibility(post.Visibility) {
		return nil, fmt.Errorf("NOT_ELIGIBLE")
	}

	// 3. Quote repost validation
	if input.Type == "quote" {
		text := strings.TrimSpace(input.QuoteText)
		if text == "" {
			return nil, fmt.Errorf("QUOTE_TEXT_REQUIRED")
		}
		if len([]rune(text)) > 500 {
			return nil, fmt.Errorf("QUOTE_TEXT_TOO_LONG")
		}
		input.QuoteText = text
	}

	// 4. Check if user already has an active repost
	existing, err := s.pgStore.GetActiveRepost(ctx, input.UserID, input.PostID)
	if err != nil {
		return nil, err
	}

	var repost *postgres.Repost

	if existing != nil {
		// Same type → 409 conflict
		if existing.RepostType == input.Type {
			return nil, fmt.Errorf("ALREADY_REPOSTED")
		}
		// Different type → switch (soft-delete old, create new, net-zero counter)
		repost, err = s.pgStore.SwitchRepostType(
			ctx, input.UserID, input.PostID,
			input.Type, input.QuoteText, post.Visibility,
			input.SourceContextType, input.SourceContextID,
		)
		if err != nil {
			return nil, err
		}
		// Net-zero counter change (decrement old + increment new), but we still
		// publish the event for feed fanout with the new repost.
	} else {
		// Fresh repost
		repost = &postgres.Repost{
			UserID:            input.UserID,
			OriginalPostID:    input.PostID,
			RepostType:        input.Type,
			QuoteText:         input.QuoteText,
			Visibility:        post.Visibility,
			SourceContextType: input.SourceContextType,
			SourceContextID:   input.SourceContextID,
		}
		if err := s.pgStore.CreateRepost(ctx, repost); err != nil {
			if err.Error() == "ALREADY_REPOSTED" {
				return nil, fmt.Errorf("ALREADY_REPOSTED")
			}
			return nil, err
		}
		// Increment the sharded post_engagement_counts.repost_count
		// (replaces the legacy per-event PG UPDATE that was the hot
		// row on viral reposts).
		if err := s.adjustEngagementCount(ctx, s.repostCounter, input.PostID, "repost_count", 1); err != nil {
			slog.Warn("failed to increment repost count", "error", err, "post_id", input.PostID)
		}
		repostCountKey := fmt.Sprintf("post:%s:repost_count", input.PostID)
		s.rdb.Incr(ctx, repostCountKey)
		s.rdb.Expire(ctx, repostCountKey, 7*24*time.Hour)
	}

	// Publish event
	if s.producer != nil {
		sourceCtxID := ""
		if repost.SourceContextID != nil {
			sourceCtxID = repost.SourceContextID.String()
		}
		go func() {
			if err := s.producer.PublishPostReposted(
				context.Background(),
				repost.ID, repost.UserID, repost.OriginalPostID, post.AuthorID,
				repost.RepostType, repost.QuoteText, repost.Visibility,
				repost.SourceContextType, sourceCtxID,
			); err != nil {
				slog.Warn("failed to publish post.reposted event", "error", err)
			}
		}()
	}
	s.invalidateFeedHydration(ctx, input.UserID, input.PostID)

	return &RepostResult{
		ID:             repost.ID,
		OriginalPostID: repost.OriginalPostID,
		Type:           repost.RepostType,
		QuoteText:      repost.QuoteText,
		Status:         repost.Status,
		CreatedAt:      repost.CreatedAt.Format(time.RFC3339),
	}, nil
}

// UndoRepost soft-deletes the active repost for (user, post) and decrements counters.
func (s *Service) UndoRepost(ctx context.Context, userID, postID uuid.UUID) error {
	// Look up the active repost so we can get its ID/type for the event
	existing, err := s.pgStore.GetActiveRepost(ctx, userID, postID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("REPOST_NOT_FOUND")
	}

	// Fetch original post author for event
	post, _ := s.pgStore.GetPost(ctx, postID)
	var originalAuthorID uuid.UUID
	if post != nil {
		originalAuthorID = post.AuthorID
	}

	// Soft-delete
	if err := s.pgStore.SoftDeleteRepost(ctx, userID, postID); err != nil {
		return err
	}

	// Decrement the sharded post_engagement_counts.repost_count.
	if err := s.adjustEngagementCount(ctx, s.repostCounter, postID, "repost_count", -1); err != nil {
		slog.Warn("failed to decrement repost count", "error", err, "post_id", postID)
	}
	repostCountKey := fmt.Sprintf("post:%s:repost_count", postID)
	s.rdb.Decr(ctx, repostCountKey)

	// Publish undo event
	if s.producer != nil {
		go func() {
			if err := s.producer.PublishPostRepostUndone(
				context.Background(),
				existing.ID, userID, postID, originalAuthorID, existing.RepostType,
			); err != nil {
				slog.Warn("failed to publish post.repost_undone event", "error", err)
			}
		}()
	}
	s.invalidateFeedHydration(ctx, userID, postID)

	return nil
}

// RepostStateResult is the response shape for GET /posts/{postId}/repost/me.
type RepostStateResult struct {
	HasReposted bool       `json:"has_reposted"`
	RepostID    *uuid.UUID `json:"repost_id,omitempty"`
	Type        string     `json:"type,omitempty"`
	QuoteText   string     `json:"quote_text,omitempty"`
	CreatedAt   string     `json:"created_at,omitempty"`
}

// GetRepostState returns the current user's repost state for a given post.
func (s *Service) GetRepostState(ctx context.Context, userID, postID uuid.UUID) (*RepostStateResult, error) {
	repost, err := s.pgStore.GetActiveRepost(ctx, userID, postID)
	if err != nil {
		return nil, err
	}
	if repost == nil {
		return &RepostStateResult{HasReposted: false}, nil
	}
	return &RepostStateResult{
		HasReposted: true,
		RepostID:    &repost.ID,
		Type:        repost.RepostType,
		QuoteText:   repost.QuoteText,
		CreatedAt:   repost.CreatedAt.Format(time.RFC3339),
	}, nil
}

// ReposterItem is a single entry in the "who reposted this" list.
type ReposterItem struct {
	UserID     uuid.UUID `json:"user_id"`
	RepostedAt string    `json:"reposted_at"`
}

// ListRepostersResult is the response shape for GET /posts/{postId}/reposters.
type ListRepostersResult struct {
	Reposters  []ReposterItem `json:"reposters"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// ListReposters returns a paginated list of users who reposted a post.
func (s *Service) ListReposters(ctx context.Context, postID uuid.UUID, limit int, cursor string) (*ListRepostersResult, error) {
	reposts, nextCursor, err := s.pgStore.ListReposters(ctx, postID, limit, cursor)
	if err != nil {
		return nil, err
	}
	items := make([]ReposterItem, 0, len(reposts))
	for _, r := range reposts {
		items = append(items, ReposterItem{
			UserID:     r.UserID,
			RepostedAt: r.CreatedAt.Format(time.RFC3339),
		})
	}
	return &ListRepostersResult{Reposters: items, NextCursor: nextCursor}, nil
}

// UserRepostItem is a single repost in the user's profile reposts feed.
type UserRepostItem struct {
	RepostID       uuid.UUID `json:"repost_id"`
	Type           string    `json:"type"`
	QuoteText      string    `json:"quote_text,omitempty"`
	OriginalPostID uuid.UUID `json:"original_post_id"`
	CreatedAt      string    `json:"created_at"`
	// Post is the original post as THIS viewer may read it (the batch read:
	// the original author's privacy, blocks, review state, age gate, and the
	// members-only redaction). A repost whose original the viewer may not
	// read is left off the page, so this is always set on a listed item.
	Post *PostDetail `json:"post,omitempty"`
}

// ListUserRepostsResult is the response shape for GET
// /v1/creators/:creatorId/reposts (and the older /v1/users/:userId/reposts).
type ListUserRepostsResult struct {
	Items      []UserRepostItem `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// BatchGetRepostStates returns repost states for multiple posts for a single user.
// Used for hydrating viewer_context in feed responses.
func (s *Service) BatchGetRepostStates(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]*RepostStateResult, error) {
	reposts, err := s.pgStore.BatchGetActiveReposts(ctx, userID, postIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[uuid.UUID]*RepostStateResult, len(postIDs))
	for _, pid := range postIDs {
		r, ok := reposts[pid]
		if !ok {
			result[pid] = &RepostStateResult{HasReposted: false}
			continue
		}
		result[pid] = &RepostStateResult{
			HasReposted: true,
			RepostID:    &r.ID,
			Type:        r.RepostType,
			QuoteText:   r.QuoteText,
			CreatedAt:   r.CreatedAt.Format(time.RFC3339),
		}
	}
	return result, nil
}
