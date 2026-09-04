package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Module 1 P0-4 — viewer long-video frequency for the social home feed.
//
// Codex-approved initial behavior (all server-configurable via env):
//   hidden    → hard exclusion (0% of social home, every page, every mode)
//   reduced   → score multiplier 0.25, composition target 10%
//   balanced  → score multiplier 1.0,  composition target 25%  (default)
//   preferred → score multiplier 1.5,  composition target 50%
//
// Scope: ranked and chronological social home (+ delta counts). PostTube
// surfaces (/feed/videos, /feed/watch), flicks, subscriptions, explicit
// long-video searches, and direct links are NOT affected.
//
// The composition number is a *target cap*: excess long videos are demoted
// below the next non-video item rather than dropped, so no eligible post is
// ever lost — it just stops dominating the page.

type lvTier struct {
	Multiplier float64
	TargetCap  float64 // max fraction of long videos in any page prefix
}

var defaultLVTiers = map[string]lvTier{
	"reduced":   {Multiplier: 0.25, TargetCap: 0.10},
	"balanced":  {Multiplier: 1.0, TargetCap: 0.25},
	"preferred": {Multiplier: 1.5, TargetCap: 0.50},
}

// loadLVTiers applies env overrides of the form FEED_LV_MULT_REDUCED /
// FEED_LV_CAP_REDUCED (same for BALANCED / PREFERRED). Invalid values are
// ignored with a log line — never a boot failure.
func loadLVTiers() map[string]lvTier {
	tiers := make(map[string]lvTier, len(defaultLVTiers))
	for name, t := range defaultLVTiers {
		upper := map[string]string{"reduced": "REDUCED", "balanced": "BALANCED", "preferred": "PREFERRED"}[name]
		if v := os.Getenv("FEED_LV_MULT_" + upper); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				t.Multiplier = f
			} else {
				log.Printf("ignoring invalid FEED_LV_MULT_%s=%q", upper, v)
			}
		}
		if v := os.Getenv("FEED_LV_CAP_" + upper); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
				t.TargetCap = f
			} else {
				log.Printf("ignoring invalid FEED_LV_CAP_%s=%q", upper, v)
			}
		}
		tiers[name] = t
	}
	return tiers
}

// policyEpoch is a LAST-RESORT fallback only (fixes-v2 / Codex P1-2).
//
// Row-level provenance in `feed_distribution` is the primary signal: the
// consumer writes a row for every post it sees, so "explicitly eligible",
// "explicitly excluded" and "no policy row" are directly distinguishable.
// The epoch is consulted only when BOTH the authoritative lookup and the
// cache are unavailable — i.e. we have no row-level information at all.
// It is deployment metadata, not policy provenance, and is treated as
// such: a temporary migration-window heuristic.
//
// Set FEED_POLICY_EPOCH (RFC3339) to the actual deploy timestamp.
var policyEpoch = loadPolicyEpoch()

func loadPolicyEpoch() time.Time {
	const defaultEpoch = "2026-08-01T00:00:00Z"
	raw := os.Getenv("FEED_POLICY_EPOCH")
	if raw == "" {
		raw = defaultEpoch
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		log.Printf("invalid FEED_POLICY_EPOCH=%q; using %s", raw, defaultEpoch)
		t, _ = time.Parse(time.RFC3339, defaultEpoch)
	}
	return t
}

// markPolicyGoverned stamps each candidate with whether it could carry a
// distribution policy. Called on every candidate list before filtering.
func markPolicyGoverned(items []FeedItem) []FeedItem {
	for i := range items {
		items[i].PolicyGoverned = !items[i].CreatedAt.Before(policyEpoch)
	}
	return items
}

// isLongVideoType matches the long-form content types (current + legacy
// producer spellings). Flicks/reels are short-form and never affected.
func isLongVideoType(contentType string) bool {
	return contentType == "long_video" || contentType == "video"
}

// ValidLongVideoFrequency is the allowed preference set (HTTP validation).
func ValidLongVideoFrequency(freq string) bool {
	switch freq {
	case "hidden", "reduced", "balanced", "preferred":
		return true
	}
	return false
}

// GetLongVideoFrequency returns the viewer preference, defaulting to
// "balanced" on any error (fail-open to current behavior).
func (s *Service) GetLongVideoFrequency(ctx context.Context, userID uuid.UUID) string {
	freq, err := s.pgStore.GetLongVideoFrequency(ctx, userID)
	if err != nil || !ValidLongVideoFrequency(freq) {
		return "balanced"
	}
	return freq
}

