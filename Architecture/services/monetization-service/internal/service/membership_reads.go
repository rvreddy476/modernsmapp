package service

// The membership read routes (23 Oct 2026):
//
//   GET /memberships                 the caller's memberships, each naming its
//                                    creator {id, username, display_name, avatar_url}
//   GET /memberships/:subscriptionId one of them, plus the tier's perks and a
//                                    scheduled downgrade (the caller's own only;
//                                    anyone else's, and an unknown id, is 404)
//   GET /members?cursor=&limit=      the CALLER's members, as their creator
//
// Profiles come from identity-profile's batch read, one call per answer, and
// are best-effort: a lookup that fails (or no source configured) leaves the
// person as {id} and the route still answers. A member row carries only what
// that member pays THIS creator; nothing about their other memberships.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	// ErrMembershipNotFound: no such subscription, or not the caller's — 404.
	ErrMembershipNotFound = errors.New("MEMBERSHIP_NOT_FOUND")
	// ErrInvalidMembersCursor: a members-list cursor this service did not mint — 400.
	ErrInvalidMembersCursor = errors.New("INVALID_CURSOR")
)

// Members list page size.
const (
	DefaultMembersPageSize = 20
	MaxMembersPageSize     = 50
)

// PublicProfile is the public face of an account as identity-profile's batch
// read answers it. Any field may be empty.
type PublicProfile struct {
	Username    string
	DisplayName string
	AvatarURL   string
}

// ProfileSource answers public profiles for a set of accounts; an id it does
// not return is an account it does not show (deleted, hidden).
type ProfileSource interface {
	Profiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]PublicProfile, error)
}

// WithProfiles installs the profile source the membership reads name people
// with. nil leaves every person as {id}.
func (s *Service) WithProfiles(p ProfileSource) *Service {
	s.profiles = p
	return s
}

// profilesFor is one best-effort batch lookup: nil on any failure.
func (s *Service) profilesFor(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]PublicProfile {
	if s.profiles == nil || len(ids) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	uniq := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	out, err := s.profiles.Profiles(ctx, uniq)
	if err != nil {
		slog.WarnContext(ctx, "memberships: profile lookup failed; answering ids only", "count", len(uniq), "error", err)
		return nil
	}
	return out
}

// MembershipPerson is a creator or a member as the membership reads name
// them: the id always, the profile fields when the lookup answered.
type MembershipPerson struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
}

// MembershipCreator is the creator of a membership.
type MembershipCreator = MembershipPerson

func personOf(id uuid.UUID, profiles map[uuid.UUID]PublicProfile) MembershipPerson {
	p := MembershipPerson{ID: id}
	if prof, ok := profiles[id]; ok {
		p.Username, p.DisplayName, p.AvatarURL = prof.Username, prof.DisplayName, prof.AvatarURL
	}
	return p
}

// MembershipView is one row of GET /memberships.
type MembershipView struct {
	SubscriptionID   uuid.UUID          `json:"subscription_id"`
	Creator          MembershipCreator  `json:"creator"`
	Tier             MembershipTierView `json:"tier"`
	Status           string             `json:"status"`
	CurrentPeriodEnd time.Time          `json:"current_period_end"`
	AccessUntil      *time.Time         `json:"access_until"`
	RenewAvailable   bool               `json:"renew_available"`
}

// MembershipTierView is the tier a membership is on, at its locked price.
type MembershipTierView struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	PricePaise int64     `json:"price_paise"`
}

func accessUntilOf(sub *postgres.Subscription) *time.Time {
	if until, ok := SubscriptionAccessUntil(sub); ok {
		u := until
		return &u
	}
	return nil
}

