// Package graphclient is post-service's client for graph-service's relationship
// surface.
//
// Module 4 LB-1 — WHY THIS IS IN pkg/ AND NOT internal/.
//
// The client and the handler disagreed about the response envelope: this
// decoded `data` as an array of objects, graph-service emits an array of UUID
// strings. Every page failed to decode, the client correctly reported
// unresolved, and the story feed answered 503 to every viewer who follows
// anyone. Both services' suites stayed green because post-service tested this
// against a stub and graph-service never decoded its own body with the real
// consumer.
//
// A test that binds the two real halves cannot live in either service while
// both sides are `internal/` — Go forbids the cross-module import. So the
// client moved here, exactly as Module 3 moved the token minter to
// `pkg/accesstoken` for the same reason. graph-service's own integration test
// now imports this package and drives it against its real handler.
package graphclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Relationship is the viewer→target relationship as the graph reports it.
type Relationship struct {
	Follows bool `json:"follows"`
	// Blocked and BlockedBy are BOTH required: every block rule on this
	// platform is symmetric, and a contract carrying one direction cannot
	// express it.
	Blocked   bool `json:"blocked"`
	BlockedBy bool `json:"blocked_by"`
	IsMuted   bool `json:"is_muted"`
	// ViewerIsCloseFriendOfTarget: the TARGET has the viewer on the TARGET's
	// close-friends list. Not the same fact as the viewer's own list.
	ViewerIsCloseFriendOfTarget bool `json:"viewer_is_close_friend_of_target"`
}

// Client talks to graph-service over the internal network.
//
// Every failure is an error. There is no path that turns "I could not find
// out" into an empty relationship set, because an empty set reads as "nothing
// restricts this viewer".
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func New(baseURL, internalKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		http:        httpClient,
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if c == nil || c.baseURL == "" {
		return fmt.Errorf("graph service URL not configured")
	}
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call graph-service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graph-service %s returned %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode graph-service response: %w", err)
	}
	return nil
}

// MaxFollowingTraversal bounds one feed request. Tracked as debt: a viewer
// following more than this has their audience silently truncated.
const MaxFollowingTraversal = 5000

// Following returns the authors this viewer follows.
//
// It decodes exactly ONE shape: `{"data":["<uuid>", ...]}`, which is what
// GetFollowing's default (offset) branch emits via api.JSON over []uuid.UUID.
// Accepting several shapes "just in case" is what let the original mismatch
// survive — a client that tolerates anything cannot detect that it is talking
// to the wrong contract.
func (c *Client) Following(ctx context.Context, viewerID string) ([]string, error) {
	const pageSize = 100
	var all []string
	for offset := 0; ; offset += pageSize {
		var page struct {
			Data []string `json:"data"`
		}
		path := fmt.Sprintf("/v1/graph/following/%s?limit=%d&offset=%d",
			url.PathEscape(viewerID), pageSize, offset)
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		for _, id := range page.Data {
			if id = strings.TrimSpace(id); id != "" {
				all = append(all, id)
			}
		}
		if len(page.Data) < pageSize || len(all) >= MaxFollowingTraversal {
			return all, nil
		}
	}
}

// RelationshipBatch returns the viewer's relationship to each target.
//
// The caller is responsible for treating a target missing from the result as
// unresolved rather than as "no relationship".
func (c *Client) RelationshipBatch(ctx context.Context, viewerID string, targetIDs []string) (map[string]Relationship, error) {
	if len(targetIDs) == 0 {
		return map[string]Relationship{}, nil
	}
	var out map[string]Relationship
	body := map[string]any{"viewer_id": viewerID, "target_ids": targetIDs}
	if err := c.do(ctx, http.MethodPost, "/v1/graph/relationships/batch", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Action names accepted by graph-service's POST /v1/internal/graph/can.
const (
	ActionViewPosts = "view_posts"
	ActionComment   = "comment"
)

// MaxCanBatch mirrors graph-service's per-call ceiling; a larger batch is
// rejected with 400 BATCH_TOO_LARGE rather than truncated.
const MaxCanBatch = 100

// Can asks graph-service whether viewerID may perform action against each
// target. The answer is the §4 permission matrix resolved against the
// target's CURRENT privacy settings (account_visibility for view_posts,
// allow_comments_from for comment) and the live follow/connection graph.
//
// A target missing from the result is unresolved and must be treated as
// denied by the caller; this function never invents an answer. Callers chunk
// at MaxCanBatch.
func (c *Client) Can(ctx context.Context, viewerID, action string, targetIDs []string) (map[string]bool, error) {
	if len(targetIDs) == 0 {
		return map[string]bool{}, nil
	}
	if len(targetIDs) > MaxCanBatch {
		return nil, fmt.Errorf("can: %d targets exceeds the %d per-call limit", len(targetIDs), MaxCanBatch)
	}
	var out struct {
		Data map[string]bool `json:"data"`
	}
	body := map[string]any{"viewer_id": viewerID, "action": action, "target_ids": targetIDs}
	if err := c.do(ctx, http.MethodPost, "/v1/internal/graph/can", body, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		return map[string]bool{}, nil
	}
	return out.Data, nil
}
