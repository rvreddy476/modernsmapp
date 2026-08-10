// Package subscribers is the internal client for user-service's
// channel-subscriber contract (Module 1 P0-3).
//
// Subscriber identities are only ever fetched over the internal route
// (X-Internal-Service-Key), never a public API, and only for the purpose
// of fanning out upload notifications.
package subscribers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Page is one keyset page of notify-eligible subscriber IDs.
type Page struct {
	IDs       []uuid.UUID
	NextAfter uuid.UUID
	HasMore   bool
}

// ChannelByOwner resolves an author to their canonical channel id.
// Returns uuid.Nil when the user has no channel — callers must NOT fall
// back to followers in that case (Codex P0-3).
func (c *Client) ChannelByOwner(ctx context.Context, ownerID uuid.UUID) (uuid.UUID, error) {
	url := fmt.Sprintf("%s/internal/channels/by-owner/%s", c.baseURL, ownerID)
	body, err := c.get(ctx, url)
	if err != nil {
		return uuid.Nil, err
	}
	var env struct {
		Data struct {
			ChannelID string `json:"channel_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return uuid.Nil, err
	}
	if env.Data.ChannelID == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(env.Data.ChannelID)
}

// SubscriberIDs fetches one keyset page of notify-eligible subscribers.
func (c *Client) SubscriberIDs(ctx context.Context, channelID, after uuid.UUID, limit int) (*Page, error) {
	url := fmt.Sprintf("%s/internal/channels/%s/subscriber-ids?after=%s&limit=%d",
		c.baseURL, channelID, after, limit)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var env struct {
		Data struct {
			SubscriberIDs []string `json:"subscriber_ids"`
			NextAfter     string   `json:"next_after"`
			HasMore       bool     `json:"has_more"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	page := &Page{HasMore: env.Data.HasMore}
	for _, raw := range env.Data.SubscriberIDs {
		if id, err := uuid.Parse(raw); err == nil {
			page.IDs = append(page.IDs, id)
		}
	}
	if env.Data.NextAfter != "" {
		page.NextAfter, _ = uuid.Parse(env.Data.NextAfter)
	}
	return page, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return nil, fmt.Errorf("user-service returned %d for %s", resp.StatusCode, url)
	}
	// Bounded read: a page is at most 1000 ids, so 4 MB is generous.
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