func membershipViewOf(sub *postgres.Subscription, profiles map[uuid.UUID]PublicProfile, now time.Time) MembershipView {
	return MembershipView{
		SubscriptionID: sub.ID, Creator: personOf(sub.CreatorID, profiles),
		Tier:   MembershipTierView{ID: sub.TierID, Name: sub.TierName, PricePaise: sub.PricePaise},
		Status: sub.Status, CurrentPeriodEnd: sub.CurrentPeriodEnd,
		AccessUntil:    accessUntilOf(sub),
		RenewAvailable: membership.CanRenew(sub.Status, sub.ActivatedAt != nil, sub.CurrentPeriodEnd, now),
	}
}

// ListMyMemberships returns the caller's memberships: live, pending, and
// ended ones still inside the 30-day renewal window.
func (s *Service) ListMyMemberships(ctx context.Context, subscriberID uuid.UUID) ([]MembershipView, error) {
	now := s.clock()
	subs, err := s.members.ListSubscriberMemberships(ctx, subscriberID, now.Add(-membership.RenewAfterExpiryWindow))
	if err != nil {
		return nil, err
	}
	creators := make([]uuid.UUID, 0, len(subs))
	for i := range subs {
		creators = append(creators, subs[i].CreatorID)
	}
	profiles := s.profilesFor(ctx, creators)
	out := make([]MembershipView, 0, len(subs))
	for i := range subs {
		out = append(out, membershipViewOf(&subs[i], profiles, now))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// One membership
// ---------------------------------------------------------------------------

// MembershipDetailTier is the tier with its perks.
type MembershipDetailTier struct {
	MembershipTierView
	Perks json.RawMessage `json:"perks"`
}

// PendingDowngrade is a downgrade scheduled for the next period.
type PendingDowngrade struct {
	TierID      uuid.UUID `json:"tier_id"`
	TierName    string    `json:"tier_name"`
	PricePaise  int64     `json:"price_paise"`
	EffectiveAt time.Time `json:"effective_at"`
}

// MembershipDetailView is GET /memberships/:subscriptionId: a list row, the
// tier's perks, and the scheduled downgrade (null when none).
type MembershipDetailView struct {
	SubscriptionID   uuid.UUID            `json:"subscription_id"`
	Creator          MembershipCreator    `json:"creator"`
	Tier             MembershipDetailTier `json:"tier"`
	Status           string               `json:"status"`
	CurrentPeriodEnd time.Time            `json:"current_period_end"`
	AccessUntil      *time.Time           `json:"access_until"`
	RenewAvailable   bool                 `json:"renew_available"`
	PendingDowngrade *PendingDowngrade    `json:"pending_downgrade"`
}

// perksOf is a tier's perks as a JSON array ([] for none or a missing tier).
func perksOf(t *postgres.CreatorTier) json.RawMessage {
	if t == nil {
		return json.RawMessage(`[]`)
	}
	raw := strings.TrimSpace(string(t.Perks))
	if raw == "" || raw == "null" || !strings.HasPrefix(raw, "[") {
		return json.RawMessage(`[]`)
	}
	return json.RawMessage(raw)
}

// GetMyMembership returns one of the caller's memberships. Someone else's
// and an unknown id are both ErrMembershipNotFound: the answer never says a
// subscription exists.
func (s *Service) GetMyMembership(ctx context.Context, subscriberID, subscriptionID uuid.UUID) (*MembershipDetailView, error) {
	sub, err := s.members.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub == nil || sub.SubscriberID != subscriberID {
		return nil, ErrMembershipNotFound
	}
	now := s.clock()
	row := membershipViewOf(sub, s.profilesFor(ctx, []uuid.UUID{sub.CreatorID}), now)
	tier, err := s.members.GetCreatorTier(ctx, sub.TierID)
	if err != nil {
		return nil, err
	}
	out := &MembershipDetailView{
		SubscriptionID: row.SubscriptionID, Creator: row.Creator,
		Tier:   MembershipDetailTier{MembershipTierView: row.Tier, Perks: perksOf(tier)},
		Status: row.Status, CurrentPeriodEnd: row.CurrentPeriodEnd, AccessUntil: row.AccessUntil,
		RenewAvailable: row.RenewAvailable,
	}
	if sub.PendingTierID != nil {
		d := &PendingDowngrade{TierID: *sub.PendingTierID, EffectiveAt: sub.CurrentPeriodEnd}
		if sub.PendingPricePaise != nil {
			d.PricePaise = *sub.PendingPricePaise
		}
		pending, err := s.members.GetCreatorTier(ctx, *sub.PendingTierID)
		if err != nil {
			return nil, err
		}
		if pending != nil {
			d.TierName = pending.Name
			if sub.PendingPricePaise == nil {
				d.PricePaise = pending.PricePaise
			}
		}
		out.PendingDowngrade = d
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// A creator's members
// ---------------------------------------------------------------------------

// MemberView is one row of GET /members: who, on which of the creator's
// tiers at the price they pay this creator, since when, and until when.
type MemberView struct {
	SubscriptionID uuid.UUID          `json:"subscription_id"`
	Subscriber     MembershipPerson   `json:"subscriber"`
	Tier           MembershipTierView `json:"tier"`
	Status         string             `json:"status"`
	Since          time.Time          `json:"since"`
	AccessUntil    *time.Time         `json:"access_until"`
}

func encodeMembersCursor(c postgres.MemberCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.ActivatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()))
}

func decodeMembersCursor(raw string) (*postgres.MemberCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidMembersCursor
	}
	at, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, ErrInvalidMembersCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, ErrInvalidMembersCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, ErrInvalidMembersCursor
	}
	return &postgres.MemberCursor{ActivatedAt: t, ID: u}, nil
}

