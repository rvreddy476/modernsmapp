package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tier 3c / 1a — Membership gating + Redis-backed entitlement cache.
//
// The post-service holds the column (`tier_required_id`) but does not
// own subscription state, so it asks monetization-service whether the
// caller is entitled to read a gated post. On failure the gate is
// closed (fail-secure): if monetization is down, the body is redacted
// rather than leaking through.
//
// Tier 1a — every gated post read used to be a synchronous HTTP
// roundtrip to monetization-service. For a hot creator this is
// dozens of duplicate calls per second answering the same question.
// We now cache the (subscriber, creator, requiredTier) → (allowed,
// reason) tuple in Redis with a short TTL, so subsequent reads
// inside the TTL window skip the HTTP call entirely.
//
// Staleness: subscriptions don't churn at second-granularity, so a
// short TTL trades correctness for a 60× reduction in monetization
// load with acceptable propagation lag.

const (
	entitlementCacheKeyPrefix = "ent:"
	// Allowed answers cache for 60s — long enough to absorb burst
	// reads on hot posts, short enough that an unsubscribe propagates
	// within a minute. (Phase 2: a Kafka consumer of
	// monetization.entitlement.changed will invalidate keys
	// immediately for the granted/revoked/upgraded cases.)
	entitlementCacheTTLAllowed = 60 * time.Second
	// Denied answers cache for a shorter window — when a fan
	// subscribes mid-session we want them unblocked quickly.
	entitlementCacheTTLDenied = 30 * time.Second
)

// SetPostMembershipGate is called by the handler after it has
// confirmed the caller owns the post. tierID == nil clears the gate
// (post becomes public again).
func (s *Service) SetPostMembershipGate(ctx context.Context, postID uuid.UUID, tierID *uuid.UUID) error {
	return s.pgStore.SetPostMembershipGate(ctx, postID, tierID)
}

// IsPostAuthor returns whether actor is the post's author. Cheap pgx
// roundtrip, used to short-circuit gating + to authorise the
// PUT /membership endpoint.
func (s *Service) IsPostAuthor(ctx context.Context, postID, actorID uuid.UUID) (bool, error) {
	authorID, _, err := s.pgStore.GetPostAuthorAndGate(ctx, postID)
	if err != nil {
		return false, err
	}
	return authorID == actorID, nil
}

