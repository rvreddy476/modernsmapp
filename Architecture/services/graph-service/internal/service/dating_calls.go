package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DatingMatchProvider answers whether two users hold an OPEN dating match.
// The production implementation asks chat-service, which owns that state;
// tests substitute a stub.
//
// The contract is deliberately three-valued in effect: (true, nil) grants,
// (false, nil) denies, and (_, err) must be treated by callers as DENY. No
// implementation may report an unknown answer as true.
type DatingMatchProvider interface {
	HasOpenDatingMatch(ctx context.Context, userA, userB uuid.UUID) (bool, error)
}

// DatingMatchCacheTTL bounds how stale a cached match answer may be.
//
// Three seconds, matching [PrivacyCacheTTL] and for the same reason: this
// service has no invalidation channel telling it a match just closed, so the
// TTL is the size of the window in which an unmatched or blocked pair could
// still place a call. Three seconds of that is acceptable; a minute is not.
// A cached "no" is equally short-lived, so a brand-new match waits at most
// this long for its call button to light up.
const DatingMatchCacheTTL = 3 * time.Second

// WithDatingCalls wires the dating-match call grant. enabled is the
// DATING_CALLS_ENABLED kill switch: when false the fact is never fetched and
// never true, so call permissions behave exactly as they did before this
// feature existed. provider may be nil, which is equivalent to disabled.
func (s *Service) WithDatingCalls(enabled bool, provider DatingMatchProvider) *Service {
	s.datingCallsEnabled = enabled && provider != nil
	s.datingMatches = provider
	return s
}

// DatingCallsEnabled reports whether the dating-match call grant is live.
func (s *Service) DatingCallsEnabled() bool { return s.datingCallsEnabled }

// datingMatchFact resolves the DatingMatch fact, cached in Redis for
// [DatingMatchCacheTTL]. It FAILS CLOSED in every uncertain case — flag off,
// no provider, transport error, non-200, undecodable body — because the only
// thing this fact can do is grant a call channel. A dependency outage must
// narrow permissions, never widen them.
func (s *Service) datingMatchFact(ctx context.Context, actorID, targetID uuid.UUID) bool {
	if !s.datingCallsEnabled || s.datingMatches == nil {
		return false
	}
	if actorID == targetID || actorID == uuid.Nil || targetID == uuid.Nil {
		return false
	}

	// The fact is symmetric, so both directions share one cache entry.
	cacheKey := "dating_match:" + datingPairKey(actorID, targetID)
	if s.rdb != nil {
		if val, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
			switch val {
			case "1":
				return true
			case "0":
				return false
			}
			// Anything else is a corrupt entry: ignore it and re-fetch.
		}
	}

	open, err := s.datingMatches.HasOpenDatingMatch(ctx, actorID, targetID)
	if err != nil {
		log.Printf("[graph] dating-match fetch failed for %s<->%s, treating as no match: %v", actorID, targetID, err)
		return false
	}

	if s.rdb != nil {
		v := "0"
		if open {
			v = "1"
		}
		s.rdb.Set(ctx, cacheKey, v, DatingMatchCacheTTL)
	}
	return open
}

// datingPairKey orders a pair so either direction hashes to one cache key.
func datingPairKey(a, b uuid.UUID) string {
	x, y := a.String(), b.String()
	if x > y {
		x, y = y, x
	}
	return x + ":" + y
}

// httpDatingMatchProvider asks chat-service's internal-only endpoint
// POST /internal/v1/chat/dating-match/state, which reads exactly the
// conversation row that gates dating messages. Calls and chat therefore open
// and close together — they cannot disagree about whether a match is live.
type httpDatingMatchProvider struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPDatingMatchProvider builds the chat-service-backed provider.
// baseURL must be a non-empty absolute http(s) URL; internalKey must be
// non-empty, because the endpoint refuses unauthenticated callers and a
// provider that can only ever be refused is a fact that is always false.
func NewHTTPDatingMatchProvider(baseURL, internalKey string, client *http.Client) (DatingMatchProvider, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("MESSAGE_SERVICE_URL is required when DATING_CALLS_ENABLED=true")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("MESSAGE_SERVICE_URL must be an absolute http(s) URL, got %q", baseURL)
	}
	if strings.TrimSpace(internalKey) == "" {
		return nil, errors.New("INTERNAL_SERVICE_KEY is required when DATING_CALLS_ENABLED=true")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &httpDatingMatchProvider{baseURL: baseURL, internalKey: internalKey, client: client}, nil
}

func (p *httpDatingMatchProvider) HasOpenDatingMatch(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	payload, err := json.Marshal(map[string]string{
		"user_a": userA.String(),
		"user_b": userB.String(),
	})
	if err != nil {
		return false, err
	}
	// POST with the ids in the body: user ids do not belong in a URL, where
	// they would land in every proxy and access log along the way.
	url := p.baseURL + "/internal/v1/chat/dating-match/state"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", p.internalKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("chat-service returned %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			OpenMatch *bool `json:"open_match"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, fmt.Errorf("decode dating-match response: %w", err)
	}
	// A 200 that omits the field is an unknown answer, not a "no" we can
	// cache: report it as an error so the caller fails closed without
	// caching a fabricated negative.
	if envelope.Data.OpenMatch == nil {
		return false, errors.New("dating-match response missing open_match")
	}
	return *envelope.Data.OpenMatch, nil
}
