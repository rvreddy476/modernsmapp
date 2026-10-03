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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

	// Lane D3: no vouch across a block, refused before any graph call.
	if err := s.requireNotBlocked(ctx, voucherID, voucheeID); err != nil {
		return nil, err
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
	if err := s.vouchVisible(ctx, v); err != nil {
		return err
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
	if err := s.vouchVisible(ctx, v); err != nil {
		return err
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

// vouchVisible returns store.ErrVouchNotFound when voucher and vouchee are
// blocked either way, so a blocked vouch cannot be acted on.
func (s *Service) vouchVisible(ctx context.Context, v *store.Vouch) error {
	if err := s.requireNotBlocked(ctx, v.VoucherID, v.VoucheeID); err != nil {
		if errors.Is(err, ErrCandidateUnavailable) {
			return store.ErrVouchNotFound
		}
		return err
	}
	return nil
}

// ListVouchesFor returns at most MaxVouchesDisplayedPerProfile vouches for
// public display, ordered by recency, as viewerID may see them (uuid.Nil
// for no viewer). Vouches across a block, from a deleted or suspended
// voucher, or involving a user blocked with the viewer are left out. We
// sort defensively even though the store already orders by created_at —
// display-cap logic might evolve to factor in trust score later.
func (s *Service) ListVouchesFor(ctx context.Context, viewerID, voucheeID uuid.UUID, status string) ([]*store.Vouch, error) {
	vouches, err := s.store.ListVouchesForViewer(ctx, viewerID, voucheeID, status)
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

// ListVouchesSent returns the caller's sent vouches, leaving out vouchees
// blocked either way or with a deleted or suspended profile.
func (s *Service) ListVouchesSent(ctx context.Context, voucherID uuid.UUID) ([]*store.Vouch, error) {
	return s.store.ListVouchesSentVisible(ctx, voucherID)
}

// httpGraphServiceClient reads graph-service /v1/graph/relationship.
type httpGraphServiceClient struct {
	baseURL string
	key     string
	client  *http.Client
}

// NewHTTPGraphServiceClient reads graph-service's relationship route with
// the internal key (no user identity), like NewHTTPConnectionChecker. An
// empty baseURL falls back to the docker-compose address.
func NewHTTPGraphServiceClient(baseURL, internalKey string, c *http.Client) GraphServiceClient {
	if baseURL == "" {
		baseURL = "http://graph-service:8083"
	}
	if c == nil {
		c = &http.Client{Timeout: 3 * time.Second}
	}
	return &httpGraphServiceClient{baseURL: strings.TrimRight(baseURL, "/"), key: internalKey, client: c}
}

// IsMutualFollow asks graph-service whether a follows b AND b follows a,
// with no block in either direction. Any non-2xx is an error, never a quiet
// "not mutual": a wrong route or a missing key must not read as an answer.
func (c *httpGraphServiceClient) IsMutualFollow(ctx context.Context, a, b uuid.UUID) (bool, error) {
	q := url.Values{"user_id": {a.String()}, "other_id": {b.String()}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/graph/relationship?"+q.Encode(), nil)
	if err != nil {
		return false, fmt.Errorf("build relationship request: %w", err)
	}
	if c.key != "" {
		req.Header.Set(internalKeyHeader, c.key)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("graph-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("graph-service relationship status %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			Follows    bool `json:"follows"`
			FollowedBy bool `json:"followed_by"`
			Blocked    bool `json:"blocked"`
			BlockedBy  bool `json:"blocked_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&env); err != nil {
		return false, fmt.Errorf("decode relationship: %w", err)
	}
	d := env.Data
	return d.Follows && d.FollowedBy && !d.Blocked && !d.BlockedBy, nil
}

// community-service pages /v1/communities/my at most 100 per request.
const (
	communityPageSize = 100
	communityMaxPages = 10
)

// httpCommunityServiceClient verifies shared community membership.
type httpCommunityServiceClient struct {
	baseURL string
	key     string
	client  *http.Client
}

// NewHTTPCommunityServiceClient reads community-service with the internal
// key. An empty baseURL falls back to the docker-compose address.
func NewHTTPCommunityServiceClient(baseURL, internalKey string, c *http.Client) CommunityServiceClient {
	if baseURL == "" {
		baseURL = "http://community-service:8107"
	}
	if c == nil {
		c = &http.Client{Timeout: 3 * time.Second}
	}
	return &httpCommunityServiceClient{baseURL: strings.TrimRight(baseURL, "/"), key: internalKey, client: c}
}

// UsersShareCommunity reports whether a and b are both active members of
// communityID. community-service has no per-user membership read, so this
// walks each user's GET /v1/communities/my (X-User-Id names the user; the
// list already excludes banned and pending members). Any non-2xx is an
// error, never a quiet "not a member".
func (c *httpCommunityServiceClient) UsersShareCommunity(ctx context.Context, a, b uuid.UUID, communityID uuid.UUID) (bool, error) {
	for _, u := range []uuid.UUID{a, b} {
		ok, err := c.isMember(ctx, u, communityID)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (c *httpCommunityServiceClient) isMember(ctx context.Context, userID, communityID uuid.UUID) (bool, error) {
	want := communityID.String()
	for page := 0; page < communityMaxPages; page++ {
		ids, err := c.myCommunities(ctx, userID, page*communityPageSize)
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			if id == want {
				return true, nil
			}
		}
		if len(ids) < communityPageSize {
			return false, nil
		}
	}
	return false, nil
}

func (c *httpCommunityServiceClient) myCommunities(ctx context.Context, userID uuid.UUID, offset int) ([]string, error) {
	q := url.Values{"limit": {strconv.Itoa(communityPageSize)}, "offset": {strconv.Itoa(offset)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/communities/my?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build communities request: %w", err)
	}
	req.Header.Set("X-User-Id", userID.String())
	if c.key != "" {
		req.Header.Set(internalKeyHeader, c.key)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("community-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("community-service communities status %d", resp.StatusCode)
	}
	var env struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode communities: %w", err)
	}
	ids := make([]string, 0, len(env.Data))
	for _, d := range env.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}
