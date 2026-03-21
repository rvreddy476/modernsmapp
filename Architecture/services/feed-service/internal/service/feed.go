package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/atpost/feed-service/internal/ranking"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	scyllaStore       *scylla.TimelineStore
	pgStore           *postgres.MetaStore
	rdb               *redis.Client
	graphURL          string
	postServiceURL    string
	profileServiceURL string
	ranker            *ranking.Ranker
}

func New(scylla *scylla.TimelineStore, pg *postgres.MetaStore, rdb *redis.Client) *Service {
	graphURL := os.Getenv("GRAPH_SERVICE_URL")
	if graphURL == "" {
		graphURL = "http://graph-service:8083"
	}
	postServiceURL := os.Getenv("POST_SERVICE_URL")
	if postServiceURL == "" {
		postServiceURL = "http://post-service:8084"
	}
	profileServiceURL := os.Getenv("PROFILE_SERVICE_URL")
	if profileServiceURL == "" {
		profileServiceURL = "http://identity-profile:8098"
	}
	return &Service{
		scyllaStore:       scylla,
		pgStore:           pg,
		rdb:               rdb,
		graphURL:          graphURL,
		postServiceURL:    postServiceURL,
		profileServiceURL: profileServiceURL,
	}
}

// SetRanker injects the ranking middleware after construction.
func (s *Service) SetRanker(r *ranking.Ranker) {
	s.ranker = r
}

