package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

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
	return envelope.Data.CreatorID, envelope.Data.ListingID, nil
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
