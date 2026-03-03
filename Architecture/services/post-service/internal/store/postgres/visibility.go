package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VisibilityPolicy mirrors the visibility.policies table.
type VisibilityPolicy struct {
	ID         uuid.UUID   `json:"id"`
	OwnerID    uuid.UUID   `json:"owner_id"`
	Mode       string      `json:"mode"` // public|followers|friends|circles|only_me
	AllowLists []uuid.UUID `json:"allow_lists,omitempty"`
	AllowUsers []uuid.UUID `json:"allow_users,omitempty"`
	DenyUsers  []uuid.UUID `json:"deny_users,omitempty"`
}

// CreatePolicyInput is used to create a new visibility policy.
type CreatePolicyInput struct {
	OwnerID    uuid.UUID
	Mode       string
	AllowLists []uuid.UUID
	AllowUsers []uuid.UUID
	DenyUsers  []uuid.UUID
}

// CreateVisibilityPolicy creates a policy and its allow/deny sets in a single transaction.
func CreateVisibilityPolicy(ctx context.Context, db *pgxpool.Pool, in CreatePolicyInput) (*VisibilityPolicy, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var policyID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO visibility.policies (owner_id, mode) VALUES ($1, $2) RETURNING id`,
		in.OwnerID, in.Mode,
	).Scan(&policyID)
	if err != nil {
		return nil, err
	}

	for _, listID := range in.AllowLists {
		_, err = tx.Exec(ctx,
			`INSERT INTO visibility.policy_allow_lists (policy_id, list_id) VALUES ($1, $2)`,
			policyID, listID,
		)
		if err != nil {
			return nil, err
		}
	}

	for _, userID := range in.AllowUsers {
		_, err = tx.Exec(ctx,
			`INSERT INTO visibility.policy_allow_users (policy_id, user_id) VALUES ($1, $2)`,
			policyID, userID,
		)
		if err != nil {
			return nil, err
		}
	}

	for _, userID := range in.DenyUsers {
		_, err = tx.Exec(ctx,
			`INSERT INTO visibility.policy_deny_users (policy_id, user_id) VALUES ($1, $2)`,
			policyID, userID,
		)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &VisibilityPolicy{
		ID:         policyID,
		OwnerID:    in.OwnerID,
		Mode:       in.Mode,
		AllowLists: in.AllowLists,
		AllowUsers: in.AllowUsers,
		DenyUsers:  in.DenyUsers,
	}, nil
}

// GetVisibilityPolicy fetches a policy with its allow/deny sets.
func GetVisibilityPolicy(ctx context.Context, db *pgxpool.Pool, policyID uuid.UUID) (*VisibilityPolicy, error) {
	p := &VisibilityPolicy{ID: policyID}

	err := db.QueryRow(ctx,
		`SELECT owner_id, mode FROM visibility.policies WHERE id = $1`,
		policyID,
	).Scan(&p.OwnerID, &p.Mode)
	if err != nil {
		return nil, err
	}

	// Allow lists
	rows, err := db.Query(ctx, `SELECT list_id FROM visibility.policy_allow_lists WHERE policy_id = $1`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		p.AllowLists = append(p.AllowLists, id)
	}

	// Allow users
	rows2, err := db.Query(ctx, `SELECT user_id FROM visibility.policy_allow_users WHERE policy_id = $1`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var id uuid.UUID
		if err := rows2.Scan(&id); err != nil {
			return nil, err
		}
		p.AllowUsers = append(p.AllowUsers, id)
	}

	// Deny users
	rows3, err := db.Query(ctx, `SELECT user_id FROM visibility.policy_deny_users WHERE policy_id = $1`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows3.Close()
	for rows3.Next() {
		var id uuid.UUID
		if err := rows3.Scan(&id); err != nil {
			return nil, err
		}
		p.DenyUsers = append(p.DenyUsers, id)
	}

	return p, nil
}

// UpdatePostVisibilityPolicy creates a new policy (or updates existing) and sets it on the post.
// Returns the new policy ID.
func UpdatePostVisibilityPolicy(ctx context.Context, db *pgxpool.Pool, postID uuid.UUID, in CreatePolicyInput) (uuid.UUID, error) {
	policy, err := CreateVisibilityPolicy(ctx, db, in)
	if err != nil {
		return uuid.Nil, err
	}

	_, err = db.Exec(ctx,
		`UPDATE posts SET visibility = $1, visibility_policy_id = $2, updated_at = NOW() WHERE id = $3`,
		in.Mode, policy.ID, postID,
	)
	return policy.ID, err
}
