package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/facebook-like/feed-service/internal/ranking"
	"github.com/facebook-like/feed-service/internal/store/postgres"
	"github.com/facebook-like/feed-service/internal/store/scylla"
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
	PostID    uuid.UUID `json:"post_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
	Score     float64   `json:"score,omitempty"`
}

func (s *Service) GetHomeFeed(ctx context.Context, userID uuid.UUID, limit int, feedMode string, excludeSelf bool) ([]FeedItem, error) {
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
			PostID:    item.PostID,
			AuthorID:  item.AuthorID,
			CreatedAt: item.CreatedAt,
		})
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
			PostID:    item.PostID,
			AuthorID:  item.AuthorID,
			CreatedAt: item.CreatedAt,
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

func (s *Service) FanoutPost(ctx context.Context, postID, authorID uuid.UUID, createdAt time.Time) error {
	// 1. Always add to Author Timeline
	if err := s.scyllaStore.AddToAuthorTimeline(ctx, authorID, postID, createdAt); err != nil {
		return err
	}

	// 2. Also add to Author's own Home Timeline (so they see their own posts)
	if err := s.scyllaStore.AddToHomeTimeline(ctx, authorID, postID, authorID, createdAt); err != nil {
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
		if err := s.scyllaStore.AddToHomeTimeline(ctx, recipientID, postID, authorID, createdAt); err != nil {
			log.Printf("Failed to push to timeline for user %s: %v", recipientID, err)
		}
	}

	return nil
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

// feedItemsToCandidates converts service FeedItems to ranking Candidates.
func feedItemsToCandidates(items []FeedItem) []ranking.Candidate {
	out := make([]ranking.Candidate, len(items))
	for i, item := range items {
		out[i] = ranking.Candidate{
			PostID:    item.PostID,
			AuthorID:  item.AuthorID,
			CreatedAt: item.CreatedAt,
			Score:     item.Score,
		}
	}
	return out
}

// candidatesToFeedItems converts ranking Candidates back to service FeedItems.
func candidatesToFeedItems(candidates []ranking.Candidate) []FeedItem {
	out := make([]FeedItem, len(candidates))
	for i, c := range candidates {
		out[i] = FeedItem{
			PostID:    c.PostID,
			AuthorID:  c.AuthorID,
			CreatedAt: c.CreatedAt,
			Score:     c.Score,
		}
	}
	return out
}
