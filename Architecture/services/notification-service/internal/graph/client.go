// Package graph is a small read-only HTTP client over graph-service.
// Used by the notification consumer to fan a followed-creator's post out
// to their followers.
package graph

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
		http:        &http.Client{Timeout: 6 * time.Second},
	}
}

// GetFollowers returns the list of user IDs that follow `userID`. Pagination
// is offset-based; pass limit > 0 to cap. The endpoint returns flat UUID
// strings (not user objects) so we decode into []string and parse.
//
// Safe on a nil receiver — returns (nil, nil) so callers don't have to gate.
func (c *Client) GetFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/v1/graph/followers/%s?limit=%d&offset=%d", c.baseURL, userID, limit, offset)
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
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("graph followers: status %d", resp.StatusCode)
	}
	// Envelope: {"data": ["uuid1", "uuid2", ...]}
	var env struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(env.Data))
	for _, s := range env.Data {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out, nil
}

// GetFilteredConnectionRequestSenders returns the set of sender IDs whose
// connection requests to `receiverID` were auto-filtered (hidden) by
// trust-safety-service. Used by the friend-request notification path
// (P1.4b) to suppress the push for abusive requests.
//
// The endpoint authorizes via X-User-Id (the recipient) plus the internal
// service key. Response is the standard envelope whose `data` is an array
// of connection-request objects, each carrying a `sender_id`.
//
// Safe on a nil receiver — returns (nil, nil).
func (c *Client) GetFilteredConnectionRequestSenders(ctx context.Context, receiverID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/v1/graph/connection-requests/filtered", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-User-Id", receiverID.String())
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph filtered connection-requests: status %d", resp.StatusCode)
	}
	// Envelope: {"data": [{"sender_id": "uuid", ...}, ...]}
	var env struct {
		Data []struct {
			SenderID string `json:"sender_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]struct{}, len(env.Data))
	for _, r := range env.Data {
		if id, err := uuid.Parse(r.SenderID); err == nil {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

// FollowerPageMax is the largest page graph-service's cursor listing will
// serve. Its store answers a limit above 100 with a page of 20 rather than
// an error, so asking for more would quietly shrink every page; FollowerIDs
// clamps to this instead.
const FollowerPageMax = 100

// FollowerPage is one keyset page of a user's followers. NextCursor is
// graph-service's own token ("" when the listing is exhausted) and is
// passed back verbatim: this client never interprets it.
type FollowerPage struct {
	IDs        []uuid.UUID
	NextCursor string
}

// FollowerIDs fetches one page of the users who follow `userID` over the
// cursor variant of GET /v1/graph/followers/{id} (paginate=cursor). Unlike
// the offset variant GetFollowers uses, which graph-service refuses past
// offset 10,000, the cursor walk has no depth limit, so a durable fan-out
// can page a follower list of any size to exhaustion. `cursor` is empty
// for the first page.
//
// No X-User-Id is sent: the listing is the internal view, not one viewer's.
// graph-service leaves hidden (suspended or deactivated) accounts out.
//
// Unlike GetFollowers this is NOT quiet on a nil or unconfigured client,
// and any non-200 is an error: an empty page ends the follower walk for
// good, so "could not ask" must stay retryable.
func (c *Client) FollowerIDs(ctx context.Context, userID uuid.UUID, cursor string, limit int) (*FollowerPage, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("graph followers: client not configured")
	}
	if limit <= 0 || limit > FollowerPageMax {
		limit = FollowerPageMax
	}
	q := url.Values{}
	q.Set("paginate", "cursor")
	q.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	target := fmt.Sprintf("%s/v1/graph/followers/%s?%s", c.baseURL, userID, q.Encode())
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
		return nil, fmt.Errorf("graph followers: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	// Envelope: {"data": {"items": ["uuid", ...], "next_cursor": "..."}}.
	// Items is a pointer so a body of another shape (the offset variant's
	// bare array, an error envelope with a 200) is an error, not "nobody".
	var env struct {
		Data *struct {
			Items      *[]string `json:"items"`
			NextCursor string    `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("graph followers decode: %w", err)
	}
	if env.Data == nil || env.Data.Items == nil {
		return nil, fmt.Errorf("graph followers: response carries no items list")
	}
	page := &FollowerPage{NextCursor: env.Data.NextCursor}
	for _, raw := range *env.Data.Items {
		if id, err := uuid.Parse(raw); err == nil {
			page.IDs = append(page.IDs, id)
		}
	}
	return page, nil
}
