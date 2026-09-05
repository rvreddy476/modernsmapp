package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Chat-app pass (2026-09-05): shareable group invite links.
//
// One LIVE link per group. Creating a new one revokes the previous so a
// leaked link can be rotated with a single call. Consumption is a guarded
// UPDATE, so two concurrent joins on a max_uses=1 link cannot both succeed.

// ErrInviteLinkNotLive is returned when a code does not resolve to a live
// (unrevoked, unexpired, under max_uses) link.
var ErrInviteLinkNotLive = errors.New("invite link is invalid, expired or revoked")

// GroupInviteLink is one shareable join link.
type GroupInviteLink struct {
	Code           string     `json:"code"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	CreatedBy      uuid.UUID  `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	MaxUses        *int       `json:"max_uses,omitempty"`
	Uses           int        `json:"uses"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

const inviteLinkColumns = `code, conversation_id, created_by, created_at, expires_at, max_uses, uses, revoked_at`

func scanInviteLink(row pgx.Row) (*GroupInviteLink, error) {
	var l GroupInviteLink
	err := row.Scan(&l.Code, &l.ConversationID, &l.CreatedBy, &l.CreatedAt, &l.ExpiresAt, &l.MaxUses, &l.Uses, &l.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// CreateInviteLink revokes the group's current live link (if any) and inserts
// the new one in the same transaction.
func (s *ConversationStore) CreateInviteLink(ctx context.Context, conversationID, createdBy uuid.UUID, code string, expiresAt *time.Time, maxUses *int) (*GroupInviteLink, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE chat.group_invite_links SET revoked_at = NOW()
		WHERE conversation_id = $1 AND revoked_at IS NULL
	`, conversationID); err != nil {
		return nil, err
	}
	link, err := scanInviteLink(tx.QueryRow(ctx, `
		INSERT INTO chat.group_invite_links (code, conversation_id, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+inviteLinkColumns, code, conversationID, createdBy, expiresAt, maxUses))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return link, nil
}

// GetLiveInviteLink returns the group's current unrevoked link, or nil.
func (s *ConversationStore) GetLiveInviteLink(ctx context.Context, conversationID uuid.UUID) (*GroupInviteLink, error) {
	return scanInviteLink(s.db.QueryRow(ctx, `
		SELECT `+inviteLinkColumns+` FROM chat.group_invite_links
		WHERE conversation_id = $1 AND revoked_at IS NULL
	`, conversationID))
}

// GetInviteLinkByCode returns the link regardless of state (callers decide
// what "live" means for previews), or nil when unknown.
func (s *ConversationStore) GetInviteLinkByCode(ctx context.Context, code string) (*GroupInviteLink, error) {
	return scanInviteLink(s.db.QueryRow(ctx, `
		SELECT `+inviteLinkColumns+` FROM chat.group_invite_links WHERE code = $1
	`, code))
}

// RevokeInviteLink revokes the group's live link. Returns false when there
// was none (idempotent).
func (s *ConversationStore) RevokeInviteLink(ctx context.Context, conversationID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.group_invite_links SET revoked_at = NOW()
		WHERE conversation_id = $1 AND revoked_at IS NULL
	`, conversationID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ConsumeInviteLink counts one join against the link, atomically refusing
// when it is revoked, expired or exhausted. Returns the consumed link.
func (s *ConversationStore) ConsumeInviteLink(ctx context.Context, code string) (*GroupInviteLink, error) {
	link, err := scanInviteLink(s.db.QueryRow(ctx, `
		UPDATE chat.group_invite_links SET uses = uses + 1
		WHERE code = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND (max_uses IS NULL OR uses < max_uses)
		RETURNING `+inviteLinkColumns, code))
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, ErrInviteLinkNotLive
	}
	return link, nil
}
