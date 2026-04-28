package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tier 3c — Membership gating.
//
// The post-service holds the column (`tier_required_id`) but does not
// own subscription state, so it asks monetization-service whether the
// caller is entitled to read a gated post. On failure the gate is
// closed (fail-secure): if monetization is down, the body is redacted
// rather than leaking through.

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
	return envelope.Data.Allowed, envelope.Data.Reason, nil
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
