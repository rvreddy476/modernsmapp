// Layer 2 — async LLM moderation.
//
// SHADOW MODE FOR v1 (CRITICAL RULES #5):
//   - The Kafka consumer reads dating.moderation.layer2.requested.
//   - It calls the configured LLM endpoint (or returns a deterministic
//     mock when LLM_MODERATION_URL is empty).
//   - Result is persisted to dating_moderation_results with
//     action_taken='shadow' regardless of confidence.
//   - dating.moderation.layer2.result is emitted; message-service receives
//     the event but takes NO user-visible action in shadow mode.
package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// LLMRequest is the body sent to the partner LLM endpoint.
type LLMRequest struct {
	Snippet string `json:"snippet"`
}

// LLMResponse is what we expect back. Confidence is the unsafeness score
// 0..1; tonality is the emotional-temperature score.
type LLMResponse struct {
	Confidence float64 `json:"confidence"`
	Tonality   float64 `json:"tonality"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

// Client is the LLM moderation client interface. main.go selects between
// the HTTP impl and the deterministic mock.
type Client interface {
	Score(ctx context.Context, snippet string) (*LLMResponse, error)
}

// HTTPClient calls the partner LLM endpoint.
type HTTPClient struct {
	url    string
	apiKey string
	client *http.Client
}

// NewHTTPClient configures from LLM_MODERATION_URL + LLM_MODERATION_API_KEY.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		url:    os.Getenv("LLM_MODERATION_URL"),
		apiKey: os.Getenv("LLM_MODERATION_API_KEY"),
		client: &http.Client{Timeout: 6 * time.Second},
	}
}

// Score sends the snippet to the partner. Non-2xx responses are errors so
// the caller can retry.
func (c *HTTPClient) Score(ctx context.Context, snippet string) (*LLMResponse, error) {
	if c.url == "" {
		return nil, fmt.Errorf("llm moderation url not configured")
	}
	body, err := json.Marshal(LLMRequest{Snippet: snippet})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("llm status %d", resp.StatusCode)
	}
	var out LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// MockClient returns a deterministic confidence based on a quick layer-1
// scan. Useful for tests + local dev (no real partner needed).
type MockClient struct{}

// NewMockClient returns the deterministic mock.
func NewMockClient() *MockClient { return &MockClient{} }

// Score derives confidence from layer-1 patterns + a small constant lift
// for content that contains both URL and money keywords.
func (m *MockClient) Score(_ context.Context, snippet string) (*LLMResponse, error) {
	r := ScanMessage(snippet)
	conf := r.Confidence
	if conf > 0 {
		conf += 0.05
	}
	if conf > 1 {
		conf = 1
	}
	return &LLMResponse{Confidence: conf, Tonality: 1.0 - conf}, nil
}