// FeedItem is the API response model
type FeedItem struct {
	PostID      uuid.UUID `json:"post_id"`
	AuthorID    uuid.UUID `json:"author_id"`
	CreatedAt   time.Time `json:"created_at"`
	Score       float64   `json:"score,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
}

func (s *Service) GetHomeFeed(ctx context.Context, userID uuid.UUID, limit int, feedMode string, excludeSelf bool, circleOnly bool) ([]FeedItem, error) {
	// Over-fetch for ranking headroom: 5x when ranked, normal otherwise
	// Also over-fetch slightly when excluding self to compensate for filtered items
	fetchLimit := limit
	if feedMode == "ranked" || feedMode == "shadow" {
		fetchLimit = limit * 5
		if fetchLimit > 500 {
			fetchLimit = 500
		}
	} else if excludeSelf {
		fetchLimit = limit + 10 // extra headroom for own posts removed
	}

	// 1. Get Home Timeline candidates
	items, err := s.scyllaStore.GetHomeTimeline(ctx, userID, fetchLimit)
	if err != nil {
		return nil, err
	}

	// Convert to FeedItems, optionally filtering out viewer's own posts
	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		if excludeSelf && item.AuthorID == userID {
			continue
		}
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}

	// Filter out blocked/muted authors
	var blockedSet map[uuid.UUID]struct{}
	blockedMuted, bmErr := s.getBlockedAndMuted(ctx, userID)
	if bmErr == nil && len(blockedMuted) > 0 {
		blockedSet = make(map[uuid.UUID]struct{}, len(blockedMuted))
		for _, id := range blockedMuted {
			blockedSet[id] = struct{}{}
		}
		filtered := candidates[:0]
		for _, c := range candidates {
			if _, blocked := blockedSet[c.AuthorID]; !blocked {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}

	// Filter to circle-only (friends) if requested
	if circleOnly && len(candidates) > 0 {
		friends, err := s.fetchCircleMembers(ctx, userID)
		if err != nil {
			log.Printf("circle_only filter: failed to fetch friends for %s: %v", userID, err)
		} else if len(friends) > 0 {
			friendSet := make(map[uuid.UUID]struct{}, len(friends))
			for _, fid := range friends {
				friendSet[fid] = struct{}{}
			}
			filtered := candidates[:0]
			for _, c := range candidates {
				if _, ok := friendSet[c.AuthorID]; ok {
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
		} else {
			candidates = nil
		}
	}

	// Cold-start fallback: if timeline is empty, fetch recent public posts (only for ranked/discovery feeds)
	if len(candidates) == 0 && feedMode == "ranked" {
		log.Printf("Cold-start fallback triggered for user %s (empty timeline), fetching from %s", userID, s.postServiceURL)
		coldItems, err := s.getRecentPublicPosts(ctx, limit*2)
		if err != nil {
			log.Printf("Cold-start fallback failed: %v", err)
		} else {
			log.Printf("Cold-start fallback returned %d posts", len(coldItems))
			for _, item := range coldItems {
				if excludeSelf && item.AuthorID == userID {
					continue
				}
				if blockedSet != nil {
					if _, blocked := blockedSet[item.AuthorID]; blocked {
						continue
					}
				}
				candidates = append(candidates, item)
			}
		}
	}

	// 2. Apply ranking if enabled
	if (feedMode == "ranked" || feedMode == "shadow") && s.ranker != nil && len(candidates) > 0 {
		rc := feedItemsToCandidates(candidates)
		rankedCandidates, err := s.ranker.Rank(ctx, userID, rc, limit)
		if err != nil {
			// Circuit breaker or error: fallback to chronological
			log.Printf("Ranking failed, falling back to chronological: %v", err)
		} else if feedMode == "ranked" {
			candidates = candidatesToFeedItems(rankedCandidates)
		}
		// In shadow mode: log ranked order but return chronological
		if feedMode == "shadow" {
			log.Printf("Shadow mode: ranked %d candidates for user %s", len(rankedCandidates), userID)
		}
	}

	// 3. Trim to requested limit
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	return candidates, nil
}

// GetFlickFeed returns the user's flick-only timeline (flick + legacy reel), scored by recency.
func (s *Service) GetFlickFeed(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	items, err := s.scyllaStore.GetHomeTimelineByContentTypes(ctx, userID, []string{"flick", "reel"}, limit*3)
	if err != nil {
		return nil, err
	}

	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}

	scored := scoreReels(candidates)
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// GetLongVideoFeed returns the user's long-video-only timeline (long_video + legacy video).
func (s *Service) GetLongVideoFeed(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	items, err := s.scyllaStore.GetHomeTimelineByContentTypes(ctx, userID, []string{"long_video", "video"}, limit*3)
	if err != nil {
		return nil, err
	}

	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}

	if s.ranker != nil && len(candidates) > 0 {
		rc := feedItemsToCandidates(candidates)
		ranked, err := s.ranker.Rank(ctx, userID, rc, limit)
		if err != nil {
			log.Printf("Long video feed ranking failed, fallback to chronological: %v", err)
		} else {
			candidates = candidatesToFeedItems(ranked)
		}
	}

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

// GetReelFeed returns the user's reel-only timeline, scored by recency.
// Acts as an alias for GetFlickFeed (backward compat).
func (s *Service) GetReelFeed(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	items, err := s.scyllaStore.GetHomeTimelineByContentTypes(ctx, userID, []string{"flick", "reel"}, limit*3)
	if err != nil {
		return nil, err
	}

	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}

	// Reels use recency-biased scoring
	scored := scoreReels(candidates)

	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// GetVideoFeed returns the user's long-video-only timeline.
// Aliases to GetLongVideoFeed (backward compat).
func (s *Service) GetVideoFeed(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	items, err := s.scyllaStore.GetHomeTimelineByContentTypes(ctx, userID, []string{"long_video", "video"}, limit*3)
	if err != nil {
		return nil, err
	}

	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}

	// Long-video feed uses the main ranker with full signals
	if s.ranker != nil && len(candidates) > 0 {
		rc := feedItemsToCandidates(candidates)
		ranked, err := s.ranker.Rank(ctx, userID, rc, limit)
		if err != nil {
			log.Printf("Video feed ranking failed, fallback to chronological: %v", err)
		} else {
			candidates = candidatesToFeedItems(ranked)
		}
	}

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

// scoreReels applies a pure recency score to reel candidates.
// score = 1.0 / (1.0 + ageMinutes * 0.01)
// This gives strong preference to content < 2 hours old without
// completely suppressing older content.
func scoreReels(items []FeedItem) []FeedItem {
	now := time.Now()
	scored := make([]FeedItem, len(items))
	copy(scored, items)
	for i := range scored {
		ageMin := now.Sub(scored[i].CreatedAt).Minutes()
		scored[i].Score = 1.0 / (1.0 + ageMin*0.01)
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored
}

// GetUserFeedMode returns the user's saved feed mode preference.
func (s *Service) GetUserFeedMode(ctx context.Context, userID uuid.UUID) string {
	// Check Redis cache first
	cached, err := s.rdb.Get(ctx, fmt.Sprintf("feed:pref:%s", userID.String())).Result()
	if err == nil && cached != "" {
		return cached
	}

	// Check Postgres
	mode, err := s.pgStore.GetFeedMode(ctx, userID)
	if err != nil || mode == "" {
		return "chronological"
	}

	// Cache for 5 minutes
	s.rdb.Set(ctx, fmt.Sprintf("feed:pref:%s", userID.String()), mode, 5*time.Minute)
	return mode
}

// SetUserFeedMode persists the user's feed mode preference.
func (s *Service) SetUserFeedMode(ctx context.Context, userID uuid.UUID, mode string) error {
	if err := s.pgStore.SetFeedMode(ctx, userID, mode); err != nil {
		return err
	}
	// Update cache
	s.rdb.Set(ctx, fmt.Sprintf("feed:pref:%s", userID.String()), mode, 5*time.Minute)
	return nil
}

// RecordSignal handles "see_less" / "see_more" user signals.
func (s *Service) RecordSignal(ctx context.Context, userID, postID uuid.UUID, signal string) error {
	return s.pgStore.RecordSignal(ctx, userID, postID, signal)
}

// DebugFeed returns full score breakdown for the user's feed candidates.
func (s *Service) DebugFeed(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	items, err := s.scyllaStore.GetHomeTimeline(ctx, userID, 100)
	if err != nil {
		return nil, err
	}

	candidates := make([]FeedItem, len(items))
	for i, item := range items {
		candidates[i] = FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		}
	}

	if s.ranker == nil {
		return map[string]interface{}{
			"candidates": candidates,
			"mode":       "no_ranker",
		}, nil
	}

	rc := feedItemsToCandidates(candidates)
	rankedCandidates, err := s.ranker.Rank(ctx, userID, rc, 20)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"candidates_count": len(candidates),
		"ranked":           candidatesToFeedItems(rankedCandidates),
	}, nil
}

func (s *Service) FanoutPost(ctx context.Context, postID, authorID uuid.UUID, createdAt time.Time, contentType string) error {
	// 1. Always add to Author Timeline
	if err := s.scyllaStore.AddToAuthorTimeline(ctx, authorID, postID, createdAt, contentType); err != nil {
		return err
	}

	// 2. Also add to Author's own Home Timeline (so they see their own posts)
	if err := s.scyllaStore.AddToHomeTimeline(ctx, authorID, postID, authorID, createdAt, contentType); err != nil {
		log.Printf("Failed to push to author's own home timeline: %v", err)
	}

	// 3. Check Celeb Status
	isCeleb, err := s.pgStore.IsCeleb(ctx, authorID)
	if err != nil {
		return err
	}

	if isCeleb {
		// Stop here (Pull model for celebs)
		return nil
	}

	// 4. Collect unique recipient IDs from followers + circle members
	recipientSet := make(map[uuid.UUID]struct{})

	// 4a. Fetch Followers
	followerIDs, err := s.fetchFollowers(ctx, authorID)
	if err != nil {
		log.Printf("Failed to fetch followers for fanout: %v", err)
	} else {
		for _, id := range followerIDs {
			recipientSet[id] = struct{}{}
		}
	}

	// 4b. Fetch Circle Members (friends from profile-service)
	friendIDs, err := s.fetchCircleMembers(ctx, authorID)
	if err != nil {
		log.Printf("Failed to fetch circle members for fanout: %v", err)
	} else {
		for _, id := range friendIDs {
			recipientSet[id] = struct{}{}
		}
	}

	// 5. Push to all recipients' Home Timelines
	for recipientID := range recipientSet {
		if recipientID == authorID {
			continue // already pushed above
		}
		if err := s.scyllaStore.AddToHomeTimeline(ctx, recipientID, postID, authorID, createdAt, contentType); err != nil {
			log.Printf("Failed to push to timeline for user %s: %v", recipientID, err)
		}
	}

	return nil
}

// getBlockedAndMuted calls graph-service to get the union of blocked and muted user IDs for userID.
func (s *Service) getBlockedAndMuted(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	url := fmt.Sprintf("%s/v1/graph/blocked-and-muted?user_id=%s", s.graphURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		UserIDs []uuid.UUID `json:"user_ids"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.UserIDs, nil
}

