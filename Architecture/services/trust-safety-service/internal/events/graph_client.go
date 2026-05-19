package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GraphClient calls graph-service over HTTP for the connection-request
// auto-filter (friends-sheets spec §5.1, §9.2). It is a thin internal client:
// every request carries the X-Internal-Service-Key header so graph-service's
// RequireInternalKey middleware accepts it.
type GraphClient struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
}

// NewGraphClient builds a GraphClient. baseURL should be the graph-service root
// (e.g. http://graph-service:8083), internalKey the shared INTERNAL_SERVICE_KEY.
func NewGraphClient(baseURL, internalKey string) *GraphClient {
	return &GraphClient{
		baseURL:     baseURL,
		internalKey: internalKey,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
}

type filterConnectionRequestBody struct {
	SenderID   string `json:"sender_id"`
	ReceiverID string `json:"receiver_id"`
}

// FilterConnectionRequest tells graph-service to move a pending connection
// request into the recipient's hidden/filtered queue. Returns an error on a
// transport failure or a non-2xx response — callers log and continue (the
// consumer must never crash on a graph-service hiccup).
func (c *GraphClient) FilterConnectionRequest(ctx context.Context, senderID, receiverID string) error {
	body, err := json.Marshal(filterConnectionRequestBody{
		SenderID:   senderID,
		ReceiverID: receiverID,
	})
	if err != nil {
		return fmt.Errorf("marshal filter request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/v1/graph/connection-request/filter",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("new filter request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call graph-service filter endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("graph-service filter endpoint returned %d: %s", resp.StatusCode, string(snippet))
	}
	return nil
}
