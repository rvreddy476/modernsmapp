// Package realtime is the shared publish/subscribe glue for AtPost's
// real-time gateway.
//
// Domain services (food, rider, post, …) call Publisher.Publish to push
// an event onto a topic-scoped Redis Pub/Sub channel. The realtime
// gateway (currently inside notification-service) holds open SSE/WS
// connections, subscribes to the same channels, and fans messages out
// to the connected clients.
//
// Topic naming convention (see prompt/figo-mopedu-realtime-audit/
// 04-realtime-architecture-plan.md):
//
//   food.order.{order_id}
//   food.restaurant.{restaurant_id}.orders
//   food.delivery_partner.{partner_id}.assignments
//   food.admin.live_orders
//   rider.ride.{ride_id}
//   rider.partner.{partner_id}.offers
//   rider.admin.live_rides
//   rider.admin.safety
//
// Authorization: a client cannot just subscribe to any topic. Domain
// services issue short-lived HMAC tokens (TokenSigner) listing the
// user's authorized topics; the gateway verifies the token
// (TokenVerifier) and only opens subscriptions for topics in the token.
//
// Token format (compact, no external dep): base64url(payload).base64url(hmac)
// where payload is `<unix-expiry>.<user-id>.<topic1>,<topic2>,…`.
package realtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// channelPrefix is prepended to every topic to namespace the Pub/Sub
// keyspace and avoid colliding with other Redis uses.
const channelPrefix = "rt:"

// Publisher pushes events onto topic-scoped Redis Pub/Sub channels.
type Publisher struct {
	rdb *redis.Client
}

// NewPublisher returns a Publisher backed by the supplied Redis client.
func NewPublisher(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

// Event is the wire-format wrapper every realtime payload travels in.
// EventType is the domain event constant (e.g. "food.order.confirmed"),
// Data is the domain-specific payload. EmittedAt is set by Publish so
// subscribers can detect clock-skew or stale messages.
type Event struct {
	Topic     string          `json:"topic"`
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data"`
	EmittedAt time.Time       `json:"emitted_at"`
}

// Publish pushes the event onto the topic's Redis Stream via XADD with
// a MAXLEN ring trim. SSE subscribers (the notification-service
// gateway) read with XREAD BLOCK so they can replay missed messages
// via Last-Event-ID on reconnect.
//
// CR3-rt: this used to double-write to the legacy Pub/Sub channel
// during the cutover. The only subscriber on the `rt:` channels was
// the notification SSE handler, which moved to Streams in commit
// 8ae97c9; the Pub/Sub leg is now dead and has been dropped.
//
// Best-effort on Redis outage — Kafka remains the durable copy.
func (p *Publisher) Publish(ctx context.Context, topic, eventType string, data any) error {
	if p == nil || p.rdb == nil {
		return nil
	}
	if topic == "" {
		return errors.New("realtime: empty topic")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("realtime: marshal payload: %w", err)
	}
	env := Event{
		Topic:     topic,
		EventType: eventType,
		Data:      raw,
		EmittedAt: time.Now().UTC(),
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("realtime: marshal envelope: %w", err)
	}
	if _, err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamKey(topic),
		MaxLen: maxStreamLen,
		Approx: true,
		Values: map[string]any{
			"event_type": eventType,
			"payload":    body,
		},
	}).Result(); err != nil {
		return fmt.Errorf("realtime: XADD %s: %w", topic, err)
	}
	return nil
}

// Channel returns the namespaced Pub/Sub channel name for a topic.
// Subscribers use this to call rdb.PSubscribe.
func Channel(topic string) string { return channelPrefix + topic }

// TopicFromChannel undoes Channel(topic). Non-namespaced inputs are
// returned unchanged.
func TopicFromChannel(channel string) string {
	return strings.TrimPrefix(channel, channelPrefix)
}

// ─── Topic token ──────────────────────────────────────────────────────
//
// The gateway never authorizes topics directly — it trusts a token
// issued by a domain service. Each service knows what topics a user is
// allowed to subscribe to (because it owns the underlying data:
// orders, restaurants, partners, …) and signs a short-lived token
// listing them. Tokens use HMAC-SHA256 with a shared secret
// (REALTIME_TOKEN_SECRET) so any service can issue one and the gateway
// can verify without a round trip.

const defaultTTL = 30 * time.Minute

// TokenSigner issues topic-scoped tokens.
type TokenSigner struct {
	secret []byte
	ttl    time.Duration
}

// NewTokenSigner returns a signer using the given HMAC secret.
func NewTokenSigner(secret []byte) *TokenSigner {
	return &TokenSigner{secret: secret, ttl: defaultTTL}
}

// WithTTL overrides the default token lifetime.
func (s *TokenSigner) WithTTL(ttl time.Duration) *TokenSigner {
	s.ttl = ttl
	return s
}

// Sign returns a token granting the user access to the listed topics
// for the configured TTL. Topics MUST NOT contain '.' followed by ','
// or '|'; those are reserved separators.
func (s *TokenSigner) Sign(userID string, topics []string) (string, error) {
	if len(topics) == 0 {
		return "", errors.New("realtime: no topics to sign")
	}
	for _, t := range topics {
		if strings.ContainsAny(t, ",|") {
			return "", fmt.Errorf("realtime: topic %q contains reserved char", t)
		}
	}
	if strings.ContainsAny(userID, "|") {
		return "", errors.New("realtime: user id contains reserved char")
	}
	exp := time.Now().Add(s.ttl).Unix()
	payload := strconv.FormatInt(exp, 10) + "|" + userID + "|" + strings.Join(topics, ",")
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// TokenVerifier parses + verifies topic tokens at the gateway.
type TokenVerifier struct {
	secret []byte
}

// NewTokenVerifier returns a verifier using the given HMAC secret.
func NewTokenVerifier(secret []byte) *TokenVerifier {
	return &TokenVerifier{secret: secret}
}

// Verify parses, signature-checks, and expiry-checks the token. Returns
// the topic list and subject (user_id).
func (v *TokenVerifier) Verify(tokenStr string) (topics []string, subject string, err error) {
	parts := strings.SplitN(tokenStr, ".", 2)
	if len(parts) != 2 {
		return nil, "", errors.New("realtime: malformed token")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, "", fmt.Errorf("realtime: decode payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, "", fmt.Errorf("realtime: decode sig: %w", err)
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write(payloadBytes)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return nil, "", errors.New("realtime: invalid signature")
	}
	fields := strings.SplitN(string(payloadBytes), "|", 3)
	if len(fields) != 3 {
		return nil, "", errors.New("realtime: malformed payload")
	}
	exp, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil, "", fmt.Errorf("realtime: parse expiry: %w", err)
	}
	if time.Now().Unix() > exp {
		return nil, "", errors.New("realtime: token expired")
	}
	subject = fields[1]
	if fields[2] != "" {
		topics = strings.Split(fields[2], ",")
	}
	return topics, subject, nil
}

// MatchTopic reports whether `topic` matches any of `allowed`.
// Allowed entries may end with "*" to match any suffix, e.g.
// "food.admin.*" matches "food.admin.live_orders".
func MatchTopic(allowed []string, topic string) bool {
	for _, a := range allowed {
		if a == topic {
			return true
		}
		if strings.HasSuffix(a, "*") {
			prefix := strings.TrimSuffix(a, "*")
			if strings.HasPrefix(topic, prefix) {
				return true
			}
		}
	}
	return false
}
