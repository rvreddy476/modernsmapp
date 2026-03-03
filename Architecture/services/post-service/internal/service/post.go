package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	hashtagRegex = regexp.MustCompile(`#(\w{1,50})`)
	mentionRegex = regexp.MustCompile(`@(\w{1,30})`)
)

type Service struct {
	pgStore       *postgres.Store
	scyllaStore   *scylla.InteractionStore
	scyllaSession *gocql.Session
	rdb           *redis.Client
	producer      *postEvents.Producer  // legacy producer, optional
	engProducer   *engagement.Producer  // new engagement event producer
	rateLimiter   *engagement.RateLimiter
}

func New(pg *postgres.Store, scylla *scylla.InteractionStore, rdb *redis.Client) *Service {
	return &Service{
		pgStore:     pg,
		scyllaStore: scylla,
		rdb:         rdb,
		rateLimiter: engagement.NewRateLimiter(rdb),
	}
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
	Counts         *scylla.Counts `json:"counts"`
	ViewerReaction *string        `json:"viewer_reaction,omitempty"`
	IsBookmarked   bool           `json:"is_bookmarked"`
}

// CreatePostInput holds all fields for creating a new post.
type CreatePostInput struct {
	AuthorID       uuid.UUID
	Text           string
	Visibility     string
	ContentType    string
	MediaIDs       []uuid.UUID
	Feeling        *string
	Activity       *string
	ActivityDetail *string
	RichText       json.RawMessage
	Poll           *CreatePollInput
	NoComments     bool
	NoLikes        bool
	LocationName   *string
	LocationLat    *float64
	LocationLng    *float64
	PostType       string
	AppOrigin      string
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
func extractMentions(text string) []string {
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var usernames []string
	for _, match := range matches {
		username := match[1]
		if !seen[username] {
			seen[username] = true
			usernames = append(usernames, username)
		}
	}
	return usernames
}

// reelMaxDurationSeconds is the maximum duration (inclusive) for a video to
// be auto-classified as a reel. Matches analytics-service threshold (90s).
const reelMaxDurationSeconds = 90

// validContentTypes is the allowed set for content_type.
var validContentTypes = map[string]bool{
	"post": true, "poll": true, "reel": true, "video": true,
}

// classifyVideoContentType returns "reel" or "video" based on duration.
func classifyVideoContentType(durationSeconds int) string {
	if durationSeconds > 0 && durationSeconds <= reelMaxDurationSeconds {
		return "reel"
	}
	return "video"
}

func (s *Service) CreatePost(ctx context.Context, input *CreatePostInput) (*postgres.Post, error) {
	contentType := input.ContentType
	if contentType == "" {
		contentType = "post"
	}

	// Validate content_type
	if !validContentTypes[contentType] {
		return nil, fmt.Errorf("invalid content_type %q: must be post, poll, reel, or video", contentType)
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

	// Extract @mentions from text (stored as usernames for now; could resolve to UUIDs)
	_ = extractMentions(input.Text)

	p := &postgres.Post{
		ID:             uuid.New(),
		AuthorID:       input.AuthorID,
		Text:           input.Text,
		Visibility:     input.Visibility,
		ContentType:    contentType,
		Feeling:        input.Feeling,
		Activity:       input.Activity,
		ActivityDetail: input.ActivityDetail,
		RichText:       input.RichText,
		NoComments:     input.NoComments,
		NoLikes:        input.NoLikes,
		Hashtags:       hashtags,
		LocationName:   input.LocationName,
		LocationLat:    input.LocationLat,
		LocationLng:    input.LocationLng,
		PostType:       postType,
		AppOrigin:      appOrigin,
		CreatedAt:      time.Now(),
	}

	// Attach media (resolve kind and duration from media_assets table)
	var maxDuration int
	for _, mediaID := range input.MediaIDs {
		kind := s.pgStore.ResolveMediaKind(ctx, mediaID)
		p.Media = append(p.Media, postgres.PostMedia{
			MediaID: mediaID,
			Kind:    kind,
		})
		if kind == "video" {
			dur := s.pgStore.ResolveMediaDuration(ctx, mediaID)
			if dur > maxDuration {
				maxDuration = dur
			}
		}
	}

	// Auto-classify: if caller sent default content_type but attached video,
	// derive reel vs video from duration
	if maxDuration > 0 && contentType == "post" {
		p.ContentType = classifyVideoContentType(maxDuration)
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

	if err := s.pgStore.CreatePost(ctx, p); err != nil {
		return nil, err
	}

	// Invalidate author content counts cache
	s.rdb.Del(ctx, fmt.Sprintf("post:author-counts:%s", input.AuthorID))

	// Fire-and-forget: Kafka + Redis publish in background
	go func() {
		bgCtx := context.Background()

		if s.producer != nil {
			if err := s.producer.PublishPostCreated(bgCtx, p.ID, p.AuthorID, p.Text, p.Visibility, p.ContentType, maxDuration); err != nil {
				log.Printf("Warning: failed to publish PostCreated event: %v", err)
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
	}()

	return p, nil
}

func (s *Service) GetPost(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*PostDetail, error) {
	p, err := s.pgStore.GetPost(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
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

	// Enrich with viewer-specific state
	if viewerID != nil {
		reaction, _ := s.scyllaStore.GetReaction(ctx, id, *viewerID)
		if reaction != "" {
			detail.ViewerReaction = &reaction
		}
		bookmarked, _ := s.pgStore.IsBookmarked(ctx, *viewerID, id)
		detail.IsBookmarked = bookmarked
	}

	return detail, nil
}

// GetPostsByAuthor returns paginated posts by a specific author.
func (s *Service) GetPostsByAuthor(ctx context.Context, authorID uuid.UUID, contentType string, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetPostsByAuthor(ctx, authorID, contentType, limit, cursor)
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
	if err := s.scyllaStore.React(ctx, postID, userID, reaction); err != nil {
		return err
	}

	// Fire-and-forget: Kafka + Redis publish in background
	go func() {
		bgCtx := context.Background()

		// Emit Kafka event
		if s.producer != nil {
			post, err := s.pgStore.GetPost(bgCtx, postID)
			if err == nil && post != nil {
				if err := s.producer.PublishPostReacted(bgCtx, postID, post.AuthorID, userID, reaction); err != nil {
					log.Printf("Warning: failed to publish PostReacted event: %v", err)
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
	return s.pgStore.AddBookmark(ctx, userID, postID)
}

func (s *Service) RemoveBookmark(ctx context.Context, userID, postID uuid.UUID) error {
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

	// Get post author for event
	post, _ := s.pgStore.GetPost(ctx, postID)
	var authorID uuid.UUID
	if post != nil {
		authorID = post.AuthorID
	}

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

	// Also publish legacy event for notification-service compatibility
	if s.producer != nil && result.IsSet && post != nil {
		go func() {
			if err := s.producer.PublishPostReacted(context.Background(), postID, authorID, userID, "like"); err != nil {
				log.Printf("Warning: failed to publish legacy PostReacted event: %v", err)
			}
		}()
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
	s.rdb.Set(ctx, shareKey, "1", 24*time.Hour)
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
				log.Printf("Warning: failed to publish share event: %v", err)
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
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment:%s", authorID), engagement.CommentLimitPerMin, time.Minute) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	comment, err := s.pgStore.CreateComment(ctx, postID, authorID, body)
	if err != nil {
		return nil, err
	}

	// Update Redis counter
	engKey := fmt.Sprintf("post:eng:%s", postID)
	s.rdb.HIncrBy(ctx, engKey, "comments", 1)

	// Get post author for event
	post, _ := s.pgStore.GetPost(ctx, postID)
	var postAuthorID uuid.UUID
	if post != nil {
		postAuthorID = post.AuthorID
	}

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

	// Also publish legacy event for notification-service
	if s.producer != nil && post != nil {
		go func() {
			if err := s.producer.PublishCommentCreated(context.Background(), comment.ID, postID, postAuthorID, authorID, body); err != nil {
				log.Printf("Warning: failed to publish legacy CommentCreated event: %v", err)
			}
		}()
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
func (s *Service) ListCommentsPG(ctx context.Context, postID uuid.UUID, cursor string, limit int) ([]postgres.Comment, string, error) {
	return s.pgStore.ListComments(ctx, postID, cursor, limit)
}

// GetCommentsAroundPG returns comments surrounding a target comment for deep-link navigation.
func (s *Service) GetCommentsAroundPG(ctx context.Context, postID, commentID uuid.UUID, limit int) ([]postgres.Comment, error) {
	return s.pgStore.GetCommentsAround(ctx, postID, commentID, limit)
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

// ============================================================
// Multi-Reactions
// ============================================================

// ReactionToggleResult is the response for the multi-reaction toggle API.
type ReactionToggleResult struct {
	ReactionType string                `json:"reaction_type"`
	IsSet        bool                  `json:"is_set"`
	Counts       *postgres.ReactionCounts `json:"counts"`
}

// ToggleReaction sets, changes, or removes a reaction on a post.
func (s *Service) ToggleReaction(ctx context.Context, postID, userID uuid.UUID, reactionType string) (*ReactionToggleResult, error) {
	if !postgres.ValidReactionTypes[reactionType] {
		return nil, fmt.Errorf("INVALID_REACTION_TYPE")
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

	// Publish event for notifications
	if s.producer != nil && isSet {
		go func() {
			post, err := s.pgStore.GetPost(context.Background(), postID)
			if err == nil && post != nil && post.AuthorID != userID {
				s.producer.PublishPostReacted(context.Background(), postID, post.AuthorID, userID, newType)
			}
		}()
	}

	return &ReactionToggleResult{
		ReactionType: newType,
		IsSet:        isSet,
		Counts:       counts,
	}, nil
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
func (s *Service) GetPostsByHashtag(ctx context.Context, hashtag string, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetPostsByHashtag(ctx, hashtag, limit, cursor)
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
