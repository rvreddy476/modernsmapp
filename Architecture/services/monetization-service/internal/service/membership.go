package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Creator memberships: tiers, subscriptions and the entitlement they grant.
//
// Everything here reads and writes through MembershipStore, which
// *postgres.Store satisfies, so the rules can be unit-tested against an
// in-memory store (internal/service/membershiptest).

// MembershipStore is the storage the membership rules use.
type MembershipStore interface {
	GetCreatorTiers(ctx context.Context, creatorID uuid.UUID) ([]postgres.CreatorTier, error)
	GetCreatorTier(ctx context.Context, tierID uuid.UUID) (*postgres.CreatorTier, error)
	CreateTierLimited(ctx context.Context, t *postgres.CreatorTier, maxActive int) error
	UpdateTierPartial(ctx context.Context, creatorID, tierID uuid.UUID, p postgres.TierPatch, maxActive int) (*postgres.CreatorTier, error)

	EnsureWallet(ctx context.Context, userID uuid.UUID) (*postgres.Wallet, error)

	GetLiveSubscription(ctx context.Context, subscriberID, creatorID uuid.UUID) (*postgres.Subscription, error)
	GetSubscriptionByIdempotencyKey(ctx context.Context, subscriberID uuid.UUID, key string) (*postgres.Subscription, error)
	GetSubscriptionByID(ctx context.Context, subID uuid.UUID) (*postgres.Subscription, error)
	Subscribe(ctx context.Context, subscriberID, creatorID, tierID uuid.UUID, tierName string, pricePaise int64, currency string, idempotencyKey string) (*postgres.Subscription, error)
	Unsubscribe(ctx context.Context, subscriberID, creatorID uuid.UUID) (*postgres.Subscription, error)
	SetSubscriptionStatus(ctx context.Context, subID uuid.UUID, status string) error
	PauseSubscription(ctx context.Context, subID uuid.UUID, pauseUntil time.Time) error
	ResumeSubscription(ctx context.Context, subID uuid.UUID) error
	CancelAtPeriodEnd(ctx context.Context, subID uuid.UUID, reason string) error
	UpgradeSubscription(ctx context.Context, in postgres.UpgradeInput) error
	SetPendingTier(ctx context.Context, subID, expectedTierID uuid.UUID, pendingTierID *uuid.UUID, pendingPricePaise *int64) error
	InsertSubscriptionEvent(ctx context.Context, subID uuid.UUID, eventType, oldStatus, newStatus string, metadata json.RawMessage) error
	GetSubscriptionEvents(ctx context.Context, subID uuid.UUID, limit, offset int) ([]postgres.SubscriptionEvent, error)
}

// WithMembershipStore replaces the store the membership rules use. Tests
// only; production uses the *postgres.Store passed to New.
func (s *Service) WithMembershipStore(m MembershipStore) *Service {
	s.members = m
	return s
}

// WithClock replaces time.Now for the membership rules. Tests only.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Entitlement actions published on entitlement.changed. post-service drops
// its cached answer for the (subscriber, creator) pair on any of them; the
// action is for logs.
const (
	EntitlementGranted       = "granted"
	EntitlementRevoked       = "revoked"
	EntitlementPaused        = "paused"
	EntitlementResumed       = "resumed"
	EntitlementUpgraded      = "upgraded"
	EntitlementCancelPlanned = "cancel_scheduled"
)

// ---------------------------------------------------------------------------
// Tiers
// ---------------------------------------------------------------------------

// Tier limits (23 Oct 2026): ₹29–₹2,999 a month, at most three active
// tiers, names up to 60 characters, up to ten perks of 100 characters.
const (
	MinTierPricePaise int64 = 2_900
	MaxTierPricePaise int64 = 299_900
	MaxActiveTiers          = 3
	MaxTierNameLength       = 60
	MaxTierPerks            = 10
	MaxTierPerkLength       = 100
	TierCurrency            = "INR"
)

var (
	ErrTierNameInvalid     = errors.New("TIER_NAME_INVALID")
	ErrTierPriceOutOfRange = errors.New("TIER_PRICE_OUT_OF_RANGE")
	ErrTierPerksInvalid    = errors.New("TIER_PERKS_INVALID")
	ErrTierCurrencyInvalid = errors.New("TIER_CURRENCY_INVALID")
	ErrTierLimit           = postgres.ErrTierLimit
	ErrTierNotFound        = postgres.ErrTierNotFound
)

// TierInput is a new tier as the creator typed it.
type TierInput struct {
	Name       string
	PricePaise int64
	Currency   string
	Perks      []string
}

