package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// affiliateValidatorCacheTTL bounds how stale a (link, caller) validation
// can be. 5 minutes balances "no DOS on monetization-service from a
// creator bulk-tagging" against "creator revokes a link → it stops
// validating within reasonable time". The entitlement-cache shape in
// membership.go uses the same trade-off.
const affiliateValidatorCacheTTL = 5 * time.Minute

// ValidateAffiliateLink calls monetization-service to confirm that
// `linkID` exists and belongs to `callerID`. Returns the resolved
// (creator_id, listing_id) pair so the caller (product-tag handler)
// can use them to decorate the tag or downstream events.
//
// Fail mode is CLOSED — if monetization-service is unreachable, tag
// creation is rejected. This matches the H4-shaped "money path stays
// honest even during incidents" principle: a creator tagging a product
// they don't own is a real fraud vector (commission attribution).
//
// Endpoint: GET /v1/monetization/affiliate/links/:linkId
//   200 with body → check creator_id
//   404           → ErrAffiliateLinkNotFound
//   other         → wrapped error
//
// Reuses the same internal-key + httpClient plumbing the membership
// gate already established.
func (s *Service) ValidateAffiliateLink(
	ctx context.Context,
	linkID uuid.UUID,
	callerID uuid.UUID,
) (creatorID, listingID uuid.UUID, err error) {
	// Redis hot-path cache. A creator bulk-tagging 50 reels with the
	// same affiliate link would otherwise hit monetization-service 50×
	// in a tight loop. Negative results aren't cached — the validator
	// is the fraud gate, and a creator fixing their link (re-activating
	// it) should be effective immediately.
	if cached, ok := s.lookupValidatorCache(ctx, linkID, callerID); ok {
		return cached.CreatorID, cached.ListingID, nil
	}

	if s.monetizationServiceURL == "" {
		return uuid.Nil, uuid.Nil, errors.New("MONETIZATION_URL_UNSET")
	}

	endpoint := fmt.Sprintf("%s/v1/monetization/affiliate/links/%s",
		s.monetizationServiceURL, linkID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("affiliate validator: build request: %w", err)
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	// Forward the caller as X-User-Id for monetization-service's own
	// audit trail. Validator endpoint doesn't gate on this — it gates
	// on the internal-key — but tracing the right user matters.
	req.Header.Set("X-User-Id", callerID.String())

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("affiliate validator: monetization unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return uuid.Nil, uuid.Nil, ErrAffiliateLinkNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return uuid.Nil, uuid.Nil, fmt.Errorf("affiliate validator: monetization status %d", resp.StatusCode)
	}

	var envelope struct {
		Data struct {
			ID        uuid.UUID `json:"id"`
			CreatorID uuid.UUID `json:"creator_id"`
			ListingID uuid.UUID `json:"listing_id"`
			IsActive  bool      `json:"is_active"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("affiliate validator: decode: %w", err)
	}

	if !envelope.Data.IsActive {
		return uuid.Nil, uuid.Nil, ErrAffiliateLinkInactive
	}
	if envelope.Data.CreatorID != callerID {
		return uuid.Nil, uuid.Nil, ErrAffiliateLinkNotOwned
	}

	// Cache the positive result. Negative cases (not found / inactive /
	// not owned) are NOT cached — see the comment at the top.
	s.storeValidatorCache(ctx, linkID, callerID, envelope.Data.CreatorID, envelope.Data.ListingID)
	return envelope.Data.CreatorID, envelope.Data.ListingID, nil
}

type validatorCacheEntry struct {
	CreatorID uuid.UUID `json:"creator_id"`
	ListingID uuid.UUID `json:"listing_id"`
}

func validatorCacheKey(linkID, callerID uuid.UUID) string {
	return fmt.Sprintf("affiliate_validator:%s:%s", linkID, callerID)
}

func (s *Service) lookupValidatorCache(
	ctx context.Context,
	linkID, callerID uuid.UUID,
) (validatorCacheEntry, bool) {
	if s.rdb == nil {
		return validatorCacheEntry{}, false
	}
	raw, err := s.rdb.Get(ctx, validatorCacheKey(linkID, callerID)).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Debug("affiliate validator cache lookup error",
				"link_id", linkID, "caller_id", callerID, "err", err)
		}
		return validatorCacheEntry{}, false
	}
	var entry validatorCacheEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		// Malformed entry — refuse to use it, surface as cache miss
		// so the validator re-runs and overwrites with a clean value.
		return validatorCacheEntry{}, false
	}
	return entry, true
}

func (s *Service) storeValidatorCache(
	ctx context.Context,
	linkID, callerID, creatorID, listingID uuid.UUID,
) {
	if s.rdb == nil {
		return
	}
	payload, err := json.Marshal(validatorCacheEntry{
		CreatorID: creatorID,
		ListingID: listingID,
	})
	if err != nil {
		// Marshalling two UUIDs can't realistically fail; if it does,
		// not caching is safe.
		return
	}
	if err := s.rdb.Set(ctx, validatorCacheKey(linkID, callerID),
		string(payload), affiliateValidatorCacheTTL).Err(); err != nil {
		slog.Debug("affiliate validator cache set error",
			"link_id", linkID, "caller_id", callerID, "err", err)
	}
}

// InvalidateAffiliateValidatorCache lets the monetization-event consumer
// drop a stale entry when a link is deactivated, transferred, or the
// underlying listing is unpublished. Wired by the
// monetization.affiliate.link_changed event consumer (TODO).
func (s *Service) InvalidateAffiliateValidatorCache(
	ctx context.Context,
	linkID uuid.UUID,
) {
	if s.rdb == nil {
		return
	}
	// SCAN-MATCH on the prefix — there can be many callers per link.
	pattern := fmt.Sprintf("affiliate_validator:%s:*", linkID)
	iter := s.rdb.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		_ = s.rdb.Del(ctx, iter.Val()).Err()
	}
}

// Errors the handler maps:
//   ErrAffiliateLinkNotFound  → 404
//   ErrAffiliateLinkInactive  → 410 Gone
//   ErrAffiliateLinkNotOwned  → 403
var (
	ErrAffiliateLinkNotFound = errors.New("affiliate link not found")
	ErrAffiliateLinkInactive = errors.New("affiliate link is inactive")
	ErrAffiliateLinkNotOwned = errors.New("affiliate link is owned by a different creator")
)
