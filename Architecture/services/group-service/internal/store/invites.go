package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type GroupInvite struct {
	ID        uuid.UUID  `json:"id"`
	GroupID   uuid.UUID  `json:"group_id"`
	InviterID uuid.UUID  `json:"inviter_id"`
	InviteeID uuid.UUID  `json:"invitee_id"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type GroupJoinRequest struct {
	ID               uuid.UUID  `json:"id"`
	GroupID          uuid.UUID  `json:"group_id"`
	UserID           uuid.UUID  `json:"user_id"`
	Status           string     `json:"status"`
	ReviewedByUserID *uuid.UUID `json:"reviewed_by_user_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
}

// --- Invites ---

// CreateInvite creates or re-opens an invite for a user to a group.
func (s *Store) CreateInvite(ctx context.Context, inv *GroupInvite) error {
	err := s.db.QueryRow(ctx, `
		INSERT INTO group_invites (group_id, inviter_id, invitee_id, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (group_id, invitee_id) DO UPDATE
		SET status = 'pending', inviter_id = $2, updated_at = NOW(), expires_at = $4
		RETURNING id, status, created_at, updated_at
	`, inv.GroupID, inv.InviterID, inv.InviteeID, inv.ExpiresAt).Scan(
		&inv.ID, &inv.Status, &inv.CreatedAt, &inv.UpdatedAt,
	)
	return err
}

// CreateInviteBatch creates invites for multiple users at once.
func (s *Store) CreateInviteBatch(ctx context.Context, groupID, inviterID uuid.UUID, inviteeIDs []uuid.UUID, expiresAt *time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, inviteeID := range inviteeIDs {
		_, err := tx.Exec(ctx, `
			INSERT INTO group_invites (group_id, inviter_id, invitee_id, expires_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, invitee_id) DO UPDATE
			SET status = 'pending', inviter_id = $2, updated_at = NOW(), expires_at = $4
		`, groupID, inviterID, inviteeID, expiresAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetInviteByID returns an invite by its primary key.
func (s *Store) GetInviteByID(ctx context.Context, id uuid.UUID) (*GroupInvite, error) {
	var inv GroupInvite
	err := s.db.QueryRow(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at, expires_at
		FROM group_invites WHERE id = $1
	`, id).Scan(
		&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
		&inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// UpdateInviteStatus changes the status of an invite.
func (s *Store) UpdateInviteStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_invites SET status = $2, updated_at = NOW() WHERE id = $1
	`, id, status)
	return err
}

// ListInvitesByUser returns pending invites for a given user.
func (s *Store) ListInvitesByUser(ctx context.Context, userID uuid.UUID) ([]GroupInvite, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at, expires_at
		FROM group_invites
		WHERE invitee_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []GroupInvite
	for rows.Next() {
		var inv GroupInvite
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
			&inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// ListGroupInvites returns pending invites for a given group.
func (s *Store) ListGroupInvites(ctx context.Context, groupID uuid.UUID) ([]GroupInvite, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at, expires_at
		FROM group_invites
		WHERE group_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []GroupInvite
	for rows.Next() {
		var inv GroupInvite
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
			&inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// --- Join Requests ---

// CreateJoinRequest creates a new join request.
func (s *Store) CreateJoinRequest(ctx context.Context, jr *GroupJoinRequest) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO group_join_requests (group_id, user_id)
		VALUES ($1, $2)
		RETURNING id, status, created_at
	`, jr.GroupID, jr.UserID).Scan(&jr.ID, &jr.Status, &jr.CreatedAt)
}

// GetJoinRequestByID returns a join request by its primary key.
func (s *Store) GetJoinRequestByID(ctx context.Context, id uuid.UUID) (*GroupJoinRequest, error) {
	var jr GroupJoinRequest
	err := s.db.QueryRow(ctx, `
		SELECT id, group_id, user_id, status, reviewed_by_user_id, created_at, reviewed_at
		FROM group_join_requests WHERE id = $1
	`, id).Scan(&jr.ID, &jr.GroupID, &jr.UserID, &jr.Status,
		&jr.ReviewedByUserID, &jr.CreatedAt, &jr.ReviewedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &jr, nil
}

// ListJoinRequests returns pending join requests for a group.
func (s *Store) ListJoinRequests(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupJoinRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, user_id, status, reviewed_by_user_id, created_at, reviewed_at
		FROM group_join_requests
		WHERE group_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3
	`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []GroupJoinRequest
	for rows.Next() {
		var jr GroupJoinRequest
		if err := rows.Scan(&jr.ID, &jr.GroupID, &jr.UserID, &jr.Status,
			&jr.ReviewedByUserID, &jr.CreatedAt, &jr.ReviewedAt); err != nil {
			return nil, err
		}
		requests = append(requests, jr)
	}
	return requests, rows.Err()
}

// UpdateJoinRequestStatus updates the status of a join request.
func (s *Store) UpdateJoinRequestStatus(ctx context.Context, id uuid.UUID, status string, reviewedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_join_requests
		SET status = $2, reviewed_by_user_id = $3, reviewed_at = NOW()
		WHERE id = $1
	`, id, status, reviewedBy)
	return err
}

// IncrementPendingRequestCount increments the pending request count on a group.
func (s *Store) IncrementPendingRequestCount(ctx context.Context, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE groups SET pending_request_count = pending_request_count + 1 WHERE id = $1`, groupID)
	return err
}

// DecrementPendingRequestCount decrements the pending request count on a group.
func (s *Store) DecrementPendingRequestCount(ctx context.Context, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE groups SET pending_request_count = GREATEST(pending_request_count - 1, 0) WHERE id = $1`, groupID)
	return err
}