// TierPatch is a partial tier update: nil fields are left unchanged.
type TierPatch struct {
	Name       *string
	PricePaise *int64
	Currency   *string
	Perks      *[]string
	IsActive   *bool
}

func normaliseTierName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxTierNameLength {
		return "", fmt.Errorf("%w: name must be 1 to %d characters", ErrTierNameInvalid, MaxTierNameLength)
	}
	return name, nil
}

func validateTierPrice(p int64) error {
	if p < MinTierPricePaise || p > MaxTierPricePaise {
		return fmt.Errorf("%w: price_paise must be between %d and %d", ErrTierPriceOutOfRange, MinTierPricePaise, MaxTierPricePaise)
	}
	return nil
}

func normaliseTierCurrency(c string) (string, error) {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		c = TierCurrency
	}
	if c != TierCurrency {
		return "", fmt.Errorf("%w: only %s is offered", ErrTierCurrencyInvalid, TierCurrency)
	}
	return c, nil
}

// normaliseTierPerks trims each perk, drops blank ones, and enforces the
// count and length limits. Returns the JSON array to store.
func normaliseTierPerks(perks []string) (json.RawMessage, error) {
	out := make([]string, 0, len(perks))
	for _, p := range perks {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if utf8.RuneCountInString(p) > MaxTierPerkLength {
			return nil, fmt.Errorf("%w: each perk must be at most %d characters", ErrTierPerksInvalid, MaxTierPerkLength)
		}
		out = append(out, p)
	}
	if len(out) > MaxTierPerks {
		return nil, fmt.Errorf("%w: at most %d perks", ErrTierPerksInvalid, MaxTierPerks)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// GetCreatorTiers returns all tiers for a creator.
func (s *Service) GetCreatorTiers(ctx context.Context, creatorID uuid.UUID) ([]postgres.CreatorTier, error) {
	return s.members.GetCreatorTiers(ctx, creatorID)
}

// CreateTier validates and creates an active monthly tier for creatorID.
func (s *Service) CreateTier(ctx context.Context, creatorID uuid.UUID, in TierInput) (*postgres.CreatorTier, error) {
	name, err := normaliseTierName(in.Name)
	if err != nil {
		return nil, err
	}
	if err := validateTierPrice(in.PricePaise); err != nil {
		return nil, err
	}
	currency, err := normaliseTierCurrency(in.Currency)
	if err != nil {
		return nil, err
	}
	perks, err := normaliseTierPerks(in.Perks)
	if err != nil {
		return nil, err
	}
	t := &postgres.CreatorTier{
		CreatorID:     creatorID,
		Name:          name,
		PricePaise:    in.PricePaise,
		Currency:      currency,
		Perks:         perks,
		IsActive:      true,
		BillingPeriod: "monthly",
	}
	if err := s.members.CreateTierLimited(ctx, t, MaxActiveTiers); err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateTier applies a partial update to a tier creatorID owns. Only the
// fields p sets are validated and changed. Changing the price never touches
// existing members: a subscription keeps its locked price until it changes
// tier, and access is decided by tier level (see CheckEntitlement).
func (s *Service) UpdateTier(ctx context.Context, creatorID, tierID uuid.UUID, p TierPatch) (*postgres.CreatorTier, error) {
	var sp postgres.TierPatch
	if p.Name != nil {
		name, err := normaliseTierName(*p.Name)
		if err != nil {
			return nil, err
		}
		sp.Name = &name
	}
	if p.PricePaise != nil {
		if err := validateTierPrice(*p.PricePaise); err != nil {
			return nil, err
		}
		price := *p.PricePaise
		sp.PricePaise = &price
	}
	if p.Currency != nil {
		c, err := normaliseTierCurrency(*p.Currency)
		if err != nil {
			return nil, err
		}
		sp.Currency = &c
	}
	if p.Perks != nil {
		perks, err := normaliseTierPerks(*p.Perks)
		if err != nil {
			return nil, err
		}
		sp.Perks = perks
	}
	if p.IsActive != nil {
		active := *p.IsActive
		sp.IsActive = &active
	}
	return s.members.UpdateTierPartial(ctx, creatorID, tierID, sp, MaxActiveTiers)
}

// ---------------------------------------------------------------------------
// Subscribe / unsubscribe
// ---------------------------------------------------------------------------

// ErrInvalidAmount is returned when an amount is out of valid range.
var ErrInvalidAmount = errors.New("invalid amount")

// ErrIdempotencyKeyReused: the subscriber already used this idempotency key
// for a different creator or tier.
var ErrIdempotencyKeyReused = errors.New("IDEMPOTENCY_KEY_REUSED")

const maxAmountPaise int64 = 100_000_000 // 1,000,000.00 INR in paise

// replayByKey returns the subscription the subscriber created under key,
// if it is for the same creator and tier; ErrIdempotencyKeyReused if the
// key was used for something else; (nil, nil) if the key is unused.
func (s *Service) replayByKey(ctx context.Context, subscriberID, creatorID, tierID uuid.UUID, key string) (*postgres.Subscription, error) {
	existing, err := s.members.GetSubscriptionByIdempotencyKey(ctx, subscriberID, key)
	if err != nil || existing == nil {
		return nil, err
	}
	if existing.CreatorID != creatorID || existing.TierID != tierID {
		return nil, ErrIdempotencyKeyReused
	}
	return existing, nil
}

// Subscribe validates the tier exists and is active, then creates the subscription.
// Charges the subscriber wallet and credits the creator wallet.
// idempotencyKey is optional; if non-empty, a repeat call by the same
// subscriber with the same key returns the subscription it created.
func (s *Service) Subscribe(ctx context.Context, subscriberID, creatorID uuid.UUID, tierID uuid.UUID, idempotencyKey string) (*postgres.Subscription, error) {
	if subscriberID == creatorID {
		return nil, fmt.Errorf("CANNOT_SUBSCRIBE_TO_SELF")
	}

	if idempotencyKey != "" {
		existing, err := s.replayByKey(ctx, subscriberID, creatorID, tierID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
	}

	tier, err := s.members.GetCreatorTier(ctx, tierID)
	if err != nil {
		return nil, err
	}
	if tier == nil {
		return nil, fmt.Errorf("TIER_NOT_FOUND")
	}
	if !tier.IsActive {
		return nil, fmt.Errorf("TIER_INACTIVE")
	}
	if tier.CreatorID != creatorID {
		return nil, fmt.Errorf("TIER_CREATOR_MISMATCH")
	}
	if tier.PricePaise <= 0 || tier.PricePaise > maxAmountPaise {
		return nil, fmt.Errorf("amount out of valid range: %w", ErrInvalidAmount)
	}

	// Any live subscription — not only an active one — means the subscriber
	// already has this creator (the same set the unique index enforces).
	existing, err := s.members.GetLiveSubscription(ctx, subscriberID, creatorID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, postgres.ErrAlreadySubscribed
	}

	if _, err := s.members.EnsureWallet(ctx, subscriberID); err != nil {
		return nil, err
	}
	if _, err := s.members.EnsureWallet(ctx, creatorID); err != nil {
		return nil, err
	}

	sub, err := s.members.Subscribe(ctx, subscriberID, creatorID, tier.ID, tier.Name, tier.PricePaise, tier.Currency, idempotencyKey)
	if err != nil {
		// Lost a race: the other request committed first. A same-key twin is
		// a replay; anything else is a second subscription and is refused.
		if idempotencyKey != "" && (errors.Is(err, postgres.ErrIdempotencyKeyTaken) || errors.Is(err, postgres.ErrAlreadySubscribed)) {
			if replay, rerr := s.replayByKey(ctx, subscriberID, creatorID, tierID, idempotencyKey); rerr != nil {
				return nil, rerr
			} else if replay != nil {
				return replay, nil
			}
		}
		if errors.Is(err, postgres.ErrIdempotencyKeyTaken) {
			return nil, ErrIdempotencyKeyReused
		}
		return nil, err
	}
	// Invalidate downstream caches immediately so a fan who just subscribed
	// sees gated content on the next read instead of waiting for the TTL.
	s.publishEntitlementChanged(ctx, sub.ID, subscriberID, creatorID, EntitlementGranted)
	return sub, nil
}

// Unsubscribe cancels the subscriber's live subscription to a creator now.
func (s *Service) Unsubscribe(ctx context.Context, subscriberID, creatorID uuid.UUID) error {
	sub, err := s.members.Unsubscribe(ctx, subscriberID, creatorID)
	if err != nil {
		return err
	}
	if err := s.members.InsertSubscriptionEvent(ctx, sub.ID, "cancelled_immediately", sub.Status, "cancelled", nil); err != nil {
		logEventWriteFailure(err)
	}
	s.publishEntitlementChanged(ctx, sub.ID, subscriberID, creatorID, EntitlementRevoked)
	return nil
}

// GetLiveSubscription returns the subscriber's live subscription to a creator, or nil.
func (s *Service) GetLiveSubscription(ctx context.Context, subscriberID, creatorID uuid.UUID) (*postgres.Subscription, error) {
	return s.members.GetLiveSubscription(ctx, subscriberID, creatorID)
}
