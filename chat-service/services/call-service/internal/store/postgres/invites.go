package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *CallStore) CreateInvite(ctx context.Context, inv *domain.CallInvite) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO calls.call_invites
			(id, call_session_id, inviter_user_id, invitee_user_id,
			 delivery_channel, delivery_status, response_status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		inv.ID, inv.CallSessionID, inv.InviterUserID, inv.InviteeUserID,
		inv.DeliveryChannel, inv.DeliveryStatus, inv.ResponseStatus, inv.CreatedAt,
	)
	return err
}

func (s *CallStore) GetInvite(ctx context.Context, inviteID uuid.UUID) (*domain.CallInvite, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, call_session_id, inviter_user_id, invitee_user_id,
		       delivery_channel, delivery_status, response_status,
		       created_at, delivered_at, responded_at, metadata_json
		FROM calls.call_invites WHERE id = $1`, inviteID)

	var inv domain.CallInvite
	err := row.Scan(
		&inv.ID, &inv.CallSessionID, &inv.InviterUserID, &inv.InviteeUserID,
		&inv.DeliveryChannel, &inv.DeliveryStatus, &inv.ResponseStatus,
		&inv.CreatedAt, &inv.DeliveredAt, &inv.RespondedAt, &inv.MetadataJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *CallStore) UpdateInviteResponse(ctx context.Context, inviteID uuid.UUID, responseStatus string) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_invites
		SET response_status = $2, responded_at = $3
		WHERE id = $1`,
		inviteID, responseStatus, now,
	)
	return err
}

func (s *CallStore) ListPendingInvitesForUser(ctx context.Context, userID uuid.UUID) ([]domain.CallInvite, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.id, i.call_session_id, i.inviter_user_id, i.invitee_user_id,
		       i.delivery_channel, i.delivery_status, i.response_status,
		       i.created_at, i.delivered_at, i.responded_at, i.metadata_json
		FROM calls.call_invites i
		JOIN calls.call_sessions cs ON cs.id = i.call_session_id
		WHERE i.invitee_user_id = $1 AND i.response_status = 'pending'
		  AND cs.state IN ('initiated', 'ringing', 'active')
		ORDER BY i.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []domain.CallInvite
	for rows.Next() {
		var inv domain.CallInvite
		if err := rows.Scan(
			&inv.ID, &inv.CallSessionID, &inv.InviterUserID, &inv.InviteeUserID,
			&inv.DeliveryChannel, &inv.DeliveryStatus, &inv.ResponseStatus,
			&inv.CreatedAt, &inv.DeliveredAt, &inv.RespondedAt, &inv.MetadataJSON,
		); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, nil
}

func (s *CallStore) CountInvitesForCall(ctx context.Context, callID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM calls.call_invites
		WHERE call_session_id = $1`, callID).Scan(&count)
	return count, err
}

func (s *CallStore) CountDailyInvitesByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM calls.call_invites
		WHERE inviter_user_id = $1 AND created_at > NOW() - INTERVAL '24 hours'`, userID).Scan(&count)
	return count, err
}

// ExpirePendingInvitesForCall marks all pending invites as expired.
func (s *CallStore) ExpirePendingInvitesForCall(ctx context.Context, callID uuid.UUID) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_invites
		SET response_status = 'expired', responded_at = $2
		WHERE call_session_id = $1 AND response_status = 'pending'`,
		callID, now,
	)
	return err
}

// GetInviteByCallAndUser finds an invite for a specific user in a call.
func (s *CallStore) GetInviteByCallAndUser(ctx context.Context, callID, userID uuid.UUID) (*domain.CallInvite, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, call_session_id, inviter_user_id, invitee_user_id,
		       delivery_channel, delivery_status, response_status,
		       created_at, delivered_at, responded_at, metadata_json
		FROM calls.call_invites
		WHERE call_session_id = $1 AND invitee_user_id = $2`, callID, userID)

	var inv domain.CallInvite
	err := row.Scan(
		&inv.ID, &inv.CallSessionID, &inv.InviterUserID, &inv.InviteeUserID,
		&inv.DeliveryChannel, &inv.DeliveryStatus, &inv.ResponseStatus,
		&inv.CreatedAt, &inv.DeliveredAt, &inv.RespondedAt, &inv.MetadataJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}
