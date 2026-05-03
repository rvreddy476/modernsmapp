// Dating event handlers — mirrors the qa_consumer.go pattern. Sprint 3
// covers spark.created, spark.matched, match.first_message, match.expired.
//
// The handler functions consume the typed payloads produced by
// dating-service/internal/events/producer.go.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// datingClient is a small read-only HTTP client over dating-service. We use
// it to look up the recipient's first name for human-readable notification
// titles. Profile-preview lookup is best-effort: a failure falls back to a
// generic title.
type datingClient struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func newDatingClient() *datingClient {
	baseURL := os.Getenv("DATING_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://dating-service:8112"
	}
	return &datingClient{
		baseURL:     baseURL,
		internalKey: os.Getenv("INTERNAL_SERVICE_KEY"),
		http:        &http.Client{Timeout: 4 * time.Second},
	}
}

// datingProfilePreview is the minimal shape we need.
type datingProfilePreview struct {
	UserID    string `json:"user_id"`
	FirstName string `json:"first_name"`
}

// getPreview returns the user's first name; empty string on any error.
func (c *datingClient) getFirstName(ctx context.Context, userID string) string {
	if c == nil || userID == "" {
		return ""
	}
	url := fmt.Sprintf("%s/v1/dating/profile/%s/preview", c.baseURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}
	var direct datingProfilePreview
	if err := json.NewDecoder(resp.Body).Decode(&direct); err == nil && direct.FirstName != "" {
		return direct.FirstName
	}
	return ""
}

// handleDatingEvent is the dispatch entry point invoked from the main
// consumer's processMessage when no other handler claimed the event.
// Returns true when the event was claimed (handled or knowingly ignored).
func (c *Consumer) handleDatingEvent(ctx context.Context, envelope events.EventEnvelope) (bool, error) {
	switch envelope.EventType {
	case events.EventDatingSparkCreated:
		return true, c.handleDatingSparkCreated(ctx, envelope.Payload)
	case events.EventDatingSparkMatched:
		return true, c.handleDatingSparkMatched(ctx, envelope.Payload)
	case events.EventDatingMatchFirstMessage:
		return true, c.handleDatingMatchFirstMessage(ctx, envelope.Payload)
	case events.EventDatingMatchExpired:
		return true, c.handleDatingMatchExpired(ctx, envelope.Payload)
	case events.EventDatingMatchFormed,
		events.EventDatingMatchClosed,
		events.EventDatingMatchQuiet,
		events.EventDatingStashAdded,
		events.EventDatingStashRemoved,
		events.EventDatingStashReactivated,
		events.EventDatingProfileCreated,
		events.EventDatingProfileUpdated,
		events.EventDatingProfilePaused,
		events.EventDatingProfileDeleted:
		// Known-but-not-pushed; claim the event so the default branch
		// doesn't log a warning.
		return true, nil
	}
	return false, nil
}

// dating.spark.created → notify the recipient.
type datingSparkCreatedPayload struct {
	SparkID    string    `json:"spark_id"`
	FromUserID string    `json:"from_user_id"`
	ToUserID   string    `json:"to_user_id"`
	TargetKind string    `json:"target_kind"`
	TargetRef  string    `json:"target_ref"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (c *Consumer) handleDatingSparkCreated(ctx context.Context, raw json.RawMessage) error {
	var e datingSparkCreatedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	to, err := uuid.Parse(e.ToUserID)
	if err != nil {
		return err
	}
	from, _ := uuid.Parse(e.FromUserID)
	sparkID, _ := uuid.Parse(e.SparkID)

	deepLink := fmt.Sprintf("/dating/sparks/incoming?spark_id=%s", e.SparkID)
	return c.service.CreateNotification(ctx, to, from, "dating.spark.created", "dating_spark", sparkID, deepLink, e.CreatedAt)
}

// dating.spark.matched → notify both participants.
type datingSparkMatchedPayload struct {
	MatchID   string    `json:"match_id"`
	UserA     string    `json:"user_a"`
	UserB     string    `json:"user_b"`
	MatchedAt time.Time `json:"matched_at"`
}

func (c *Consumer) handleDatingSparkMatched(ctx context.Context, raw json.RawMessage) error {
	var e datingSparkMatchedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	a, errA := uuid.Parse(e.UserA)
	b, errB := uuid.Parse(e.UserB)
	if errA != nil || errB != nil {
		return fmt.Errorf("invalid user ids in spark.matched: a=%v b=%v", errA, errB)
	}
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)

	// Notify both directions.
	if err := c.service.CreateNotification(ctx, a, b, "dating.match.formed", "dating_match", matchID, deepLink, e.MatchedAt); err != nil {
		slog.Warn("dating: notify a failed", "user", a, "error", err)
	}
	return c.service.CreateNotification(ctx, b, a, "dating.match.formed", "dating_match", matchID, deepLink, e.MatchedAt)
}

// dating.match.first_message → notify the recipient.
type datingMatchFirstMessagePayload struct {
	MatchID     string    `json:"match_id"`
	ActorID     string    `json:"actor_id"`
	RecipientID string    `json:"recipient_id"`
	FirstSentAt time.Time `json:"first_sent_at"`
}

func (c *Consumer) handleDatingMatchFirstMessage(ctx context.Context, raw json.RawMessage) error {
	var e datingMatchFirstMessagePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	recipient, err := uuid.Parse(e.RecipientID)
	if err != nil {
		return err
	}
	actor, _ := uuid.Parse(e.ActorID)
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)
	return c.service.CreateNotification(ctx, recipient, actor, "dating.match.first_message", "dating_match", matchID, deepLink, e.FirstSentAt)
}

// dating.match.expired → notify the user who didn't reply (i.e. the one we
// know didn't trigger first_message — which is what made the match expire).
// Spec: "the user who didn't send the first message". We don't know which
// of UserA/UserB it was at this point because nobody sent one. Notify both.
type datingMatchExpiredPayload struct {
	MatchID   string    `json:"match_id"`
	UserA     string    `json:"user_a"`
	UserB     string    `json:"user_b"`
	ExpiredAt time.Time `json:"expired_at"`
}

func (c *Consumer) handleDatingMatchExpired(ctx context.Context, raw json.RawMessage) error {
	var e datingMatchExpiredPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	a, errA := uuid.Parse(e.UserA)
	b, errB := uuid.Parse(e.UserB)
	if errA != nil || errB != nil {
		return fmt.Errorf("invalid user ids in match.expired: a=%v b=%v", errA, errB)
	}
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)

	// No actor here — the match expired without anyone messaging. We pass
	// the *other* participant as the "actor" for the recipient's notif so
	// the UI can render their name.
	if err := c.service.CreateNotification(ctx, a, b, "dating.match.expired", "dating_match", matchID, deepLink, e.ExpiredAt); err != nil {
		slog.Warn("dating: expired notify a failed", "user", a, "error", err)
	}
	return c.service.CreateNotification(ctx, b, a, "dating.match.expired", "dating_match", matchID, deepLink, e.ExpiredAt)
}