// SetLongVideoFrequency persists the viewer preference.
func (s *Service) SetLongVideoFrequency(ctx context.Context, userID uuid.UUID, freq string) error {
	return s.pgStore.SetLongVideoFrequency(ctx, userID, freq)
}

// applyLongVideoFrequency enforces the viewer's long-video tier on an
// ordered candidate list. ranked=true additionally applies the score
// multiplier and re-sorts (stable) before the composition pass.
func (s *Service) applyLongVideoFrequency(candidates []FeedItem, freq string, ranked bool) []FeedItem {
	if len(candidates) == 0 || freq == "" {
		return candidates
	}
	if freq == "hidden" {
		out := candidates[:0]
		for _, c := range candidates {
			if !isLongVideoType(c.ContentType) {
				out = append(out, c)
			}
		}
		return out
	}
	tier, ok := s.lvTiers[freq]
	if !ok {
		return candidates // unknown tier — leave the feed untouched
	}

	// Score multiplier (ranked mode only — chronological order is time).
	if ranked && tier.Multiplier != 1.0 {
		for i := range candidates {
			if isLongVideoType(candidates[i].ContentType) {
				candidates[i].Score *= tier.Multiplier
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].Score > candidates[j].Score
		})
	}

	// Composition target: in every output prefix, long videos stay at or
	// under the cap. Excess long videos are demoted (kept, moved later),
	// so nothing is dropped.
	var lvQueue, rest []FeedItem
	for _, c := range candidates {
		if isLongVideoType(c.ContentType) {
			lvQueue = append(lvQueue, c)
		} else {
			rest = append(rest, c)
		}
	}
	if len(lvQueue) == 0 {
		return candidates
	}
	out := make([]FeedItem, 0, len(candidates))
	li, ri := 0, 0
	lvUsed := 0
	for len(out) < len(candidates) {
		slot := len(out) + 1
		// ceil-based allowance: lvUsed may reach ceil(cap·slot). This lets
		// a strong long video take an early slot (preferred cap 0.5 admits
		// one at position 1) while every 20-item page still lands on the
		// 10% / 25% / 50% targets (2 / 5 / 10 items).
		lvAllowed := lvUsed < int(math.Ceil(tier.TargetCap*float64(slot)))
		lvNext := li < len(lvQueue) && (ri >= len(rest) || earlierThan(lvQueue[li], rest[ri]))
		switch {
		case lvNext && (lvAllowed || ri >= len(rest)):
			out = append(out, lvQueue[li])
			li++
			lvUsed++
		case ri < len(rest):
			out = append(out, rest[ri])
			ri++
		default:
			// Only long videos remain — cap is a target, not a drop rule.
			out = append(out, lvQueue[li])
			li++
			lvUsed++
		}
	}
	return out
}

// earlierThan orders two feed items by their pre-pass position proxy:
// ranked mode compares score, otherwise recency.
func earlierThan(a, b FeedItem) bool {
	if a.Score != 0 || b.Score != 0 {
		return a.Score >= b.Score
	}
	return !a.CreatedAt.Before(b.CreatedAt)
}

// fetchSubscribedCreators returns the creator IDs whose channels the
// viewer subscribes to (Module 1 P0-3). Backs the PostTube Subscriptions
// feed. Uses the internal user-service contract; an error propagates so
// callers fail closed rather than falling back to the follow graph.
// maxSubscriptionsPerViewer is an EXPLICIT product limit on how many
// channel subscriptions shape the Subscriptions feed (fixes-v2 / P2-2).
//
// v1 stopped after 50 pages and returned the truncated set as success —
// a silent ceiling. The limit is now a named, configurable product rule
// (FEED_MAX_SUBSCRIPTIONS) and exceeding it is reported to the caller as
// an error rather than quietly dropping creators from the viewer's feed.
var maxSubscriptionsPerViewer = intFromEnv("FEED_MAX_SUBSCRIPTIONS", 50_000)

// ErrSubscriptionLimitExceeded means the viewer has more subscriptions
// than the configured product limit, so the set cannot be built
// completely. Callers fail closed rather than serve a partial feed that
// silently omits creators.
var ErrSubscriptionLimitExceeded = errors.New("viewer exceeds the supported subscription limit")

func intFromEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		log.Printf("ignoring invalid %s=%q", key, v)
	}
	return fallback
}

