// Moderation service — orchestrates the layer-1 (sync) and layer-2 (async)
// scans behind a single feature-flag-controlled mode switch.
//
// SHADOW MODE FOR v1 (CRITICAL RULES #5):
// The default mode is "shadow". Strict mode is feature-flag-gated by
// `pulse_moderation_strict` and OFF by default. In shadow mode the
// returned ScanResult has ActionTaken="shadow" and the persisted row in
// dating_moderation_results also has action_taken='shadow', regardless of
// the underlying recommendation. There is no user-visible action.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/moderation"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// (sync import lives in dating.go alongside the Service struct definition)

// FeatureFlagsClient resolves boolean feature flags. Tests inject a stub.
type FeatureFlagsClient interface {
	BoolFlag(ctx context.Context, name string) (bool, error)
}

// SetFeatureFlagsClient injects the flags client.
func (s *Service) SetFeatureFlagsClient(c FeatureFlagsClient) { s.flagsClient = c }

// ModerationLLMClient is the layer-2 LLM client interface. main.go selects
// between the production HTTP impl and the mock.
type ModerationLLMClient interface {
	Score(ctx context.Context, snippet string) (*moderation.LLMResponse, error)
}

// SetModerationLLMClient injects the LLM client.
func (s *Service) SetModerationLLMClient(c ModerationLLMClient) { s.moderationLLM = c }

// ScanRequest is the input for ScanLayer1 (called over internal HTTP by
// message-service).
type ScanRequest struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	SenderID       uuid.UUID `json:"sender_id"`
	Body           string    `json:"body"`
}

// ScanResult is the layer-1 verdict returned to the caller. ActionTaken is
// "shadow" in shadow mode, otherwise the layer-1 recommendation.
type ScanResult struct {
	MessageID      uuid.UUID `json:"message_id"`
	Confidence     float64   `json:"confidence"`
	Patterns       []string  `json:"patterns"`
	ActionTaken    string    `json:"action_taken"`
	Suggestion     string    `json:"suggestion,omitempty"`
	StrictMode     bool      `json:"strict_mode"`
	Layer2Queued   bool      `json:"layer2_queued"`
}

// ScanLayer1 runs the synchronous regex scan. In shadow mode this is the
// only thing message-service should look at when deciding whether to
// surface a banner — and it must NOT, because action_taken="shadow".
func (s *Service) ScanLayer1(ctx context.Context, req ScanRequest) (*ScanResult, error) {
	if req.MessageID == uuid.Nil {
		return nil, fmt.Errorf("invalid: message_id required")
	}
	if req.ConversationID == uuid.Nil {
		return nil, fmt.Errorf("invalid: conversation_id required")
	}
	verdict := moderation.ScanMessage(req.Body)
	strict, _ := s.isModerationStrict(ctx)
	action := verdict.ActionTaken
	if !strict {
		action = "shadow"
	} else {
		// Strict mode: map the recommendation to a real action.
		switch verdict.ActionTaken {
		case "block":
			action = "block"
		case "warn":
			action = "warn"
		default:
			action = "shadow"
		}
	}
	if err := s.store.RecordModerationResult(ctx, store.ModerationResult{
		MessageID:      req.MessageID,
		ConversationID: req.ConversationID,
		Layer:          1,
		Confidence:     verdict.Confidence,
		Patterns:       verdict.Patterns,
		ActionTaken:    action,
	}); err != nil {
		// Persistence failure on a moderation row is not safety-critical
		// (the scan still returns); we log at WARN.
		slog.Warn("moderation: persist layer1 result failed", "message_id", req.MessageID, "error", err)
	}
	if s.producer != nil {
		_ = s.producer.PublishModerationLayer1Result(ctx, eventPayloadL1(req, verdict, action))
	}
	queued := false
	if verdict.Confidence >= 0.5 || strict {
		// Async layer-2 request. Even in shadow mode we kick off layer 2
		// because the dashboard wants the LLM verdict.
		if s.producer != nil {
			_ = s.producer.PublishModerationLayer2Requested(ctx, datingLayer2Req(req))
			queued = true
		}
	}
	return &ScanResult{
		MessageID:    req.MessageID,
		Confidence:   verdict.Confidence,
		Patterns:     verdict.Patterns,
		ActionTaken:  action,
		Suggestion:   verdict.Suggestion,
		StrictMode:   strict,
		Layer2Queued: queued,
	}, nil
}