// fetchFollowers calls graph-service to get the follower list for a user.
// It paginates through all results (max 100 per page).
func (s *Service) fetchFollowers(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var allFollowers []uuid.UUID
	offset := 0
	limit := 100

	for {
		url := fmt.Sprintf("%s/v1/graph/followers/%s?limit=%d&offset=%d", s.graphURL, userID.String(), limit, offset)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, string(body))
		}

		var envelope struct {
			Data []uuid.UUID `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("unmarshal followers: %w", err)
		}

		allFollowers = append(allFollowers, envelope.Data...)

		// If we got fewer than limit, we've fetched all pages
		if len(envelope.Data) < limit {
			break
		}
		offset += limit
	}

	return allFollowers, nil
}

// fetchCircleMembers calls profile-service to get the friends list for a user.
func (s *Service) fetchCircleMembers(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var allFriends []uuid.UUID
	offset := 0
	limit := 100

	for {
		url := fmt.Sprintf("%s/v1/profiles/%s/friends?limit=%d&offset=%d", s.profileServiceURL, userID.String(), limit, offset)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("profile-service request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("profile-service returned %d: %s", resp.StatusCode, string(body))
		}

		var envelope struct {
			Data struct {
				Items []struct {
					UserID string `json:"user_id"`
				} `json:"items"`
				Meta struct {
					HasNext bool `json:"has_next"`
				} `json:"meta"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("unmarshal friends: %w", err)
		}

		for _, item := range envelope.Data.Items {
			id, err := uuid.Parse(item.UserID)
			if err != nil {
				continue
			}
			allFriends = append(allFriends, id)
		}

		if !envelope.Data.Meta.HasNext || len(envelope.Data.Items) < limit {
			break
		}
		offset += limit
	}

	return allFriends, nil
}