// fetchSubscribedCreators pages the keyset contract to exhaustion.
func (s *Service) fetchSubscribedCreators(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	const pageSize = 1000
	maxPages := (maxSubscriptionsPerViewer + pageSize - 1) / pageSize

	var out []uuid.UUID
	after := uuid.Nil
	for page := 0; page < maxPages; page++ {
		url := fmt.Sprintf("%s/internal/users/%s/subscribed-owner-ids?after=%s&limit=%d",
			s.userServiceURL, viewerID, after, pageSize)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		resp, err := s.userClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("user-service request failed: %w", err)
		}
		var env struct {
			Data struct {
				OwnerIDs  []string `json:"owner_ids"`
				NextAfter string   `json:"next_after"`
				HasMore   bool     `json:"has_more"`
			} `json:"data"`
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("user-service returned %d", resp.StatusCode)
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode subscriptions: %w", decodeErr)
		}

		for _, raw := range env.Data.OwnerIDs {
			if id, err := uuid.Parse(raw); err == nil {
				out = append(out, id)
			}
		}
		if !env.Data.HasMore || env.Data.NextAfter == "" {
			return out, nil
		}
		next, err := uuid.Parse(env.Data.NextAfter)
		if err != nil {
			return out, nil
		}
		after = next
	}
	// Exhausted the page budget with more still available: surface it
	// instead of returning a silently truncated set as success.
	log.Printf("subscriptions: viewer %s exceeds the %d-subscription product limit",
		viewerID, maxSubscriptionsPerViewer)
	return nil, fmt.Errorf("%w (limit %d)", ErrSubscriptionLimitExceeded, maxSubscriptionsPerViewer)
}

// RecordDistribution persists a post's main-feed eligibility (rev-guarded
// upsert; see store.UpsertDistribution) and refreshes the read cache.
//
// fixes-v2 / Codex P1-2: v1 wrote Postgres only. A cached "eligible" for a
// post the creator had since switched to main_feed=false survived for the
// full 24 h TTL, and degraded mode would happily serve the opted-out post
// from that stale entry. The cache is now updated on every accepted
// revision, and cleared if the write did not apply so a stale value can
// never outlive the truth.
func (s *Service) RecordDistribution(ctx context.Context, postID uuid.UUID, mainFeed bool, rev int64) error {
	if err := s.pgStore.UpsertDistribution(ctx, postID, mainFeed, rev); err != nil {
		// Do not leave a now-questionable cache entry behind.
		s.invalidateMainFeedPolicy(ctx, postID)
		return err
	}
	// Re-read the authoritative value: the upsert is rev-guarded, so a
	// stale (out-of-order) event may legitimately not have applied — in
	// which case caching OUR value would install the wrong answer.
	excluded, err := s.pgStore.ExcludedFromMainFeed(ctx, []uuid.UUID{postID})
	if err != nil {
		s.invalidateMainFeedPolicy(ctx, postID)
		return nil // the durable write succeeded; cache is best-effort
	}
	s.cacheMainFeedPolicies(ctx, []uuid.UUID{postID}, excluded)
	return nil
}

// invalidateMainFeedPolicy drops a cached decision. An absent entry makes
// degraded mode fall back to the conservative provenance rule rather than
// trusting a value that may no longer be true.
func (s *Service) invalidateMainFeedPolicy(ctx context.Context, postID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	if err := s.rdb.Del(ctx, mainFeedPolicyCacheKey(postID)).Err(); err != nil {
		log.Printf("main-feed policy cache invalidate failed for %s: %v", postID, err)
	}
}

// filterMainFeedExcluded removes posts whose distribution policy opted out
// of the social home surface (P0-1).
//
// Failure policy (Codex P1-2): creator-controlled distribution must not be
// overridden by our availability problems, but blanking the whole feed on
// a Postgres blip is equally unacceptable. The middle ground:
//
//   - The timeline projection carries a `PolicyGoverned` marker for every
//     post created under the new policy regime. On a metadata error we
//     drop exactly those uncertain candidates and keep the known-legacy
//     ones, so a PostTube-only upload can never leak while the feed stays
//     populated with content that provably predates policies.
//   - A Redis last-known-policy cache absorbs the common transient case,
//     so most errors never reach the fallback at all.
func (s *Service) filterMainFeedExcluded(ctx context.Context, candidates []FeedItem) []FeedItem {
	if len(candidates) == 0 {
		return candidates
	}
	candidates = markPolicyGoverned(candidates)
	ids := make([]uuid.UUID, len(candidates))
	for i, c := range candidates {
		ids[i] = c.PostID
	}

	excluded, err := s.pgStore.ExcludedFromMainFeed(ctx, ids)
	if err != nil {
		log.Printf("main-feed exclusion lookup failed; falling back to cache + conservative drop: %v", err)
		return s.filterMainFeedExcludedDegraded(ctx, candidates)
	}

	// Refresh the cache so a later outage has recent truth to work from.
	s.cacheMainFeedPolicies(ctx, ids, excluded)

	if len(excluded) == 0 {
		return candidates
	}
	out := candidates[:0]
	for _, c := range candidates {
		if _, skip := excluded[c.PostID]; !skip {
			out = append(out, c)
		}
	}
	return out
}