// ProcessLayer2 is the consumer entry point. It calls the LLM, persists
// the row, and emits dating.moderation.layer2.result. In shadow mode the
// action_taken is forced to "shadow".
func (s *Service) ProcessLayer2(ctx context.Context, messageID, conversationID uuid.UUID, snippet string) error {
	if s.moderationLLM == nil {
		return fmt.Errorf("llm client not configured")
	}
	resp, err := s.moderationLLM.Score(ctx, snippet)
	if err != nil {
		return fmt.Errorf("layer2 score: %w", err)
	}
	strict, _ := s.isModerationStrict(ctx)
	action := "shadow"
	if strict {
		switch {
		case resp.Confidence >= 0.85:
			action = "block"
		case resp.Confidence >= 0.6:
			action = "warn"
		default:
			action = "shadow"
		}
	}
	if err := s.store.RecordModerationResult(ctx, store.ModerationResult{
		MessageID:      messageID,
		ConversationID: conversationID,
		Layer:          2,
		Confidence:     resp.Confidence,
		Patterns:       []string{},
		ActionTaken:    action,
	}); err != nil {
		return fmt.Errorf("persist layer2 result: %w", err)
	}
	if s.producer != nil {
		_ = s.producer.PublishModerationLayer2Result(ctx, datingLayer2Result(messageID, conversationID, resp, action))
	}
	return nil
}

// isModerationStrict reads the feature flag. Failure to read defaults to
// FALSE (shadow mode), which is the safer answer when the flag service is
// unreachable. CRITICAL RULES #5: shadow is the default.
//
// Sprint 6: cached with a 60s TTL so a flag flip in feature-flag-service
// applies within a minute without restarting dating-service. Cache is
// best-effort — read errors do NOT poison subsequent reads.
func (s *Service) isModerationStrict(ctx context.Context) (bool, error) {
	if s.flagsClient == nil {
		return false, nil
	}
	if v, ok := s.readStrictCache(); ok {
		return v, nil
	}
	on, err := s.flagsClient.BoolFlag(ctx, "pulse_moderation_strict")
	if err != nil {
		slog.Warn("moderation: feature flag fetch failed; defaulting to shadow", "error", err)
		return false, err
	}
	s.writeStrictCache(on)
	return on, nil
}

// strictModeCacheTTL is the dating-service-side TTL for the
// pulse_moderation_strict flag value. Short enough that a flag flip is
// observable in tens of seconds; long enough that the LLM moderation hot
// path doesn't hammer feature-flag-service.
const strictModeCacheTTL = 60 * time.Second

func (s *Service) readStrictCache() (bool, bool) {
	s.strictMu.RLock()
	defer s.strictMu.RUnlock()
	if s.strictCacheAt.IsZero() {
		return false, false
	}
	if time.Since(s.strictCacheAt) > strictModeCacheTTL {
		return false, false
	}
	return s.strictCacheVal, true
}

func (s *Service) writeStrictCache(v bool) {
	s.strictMu.Lock()
	defer s.strictMu.Unlock()
	s.strictCacheVal = v
	s.strictCacheAt = time.Now()
}

// eventPayloadL1 builds the layer-1 result event body.
func eventPayloadL1(req ScanRequest, v moderation.ScanResult, action string) datingevents.ModerationLayer1ResultPayload {
	p := datingevents.ModerationLayer1ResultPayload{
		MessageID:      req.MessageID.String(),
		ConversationID: req.ConversationID.String(),
		Confidence:     v.Confidence,
		Patterns:       v.Patterns,
		ActionTaken:    action,
		Suggestion:     v.Suggestion,
		ScannedAt:      time.Now(),
	}
	if req.SenderID != uuid.Nil {
		p.SenderID = req.SenderID.String()
	}
	return p
}

// datingLayer2Req builds the layer-2 async request payload.
func datingLayer2Req(req ScanRequest) datingevents.ModerationLayer2RequestedPayload {
	p := datingevents.ModerationLayer2RequestedPayload{
		MessageID:      req.MessageID.String(),
		ConversationID: req.ConversationID.String(),
		Snippet:        req.Body,
		RequestedAt:    time.Now(),
	}
	if req.SenderID != uuid.Nil {
		p.SenderID = req.SenderID.String()
	}
	return p
}

// datingLayer2Result builds the layer-2 result payload.
func datingLayer2Result(messageID, conversationID uuid.UUID, resp *moderation.LLMResponse, action string) datingevents.ModerationLayer2ResultPayload {
	return datingevents.ModerationLayer2ResultPayload{
		MessageID:      messageID.String(),
		ConversationID: conversationID.String(),
		Confidence:     resp.Confidence,
		Tonality:       resp.Tonality,
		ActionTaken:    action,
		ScannedAt:      time.Now(),
	}
}

// httpFeatureFlagsClient calls feature-flag-service /v1/flags/:name.
type httpFeatureFlagsClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPFeatureFlagsClient configures from FEATURE_FLAG_SERVICE_URL.
func NewHTTPFeatureFlagsClient() FeatureFlagsClient {
	base := os.Getenv("FEATURE_FLAG_SERVICE_URL")
	if base == "" {
		base = "http://feature-flag-service:8104"
	}
	return &httpFeatureFlagsClient{baseURL: base, client: &http.Client{Timeout: 2 * time.Second}}
}

// BoolFlag returns the boolean value of `name`. Errors map to false (shadow).
func (c *httpFeatureFlagsClient) BoolFlag(ctx context.Context, name string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/flags/"+name, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("flags unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("flags status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Enabled bool `json:"enabled"`
			Value   any  `json:"value"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, err
	}
	return envelope.Data.Enabled, nil
}

