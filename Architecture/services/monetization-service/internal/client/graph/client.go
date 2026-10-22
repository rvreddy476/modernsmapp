// Package graph is monetization's one question to graph-service: is there a
// block between these two people, in either direction?
//
// Security sweep, 21 Oct 2026 (#16): SendTip went straight to the daily cap
// and the wallets, so a user the creator had blocked could still pay them —
// with a 250-character message attached. chat-service asks the same route
// before it seats someone in a conversation
// (chat-service/services/message-service/internal/service/groups.go).
//
//	POST {GRAPH_SERVICE_URL}/v1/internal/graph/blocked-any
//	X-Internal-Service-Key: <key>
//	{"user_id": "<a>", "candidate_ids": ["<b>"]}
//	→ 200 {"blocked_user_ids": ["<b>"]}   a block exists, either way
//	→ 200 {"blocked_user_ids": []}        none
//
// The route is under /v1/internal, which the gateway refuses at the edge.
// Every failure — transport, non-200, a malformed body — is an error the
// caller must treat as "could not tell", never as "not blocked".
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BlockedAnyPath is graph-service's either-direction block lookup.
const BlockedAnyPath = "/v1/internal/graph/blocked-any"

// maxBody caps how much of graph's answer is read; one id fits in far less.
const maxBody = 64 << 10

// Client asks graph-service about blocks.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New returns nil when baseURL is empty, so main can tell "not configured".
func New(baseURL, internalKey string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{baseURL: baseURL, internalKey: internalKey, http: &http.Client{Timeout: 3 * time.Second}}
}

// BlockedEitherWay reports whether a blocked b or b blocked a.
func (c *Client) BlockedEitherWay(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if c == nil {
		return false, errors.New("graph: not configured")
	}
	body, err := json.Marshal(map[string]any{"user_id": a, "candidate_ids": []uuid.UUID{b}})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+BlockedAnyPath, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("graph: blocked-any: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return false, fmt.Errorf("graph: blocked-any returned %d", resp.StatusCode)
	}
	var out struct {
		BlockedUserIDs *[]uuid.UUID `json:"blocked_user_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return false, fmt.Errorf("graph: blocked-any body: %w", err)
	}
	// A body without the field is not "nobody is blocked".
	if out.BlockedUserIDs == nil {
		return false, errors.New("graph: blocked-any answered without blocked_user_ids")
	}
	return len(*out.BlockedUserIDs) > 0, nil
}
