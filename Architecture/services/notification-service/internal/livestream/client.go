// Package livestream is the internal client for live-service-v2's reminder
// contract (Live in PostTube and Reels, 2 Oct 2026).
//
// A viewer who taps "Notify me" on a scheduled stream is stored by
// live-service-v2. When the stream goes live this service asks, over the
// internal route only (X-Internal-Service-Key), who those people are, for
// the single purpose of telling them. The ids never ride the event.
package livestream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func New(baseURL, internalKey string) *Client {
	return &Client{
		baseURL:     baseURL,
		internalKey: internalKey,
		http:        &http.Client{Timeout: 10 * time.Second},
	}
}

// Page is one page of reminder holders. NextAfter is the route's own token
// and is passed back verbatim: this client never interprets it.
type Page struct {
	IDs       []uuid.UUID
	NextAfter string
	HasMore   bool
}

// ReminderUserIDs fetches one page of the users who set a reminder on a
// stream. `after` is empty for the first page.
//
// A 404 is an error, not an empty page: an empty page ends the reminder
// walk for good, so "the route is missing" or "the stream is unknown" must
// stay retryable rather than silently telling nobody.
func (c *Client) ReminderUserIDs(ctx context.Context, streamID uuid.UUID, after string, limit int) (*Page, error) {
	q := url.Values{}
	q.Set("after", after)
	q.Set("limit", fmt.Sprintf("%d", limit))
	target := fmt.Sprintf("%s/v1/livestream/internal/streams/%s/reminders?%s", c.baseURL, streamID, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("live-service-v2 returned %d for stream %s reminders", resp.StatusCode, streamID)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var env struct {
		Data struct {
			UserIDs   []string `json:"user_ids"`
			NextAfter string   `json:"next_after"`
			HasMore   bool     `json:"has_more"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("reminders decode: %w", err)
	}
	page := &Page{NextAfter: env.Data.NextAfter, HasMore: env.Data.HasMore}
	for _, raw := range env.Data.UserIDs {
		if id, err := uuid.Parse(raw); err == nil {
			page.IDs = append(page.IDs, id)
		}
	}
	return page, nil
}
