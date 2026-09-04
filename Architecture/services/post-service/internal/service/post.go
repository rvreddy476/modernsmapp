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
	"os"
	"regexp"
	"strings"
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
	mentionRegex = regexp.MustCompile(`@(\w{1,30})`)
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
	pgStore                *postgres.Store
	scyllaStore            *scylla.InteractionStore
	scyllaSession          *gocql.Session
	rdb                    *redis.Client
	producer               *postEvents.Producer // legacy producer, optional
	engProducer            *engagement.Producer // new engagement event producer
	rateLimiter            *engagement.RateLimiter
	spam                   *spam.Detector
	userServiceURL         string
	profileServiceURL      string
	mediaServiceURL        string
	graphServiceURL        string
	monetizationServiceURL string
	reviewerServiceURL     string
	trustSafetyURL         string
	// requireStandingCheck makes an unreachable trust-safety service block
	// scheduled publication instead of letting it through (Codex P2-1).
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

	// channels is the Tube channel store (channels.go). Nil when there is no
	// Postgres store; every channel flow then fails closed.
	channels channelStore
}

func New(pg *postgres.Store, scylla *scylla.InteractionStore, rdb *redis.Client) *Service {
	svc := &Service{
		pgStore:     pg,
		scyllaStore: scylla,
		rdb:         rdb,
		rateLimiter: engagement.NewRateLimiter(rdb),
		spam:        spam.New(rdb),
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
	if pg != nil {
		svc.hiddenAuthors = pg
		svc.channels = pg
	}
	if rdb != nil {
		svc.likeCounter = counters.New(rdb, counters.Config{EntityKind: "post_like_count", Shards: 32})
		svc.commentCounter = counters.New(rdb, counters.Config{EntityKind: "post_comment_count", Shards: 32})
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
			return s.pgStore.IncrementAudioUseCount(ctx, audioTrackID)
		}
		return nil
	}
	return s.pgStore.IncrementAudioUseCount(ctx, audioTrackID)
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
func (s *Service) AutoResolveFlagged(ctx context.Context, postID uuid.UUID, status string) (bool, error) {
	ok, err := s.pgStore.SetReviewStatusFromFlagged(ctx, postID, status)
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
func (s *Service) PromoteStaged(ctx context.Context, postID uuid.UUID, visibility string) (bool, error) {
	ok, err := s.pgStore.SetVisibilityFromStaged(ctx, postID, visibility)
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
		username := match[1]
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
// Flick = up to 5 minutes, Long Video = more than 5 minutes (founder: shorts
// max 3–5 min; 5 chosen, 2026-09-05). Must match postclassify.FlickMaxDurationSeconds.
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
// Only the generic "post" intent — a plain post that happens to attach a
// video, no kind chosen — is classified from the measurement, and defaults
// to long_video while the measurement is pending; the MediaTranscodeConsumer
// then reclassifies it once the numbers land. `explicit` reports whether the
// answer was the author's choice, so that consumer knows to leave it alone.
func resolveVideoContentType(intent string, durationSeconds int, width, height int) (contentType string, explicit bool) {
	switch intent {
	case "flick", "reel":
		return "flick", true
	case "long_video", "video":
		return "long_video", true
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

	// DURABLE IDEMPOTENCY — fast path (C-LB-3.3).
	//
	// An ordinary retry is answered here without redoing validation, media
	// authority, spam scoring and event construction. This read is only an
	// optimisation: the authority is the unique index inside
	// CreatePostWithEventIdempotent, because another request can claim the key
	// between this lookup and that insert.
	if input.CreateKey != "" {
		postID, fingerprint, found, lookupErr := s.pgStore.LookupCreateIdempotency(
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
		return nil, fmt.Errorf("invalid content_type %q: must be post, poll, flick, or long_video", contentType)
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

	// Flick category is a closed taxonomy (categories.go). Only flicks: the
	// long-video path still carries the free-text category the video
	// classifier and the category override route write, and changing that
	// contract is not this pass. Empty stays allowed — a category is a
	// choice, not a requirement, and neither is a title.
	category := input.Category
	if contentType == "flick" {
		normalized, catErr := NormalizeFlickCategory(category)
		if catErr != nil {
			return nil, catErr
		}
		category = normalized
	}

	taggedUsers, tagErr := NormalizeTaggedUsers(input.AuthorID, input.TaggedUserIDs)
	if tagErr != nil {
		return nil, tagErr
	}

	// Trusted Circle after-hours protection. When the author has
	// `tc_after_hours_posts` ON (default ON), posts created during the
	// after-hours window 22:00–06:00 local time get auto-restricted to
	// the user's trusted circle audience instead of the visibility the
	// client supplied. Designed to protect "late-night drafts, vent
	// posts, raw thoughts" from full-audience reach.
	//
	// Best-effort: a user-service blip falls through to the supplied
	// visibility. The user can always manually pick a wider audience
	// for a specific post — this only fires when they leave the
	// default visibility selection alone.
	//
	// Slice C / C-LB-2: "leave the default alone" is now actually detected.
	// The condition used to match on the VALUE, which a defaulting client and
	// a deliberate one produce identically, so an explicitly Public post made
	// at 23:00 was silently rewritten to `trusted` while the composer, the
	// response and the author all still said Public. Consent to a narrower
	// audience cannot be inferred from a normal publish.
	//
	// A future auto-audience feature needs its own request signal and its own
	// enforced audience contract; until then an explicit choice is honoured.
	//
	// The toggle fetch stays INSIDE the guard: an explicit audience must not
	// cost a cross-service call to arrive at the same answer.
	if audienceMayBeAutoRestricted(input.VisibilityExplicit, input.Visibility) &&
		s.shouldRestrictToTrustedCircle(ctx, input.AuthorID, time.Now()) {
		input.Visibility = "trusted"
		slog.Info("post: after-hours protection applied",
			"author_id", input.AuthorID, "visibility", input.Visibility)
	}

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
	hashtags = s.filterBlockedHashtags(ctx, hashtags)

	// Extract @mentions from text
	mentions := extractMentions(input.Text)

	// Default reel metadata values
	lang := input.Language
	if lang == "" {
		lang = "en"
	}
	license := input.License
	if license == "" {
		license = "standard"
	}
	remixSetting := input.RemixSetting
	if remixSetting == "" {
		remixSetting = "allow"
	}
	commentMod := input.CommentModeration
	if commentMod == "" {
		commentMod = "none"
	}
	commentAcc := input.CommentAccess
	if commentAcc == "" {
		commentAcc = "everyone"
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
		CoverMediaID:      input.CoverMediaID,
		OriginalAudioVol:  origVol,
		OverlayAudioVol:   overlayVol,
		HideShare:         input.HideShare,
		AllowDownload:     input.AllowDownload,
		TaggedUserIDs:     taggedUsers,
		CreatedAt:         time.Now(),
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
	// video (founder, 2026-09-04/05). Only a plain "post" with a video is
	// classified from the measurement (spec v2.1: flick = ≤300s AND
	// portrait/square; long_video = everything else), and defaults to
	// long_video while transcode is still pending. See
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
		// Persisting `explicit` is what lets the MediaTranscodeConsumer
		// tell an author's long_video from a "post" that defaulted to
		// long_video while transcode was pending: it reclassifies only the
		// latter once the measurement lands.
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

	// Build the PostCreated event BEFORE the insert so the outbox row
	// commits in the same transaction as the post (Codex P0-1: event and
	// row are atomic; closes the old commit→outbox dual-write window).
	createEventType := ""
	var createPayload interface{}
	if s.producer != nil {
		resolved := ResolveDistribution(effectivePolicy)
		pc := events.PostCreatedPayload{
			PostID:          p.ID.String(),
			AuthorID:        p.AuthorID.String(),
			Text:            p.Text,
			Visibility:      p.Visibility,
			ContentType:     p.ContentType,
			DurationSeconds: maxDuration,
			CreatedAt:       p.CreatedAt,
			DistributionRev: p.DistributionRev,
			// Module 2 M2-P0-1: carry the CANONICAL persisted moderation
			// state so search can refuse to index held content. Without
			// this, a post gated at 'pending' by the video/voice safety
			// check was indexed and publicly findable immediately.
			ReviewStatus: p.ReviewStatus,
			// Creation is always revision 1; later transitions increment.
			SearchRev: 1,
		}
		// Additive pointer fields: stamped whenever an intent exists —
		// either a typed policy or explicit legacy fields (P1-1). Events
		// for clients that expressed no opinion stay byte-compatible.
		if effectivePolicy != nil {
			mf, ns := resolved.MainFeed, resolved.NotifySubscribers
			pc.MainFeed = &mf
			pc.NotifySubscribers = &ns
		}
		// Subscriber fan-out key (P0-3): best-effort canonical channel
		// lookup for video uploads. Empty on failure — the notification
		// consumer treats a missing channel as "no subscriber fan-out".
		if isVideoContentType(p.ContentType) {
			pc.ChannelID = s.lookupChannelIDForUser(ctx, p.AuthorID)
		}
		createEventType = events.PostCreated
		createPayload = pc
	}

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
	if err := s.pgStore.CreatePostWithEventIdempotent(
		ctx, p, createEventType, createPayload, idem,
	); err != nil {
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

	// Persist @mentions to post_mentions table
	if len(mentions) > 0 {
		DetectAndStoreMentions(ctx, p.ID, p.ContentType, p.Text, s.pgStore)
	}

	// Create video_metadata for video content types.
	//
	// ALWAYS for a video attachment, even before transcode has measured it
	// (instant publish, 2026-09-04). The row is the join the
	// MediaTranscodeConsumer uses to find this post when the transcode
	// completes — to flip the `pending` review gate, wire the HLS URL and
	// reclassify. A post created while its media was still processing used
	// to get no row at all, so it could never be released.
	if videoMediaID != uuid.Nil {
		vm := &postgres.VideoMetadata{
			PostID:       p.ID,
			UploadStatus: "pending",
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

	// Resolve @mentions and emit user.mentioned events (fire and forget)
	if s.producer != nil && len(mentions) > 0 {
		postID := p.ID
		authorID := p.AuthorID
		for _, uname := range mentions {
			go func(username string) {
				ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				userID, err := s.lookupUserByUsername(ctx2, username)
				if err != nil || userID == "" {
					return
				}
				mentionedID, err := uuid.Parse(userID)
				if err != nil || mentionedID == authorID {
					return // skip self-mentions
				}
				if err := s.producer.PublishUserMentioned(ctx2, mentionedID, authorID, postID.String()); err != nil {
					log.Printf("Warning: failed to publish UserMentioned event for @%s: %v", username, err)
				}
			}(uname)
		}
	}

	// Invalidate author content counts cache
	s.rdb.Del(ctx, fmt.Sprintf("post:author-counts:%s", input.AuthorID))

	// PostCreated now rides the same transaction as the post row (see
	// CreatePostWithEvent above) — the outbox worker publishes it to
	// Kafka on its next 5s tick with retry until success.

	// Fire-and-forget: ephemeral Redis pub/sub for live signaling.
	// Not durable — clients tolerate missing one notification and
	// catch up on next REST fetch; SSE replay covers the gap. The
	// durable Kafka event goes through the outbox above.
	go func() {
		bgCtx := context.Background()

		// Bump trending hashtag scores for today's bucket. The reader is
		// search-service `GetTrending` and post-service `GetTrendingHashtagsFeed`,
		// both of which read from `trending:hashtags:{YYYY-MM-DD}` (UTC).
		if len(p.Hashtags) > 0 {
			today := time.Now().UTC().Format("2006-01-02")
			key := "trending:hashtags:" + today
			pipe := s.rdb.Pipeline()
			for _, tag := range p.Hashtags {
				pipe.ZIncrBy(bgCtx, key, 1, tag)
			}
			// 48h TTL keeps the previous day's set alive briefly so reads
			// that race past midnight don't return empty.
			pipe.Expire(bgCtx, key, 48*time.Hour)
			if _, err := pipe.Exec(bgCtx); err != nil {
				log.Printf("Warning: failed to update trending:hashtags: %v", err)
			}

			// Counter-sharding rollout: per-tag +1 into the aggregate
			// `hashtags.use_count` counter (kind: "hashtag_use_count").
			// The flush worker (cmd/server) materializes shard sums back
			// into the PG row every 10s. This replaces what would have
			// been a per-event `UPDATE hashtags SET use_count = use_count + 1`
			// hot-row contention pattern at trending-tag scale. The PG
			// fallback (Redis-less dev loops) is the UPSERT inside
			// adjustHashtagUseCount.
			for _, tag := range p.Hashtags {
				cleaned := strings.ToLower(strings.TrimPrefix(tag, "#"))
				if cleaned == "" {
					continue
				}
				if err := s.adjustHashtagUseCount(bgCtx, cleaned); err != nil {
					log.Printf("Warning: failed to bump hashtags.use_count for %s: %v", cleaned, err)
				}
			}
		}

		snippet := p.Text
		if len(snippet) > 120 {
			snippet = snippet[:120]
		}
		feedSignal, _ := json.Marshal(map[string]interface{}{
			"type": "new_post",
			"payload": map[string]interface{}{
				"post_id":      p.ID.String(),
				"author_id":    p.AuthorID.String(),
				"content_type": p.ContentType,
				"snippet":      snippet,
				"created_at":   p.CreatedAt,
			},
		})
		s.rdb.Publish(bgCtx, "feed:new_post", feedSignal)

		// Per-hashtag real-time push. Same shape as feed:new_post so
		// the SSE handler in internal/http/hashtag_stream.go can
		// forward straight through. One channel per tag — clients
		// subscribed to a specific tag only see posts that actually
		// carry it, no client-side filtering needed.
		for _, tag := range p.Hashtags {
			cleaned := strings.ToLower(strings.TrimPrefix(tag, "#"))
			if cleaned == "" {
				continue
			}
			s.rdb.Publish(bgCtx, "hashtag:"+cleaned+":new_post", feedSignal)
		}
	}()

	// The create response is the first render of the post the composer
	// navigates to. It carries the per-media pipeline state and
	// is_processing so the client can show "uploading… improving quality"
	// instead of a broken player.
	if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
		return nil, err
	}
	return p, nil
}

// getViewCount reads the display view counter analytics-service maintains
// in shared Redis (post:views:{id} hash, "display" field). Best-effort:
// returns 0 on any miss / Redis error.
func (s *Service) getViewCount(ctx context.Context, postID uuid.UUID) int64 {
	if s.rdb == nil {
		return 0
	}
	n, err := s.rdb.HGet(ctx, "post:views:"+postID.String(), "display").Int64()
	if err != nil {
		return 0
	}
	return n
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
	if p.ReviewStatus != "" && p.ReviewStatus != "approved" {
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
	if hiddenWhileProcessing(p, viewerID) {
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

	counts, err := s.scyllaStore.GetCounts(ctx, id)
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

	// Repost count from PG
	repostCount, _ := s.pgStore.GetRepostCount(ctx, id)
	detail.RepostCount = repostCount

	// A post is repostable if it's public (non-private)
	detail.IsRepostable = p.Visibility != "private"

	// Enrich with viewer-specific state
	if viewerID != nil {
		reaction, err := s.scyllaStore.GetReaction(ctx, id, *viewerID)
		if err != nil {
			return nil, fmt.Errorf("load viewer reaction: %w", err)
		}
		if reaction != "" {
			detail.ViewerReaction = &reaction
			detail.HasReacted = true
		}
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
	}

	// Tube: the author's channel card on a long video (channels.go).
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, []*PostDetail{detail})

	return detail, nil
}

// GetPostsByAuthor returns paginated posts by a specific author. The author
// sees their own posts regardless of review status (a flagged reel still
// shows in their own profile grid); every other viewer sees only approved.
func (s *Service) GetPostsByAuthor(ctx context.Context, authorID uuid.UUID, contentType string, limit int, cursor string, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	isAuthor := viewerID != nil && *viewerID == authorID
	// Private account: the whole grid is follower-only. An empty page — not
	// an error — is the honest answer for a stranger: the profile itself
	// stays reachable (with is_private=true) and the client renders the
	// "This account is private" state. Fail-closed on an unresolved graph.
	if !isAuthor && !s.canViewAuthor(ctx, viewerID, authorID) {
		return []PostDetail{}, "", nil
	}
	posts, nextCursor, err := s.pgStore.GetPostsByAuthor(ctx, authorID, contentType, limit, cursor, isAuthor)
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

	// Merge counts from Scylla for each visible post
	details := make([]PostDetail, 0, len(posts))
	for _, p := range posts {
		post := p // copy to avoid pointer reuse
		if !isAuthor {
			switch strings.ToLower(post.Visibility) {
			case "", "public", "unlisted":
			case "followers", "circle":
				if !viewerFollows() {
					continue
				}
			default: // private, or an unknown value — fail closed
				continue
			}
		}
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
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
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
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
	return details, nextCursor, nil
}

// GetPostsByIDs returns a map of post_id → PostDetail for the given IDs.
// If viewerID is provided, viewer-specific state (reaction, bookmark) is included.
func (s *Service) GetPostsByIDs(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
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

	result := make(map[uuid.UUID]*PostDetail, len(posts))
	for _, p := range posts {
		post := p // copy to avoid pointer reuse

		if viewerID == nil || *viewerID != post.AuthorID {
			if !viewableAuthor[post.AuthorID] {
				continue
			}
		}

		// Audit CF1: defense-in-depth visibility filter on the batch
		// path. Feed-service's fanout writes recipient timelines without
		// consulting post visibility, so a `private` post still ends up
		// in follower timelines. Drop it here unconditionally unless the
		// viewer is the author. `followers`/`circle` are trusted to be
		// gated by the recipient-set the fanout produced; the broader
		// fix (visibility-aware fanout) is tracked separately.
		if strings.EqualFold(post.Visibility, "private") {
			if viewerID == nil || *viewerID != post.AuthorID {
				continue
			}
		}

		// reels/posttube item 5: hide non-approved posts (flagged /
		// rejected / pending) from everyone but the author — mirrors the
		// GetPost gate so feed hydration never surfaces moderated-out
		// content even when fanout already wrote a recipient timeline row.
		if post.ReviewStatus != "" && !strings.EqualFold(post.ReviewStatus, "approved") {
			if viewerID == nil || *viewerID != post.AuthorID {
				continue
			}
		}

		// Instant publish: a post whose media is not yet ready+passed is
		// the author's alone. Feed fanout wrote the follower timeline rows
		// at create time; this is where they stay hidden until the media
		// lands (feed-service mirrors it at its hydration tail).
		if hiddenWhileProcessing(&post, viewerID) {
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
			IsRepostable: post.Visibility != "private",
		}

		// Enrich with viewer-specific state
		if viewerID != nil {
			reaction := reactions[post.ID]
			if reaction != "" {
				detail.ViewerReaction = &reaction
				detail.HasReacted = true
			}
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

		result[post.ID] = detail
	}

	return result, nil
}

// GetAuthorCounts returns post counts grouped by content type.
func (s *Service) GetAuthorCounts(ctx context.Context, authorID uuid.UUID) (map[string]int64, error) {
	return s.pgStore.GetPostCountsByAuthor(ctx, authorID)
}

// TogglePin sets or unsets pinned status, enforcing max 3 pinned per author.
func (s *Service) TogglePin(ctx context.Context, postID, authorID uuid.UUID, pinned bool) error {
	if pinned {
		count, err := s.pgStore.CountPinnedByAuthor(ctx, authorID)
		if err != nil {
			return err
		}
		if count >= 3 {
			return fmt.Errorf("maximum 3 pinned posts allowed")
		}
	}
	return s.pgStore.SetPinned(ctx, postID, authorID, pinned)
}

func (s *Service) React(ctx context.Context, postID, userID uuid.UUID, reaction string) error {
	// Audit C5 + H2: one fetch covers visibility + author-id for
	// the PostReacted event. Was: visibility check did a GetPost,
	// the goroutine below did *another* GetPost just for AuthorID.
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return err
	}
	if err := s.scyllaStore.React(ctx, postID, userID, reaction); err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)

	// Audit H4: PostReacted via outbox. Synchronous insert so a
	// process crash in the React goroutine window doesn't drop the
	// notification trigger.
	if s.producer != nil {
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
		counts, _ := s.scyllaStore.GetCounts(bgCtx, postID)
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
	if err := s.scyllaStore.Unreact(ctx, postID, userID); err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)

	// Fire-and-forget: Redis publish in background
	go func() {
		bgCtx := context.Background()
		counts, _ := s.scyllaStore.GetCounts(bgCtx, postID)
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
	return s.scyllaStore.GetReaction(ctx, postID, userID)
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
		counts, _ := s.scyllaStore.GetCounts(bgCtx, postID)
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

func (s *Service) GetBookmarks(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetBookmarks(ctx, userID, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts, IsBookmarked: true}
	}

	// Instant publish (processing.go): the bookmarker is the viewer.
	details, err = s.attachMediaStateToDetails(ctx, details, &userID)
	if err != nil {
		return nil, "", err
	}
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

// CastVote records a user's vote on a poll option.
func (s *Service) CastVote(ctx context.Context, postID, optionID, userID uuid.UUID) error {
	// Check poll exists and hasn't ended
	poll, err := s.pgStore.GetPoll(ctx, postID)
	if err != nil {
		return err
	}
	if poll == nil {
		return fmt.Errorf("poll not found")
	}
	if poll.HasEnded {
		return fmt.Errorf("poll has ended")
	}

	// If single-choice, check if user already voted
	if !poll.AllowsMultiple {
		existing, _ := s.pgStore.GetUserPollVotes(ctx, postID, userID)
		if len(existing) > 0 {
			return fmt.Errorf("already voted on this poll")
		}
	}

	return s.pgStore.CastVote(ctx, postID, optionID, userID)
}

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
		if err := s.scyllaStore.React(ctx, postID, userID, "like"); err != nil {
			log.Printf("Warning: failed to write reaction to ScyllaDB: %v", err)
		}
	} else {
		if err := s.scyllaStore.Unreact(ctx, postID, userID); err != nil {
			log.Printf("Warning: failed to remove reaction from ScyllaDB: %v", err)
		}
	}
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
		if _, err := s.pgStore.SaveItem(ctx, userID, "post", postID, ""); err != nil {
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

// ToggleCommentLike executes the atomic Lua toggle for comment likes with mutual exclusion.
func (s *Service) ToggleCommentLike(ctx context.Context, commentID, userID uuid.UUID) (*CommentLikeToggleResult, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment_like:%s", userID), engagement.CommentLikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	result, err := engagement.ToggleCommentLike(ctx, s.rdb, userID, commentID)
	if err != nil {
		return nil, err
	}

	// Update PostgreSQL like_count
	likeDelta := 1
	if !result.IsSet {
		likeDelta = -1
	}
	if err := s.pgStore.IncrementCommentLikeCount(ctx, commentID, likeDelta); err != nil {
		log.Printf("Warning: failed to update comment like_count: %v", err)
	}

	// If a dislike was removed by mutual exclusion, decrement dislike_count in PG
	if result.OppositeRemoved {
		if err := s.pgStore.IncrementCommentDislikeCount(ctx, commentID, -1); err != nil {
			log.Printf("Warning: failed to update comment dislike_count: %v", err)
		}
	}

	eventType := engagement.EventCommentLiked
	if !result.IsSet {
		eventType = engagement.EventCommentUnliked
	}

	if s.engProducer != nil {
		event := engagement.BuildEvent(eventType, uuid.Nil, userID, uuid.Nil, commentID, "comment", "like", result.IsSet, result.Seq, result.ActionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment like event: %v", err)
			}
		}()
		// If dislike was removed, also publish that event
		if result.OppositeRemoved {
			dislikeEvent := engagement.BuildEvent(engagement.EventCommentUndisliked, uuid.Nil, userID, uuid.Nil, commentID, "comment", "dislike", false, result.Seq, result.ActionTS)
			go func() {
				if err := s.engProducer.Publish(context.Background(), dislikeEvent); err != nil {
					log.Printf("Warning: failed to publish comment undislike event: %v", err)
				}
			}()
		}
	}

	// Publish social event for notifications (only on like, not unlike)
	if s.producer != nil && result.IsSet {
		go func() {
			bgCtx := context.Background()
			comment, err := s.pgStore.GetCommentByID(bgCtx, commentID)
			if err != nil {
				log.Printf("Warning: failed to look up comment for notification: %v", err)
				return
			}
			if comment.AuthorID == userID {
				return // Don't notify on self-like
			}
			if err := s.producer.PublishCommentReacted(bgCtx, commentID, comment.PostID, comment.AuthorID, userID, "like"); err != nil {
				log.Printf("Warning: failed to publish CommentReacted event: %v", err)
			}
		}()
	}

	return &CommentLikeToggleResult{Liked: result.IsSet, Count: result.LikeCount, DislikeCount: result.DislikeCount}, nil
}

// CommentDislikeToggleResult is the response shape for the comment dislike toggle API.
type CommentDislikeToggleResult struct {
	Disliked     bool  `json:"disliked"`
	DislikeCount int64 `json:"dislike_count"`
	LikeCount    int64 `json:"like_count"`
}

// ToggleCommentDislike executes the atomic Lua toggle for comment dislikes with mutual exclusion.
func (s *Service) ToggleCommentDislike(ctx context.Context, commentID, userID uuid.UUID) (*CommentDislikeToggleResult, error) {
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
	post, err := s.pgStore.GetPost(ctx, postID)
	if err != nil || post == nil {
		return nil, fmt.Errorf("POST_NOT_FOUND")
	}
	if post.Visibility == "private" || (post.Visibility == "followers" && shareType != "external") {
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
	if post.NoComments {
		return nil, ErrCommentsDisabled
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

	// Bump the sharded post_engagement_counts.comment_count via Redis
	// (with PG fallback inside adjustEngagementCount). The matching
	// flush worker in cmd/server/main.go materialises the shard sum
	// back to PG every ~10s; the hourly reconciler is the safety net.
	if err := s.adjustEngagementCount(ctx, s.commentCounter, postID, "comment_count", 1); err != nil {
		slog.Warn("failed to increment comment_count", "post_id", postID, "error", err)
	}

	// Update Redis counter
	engKey := fmt.Sprintf("post:eng:%s", postID)
	s.rdb.HIncrBy(ctx, engKey, "comments", 1)

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

	return comment, nil
}

// CreateReply creates a reply to a comment. Post-owner-only enforcement.
func (s *Service) CreateReply(ctx context.Context, commentID, userID uuid.UUID, body string) (*postgres.Comment, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:reply:%s", userID), engagement.ReplyLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	reply, parentAuthorID, err := s.pgStore.CreateReply(ctx, commentID, userID, body)
	if err != nil {
		return nil, err
	}

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

	// Publish legacy event so notification-service sends a notification to the comment author
	if s.producer != nil {
		go func() {
			if err := s.producer.PublishCommentCreated(context.Background(), reply.ID, reply.PostID, parentAuthorID, userID, body); err != nil {
				log.Printf("Warning: failed to publish legacy reply notification event: %v", err)
			}
		}()
	}

	return reply, nil
}

// SoftDeleteComment marks a comment as deleted and decrements counter.
func (s *Service) SoftDeleteComment(ctx context.Context, commentID, userID uuid.UUID) error {
	postID, err := s.pgStore.SoftDeleteComment(ctx, commentID, userID)
	if err != nil {
		return err
	}

	// Decrement the sharded post_engagement_counts.comment_count.
	if err := s.adjustEngagementCount(ctx, s.commentCounter, postID, "comment_count", -1); err != nil {
		slog.Warn("failed to decrement comment_count", "post_id", postID, "error", err)
	}

	// Update Redis counter
	engKey := fmt.Sprintf("post:eng:%s", postID)
	s.rdb.HIncrBy(ctx, engKey, "comments", -1)

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
	return s.pgStore.EditComment(ctx, commentID, userID, body)
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
func (s *Service) GetCommentsAroundPG(ctx context.Context, postID, commentID uuid.UUID, viewerID *uuid.UUID, limit int) ([]postgres.Comment, error) {
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
//   - visibility == "followers" or "circle" AND the viewer follows
//     the author
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
	post, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, ErrPostNotFound
	}
	if post.AuthorID == viewerID {
		return post, nil
	}
	// Account-level gate (private accounts) before the per-post one; see
	// viewerMayViewPost. Fail-closed.
	if !s.canViewAuthor(ctx, &viewerID, post.AuthorID) {
		return nil, ErrPostNotVisible
	}
	switch strings.ToLower(post.Visibility) {
	case "", "public":
		return post, nil
	case "private":
		return nil, ErrPostNotVisible
	case "followers", "circle":
		follows, err := s.checkViewerFollowsAuthor(ctx, viewerID, post.AuthorID)
		if err != nil {
			log.Printf("Warning: visibility check graph lookup failed; rejecting: %v", err)
			return nil, ErrPostNotVisible
		}
		if !follows {
			return nil, ErrPostNotVisible
		}
		return post, nil
	default:
		// Unknown visibility value: treat as private (defense in
		// depth — a typo in a migration shouldn't open up engagement).
		return nil, ErrPostNotVisible
	}
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
		return false
	case "followers", "circle":
		if viewerID == nil {
			return false
		}
		follows, err := s.checkViewerFollowsAuthor(ctx, *viewerID, post.AuthorID)
		if err != nil {
			log.Printf("Warning: visibility check graph lookup failed; rejecting: %v", err)
			return false
		}
		return follows
	default:
		return false
	}
}

// checkViewerFollowsAuthor does a single graph-service relationship
// lookup. Returns (follows=true) when viewer→author edge exists.
// Empty graphServiceURL is treated as "no policy" — same as the
// existing fetchFollowing helper — so unit tests + dev rigs without
// graph-service skip the gate cleanly.
func (s *Service) checkViewerFollowsAuthor(ctx context.Context, viewerID, authorID uuid.UUID) (bool, error) {
	if s.graphServiceURL == "" {
		return true, nil
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
		return false, fmt.Errorf("build relationship request: %w", err)
	}
	// graph-service gates /v1/graph/* behind the internal service key.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("relationship request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("graph-service status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Follows bool `json:"follows"`
		} `json:"data"`
		Follows bool `json:"follows"` // legacy un-wrapped shape tolerated
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, fmt.Errorf("decode relationship: %w", err)
	}
	if envelope.Data.Follows {
		return true, nil
	}
	return envelope.Follows, nil
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
	if !postgres.ValidReactionTypes[reactionType] {
		return nil, fmt.Errorf("INVALID_REACTION_TYPE")
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

	newType, isSet, err := s.pgStore.ToggleReaction(ctx, "post", postID, userID, reactionType)
	if err != nil {
		return nil, err
	}

	// Also sync to ScyllaDB for feed hydration compatibility
	if isSet {
		if err := s.scyllaStore.React(ctx, postID, userID, newType); err != nil {
			log.Printf("Warning: failed to sync reaction to ScyllaDB: %v", err)
		}
	} else {
		if err := s.scyllaStore.Unreact(ctx, postID, userID); err != nil {
			log.Printf("Warning: failed to remove reaction from ScyllaDB: %v", err)
		}
	}

	// Get updated counts
	counts, err := s.pgStore.GetReactionCounts(ctx, "post", postID)
	if err != nil {
		log.Printf("Warning: failed to get reaction counts: %v", err)
	}

	// Audit H4: PostReacted via outbox. Skip self-reactions same as
	// before — those don't generate notifications. `post` is already
	// in scope from the H2 visibility check at the top of this
	// function, so no extra GetPost round trip is needed.
	if s.producer != nil && isSet && post.AuthorID != userID {
		payload := events.PostReactedPayload{
			PostID:       postID.String(),
			PostAuthorID: post.AuthorID.String(),
			ReactorID:    userID.String(),
			ReactType:    newType,
			CreatedAt:    time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostReacted, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue PostReacted (ToggleReaction) to outbox: %v", err)
		}
	}

	return &ReactionToggleResult{
		ReactionType: newType,
		IsSet:        isSet,
		Counts:       counts,
	}, nil
}

// ── Video Creator Tools ────────────────────────────────────────

// GetVideoDetail returns the video metadata for a post.
func (s *Service) GetVideoDetail(ctx context.Context, postID uuid.UUID) (*postgres.VideoMetadata, error) {
	return s.pgStore.GetVideoMetadata(ctx, postID)
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

// PublishVideo publishes a video post, checking processing status first.
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

	return s.pgStore.PublishPost(ctx, postID)
}

// GetReactionCounts returns the breakdown of reaction counts for a post.
func (s *Service) GetReactionCounts(ctx context.Context, postID uuid.UUID) (*postgres.ReactionCounts, error) {
	return s.pgStore.GetReactionCounts(ctx, "post", postID)
}

// ============================================================
// Saved Items / Collections
// ============================================================

// SaveItem saves a post/video/reel to a user's collection.
func (s *Service) SaveItem(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionName string) (*postgres.SavedItem, error) {
	return s.pgStore.SaveItem(ctx, userID, targetType, targetID, collectionName)
}

// UnsaveItem removes a saved item.
func (s *Service) UnsaveItem(ctx context.Context, savedID, userID uuid.UUID) error {
	return s.pgStore.UnsaveItem(ctx, savedID, userID)
}

// ListSavedItems returns paginated saved items.
func (s *Service) ListSavedItems(ctx context.Context, userID uuid.UUID, collectionName string, limit int, cursor string) ([]postgres.SavedItem, string, error) {
	return s.pgStore.ListSavedItems(ctx, userID, collectionName, limit, cursor)
}

// ListCollections returns all saved collections for a user.
func (s *Service) ListCollections(ctx context.Context, userID uuid.UUID) ([]postgres.SavedCollection, error) {
	return s.pgStore.ListCollections(ctx, userID)
}

// ============================================================
// Hashtag Search
// ============================================================

// GetPostsByHashtag returns posts with a specific hashtag.
// sort accepts "top" or "recent" (default).
// contentTypes filters by content_type (e.g. ["post"], ["flick"], ["long_video"]); nil = all.
func (s *Service) GetPostsByHashtag(ctx context.Context, hashtag string, limit int, cursor, sort string, contentTypes []string) ([]PostDetail, string, error) {
	mode := postgres.HashtagSortRecent
	if sort == "top" {
		mode = postgres.HashtagSortTop
	}
	posts, nextCursor, err := s.pgStore.GetPostsByHashtag(ctx, hashtag, limit, cursor, mode, contentTypes)
	if err != nil {
		return nil, "", err
	}

	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts}
	}

	// Instant publish (processing.go): public discovery surface with no
	// viewer — a still-processing post is nobody's to see here.
	details, err = s.attachMediaStateToDetails(ctx, details, nil)
	if err != nil {
		return nil, "", err
	}
	return details, nextCursor, nil
}

// GetTrendingPosts returns trending posts globally, optionally scoped to one
// or more content types. Used by the Posttube/Reels "Trending" tabs and the
// general discover surface. cursor is the same opaque base64 string used by
// the hashtag top sort.
func (s *Service) GetTrendingPosts(ctx context.Context, contentTypes []string, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetTrendingPosts(ctx, contentTypes, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts}
	}
	// Instant publish (processing.go): public discovery surface with no
	// viewer — a still-processing post is nobody's to see here.
	details, err = s.attachMediaStateToDetails(ctx, details, nil)
	if err != nil {
		return nil, "", err
	}
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

// lookupUserByUsername resolves a username to a user ID via user-service.
func (s *Service) lookupUserByUsername(ctx context.Context, username string) (string, error) {
	if s.userServiceURL == "" {
		return "", nil
	}
	url := fmt.Sprintf("%s/v1/users/by-username/%s", s.userServiceURL, username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		UserID string `json:"user_id"`
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	return result.UserID, nil
}

// lookupChannelIDForUser resolves the author's canonical broadcast channel
// via user-service (internal contract, P0-3). Best-effort: returns "" on
// any failure or when the user has no channel — consumers treat "" as
// "no subscriber fan-out possible".
func (s *Service) lookupChannelIDForUser(ctx context.Context, userID uuid.UUID) string {
	if s.userServiceURL == "" {
		return ""
	}
	url := fmt.Sprintf("%s/internal/channels/by-owner/%s", s.userServiceURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var result struct {
		ChannelID string `json:"channel_id"`
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	return result.ChannelID
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

// shouldRestrictToTrustedCircle returns true when the author has
// `tc_after_hours_posts = true` AND the supplied time falls in the
// late-night window (22:00–06:00 server time). Server time is used
// rather than client TZ because clients don't ship a reliable TZ
// header today; switching to user-local time is a follow-up.
//
// Returns false on any user-service lookup failure — the feature
// degrades silently to "use the supplied visibility" so a settings
// service blip doesn't break post creation.
func (s *Service) shouldRestrictToTrustedCircle(ctx context.Context, authorID uuid.UUID, now time.Time) bool {
	if s.userServiceURL == "" {
		return false
	}
	on, err := s.fetchAfterHoursToggle(ctx, authorID)
	if err != nil || !on {
		return false
	}
	return isAfterHours(now)
}

// fetchAfterHoursToggle reads the user's settings from user-service.
// Lightweight call; bounded by the shared 5s http client timeout.
// Forwards INTERNAL_SERVICE_KEY when set so the user-service auth
// gate accepts the cross-service call.
func (s *Service) fetchAfterHoursToggle(ctx context.Context, authorID uuid.UUID) (bool, error) {
	url := fmt.Sprintf("%s/v1/user/%s/settings", s.userServiceURL, authorID.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("user-service settings: status %d", resp.StatusCode)
	}
	// user-service wraps responses in `{data: {...}}`; decode both shapes.
	var envelope struct {
		Data struct {
			TcAfterHoursPosts bool `json:"tc_after_hours_posts"`
		} `json:"data"`
		TcAfterHoursPosts bool `json:"tc_after_hours_posts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, err
	}
	if envelope.Data.TcAfterHoursPosts {
		return true, nil
	}
	return envelope.TcAfterHoursPosts, nil
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
	// Rate limit
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:repost:%s", input.UserID), 30, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// 1. Verify original post exists and is not deleted (GetPost filters deleted_at IS NULL)
	post, err := s.pgStore.GetPost(ctx, input.PostID)
	if err != nil || post == nil {
		return nil, fmt.Errorf("POST_NOT_FOUND")
	}

	// 2. Visibility check — private or followers-only posts cannot be reposted
	if post.Visibility == "private" {
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
}

// ListUserRepostsResult is the response shape for GET /users/{userId}/reposts.
type ListUserRepostsResult struct {
	Items      []UserRepostItem `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// ListUserReposts returns a paginated list of reposts by a given user.
func (s *Service) ListUserReposts(ctx context.Context, userID uuid.UUID, limit int, cursor string) (*ListUserRepostsResult, error) {
	reposts, nextCursor, err := s.pgStore.ListUserReposts(ctx, userID, limit, cursor)
	if err != nil {
		return nil, err
	}
	items := make([]UserRepostItem, 0, len(reposts))
	for _, r := range reposts {
		items = append(items, UserRepostItem{
			RepostID:       r.ID,
			Type:           r.RepostType,
			QuoteText:      r.QuoteText,
			OriginalPostID: r.OriginalPostID,
			CreatedAt:      r.CreatedAt.Format(time.RFC3339),
		})
	}
	return &ListUserRepostsResult{Items: items, NextCursor: nextCursor}, nil
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

// audienceMayBeAutoRestricted answers whether the after-hours rule is even
// allowed to consider this post — Slice C, C-LB-2.
//
// # WHY THIS IS A NAMED, PURE FUNCTION
//
// This is the consent boundary. The rule narrows a post's audience without
// being asked, so the one thing that must never regress is that it cannot
// touch an audience the author chose deliberately. The condition used to match
// on the VALUE alone — and a defaulting client and a deliberate one send the
// identical value — so an explicitly Public post made at 23:00 was silently
// rewritten to `trusted` while the composer, the response and the author all
// still said Public.
//
// It lived inline inside a method that makes a cross-service call and reads the
// wall clock, so it could not be tested at all. As a pure function it is a
// table test against a fixed clock, which is what NC-C2A mutates.
//
// `followers` is included because it is also a value a client can arrive at
// without a decision. An explicit `followers` is protected by the same flag.
func audienceMayBeAutoRestricted(visibilityExplicit bool, visibility string) bool {
	if visibilityExplicit {
		return false
	}
	return visibility == "" || visibility == "public" || visibility == "followers"
}

// isAfterHours is the 22:00–05:59 window, on whatever clock it is given.
//
// Separated from the toggle fetch so the window itself is testable without a
// user-service standing behind it. 06:00 is back to normal hours.
func isAfterHours(now time.Time) bool {
	hour := now.Hour()
	return hour >= 22 || hour < 6
}
