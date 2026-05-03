// Vouch service — eligibility checks, anti-spam, and Kafka emission.
//
// Eligibility (spec §15 vouching):
//   - relationship=friend           → mutual-follow on graph-service
//   - relationship=community_member → both users share a community
//   - relationship=colleague|family → no graph proof; user attests
//
// Anti-spam: a single voucher may emit at most 5 vouch *requests* per
// rolling 7-day window. Counter is enforced before insert.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// MaxVouchRequestsPerWeek is the spec §15 anti-spam ceiling.
const MaxVouchRequestsPerWeek = 5

// MaxVouchesDisplayedPerProfile is the spec-required display cap on a
// profile card (top N by recency × voucher trust score).
const MaxVouchesDisplayedPerProfile = 3

// GraphServiceClient is the read-only client used to verify mutual-follow
// for relationship='friend'. Tests inject a stub.
type GraphServiceClient interface {
	IsMutualFollow(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// CommunityServiceClient verifies shared community membership for
// relationship='community_member'.
type CommunityServiceClient interface {
	UsersShareCommunity(ctx context.Context, a, b uuid.UUID, communityID uuid.UUID) (bool, error)
}

// SetGraphServiceClient injects the graph client.
func (s *Service) SetGraphServiceClient(c GraphServiceClient) { s.graphServiceClient = c }

// SetCommunityServiceClient injects the community client.
func (s *Service) SetCommunityServiceClient(c CommunityServiceClient) { s.communityClient = c }

// RequestVouch validates eligibility, enforces the weekly rate, persists,
// and emits dating.vouch.requested.
func (s *Service) RequestVouch(ctx context.Context, voucherID, voucheeID uuid.UUID, relationship string, communityID *uuid.UUID, note string) (*store.Vouch, error) {
	if voucherID == uuid.Nil || voucheeID == uuid.Nil {
		return nil, fmt.Errorf("invalid: voucher and vouchee ids required")
	}
	if voucherID == voucheeID {
		return nil, fmt.Errorf("invalid: cannot vouch for yourself")
	}
	switch relationship {
	case "friend", "community_member", "colleague", "family":
	default:
		return nil, fmt.Errorf("invalid: relationship must be friend|community_member|colleague|family")
	}

	// Eligibility checks (graph proofs).
	switch relationship {
	case "friend":
		if s.graphServiceClient == nil {
			return nil, fmt.Errorf("graph service not configured")
		}
		ok, err := s.graphServiceClient.IsMutualFollow(ctx, voucherID, voucheeID)
		if err != nil {
			return nil, fmt.Errorf("graph mutual-follow check: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("forbidden: not mutual followers")
		}
	case "community_member":
		if communityID == nil || *communityID == uuid.Nil {
			return nil, fmt.Errorf("invalid: community_id required for community_member relationship")
		}
		if s.communityClient == nil {
			return nil, fmt.Errorf("community service not configured")
		}
		ok, err := s.communityClient.UsersShareCommunity(ctx, voucherID, voucheeID, *communityID)
		if err != nil {
			return nil, fmt.Errorf("community membership check: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("forbidden: users do not share that community")
		}
	}

	// Anti-spam: count this week's requests by voucher.
	count, err := s.store.CountVouchRequestsThisWeek(ctx, voucherID)
	if err != nil {
		return nil, err
	}
	if count >= MaxVouchRequestsPerWeek {
		return nil, fmt.Errorf("forbidden: weekly vouch request limit (%d) reached", MaxVouchRequestsPerWeek)
	}

	v, err := s.store.CreateVouchRequest(ctx, voucherID, voucheeID, relationship, communityID, note)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		if perr := s.producer.PublishVouchRequested(ctx, v.ID, voucherID, voucheeID, relationship, communityID); perr != nil {
			slog.Warn("publish vouch.requested failed", "vouch_id", v.ID, "error", perr)
		}
	}
	return v, nil
}

// AcceptVouch transitions a pending vouch to accepted. Only the vouchee
// (the auth user passed by the handler) may accept.
func (s *Service) AcceptVouch(ctx context.Context, vouchID, voucheeID uuid.UUID) error {
	v, err := s.store.GetVouch(ctx, vouchID)
	if err != nil {
		return err
	}
	if v.VoucheeID != voucheeID {
		return fmt.Errorf("forbidden: only the vouchee may accept")
	}
	if v.Status != "pending" {
		return fmt.Errorf("invalid: vouch is not pending")
	}
	if err := s.store.DecideVouch(ctx, vouchID, voucheeID, "accepted"); err != nil {
		return err
	}
	if s.producer != nil {
		if perr := s.producer.PublishVouchAccepted(ctx, vouchID, v.VoucherID, voucheeID); perr != nil {
			slog.Warn("publish vouch.accepted failed", "vouch_id", vouchID, "error", perr)
		}
	}
	return nil
}

// DeclineVouch transitions a pending vouch to declined.
func (s *Service) DeclineVouch(ctx context.Context, vouchID, voucheeID uuid.UUID) error {
	v, err := s.store.GetVouch(ctx, vouchID)
	if err != nil {
		return err
	}
	if v.VoucheeID != voucheeID {
		return fmt.Errorf("forbidden: only the vouchee may decline")
	}
	if v.Status != "pending" {
		return fmt.Errorf("invalid: vouch is not pending")
	}
	if err := s.store.DecideVouch(ctx, vouchID, voucheeID, "declined"); err != nil {
		return err
	}
	if s.producer != nil {
		if perr := s.producer.PublishVouchDeclined(ctx, vouchID, v.VoucherID, voucheeID); perr != nil {
			slog.Warn("publish vouch.declined failed", "vouch_id", vouchID, "error", perr)
		}
	}
	return nil
}

// RevokeVouch transitions a vouch to revoked. Only the original voucher
// may revoke.
func (s *Service) RevokeVouch(ctx context.Context, vouchID, voucherID uuid.UUID) error {
	v, err := s.store.GetVouch(ctx, vouchID)
	if err != nil {
		return err
	}
	if v.VoucherID != voucherID {
		return fmt.Errorf("forbidden: only the voucher may revoke")
	}
	if err := s.store.RevokeVouch(ctx, vouchID, voucherID); err != nil {
		return err
	}
	if s.producer != nil {
		if perr := s.producer.PublishVouchRevoked(ctx, vouchID, voucherID, v.VoucheeID); perr != nil {
			slog.Warn("publish vouch.revoked failed", "vouch_id", vouchID, "error", perr)
		}
	}
	return nil
}

// ListVouchesFor returns at most MaxVouchesDisplayedPerProfile vouches for
// public display, ordered by recency. We sort defensively even though the
// store already orders by created_at — display-cap logic might evolve to
// factor in trust score later.
func (s *Service) ListVouchesFor(ctx context.Context, voucheeID uuid.UUID, status string) ([]*store.Vouch, error) {
	vouches, err := s.store.ListVouchesFor(ctx, voucheeID, status)
	if err != nil {
		return nil, err
	}
	if status == "accepted" && len(vouches) > MaxVouchesDisplayedPerProfile {
		// Recency-only ordering. (Trust-score weighting will land in S6.)
		sort.SliceStable(vouches, func(i, j int) bool {
			return vouches[i].CreatedAt.After(vouches[j].CreatedAt)
		})
		vouches = vouches[:MaxVouchesDisplayedPerProfile]
	}
	return vouches, nil
}

// ListVouchesSent is a pass-through.
func (s *Service) ListVouchesSent(ctx context.Context, voucherID uuid.UUID) ([]*store.Vouch, error) {
	return s.store.ListVouchesSent(ctx, voucherID)
}

// httpGraphServiceClient calls graph-service /v1/graph/follows/mutual.
type httpGraphServiceClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPGraphServiceClient configures from GRAPH_SERVICE_URL.
func NewHTTPGraphServiceClient() GraphServiceClient {
	base := os.Getenv("GRAPH_SERVICE_URL")
	if base == "" {
		base = "http://graph-service:8108"
	}
	return &httpGraphServiceClient{baseURL: base, client: &http.Client{Timeout: 3 * time.Second}}
}

// IsMutualFollow asks graph-service whether a follows b AND b follows a.
// On any error we deny — failing closed on a safety/eligibility-adjacent
// signal is the right default (rule #6).
func (c *httpGraphServiceClient) IsMutualFollow(ctx context.Context, a, b uuid.UUID) (bool, error) {
	url := fmt.Sprintf("%s/v1/graph/follows/mutual?a=%s&b=%s", c.baseURL, a.String(), b.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("graph-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("graph-service status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Mutual bool `json:"mutual"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, fmt.Errorf("decode mutual: %w", err)
	}
	return envelope.Data.Mutual, nil
}

// httpCommunityServiceClient verifies shared community membership.
type httpCommunityServiceClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPCommunityServiceClient configures from COMMUNITY_SERVICE_URL.
func NewHTTPCommunityServiceClient() CommunityServiceClient {
	base := os.Getenv("COMMUNITY_SERVICE_URL")
	if base == "" {
		base = "http://community-service:8109"
	}
	return &httpCommunityServiceClient{baseURL: base, client: &http.Client{Timeout: 3 * time.Second}}
}

// UsersShareCommunity reads /v1/communities/:id/members?ids=a,b and returns
// true if both ids appear. We pass user identity via X-User-ID for the
// caller side; the server-to-server inspection uses a service auth header.
func (c *httpCommunityServiceClient) UsersShareCommunity(ctx context.Context, a, b uuid.UUID, communityID uuid.UUID) (bool, error) {
	url := fmt.Sprintf("%s/v1/communities/%s/members?ids=%s,%s", c.baseURL, communityID.String(), a.String(), b.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Key", key)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("community-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("community-service status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Members []struct {
				UserID string `json:"user_id"`
			} `json:"members"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, fmt.Errorf("decode members: %w", err)
	}
	seenA, seenB := false, false
	aStr, bStr := a.String(), b.String()
	for _, m := range envelope.Data.Members {
		if m.UserID == aStr {
			seenA = true
		}
		if m.UserID == bStr {
			seenB = true
		}
	}
	return seenA && seenB, nil
}

