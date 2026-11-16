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

// postBatchMax is the most ids post-service's POST /v1/posts/batch accepts
// in one request (post-service/internal/http/post_reads_handler.go,
// "Maximum 100 IDs per request", 400 above it).
const postBatchMax = 100

// postBatch asks post-service for the posts with these ids AS the viewer
// (post-service applies its own read rules for that person: private,
// deleted and still-processing posts of other people are left out) and
// returns them keyed by id. Ids post-service leaves out are simply absent.
//
// More than postBatchMax ids are sent in groups (finding feed-33). Until 16
// Nov 2026 every uncached id went in one request: GET /v1/feed/delta reads
// up to 201 timeline rows, so a viewer with more than 100 new posts since
// their anchor got 400 from post-service and 500 from the delta route on
// every poll. The groups are sent one after another; any failed group fails
// the whole call, because a partial answer would read as "the missing posts
// are not visible" and every caller filters on exactly that.
func (s *Service) postBatch(ctx context.Context, viewerID uuid.UUID, ids []string) (map[string]HydratedPost, error) {
	out := make(map[string]HydratedPost, len(ids))
	for start := 0; start < len(ids); start += postBatchMax {
		end := min(start+postBatchMax, len(ids))
		group, err := s.postBatchOnce(ctx, viewerID, ids[start:end])
		if err != nil {
			return nil, err
		}
		for k, v := range group {
			out[k] = v
		}
	}
	return out, nil
}

// postBatchOnce is one request of at most postBatchMax ids.
func (s *Service) postBatchOnce(ctx context.Context, viewerID uuid.UUID, ids []string) (map[string]HydratedPost, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"ids":       ids,
		"viewer_id": viewerID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal batch request: %w", err)
	}

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

	resp, err := s.postFor(ctx).Do(req)
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

	// {"data": {"uuid1": {...}, "uuid2": {...}}}
	var envelope struct {
		Data map[string]HydratedPost `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal batch response: %w", err)
	}
	return envelope.Data, nil
}