// isMemberOf is the members-list rule for one row, applied again here over
// whatever the store answered: the creator's own, paid for, still live.
func isMemberOf(sub *postgres.Subscription, creatorID uuid.UUID) bool {
	return sub.CreatorID == creatorID && sub.ActivatedAt != nil && postgres.IsLiveSubscriptionStatus(sub.Status)
}

// ListCreatorMembers returns one page of the creator's members, newest
// first, and the cursor of the next page ("" on the last).
func (s *Service) ListCreatorMembers(ctx context.Context, creatorID uuid.UUID, cursor string, limit int) ([]MemberView, string, error) {
	if limit <= 0 {
		limit = DefaultMembersPageSize
	}
	if limit > MaxMembersPageSize {
		limit = MaxMembersPageSize
	}
	var after *postgres.MemberCursor
	if strings.TrimSpace(cursor) != "" {
		c, err := decodeMembersCursor(strings.TrimSpace(cursor))
		if err != nil {
			return nil, "", err
		}
		after = c
	}
	subs, err := s.members.ListCreatorMembers(ctx, creatorID, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(subs) > limit {
		subs = subs[:limit]
		last := subs[limit-1]
		if last.ActivatedAt != nil {
			next = encodeMembersCursor(postgres.MemberCursor{ActivatedAt: *last.ActivatedAt, ID: last.ID})
		}
	}
	rows := make([]*postgres.Subscription, 0, len(subs))
	ids := make([]uuid.UUID, 0, len(subs))
	for i := range subs {
		if !isMemberOf(&subs[i], creatorID) {
			continue
		}
		rows = append(rows, &subs[i])
		ids = append(ids, subs[i].SubscriberID)
	}
	profiles := s.profilesFor(ctx, ids)
	out := make([]MemberView, 0, len(rows))
	for _, sub := range rows {
		out = append(out, MemberView{
			SubscriptionID: sub.ID, Subscriber: personOf(sub.SubscriberID, profiles),
			Tier:   MembershipTierView{ID: sub.TierID, Name: sub.TierName, PricePaise: sub.PricePaise},
			Status: sub.Status, Since: *sub.ActivatedAt, AccessUntil: accessUntilOf(sub),
		})
	}
	return out, next, nil
}
