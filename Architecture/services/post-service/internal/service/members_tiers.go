package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Membership tiers as post-service sees them (2026-10-23).
//
// post-service never owns a tier: monetization-service does. Two questions
// are asked of its public tier list,
//
//	GET {MONETIZATION_SERVICE_URL}/v1/monetization/creators/:creatorId/tiers
//	-> {"data":[{id, creator_id, name, price_paise, currency, is_active, ...}]}
//
// (active tiers only; the route is behind the internal key and
// MEMBERSHIPS_ENABLED):
//
//   - may this author gate a post on this tier? (ValidateTierForAuthor) —
//     on every write that sets the gate: create, both draft kinds at
//     publish, the edit and PUT /membership. The tier must be one of the
//     author's ACTIVE tiers; anything else is ErrInvalidTier (400
//     INVALID_TIER). A list that cannot be read is ErrTierLookupUnavailable
//     (503) and NOTHING is written: a post the author meant for members is
//     never created open because monetization-service was down.
//   - what does the tier a gated row is locked on cost? (attachTierPrices)
//     — on every read that runs the members-only rule, once per creator per
//     page, cached for a minute; unknown leaves the field off.

var (
	// ErrInvalidTier: the tier is not one of the author's active tiers
	// (unknown, another creator's, or switched off) — 400 INVALID_TIER.
	ErrInvalidTier = errors.New("INVALID_TIER")
	// ErrTierLookupUnavailable: monetization-service did not answer the tier
	// list — 503 MEMBERSHIP_TIERS_UNAVAILABLE. Nothing has been written.
	ErrTierLookupUnavailable = errors.New("MEMBERSHIP_TIERS_UNAVAILABLE")
)

// BlockReasonInvalidTier parks a scheduled draft whose members-only tier is
// no longer one of its author's active tiers.
const BlockReasonInvalidTier = "invalid_tier"

// CreatorTierRef is one tier as the public tier list answers it: the slice
// post-service reads.
type CreatorTierRef struct {
	ID         uuid.UUID `json:"id"`
	CreatorID  uuid.UUID `json:"creator_id"`
	PricePaise int64     `json:"price_paise"`
	IsActive   bool      `json:"is_active"`
}

const (
	// tierLookupTimeout bounds one tier-list read.
	tierLookupTimeout = mediaMembershipTimeout
	// tierPriceCacheTTL: prices change rarely; a minute keeps a hot
	// creator's feed from asking on every page.
	tierPriceCacheTTL       = time.Minute
	tierPriceCacheKeyPrefix = "tiers:creator:"
	maxTierListBody         = 64 << 10
)

// copyTierID is a fresh copy of an optional tier id (nil stays nil), so the
// row never aliases its caller's input.
func copyTierID(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	v := *id
	return &v
}

// lookupCreateIdempotency is CreatePost's idempotency fast path read;
// createIdemLookupSeam replaces it in tests.
func (s *Service) lookupCreateIdempotency(ctx context.Context, actorID uuid.UUID, key string) (uuid.UUID, string, bool, error) {
	if s.createIdemLookupSeam != nil {
		return s.createIdemLookupSeam(ctx, actorID, key)
	}
	return s.pgStore.LookupCreateIdempotency(ctx, actorID, key)
}

// writeCreatedPost is CreatePost's one write: the post row (gate included),
// its PostCreated outbox event and the idempotency claim, in one
// transaction. createPostWriteSeam replaces it in tests.
func (s *Service) writeCreatedPost(ctx context.Context, p *postgres.Post, eventType string, payload interface{}, idem *postgres.CreateIdempotency) error {
	if s.createPostWriteSeam != nil {
		return s.createPostWriteSeam(ctx, p, eventType, payload, idem)
	}
	return s.pgStore.CreatePostWithEventIdempotent(ctx, p, eventType, payload, idem)
}

