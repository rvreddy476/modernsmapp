package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Communities invite-only pilot (2026-09-12): shareable join links for a
// broadcast channel. Shape and semantics mirror the group links in
// chat-service/services/message-service/internal/store/postgres/invite_links.go
// so the two products behave alike — one LIVE link per channel, creating a
// new one revokes the previous (so a leaked link is rotated with one call),
// and consumption is a guarded UPDATE so two concurrent joins on a
// max_uses=1 link cannot both succeed.

// ErrInviteLinkNotLive is returned when a code does not resolve to a live
// (unrevoked, unexpired, under max_uses) invite.
var ErrInviteLinkNotLive = errors.New("invite link is invalid, expired or revoked")

// ChannelInvite is one shareable join link for a broadcast channel.
type ChannelInvite struct {
	ID        uuid.UUID  `json:"id"`
	ChannelID uuid.UUID  `json:"channel_id"`
	Code      string     `json:"code"`
	CreatedBy uuid.UUID  `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	MaxUses   *int       `json:"max_uses,omitempty"`
	Uses      int        `json:"uses"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

const channelInviteColumns = `id, channel_id, code, created_by, created_at, expires_at, max_uses, uses, revoked_at`

func scanChannelInvite(row pgx.Row) (*ChannelInvite, error) {
	var inv ChannelInvite
	err := row.Scan(&inv.ID, &inv.ChannelID, &inv.Code, &inv.CreatedBy, &inv.CreatedAt,
		&inv.ExpiresAt, &inv.MaxUses, &inv.Uses, &inv.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// CreateChannelInvite revokes the channel's current live invite (if any) and
// inserts the new one in the same transaction.
func (s *Store) CreateChannelInvite(ctx context.Context, channelID, createdBy uuid.UUID, code string, expiresAt *time.Time, maxUses *int) (*ChannelInvite, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE channel_invites SET revoked_at = NOW()
		WHERE channel_id = $1 AND revoked_at IS NULL
	`, channelID); err != nil {
		return nil, err
	}
	inv, err := scanChannelInvite(tx.QueryRow(ctx, `
		INSERT INTO channel_invites (channel_id, code, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+channelInviteColumns, channelID, code, createdBy, expiresAt, maxUses))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return inv, nil
}

// GetLiveChannelInvite returns the channel's current unrevoked invite, or nil.
func (s *Store) GetLiveChannelInvite(ctx context.Context, channelID uuid.UUID) (*ChannelInvite, error) {
	return scanChannelInvite(s.db.QueryRow(ctx, `
		SELECT `+channelInviteColumns+` FROM channel_invites
		WHERE channel_id = $1 AND revoked_at IS NULL
	`, channelID))
}

// GetChannelInviteByCode returns the invite regardless of state (the preview
// decides what "live" means for it), or nil when the code is unknown.
func (s *Store) GetChannelInviteByCode(ctx context.Context, code string) (*ChannelInvite, error) {
	return scanChannelInvite(s.db.QueryRow(ctx, `
		SELECT `+channelInviteColumns+` FROM channel_invites WHERE code = $1
	`, code))
}

// RevokeChannelInvite revokes the channel's live invite. Returns false when
// there was none (idempotent).
func (s *Store) RevokeChannelInvite(ctx context.Context, channelID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE channel_invites SET revoked_at = NOW()
		WHERE channel_id = $1 AND revoked_at IS NULL
	`, channelID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ConsumeChannelInvite counts one join against the invite, atomically
// refusing when it is revoked, expired or exhausted. Returns the consumed
// invite, or ErrInviteLinkNotLive.
func (s *Store) ConsumeChannelInvite(ctx context.Context, code string) (*ChannelInvite, error) {
	inv, err := scanChannelInvite(s.db.QueryRow(ctx, `
		UPDATE channel_invites SET uses = uses + 1
		WHERE code = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND (max_uses IS NULL OR uses < max_uses)
		RETURNING `+channelInviteColumns, code))
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, ErrInviteLinkNotLive
	}
	return inv, nil
}
