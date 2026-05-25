package service

import (
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
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	hashtagRegex = regexp.MustCompile(`#(\w{1,50})`)
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
	ErrLikesDisabled    = errors.New("likes are disabled on this post")
	ErrCommentsDisabled = errors.New("comments are disabled on this post")
)

type Service struct {
	pgStore         *postgres.Store
	scyllaStore     *scylla.InteractionStore
	scyllaSession   *gocql.Session
	rdb             *redis.Client
	producer        *postEvents.Producer // legacy producer, optional
	engProducer     *engagement.Producer // new engagement event producer
	rateLimiter     *engagement.RateLimiter
	spam            *spam.Detector
	userServiceURL          string
	graphServiceURL         string
	monetizationServiceURL  string
	internalServiceKey      string
	httpClient              *http.Client

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
	if rdb != nil {
		svc.likeCounter = counters.New(rdb, counters.Config{EntityKind: "post_like_count", Shards: 32})
		svc.commentCounter = counters.New(rdb, counters.Config{EntityKind: "post_comment_count", Shards: 32})
		svc.shareCounter = counters.New(rdb, counters.Config{EntityKind: "post_share_count", Shards: 32})
		svc.bookmarkCounter = counters.New(rdb, counters.Config{EntityKind: "post_bookmark_count", Shards: 32})
		svc.repostCounter = counters.New(rdb, counters.Config{EntityKind: "post_repost_count", Shards: 32})
		svc.hashtagCounter = counters.New(rdb, counters.Config{EntityKind: "hashtag_use_count", Shards: 32})
		svc.audioCounter = counters.New(rdb, counters.Config{EntityKind: "audio_use_count", Shards: 32})
	}
	return svc
}

// LikeCounter / CommentCounter / ShareCounter / BookmarkCounter /
// RepostCounter / HashtagCounter / AudioCounter expose the sharded
// counters so cmd/server can attach flush workers. Returns nil when
// Redis isn't configured.
func (s *Service) LikeCounter() *counters.Counter     { return s.likeCounter }
func (s *Service) CommentCounter() *counters.Counter  { return s.commentCounter }
func (s *Service) ShareCounter() *counters.Counter    { return s.shareCounter }
func (s *Service) BookmarkCounter() *counters.Counter { return s.bookmarkCounter }
func (s *Service) RepostCounter() *counters.Counter   { return s.repostCounter }
func (s *Service) HashtagCounter() *counters.Counter  { return s.hashtagCounter }
func (s *Service) AudioCounter() *counters.Counter    { return s.audioCounter }

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
	IsBookmarked   bool               `json:"is_bookmarked"`
	RepostCount    int                `json:"repost_count"`
	ViewerRepost   *RepostStateResult `json:"viewer_repost,omitempty"`
	IsRepostable   bool               `json:"is_repostable"`
}

