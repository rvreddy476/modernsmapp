package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// Audit HF3: hydrated posts cached per-viewer (viewer state — viewer
	// reaction, bookmark — is part of the response) for a short window
	// so a typical feed scroll doesn't keep re-hitting post-service.
	// TTL is short because counts go stale fast.
	hydrationCacheTTL = 5 * time.Minute
)

func hydrationCacheKey(viewerID, postID uuid.UUID) string {
	return fmt.Sprintf("feed:hydrate:%s:%s", viewerID, postID)
}

// HydratedPost is the fully enriched post returned to the frontend.
type HydratedPost struct {
	ID             uuid.UUID       `json:"id"`
	AuthorID       uuid.UUID       `json:"author_id"`
	Text           string          `json:"text"`
	Visibility     string          `json:"visibility"`
	ContentType    string          `json:"content_type"`
	IsPinned       bool            `json:"is_pinned"`
	Feeling        *string         `json:"feeling,omitempty"`
	Activity       *string         `json:"activity,omitempty"`
	ActivityDetail *string         `json:"activity_detail,omitempty"`
	CoverMediaID   *uuid.UUID      `json:"cover_media_id,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	Media          json.RawMessage `json:"media,omitempty"`
	Counts         json.RawMessage `json:"counts,omitempty"`
	// ViewCount is the display view count from analytics-service's
	// Redis counter (post:views:{id} → display). Enriched at hydration
	// time; `counts` carries likes/comments/shares only.
	ViewCount      int64           `json:"view_count"`
	ViewerReaction *string         `json:"viewer_reaction,omitempty"`
	IsBookmarked   bool            `json:"is_bookmarked"`
	Poll           json.RawMessage `json:"poll,omitempty"`
	Location       *string         `json:"location,omitempty"`
	Hashtags       json.RawMessage `json:"hashtags,omitempty"`
	PostType        string          `json:"post_type,omitempty"`
	AppOrigin       string          `json:"app_origin,omitempty"`
	ShareToPostbook bool            `json:"share_to_postbook"`
	Score           float64         `json:"score,omitempty"`
	VideoMetadata   json.RawMessage `json:"video_metadata,omitempty"`
	RichText        json.RawMessage `json:"rich_text,omitempty"`

	// Repost metadata — populated when this entry is a repost in someone's timeline
	IsRepost        bool       `json:"is_repost,omitempty"`
	RepostedBy      *uuid.UUID `json:"reposted_by,omitempty"`
	FeedContentType string     `json:"feed_content_type,omitempty"` // "post", "repost", "reel", etc.
}

// HydratePosts calls post-service's batch endpoint to enrich timeline entries
// with full post details (text, media, counts, etc.).
//
// Audit HF3: a Redis MGET-then-MSET cache fronts the post-service
// round trip. Cache key is per-(viewer, post) because the response
// embeds viewer state (viewer_reaction, is_bookmarked). TTL is
// intentionally short (5 min) so engagement counts don't go stale
// long enough to be misleading. On any Redis miss/error we fall
// through to the original batch fetch — best-effort cache.
func (s *Service) HydratePosts(ctx context.Context, items []FeedItem, viewerID uuid.UUID) ([]HydratedPost, error) {
	if len(items) == 0 {
		return []HydratedPost{}, nil
	}

	// 1. Collect unique post IDs
	seen := make(map[uuid.UUID]bool, len(items))
	uniquePostIDs := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		if !seen[item.PostID] {
			seen[item.PostID] = true
			uniquePostIDs = append(uniquePostIDs, item.PostID)
		}
	}

	// 1a. Check the per-viewer Redis cache for prebuilt hydrated rows.
	cached := s.fetchHydratedCache(ctx, viewerID, uniquePostIDs)

	// Build the list of post IDs we still need from post-service.
	ids := make([]string, 0, len(uniquePostIDs))
	for _, pid := range uniquePostIDs {
		if _, ok := cached[pid]; !ok {
			ids = append(ids, pid.String())
		}
	}

	envelopeData := make(map[string]HydratedPost, len(uniquePostIDs))
	for pid, h := range cached {
		envelopeData[pid.String()] = h
	}

	if len(ids) == 0 {
		// Entire batch served from cache — skip the HTTP call.
		merged := s.mergeHydratedItems(items, envelopeData, nil)
		s.enrichViewCounts(ctx, merged)
		return merged, nil
	}

	// 2. Build request body
	reqBody, err := json.Marshal(map[string]interface{}{
		"ids":       ids,
		"viewer_id": viewerID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal batch request: %w", err)
	}

	// 3. Call post-service batch endpoint
	url := fmt.Sprintf("%s/v1/posts/batch", s.postServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", viewerID.String())
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service batch request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read batch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("post-service returned %d: %s", resp.StatusCode, string(body))
	}

	// 4. Parse response: {"data": {"uuid1": {...}, "uuid2": {...}}}
	var envelope struct {
		Data map[string]HydratedPost `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal batch response: %w", err)
	}

	// 4a. Merge the fresh response with anything we got from cache.
	for k, v := range envelope.Data {
		envelopeData[k] = v
	}

	// 4b. Populate cache for fresh entries so subsequent feed pages
	// reuse them. Fire-and-forget; never block the response.
	s.storeHydratedCache(viewerID, envelope.Data)

	merged := s.mergeHydratedItems(items, envelopeData, nil)
	s.enrichViewCounts(ctx, merged)
	return merged, nil
}

