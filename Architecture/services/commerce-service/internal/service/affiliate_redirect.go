package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"
)

// AffiliateRedirect resolves an affiliate link UUID to the canonical
// product URL the click should land on, embedding the affiliate code
// in the query string so the eventual order can attribute commission
// to the right link.
//
// Flow
//
//	monetization-service GET /v1/monetization/affiliate/links/:linkId
//	  → { link_code, listing_id, is_active }
//	commerce-service local lookup of the product by listing_id
//	  → product.slug
//	→ "/c/<slug>?via=<link_code>"
//
// monetization-service owns the affiliate link + code + commission
// math; commerce-service owns the product catalog + URL schema. Each
// side stays in charge of its own surface; the redirect handler is
// just the seam between them.

// ResolveAffiliateRedirect is invoked by the public redirect endpoint.
// Returns the URL the caller should 302 to.
//
// Errors map to HTTP at the handler layer:
//   ErrAffiliateRedirectLinkNotFound  → 404
//   ErrAffiliateRedirectLinkInactive  → 410 Gone
//   ErrAffiliateRedirectProductMissing → 404 (link exists but the
//                                        product was unpublished /
//                                        deleted — surface as the
//                                        same 404 as a missing link,
//                                        no need to leak the
//                                        difference to the public).
func (s *Service) ResolveAffiliateRedirect(
	ctx context.Context,
	linkID uuid.UUID,
) (string, error) {
	if s.monetizationServiceURL == "" {
		return "", errors.New("MONETIZATION_URL_UNSET")
	}

	link, err := s.fetchAffiliateLinkFromMonetization(ctx, linkID)
	if err != nil {
		return "", err
	}

	product, err := s.store.GetProductByID(ctx, link.ListingID)
	if err != nil {
		return "", fmt.Errorf("affiliate redirect: lookup product: %w", err)
	}
	if product == nil {
		return "", ErrAffiliateRedirectProductMissing
	}

	// Anchor on a relative URL so the redirect honours whatever the
	// public host is (CloudFront, dev tunnel, localhost). Web maps
	// /products/<id> to the existing product page; mobile resolves
	// the same path via its deep-link table. via= carries the
	// affiliate code through the checkout flow for commission
	// attribution.
	v := url.Values{}
	v.Set("via", link.LinkCode)
	return fmt.Sprintf("/products/%s?%s", product.ID, v.Encode()), nil
}

type monetizationAffiliateLink struct {
	ID        uuid.UUID `json:"id"`
	LinkCode  string    `json:"link_code"`
	ListingID uuid.UUID `json:"listing_id"`
	IsActive  bool      `json:"is_active"`
}

func (s *Service) fetchAffiliateLinkFromMonetization(
	ctx context.Context,
	linkID uuid.UUID,
) (*monetizationAffiliateLink, error) {
	endpoint := fmt.Sprintf("%s/v1/monetization/affiliate/links/%s",
		s.monetizationServiceURL, linkID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("affiliate redirect: build request: %w", err)
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
		return nil, fmt.Errorf("affiliate redirect: monetization unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrAffiliateRedirectLinkNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("affiliate redirect: monetization status %d", resp.StatusCode)
	}

	var envelope struct {
		Data monetizationAffiliateLink `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("affiliate redirect: decode: %w", err)
	}
	if !envelope.Data.IsActive {
		return nil, ErrAffiliateRedirectLinkInactive
	}
	return &envelope.Data, nil
}

var (
	ErrAffiliateRedirectLinkNotFound   = errors.New("affiliate link not found")
	ErrAffiliateRedirectLinkInactive   = errors.New("affiliate link is inactive")
	ErrAffiliateRedirectProductMissing = errors.New("affiliate link target product missing")
)