// getRecentPublicPosts fetches recent public posts from post-service as a cold-start fallback
// for users with an empty home timeline (new users, no follows, etc.).
func (s *Service) getRecentPublicPosts(ctx context.Context, limit int) ([]FeedItem, error) {
	url := fmt.Sprintf("%s/v1/posts/recent?limit=%d", s.postServiceURL, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Service-Key", os.Getenv("INTERNAL_SERVICE_KEY"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("post-service returned %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Data []struct {
			ID          string    `json:"id"`
			AuthorID    string    `json:"author_id"`
			CreatedAt   time.Time `json:"created_at"`
			ContentType string    `json:"content_type"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	items := make([]FeedItem, 0, len(envelope.Data))
	for _, p := range envelope.Data {
		postID, err := uuid.Parse(p.ID)
		if err != nil {
			continue
		}
		authorID, err := uuid.Parse(p.AuthorID)
		if err != nil {
			continue
		}
		ct := p.ContentType
		if ct == "" {
			ct = "post"
		}
		items = append(items, FeedItem{
			PostID:      postID,
			AuthorID:    authorID,
			CreatedAt:   p.CreatedAt,
			ContentType: ct,
		})
	}
	return items, nil
}

// feedItemsToCandidates converts service FeedItems to ranking Candidates.
func feedItemsToCandidates(items []FeedItem) []ranking.Candidate {
	out := make([]ranking.Candidate, len(items))
	for i, item := range items {
		out[i] = ranking.Candidate{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			Score:       item.Score,
			ContentType: item.ContentType,
		}
	}
	return out
}

// FanoutRepost distributes a repost into the reposter's followers' home timelines.
// The feed entry points to the original post but is attributed to the reposter.
func (s *Service) FanoutRepost(ctx context.Context, repostID, originalPostID, reposterID uuid.UUID, createdAt time.Time, visibility string) error {
	// 1. Add to reposter's own home timeline so they see it
	if err := s.scyllaStore.AddToHomeTimeline(ctx, reposterID, originalPostID, reposterID, createdAt, "repost"); err != nil {
		log.Printf("Failed to push repost to reposter's home timeline: %v", err)
	}

	// 2. Check celeb status — if celeb, stop (pull model)
	isCeleb, err := s.pgStore.IsCeleb(ctx, reposterID)
	if err != nil {
		return err
	}
	if isCeleb {
		return nil
	}

	// 3. Only fan out public/default visibility reposts
	if visibility == "private" {
		return nil
	}

	// 4. Collect followers + friends
	recipientSet := make(map[uuid.UUID]struct{})

	followerIDs, err := s.fetchFollowers(ctx, reposterID)
	if err != nil {
		log.Printf("Failed to fetch followers for repost fanout: %v", err)
	} else {
		for _, id := range followerIDs {
			recipientSet[id] = struct{}{}
		}
	}

	friendIDs, err := s.fetchCircleMembers(ctx, reposterID)
	if err != nil {
		log.Printf("Failed to fetch circle members for repost fanout: %v", err)
	} else {
		for _, id := range friendIDs {
			recipientSet[id] = struct{}{}
		}
	}

	// 5. Push to all recipients' home timelines
	for recipientID := range recipientSet {
		if recipientID == reposterID {
			continue
		}
		if err := s.scyllaStore.AddToHomeTimeline(ctx, recipientID, originalPostID, reposterID, createdAt, "repost"); err != nil {
			log.Printf("Failed to push repost to timeline for user %s: %v", recipientID, err)
		}
	}

	return nil
}

// UndoRepostFanout marks a repost as deleted in Redis so feed hydration skips it.
func (s *Service) UndoRepostFanout(ctx context.Context, repostID, originalPostID uuid.UUID) error {
	// Mark repost as deleted in Redis for 24h — feed hydration will filter it out
	deletedKey := fmt.Sprintf("repost:deleted:%s", repostID)
	if err := s.rdb.Set(ctx, deletedKey, "1", 24*time.Hour).Err(); err != nil {
		log.Printf("Failed to mark repost deleted in Redis: %v", err)
	}
	return nil
}

// candidatesToFeedItems converts ranking Candidates back to service FeedItems.
func candidatesToFeedItems(candidates []ranking.Candidate) []FeedItem {
	out := make([]FeedItem, len(candidates))
	for i, c := range candidates {
		out[i] = FeedItem{
			PostID:      c.PostID,
			AuthorID:    c.AuthorID,
			CreatedAt:   c.CreatedAt,
			Score:       c.Score,
			ContentType: c.ContentType,
		}
	}
	return out
}