// enrichViewCounts fills HydratedPost.ViewCount from the shared Redis
// view counter (post:views:{id} hash, "display" field) that
// analytics-service maintains. One pipelined round trip for the whole
// page; best-effort — on any Redis error the counts stay 0 rather than
// failing the feed. View counts intentionally aren't part of the
// hydration cache blob, so this always reflects the live counter.
func (s *Service) enrichViewCounts(ctx context.Context, posts []HydratedPost) {
	if s.rdb == nil || len(posts) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(posts))
	for i, p := range posts {
		cmds[i] = pipe.HGet(ctx, "post:views:"+p.ID.String(), "display")
	}
	// Exec returns redis.Nil when any key/field is missing — expected for
	// posts with no views yet. Per-command parsing below handles it.
	_, _ = pipe.Exec(ctx)
	for i := range posts {
		if n, err := cmds[i].Int64(); err == nil {
			posts[i].ViewCount = n
		}
	}
}

// mergeHydratedItems flattens (feed item × hydrated post) into the
// ordered response. Reposts of an already-seen original are kept (a
// repost is a distinct feed event — "User X reposted this"); other
// duplicates are dropped. The optional `score` map overrides item.Score
// when non-nil — kept around for future re-ranking, currently unused.
func (s *Service) mergeHydratedItems(items []FeedItem, envelopeData map[string]HydratedPost, score map[uuid.UUID]float64) []HydratedPost {
	hydrated := make([]HydratedPost, 0, len(items))
	emitted := make(map[uuid.UUID]bool, len(items))
	for _, item := range items {
		isRepost := item.ContentType == "repost"
		if emitted[item.PostID] && !isRepost {
			continue
		}
		post, ok := envelopeData[item.PostID.String()]
		if !ok {
			// Post was deleted, hidden, or filtered by post-service's
			// visibility gate (audit CF1) — skip it.
			continue
		}
		post.Score = item.Score
		if score != nil {
			if v, ok := score[item.PostID]; ok {
				post.Score = v
			}
		}
		post.FeedContentType = item.ContentType
		if isRepost {
			post.IsRepost = true
			authorID := item.AuthorID
			post.RepostedBy = &authorID
		}
		hydrated = append(hydrated, post)
		emitted[item.PostID] = true
	}
	return hydrated
}

// fetchHydratedCache reads the per-(viewer, post) cache via Redis MGET.
// Missing / unparseable entries are dropped silently. Returns an empty
// map (never nil) so callers can range over it.
func (s *Service) fetchHydratedCache(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) map[uuid.UUID]HydratedPost {
	result := make(map[uuid.UUID]HydratedPost, len(postIDs))
	if s.rdb == nil || len(postIDs) == 0 {
		return result
	}
	keys := make([]string, len(postIDs))
	for i, pid := range postIDs {
		keys[i] = hydrationCacheKey(viewerID, pid)
	}
	mgetCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	vals, err := s.rdb.MGet(mgetCtx, keys...).Result()
	if err != nil {
		// Best-effort cache; never break the request.
		log.Printf("[feed-hydrate] cache MGET failed: %v", err)
		return result
	}
	for i, raw := range vals {
		s, ok := raw.(string)
		if !ok || s == "" {
			continue
		}
		var hp HydratedPost
		if err := json.Unmarshal([]byte(s), &hp); err != nil {
			continue
		}
		result[postIDs[i]] = hp
	}
	return result
}

// storeHydratedCache writes fresh hydrated rows back into Redis.
// Asynchronous so the response isn't held up by Redis write latency.
func (s *Service) storeHydratedCache(viewerID uuid.UUID, fresh map[string]HydratedPost) {
	if s.rdb == nil || len(fresh) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		pipe := s.rdb.Pipeline()
		for idStr, hp := range fresh {
			pid, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			data, err := json.Marshal(hp)
			if err != nil {
				continue
			}
			pipe.Set(ctx, hydrationCacheKey(viewerID, pid), data, hydrationCacheTTL)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("[feed-hydrate] cache SET failed: %v", err)
		}
	}()
}
