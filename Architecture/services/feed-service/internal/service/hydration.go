package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/google/uuid"
)

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
}

// HydratePosts calls post-service's batch endpoint to enrich timeline entries
// with full post details (text, media, counts, etc.).
func (s *Service) HydratePosts(ctx context.Context, items []FeedItem, viewerID uuid.UUID) ([]HydratedPost, error) {
	if len(items) == 0 {
		return []HydratedPost{}, nil
	}

	// 1. Collect unique post IDs
	seen := make(map[uuid.UUID]bool, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item.PostID] {
			seen[item.PostID] = true
			ids = append(ids, item.PostID.String())
		}
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

	// 5. Merge: preserve feed ordering, attach ranking score, skip missing/duplicate posts
	hydrated := make([]HydratedPost, 0, len(items))
	emitted := make(map[uuid.UUID]bool, len(items))
	for _, item := range items {
		if emitted[item.PostID] {
			continue
		}
		post, ok := envelope.Data[item.PostID.String()]
		if !ok {
			// Post was deleted or unavailable — skip it
			continue
		}
		post.Score = item.Score
		hydrated = append(hydrated, post)
		emitted[item.PostID] = true
	}

	return hydrated, nil
}