// creatorTiers reads the creator's active tiers from monetization-service.
// Any failure — no URL, transport, a non-200, a body that does not decode —
// is an error; the caller decides what unknown means.
func (s *Service) creatorTiers(ctx context.Context, creatorID uuid.UUID) ([]CreatorTierRef, error) {
	if s.creatorTiersSeam != nil {
		return s.creatorTiersSeam(ctx, creatorID)
	}
	if s.monetizationServiceURL == "" {
		return nil, errors.New("MONETIZATION_URL_UNSET")
	}
	ctx, cancel := context.WithTimeout(ctx, tierLookupTimeout)
	defer cancel()
	endpoint := strings.TrimRight(s.monetizationServiceURL, "/") + "/v1/monetization/creators/" + creatorID.String() + "/tiers"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("build tier request")
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tier list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("tier list: monetization returned %d", resp.StatusCode)
	}
	var env struct {
		Data *[]CreatorTierRef `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxTierListBody)).Decode(&env); err != nil || env.Data == nil {
		return nil, errors.New("tier list: malformed body")
	}
	// Defensive: only the creator's own, active tiers count, whatever the
	// route answered.
	out := make([]CreatorTierRef, 0, len(*env.Data))
	for _, t := range *env.Data {
		if t.CreatorID == creatorID && t.IsActive && t.ID != uuid.Nil {
			out = append(out, t)
		}
	}
	return out, nil
}

// ValidateTierForAuthor is the gate rule for every write that sets
// tier_required_id: nil (no gate) passes without a lookup; otherwise the tier
// must be one of the author's active tiers (ErrInvalidTier), read live from
// monetization-service (ErrTierLookupUnavailable when it cannot be).
func (s *Service) ValidateTierForAuthor(ctx context.Context, authorID uuid.UUID, tierID *uuid.UUID) error {
	if tierID == nil {
		return nil
	}
	if *tierID == uuid.Nil {
		return ErrInvalidTier
	}
	tiers, err := s.creatorTiers(ctx, authorID)
	if err != nil {
		slog.WarnContext(ctx, "members-only gate: tier list unavailable; refusing the write",
			"author_id", authorID, "tier_id", *tierID, "err", err)
		return fmt.Errorf("%w: %v", ErrTierLookupUnavailable, err)
	}
	for _, t := range tiers {
		if t.ID == *tierID && t.CreatorID == authorID && t.IsActive {
			return nil
		}
	}
	return ErrInvalidTier
}

// attachTierPrices sets TierRequiredPricePaise on every gated row: one tier
// list per distinct creator on the page (cached for a minute), bounded by
// tierLookupTimeout. A creator whose list cannot be read, or a tier not on
// it, leaves the field off. Open rows are untouched.
func (s *Service) attachTierPrices(ctx context.Context, posts []*postgres.Post) {
	gated := false
	for _, p := range posts {
		if p != nil && p.TierRequiredID != nil {
			gated = true
			break
		}
	}
	if !gated {
		return
	}
	// One budget for the whole page: a slow monetization-service costs a
	// page at most tierLookupTimeout, however many creators it gates.
	ctx, cancel := context.WithTimeout(ctx, tierLookupTimeout)
	defer cancel()
	prices := map[uuid.UUID]map[uuid.UUID]int64{} // creator -> tier -> price; nil = unknown
	for _, p := range posts {
		if p == nil || p.TierRequiredID == nil {
			continue
		}
		byTier, asked := prices[p.AuthorID]
		if !asked {
			byTier = s.creatorTierPrices(ctx, p.AuthorID)
			prices[p.AuthorID] = byTier
		}
		if price, ok := byTier[*p.TierRequiredID]; ok {
			v := price
			p.TierRequiredPricePaise = &v
		}
	}
}

// creatorTierPrices is the creator's tier -> price map: the cached list, else
// a live read (stored on success only). nil when unknown.
func (s *Service) creatorTierPrices(ctx context.Context, creatorID uuid.UUID) map[uuid.UUID]int64 {
	key := tierPriceCacheKeyPrefix + creatorID.String()
	var tiers []CreatorTierRef
	cached := false
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, key).Bytes(); err == nil && json.Unmarshal(raw, &tiers) == nil {
			cached = true
		}
	}
	if !cached {
		live, err := s.creatorTiers(ctx, creatorID)
		if err != nil {
			slog.WarnContext(ctx, "members-only read: tier prices unavailable; leaving them off",
				"author_id", creatorID, "err", err)
			return nil
		}
		tiers = live
		if s.rdb != nil {
			if raw, err := json.Marshal(tiers); err == nil {
				_ = s.rdb.Set(ctx, key, raw, tierPriceCacheTTL).Err()
			}
		}
	}
	out := make(map[uuid.UUID]int64, len(tiers))
	for _, t := range tiers {
		if t.CreatorID == creatorID && t.IsActive {
			out[t.ID] = t.PricePaise
		}
	}
	return out
}