// CreatePostInput holds all fields for creating a new post.
type CreatePostInput struct {
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
// Flick = up to 3 minutes, Long Video = more than 3 minutes.
const flickMaxDurationSeconds = 180

// validContentTypes is the allowed set for content_type.
var validContentTypes = map[string]bool{
	"post": true, "poll": true, "reel": true, "video": true,
	"flick": true, "long_video": true,
}

// classifyVideoContentType returns "flick" or "long_video" based on duration and dimensions.
// Legacy callers: "reel" maps to "flick", "video" maps to "long_video".
func classifyVideoContentType(durationSeconds int) string {
	if durationSeconds > 0 && durationSeconds <= flickMaxDurationSeconds {
		return "flick"
	}
	return "long_video"
}

// ClassifyVideo returns the computed category and orientation based on duration and dimensions.
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

// ValidateCategoryOverride checks if a category override request is valid.
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
	if input.Visibility == "" || input.Visibility == "public" || input.Visibility == "followers" {
		if s.shouldRestrictToTrustedCircle(ctx, input.AuthorID, time.Now()) {
			input.Visibility = "trusted"
			slog.Info("post: after-hours protection applied",
				"author_id", input.AuthorID, "visibility", input.Visibility)
		}
	}

	postType := input.PostType
	if postType == "" {
		postType = "text"
	}
	appOrigin := input.AppOrigin
	if appOrigin == "" {
		appOrigin = "postbook"
	}

	// Extract hashtags from text
	hashtags := extractHashtags(input.Text)

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

	p := &postgres.Post{
		ID:                uuid.New(),
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
		Title:             input.Title,
		Tags:              input.Tags,
		Category:          input.Category,
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
		CreatedAt:         time.Now(),
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
		p.Media = append(p.Media, postgres.PostMedia{
			MediaID: mediaID,
			Kind:    kind,
		})
		if kind == "video" && dur > maxDuration {
			maxDuration = dur
		}
	}

	// Auto-classify video content type per spec v2.1:
	// Flick = ≤180s AND (portrait/square); LongVideo = everything else.
	// If duration is unknown (async processing not done), default to long_video as safe fallback.
	var videoMediaID uuid.UUID
	hasVideo := false
	for _, m := range p.Media {
		if m.Kind == "video" {
			videoMediaID = m.MediaID
			hasVideo = true
			break
		}
	}
	if hasVideo {
		if maxDuration > 0 {
			// Duration known — classify properly via the shared rule.
			// Reuse the dimensions from the batch when available;
			// fall back to the per-row helper for the unlikely
			// batch-failed-but-loop-succeeded path.
			var w, h int
			if meta, ok := mediaMeta[videoMediaID]; ok {
				w, h = meta.Width, meta.Height
			} else {
				w, h, _ = s.pgStore.ResolveMediaDimensions(ctx, videoMediaID)
			}
			cat, _ := ClassifyVideo(float64(maxDuration), w, h)
			p.ContentType = cat
		} else {
			// Duration unknown (transcode pending). Respect the
			// caller's intent: if mobile said "flick"/"reel" — keep
			// it. The MediaTranscodeConsumer reclassifies once
			// duration + dimensions land. If the caller said "post"
			// (a generic post happens to attach a video) we still
			// safe-default to long_video because there's no explicit
			// short-form intent to preserve.
			switch contentType {
			case "flick", "reel":
				p.ContentType = "flick"
			case "post":
				p.ContentType = "long_video"
			}
			// content_type "long_video" or "video" stays as the
			// caller specified.
		}
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
	p.ReviewStatus = reviewStatus

	if err := s.pgStore.CreatePost(ctx, p); err != nil {
		return nil, err
	}

	// Record the auto-moderation verdict for video content (audit trail).
	// Skipped while 'pending' — the transcode consumer records the
	// terminal verdict when it finalizes the gate.
	if isVideoContentType(p.ContentType) && reviewStatus != "pending" {
		s.RecordVideoModeration(ctx, p.ID, reviewStatus, spamResult.Score)
	}

	// Persist @mentions to post_mentions table
	if len(mentions) > 0 {
		DetectAndStoreMentions(ctx, p.ID, p.ContentType, p.Text, s.pgStore)
	}

	// Create video_metadata for video content types
	if videoMediaID != uuid.Nil && maxDuration > 0 {
		width, height, _ := s.pgStore.ResolveMediaDimensions(ctx, videoMediaID)
		category, orientation := ClassifyVideo(float64(maxDuration), width, height)
		vm := &postgres.VideoMetadata{
			PostID:           p.ID,
			DurationSeconds:  float64(maxDuration),
			Width:            &width,
			Height:           &height,
			Orientation:      orientation,
			ComputedCategory: category,
			FinalCategory:    category,
			UploadStatus:     "pending",
			MediaAssetID:     &videoMediaID,
		}
		if err := s.pgStore.CreateVideoMetadata(ctx, vm); err != nil {
			log.Printf("Warning: failed to create video_metadata for post %s: %v", p.ID, err)
		}
		// Ensure post content_type matches classification
		p.ContentType = category
		s.pgStore.UpdatePostContentType(ctx, p.ID, category)
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

	// Audit H4: route PostCreated through the outbox. The previous
	// path was `go s.producer.PublishPostCreated(...)` — fire and
	// forget; a crash in the goroutine window between row commit
	// and Kafka publish silently dropped the event. Insert here
	// synchronously so the outbox worker (StartOutboxWorker) picks
	// it up on its next 5 s tick and PublishRaw's it to Kafka, with
	// the unpublished row driving retry until success.
	if s.producer != nil {
		postCreated := events.PostCreatedPayload{
			PostID:          p.ID.String(),
			AuthorID:        p.AuthorID.String(),
			Text:            p.Text,
			Visibility:      p.Visibility,
			ContentType:     p.ContentType,
			DurationSeconds: maxDuration,
			CreatedAt:       p.CreatedAt,
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostCreated, "post", p.ID, postCreated); err != nil {
			log.Printf("Warning: failed to enqueue PostCreated to outbox: %v", err)
		}
	}

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
		reaction, _ := s.scyllaStore.GetReaction(ctx, id, *viewerID)
		if reaction != "" {
			detail.ViewerReaction = &reaction
		}
		bookmarked, _ := s.pgStore.IsBookmarked(ctx, *viewerID, id)
		detail.IsBookmarked = bookmarked

		// Repost state
		repostState, _ := s.GetRepostState(ctx, *viewerID, id)
		if repostState != nil && repostState.HasReposted {
			detail.ViewerRepost = repostState
		}
	}

	return detail, nil
}

// GetPostsByAuthor returns paginated posts by a specific author. The author
// sees their own posts regardless of review status (a flagged reel still
// shows in their own profile grid); every other viewer sees only approved.
func (s *Service) GetPostsByAuthor(ctx context.Context, authorID uuid.UUID, contentType string, limit int, cursor string, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	includeNonApproved := viewerID != nil && *viewerID == authorID
	posts, nextCursor, err := s.pgStore.GetPostsByAuthor(ctx, authorID, contentType, limit, cursor, includeNonApproved)
	if err != nil {
		return nil, "", err
	}

	// Merge counts from Scylla for each post
	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p // copy to avoid pointer reuse
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		if post.ContentType == "poll" {
			poll, err := s.pgStore.GetPoll(ctx, post.ID)
			if err == nil && poll != nil {
				post.Poll = poll
			}
		}
		details[i] = PostDetail{Post: &post, Counts: counts}
	}

	return details, nextCursor, nil
}