// mainFeedPolicyCacheKey namespaces the last-known main-feed decision.
func mainFeedPolicyCacheKey(postID uuid.UUID) string {
	return "feed:mainfeed:" + postID.String()
}

// mainFeedPolicyTTL is generous: the value only changes when a creator
// edits distribution, and that path publishes an event which refreshes it.
const mainFeedPolicyTTL = 24 * time.Hour

// cacheMainFeedPolicies records the authoritative decision per post.
// "1" = eligible for social home, "0" = excluded.
func (s *Service) cacheMainFeedPolicies(ctx context.Context, ids []uuid.UUID, excluded map[uuid.UUID]struct{}) {
	if s.rdb == nil {
		return
	}
	pipe := s.rdb.Pipeline()
	for _, id := range ids {
		val := "1"
		if _, skip := excluded[id]; skip {
			val = "0"
		}
		pipe.Set(ctx, mainFeedPolicyCacheKey(id), val, mainFeedPolicyTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("main-feed policy cache write failed: %v", err)
	}
}

// filterMainFeedExcludedDegraded runs when the authoritative lookup is
// unavailable. Per candidate:
//
//	cached "0"        → drop (known opt-out)
//	cached "1"        → keep (known opt-in)
//	no cache entry +
//	  policy-governed → DROP (uncertain; a creator opt-out must never leak)
//	no cache entry +
//	  legacy post     → keep (predates policies; cannot be an opt-out)
func (s *Service) filterMainFeedExcludedDegraded(ctx context.Context, candidates []FeedItem) []FeedItem {
	cached := map[uuid.UUID]string{}
	if s.rdb != nil {
		keys := make([]string, len(candidates))
		for i, c := range candidates {
			keys[i] = mainFeedPolicyCacheKey(c.PostID)
		}
		if vals, err := s.rdb.MGet(ctx, keys...).Result(); err == nil {
			for i, v := range vals {
				if str, ok := v.(string); ok {
					cached[candidates[i].PostID] = str
				}
			}
		} else {
			log.Printf("main-feed policy cache read failed: %v", err)
		}
	}

	dropped := 0
	out := candidates[:0]
	for _, c := range candidates {
		switch cached[c.PostID] {
		case "0":
			dropped++
			continue
		case "1":
			out = append(out, c)
			continue
		}
		// No cache entry and no authoritative read. Fall back to the
		// epoch heuristic: only posts that COULD carry a policy are
		// dropped. This is the one place the epoch is consulted.
		if c.PolicyGoverned {
			dropped++
			continue
		}
		out = append(out, c)
	}
	if dropped > 0 {
		log.Printf("main-feed exclusion degraded mode: dropped %d uncertain candidate(s)", dropped)
	}
	return out
}

// mergeDiscoveryFill tops a short first page up to `limit` with discovery
// candidates. Timeline rows keep their place; a fill row already on the
// page (the viewer follows its author) is dropped rather than shown twice;
// the rest are marked sourceColdStart so the "why am I seeing this" reason
// says "recommended" instead of "following". Pure, for the unit test.
func mergeDiscoveryFill(candidates, fill []FeedItem, limit int) []FeedItem {
	if limit <= 0 || len(candidates) >= limit || len(fill) == 0 {
		return candidates
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates)+len(fill))
	for _, c := range candidates {
		seen[c.PostID] = struct{}{}
	}
	out := candidates
	for _, f := range fill {
		if len(out) >= limit {
			break
		}
		if _, dup := seen[f.PostID]; dup {
			continue
		}
		if !isLongVideoType(f.ContentType) {
			continue // post-service filtered by type; belt and braces
		}
		seen[f.PostID] = struct{}{}
		f.Source = sourceColdStart
		out = append(out, f)
	}
	return out
}
