package postclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// PostIDsByMedia asks post-service which (non-deleted) posts attach a media
// asset: GET /v1/internal/posts/by-media/:mediaId (2026-09-27). The
// has_subtitles consumer uses it to find the post documents a
// MediaSubtitlesChanged snapshot belongs to.
//
// FAILURES ARE ERRORS, as for the listings: an unanswered lookup returned as
// "no posts" would drop the snapshot and leave the flag wrong until the next
// reindex. The consumer retries (and dead-letters) instead.
func (c *Client) PostIDsByMedia(ctx context.Context, mediaID string) ([]string, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	path := "/v1/internal/posts/by-media/" + url.PathEscape(mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	httpClient := c.http
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("postclient: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("postclient: read %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("postclient: %s returned %d: %s", path, resp.StatusCode, truncate(body))
	}
	var parsed struct {
		PostIDs *[]string `json:"post_ids"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("postclient: decode %s: %w", path, err)
	}
	if parsed.PostIDs == nil {
		// A 200 without the field is not "no posts"; it is a response this
		// client does not understand.
		return nil, fmt.Errorf("postclient: %s: response has no post_ids", path)
	}
	return *parsed.PostIDs, nil
}