// GetRecentPosts returns recent public posts from all users with engagement counts.
func (s *Service) GetRecentPosts(ctx context.Context, excludeAuthor *uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetRecentPosts(ctx, excludeAuthor, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		if post.ContentType == "poll" {
			poll, err := s.pgStore.GetPoll(ctx, post.ID)
			if err == nil && poll != nil {
				post.Poll = poll
			}
		}
		details[i] = PostDetail{Post: &post, Counts: counts}
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

	result := make(map[uuid.UUID]*PostDetail, len(posts))
	for _, p := range posts {
		post := p // copy to avoid pointer reuse

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

		counts, _ := s.scyllaStore.GetCounts(ctx, post.ID)

		detail := &PostDetail{Post: &post, Counts: counts}

		// Enrich with viewer-specific state
		if viewerID != nil {
			reaction, _ := s.scyllaStore.GetReaction(ctx, post.ID, *viewerID)
			if reaction != "" {
				detail.ViewerReaction = &reaction
			}
			bookmarked, _ := s.pgStore.IsBookmarked(ctx, *viewerID, post.ID)
			detail.IsBookmarked = bookmarked
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
	return s.pgStore.AddBookmark(ctx, userID, postID)
}

func (s *Service) RemoveBookmark(ctx context.Context, userID, postID uuid.UUID) error {
	// No visibility gate on remove — a user who already bookmarked
	// must always be able to clean up their own row even if the
	// post's visibility tightened (author switched to followers-only).
	return s.pgStore.RemoveBookmark(ctx, userID, postID)
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
func (s *Service) CreateCommentPG(ctx context.Context, postID, authorID uuid.UUID, body string) (*postgres.Comment, error) {
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

	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment:%s", authorID), engagement.CommentLimitPerMin, time.Minute) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	comment, err := s.pgStore.CreateComment(ctx, postID, authorID, body)
	if err != nil {
		return nil, err
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
	AuthorID       uuid.UUID
	MediaURL       string
	MediaType      string
	Caption        string
	Visibility     string
	IsHighlight    bool
	HighlightGroup *string
}

// CreateStory creates a new ephemeral story with 24h expiry.
func (s *Service) CreateStory(ctx context.Context, input *CreateStoryInput) (*postgres.Story, error) {
	visibility := input.Visibility
	if visibility == "" {
		visibility = "followers"
	}

	story := &postgres.Story{
		ID:             uuid.New(),
		AuthorID:       input.AuthorID,
		MediaURL:       input.MediaURL,
		MediaType:      input.MediaType,
		Caption:        input.Caption,
		Visibility:     visibility,
		ViewCount:      0,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		IsHighlight:    input.IsHighlight,
		HighlightGroup: input.HighlightGroup,
		CreatedAt:      time.Now(),
	}

	if err := s.pgStore.CreateStory(ctx, story); err != nil {
		return nil, err
	}

	// Publish story created event
	if s.producer != nil {
		go func() {
			bgCtx := context.Background()
			if err := s.producer.PublishStoryCreated(bgCtx, story.ID, story.AuthorID, story.MediaType); err != nil {
				log.Printf("Warning: failed to publish story.created event: %v", err)
			}
		}()
	}

	return story, nil
}

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

// ViewStory increments the view count of a story.
func (s *Service) ViewStory(ctx context.Context, storyID uuid.UUID) error {
	return s.pgStore.IncrementStoryViewCount(ctx, storyID)
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
func (s *Service) GetPostsByHashtag(ctx context.Context, hashtag string, limit int, cursor, sort string) ([]PostDetail, string, error) {
	mode := postgres.HashtagSortRecent
	if sort == "top" {
		mode = postgres.HashtagSortTop
	}
	posts, nextCursor, err := s.pgStore.GetPostsByHashtag(ctx, hashtag, limit, cursor, mode)
	if err != nil {
		return nil, "", err
	}

	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts}
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
	hour := now.Hour()
	// 22:00–05:59 inclusive (06:00 is back to normal-hours).
	return hour >= 22 || hour < 6
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
