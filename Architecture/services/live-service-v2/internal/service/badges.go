package service

// Founding creator badge (2 Oct 2026). A creator earns `founding_creator`
// the first time one of their streams ends after at least
// LIVE_FOUNDING_MIN_LIVE on air while the founding window
// (LIVE_FOUNDING_CREATOR_UNTIL) is open. The grant is the store's, inside
// the transaction that ends the stream (postgres.FoundingRule,
// Store.ApplyTransition), so every way a stream ends — the host, LiveKit, an
// admin, the sweeper — grants it. It is permanent: later streams never
// remove it, and one an admin revoked is never granted again.
//
// No money and no promise of any: the badge is recognition only.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// AuditUserBadgeRevoke is the audit action of an admin badge revoke.
const AuditUserBadgeRevoke = "user.badge_revoke"

// ParseFoundingUntil reads LIVE_FOUNDING_CREATOR_UNTIL: empty = the window
// is open (nil), an RFC3339 time = it closes then. Anything else is an
// error, and the caller refuses to start: a typo must not silently leave
// the window open for ever.
func ParseFoundingUntil(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("LIVE_FOUNDING_CREATOR_UNTIL must be an RFC3339 time: %w", err)
	}
	return &t, nil
}

// UserBadges is GET /users/:userId/badges: the badges the user holds. Empty
// when none, when revoked, and while the user is platform live-banned.
func (s *Service) UserBadges(ctx context.Context, userID uuid.UUID) ([]postgres.Badge, error) {
	list, err := s.store.ListBadges(ctx, userID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []postgres.Badge{}
	}
	return list, nil
}

// AdminBadgeResult is the revoke answer.
type AdminBadgeResult struct {
	UserID  uuid.UUID `json:"user_id"`
	Badge   string    `json:"badge"`
	Revoked bool      `json:"revoked"`
}

// AdminRevokeBadge revokes a creator badge (audited, reason required). The
// row is kept as revoked, so the creator is not granted it again. Revoking
// one already revoked answers the same and writes no second audit row.
func (s *Service) AdminRevokeBadge(ctx context.Context, actor, userID uuid.UUID, badge, reason string) (*AdminBadgeResult, error) {
	if badge != postgres.BadgeFoundingCreator {
		return nil, ErrBadgeNotFound
	}
	if userID == uuid.Nil {
		return nil, ErrInvalidTarget
	}
	reason, err := validReason(reason, true)
	if err != nil {
		return nil, err
	}
	_, err = s.store.AdminRevokeBadge(ctx, userID, badge, reason, postgres.AuditEntry{
		ActorID: actor, Action: AuditUserBadgeRevoke, TargetType: "user", TargetID: userID.String(),
		Reason: reason, Detail: marshalDetail(map[string]any{"badge": badge}),
	})
	if errors.Is(err, postgres.ErrNotFound) {
		return nil, ErrBadgeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &AdminBadgeResult{UserID: userID, Badge: badge, Revoked: true}, nil
}