// CheckEntitlement asks monetization-service whether `subscriberID`
// can open `creatorID`'s content gated at `requiredTierID`. Returns
// (allowed, reason). When monetization-service URL is not configured
// or the request fails, returns (false, error-string) — fail-secure.
func (s *Service) CheckEntitlement(ctx context.Context, subscriberID, creatorID uuid.UUID, requiredTierID *uuid.UUID) (bool, string, error) {
	// Author always passes. Mirrors monetization-service's own rule.
	if subscriberID == creatorID {
		return true, "self", nil
	}

	// Cache hit short-circuits the HTTP call entirely.
	if cached, hit := s.entitlementCacheGet(ctx, subscriberID, creatorID, requiredTierID); hit {
		return cached.Allowed, cached.Reason, nil
	}

	// No URL configured: fail-secure (treat every gated read as denied).
	if s.monetizationServiceURL == "" {
		return false, "monetization_url_not_configured", errors.New("MONETIZATION_URL_UNSET")
	}

	q := url.Values{}
	q.Set("creator_id", creatorID.String())
	if requiredTierID != nil {
		q.Set("tier_id", requiredTierID.String())
	}
	endpoint := fmt.Sprintf("%s/v1/monetization/entitlements?%s", s.monetizationServiceURL, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, "request_build_failed", err
	}
	req.Header.Set("X-User-Id", subscriberID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "monetization_unreachable", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("monetization_status_%d", resp.StatusCode), fmt.Errorf("monetization returned %d", resp.StatusCode)
	}

	var envelope struct {
		Data struct {
			Allowed bool   `json:"allowed"`
			Reason  string `json:"reason"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, "decode_failed", err
	}

	// Cache successful answers. Skip caching transport errors so a
	// flaky monetization-service doesn't poison the cache for the
	// next 60 seconds.
	s.entitlementCachePut(ctx, subscriberID, creatorID, requiredTierID, envelope.Data.Allowed, envelope.Data.Reason)
	return envelope.Data.Allowed, envelope.Data.Reason, nil
}

// InvalidateEntitlementCache drops the cached answer for one
// (subscriber, creator) pair. Phase 2 will call this from a Kafka
// consumer of monetization.entitlement.changed; for now it's a hook
// for tests + the rare in-process call path that knows a state has
// changed (e.g. an admin endpoint that toggles a creator's gate).
//
// Drops both the no-tier and any-tier variants under that pair, so a
// downgrade/upgrade doesn't leave stale tier-specific entries behind.
func (s *Service) InvalidateEntitlementCache(ctx context.Context, subscriberID, creatorID uuid.UUID) error {
	if s.rdb == nil {
		return nil
	}
	pattern := entitlementCacheKeyPrefix + subscriberID.String() + ":" + creatorID.String() + ":*"
	iter := s.rdb.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		_ = s.rdb.Del(ctx, iter.Val()).Err()
	}
	return iter.Err()
}

// ---------------------------------------------------------------------------
// Cache plumbing — exported helpers (BuildEntitlementCacheKey,
// EncodeEntitlementCacheValue, DecodeEntitlementCacheValue) so the
// tests can drive the format directly without faking a Redis.
// ---------------------------------------------------------------------------

// CachedEntitlement is the decoded payload of a cache hit.
type CachedEntitlement struct {
	Allowed bool
	Reason  string
}

// BuildEntitlementCacheKey returns the canonical Redis key for a
// (subscriber, creator, requiredTier) tuple. requiredTier == nil is
// treated as the "any active tier" variant and gets a literal "*"
// segment so it doesn't collide with a real tier UUID.
func BuildEntitlementCacheKey(subscriberID, creatorID uuid.UUID, requiredTierID *uuid.UUID) string {
	tierPart := "*"
	if requiredTierID != nil {
		tierPart = requiredTierID.String()
	}
	return entitlementCacheKeyPrefix + subscriberID.String() + ":" + creatorID.String() + ":" + tierPart
}

// EncodeEntitlementCacheValue is the wire format we put into Redis:
// "1|reason" or "0|reason". Compact so the cache is cheap.
func EncodeEntitlementCacheValue(allowed bool, reason string) string {
	flag := "0"
	if allowed {
		flag = "1"
	}
	return flag + "|" + reason
}

// DecodeEntitlementCacheValue parses what EncodeEntitlementCacheValue
// produced. Returns (decoded, ok); ok=false on any malformed value
// so the caller falls through to the live HTTP path rather than
// trusting garbage.
func DecodeEntitlementCacheValue(s string) (CachedEntitlement, bool) {
	parts := strings.SplitN(s, "|", 2)
	if len(parts) == 0 || (parts[0] != "0" && parts[0] != "1") {
		return CachedEntitlement{}, false
	}
	c := CachedEntitlement{Allowed: parts[0] == "1"}
	if len(parts) == 2 {
		c.Reason = parts[1]
	}
	return c, true
}

func (s *Service) entitlementCacheGet(ctx context.Context, subscriberID, creatorID uuid.UUID, requiredTierID *uuid.UUID) (CachedEntitlement, bool) {
	if s.rdb == nil {
		return CachedEntitlement{}, false
	}
	key := BuildEntitlementCacheKey(subscriberID, creatorID, requiredTierID)
	v, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		return CachedEntitlement{}, false
	}
	return DecodeEntitlementCacheValue(v)
}

func (s *Service) entitlementCachePut(ctx context.Context, subscriberID, creatorID uuid.UUID, requiredTierID *uuid.UUID, allowed bool, reason string) {
	if s.rdb == nil {
		return
	}
	ttl := entitlementCacheTTLAllowed
	if !allowed {
		ttl = entitlementCacheTTLDenied
	}
	key := BuildEntitlementCacheKey(subscriberID, creatorID, requiredTierID)
	_ = s.rdb.Set(ctx, key, EncodeEntitlementCacheValue(allowed, reason), ttl).Err()
}

// RedactGatedPost zeroes the heavy body fields on a Post when the
// caller isn't entitled. Title, author, cover, content_type, and
// counts stay so the frontend can render a "subscribe to view"
// preview card. body_redacted=true tells the frontend to swap UI.
func RedactGatedPost(p *postgres.Post) {
	p.Text = ""
	p.RichText = nil
	p.Media = nil
	p.Poll = nil
	p.BodyRedacted = true
}
