package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrReviewAuditActor is returned when a review-status or visibility change is
// attempted without a usable actor. The change is refused, never unaudited.
var ErrReviewAuditActor = errors.New("review audit: actor required")

// ReviewAuditActor is who changed a post's review_status or visibility: either
// a gateway-verified user (UserID) or a service credential (Service).
type ReviewAuditActor struct {
	UserID  *uuid.UUID
	Service string
	Reason  string
}

func (a ReviewAuditActor) validate() error {
	hasUser := a.UserID != nil && *a.UserID != uuid.Nil
	hasService := strings.TrimSpace(a.Service) != ""
	if hasUser == hasService {
		return ErrReviewAuditActor
	}
	if len(a.Reason) > 2000 {
		return ErrReviewAuditActor
	}
	return nil
}

// ReviewAuditEntry is one post_review_audit row.
type ReviewAuditEntry struct {
	ID            uuid.UUID  `json:"id"`
	PostID        uuid.UUID  `json:"post_id"`
	Field         string     `json:"field"`
	PreviousValue string     `json:"previous_value"`
	NewValue      string     `json:"new_value"`
	ActorType     string     `json:"actor_type"`
	ActorUserID   *uuid.UUID `json:"actor_user_id,omitempty"`
	ActorService  *string    `json:"actor_service,omitempty"`
	Reason        *string    `json:"reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

func insertReviewAudit(ctx context.Context, tx pgx.Tx, postID uuid.UUID, field, previous, next string, actor ReviewAuditActor) error {
	actorType := "service"
	var userID *uuid.UUID
	var service *string
	if actor.UserID != nil && *actor.UserID != uuid.Nil {
		actorType = "user"
		userID = actor.UserID
	} else {
		svc := strings.TrimSpace(actor.Service)
		service = &svc
	}
	var reason *string
	if r := strings.TrimSpace(actor.Reason); r != "" {
		reason = &r
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO post_review_audit
			(post_id, field, previous_value, new_value, actor_type, actor_user_id, actor_service, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, postID, field, previous, next, actorType, userID, service, reason)
	return err
}

// ListReviewAudit returns a post's review_status / visibility audit, newest first.
func (s *Store) ListReviewAudit(ctx context.Context, postID uuid.UUID) ([]ReviewAuditEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, field, previous_value, new_value, actor_type,
		       actor_user_id, actor_service, reason, created_at
		FROM post_review_audit WHERE post_id = $1
		ORDER BY created_at DESC, id
	`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewAuditEntry
	for rows.Next() {
		var e ReviewAuditEntry
		if err := rows.Scan(&e.ID, &e.PostID, &e.Field, &e.PreviousValue, &e.NewValue, &e.ActorType,
			&e.ActorUserID, &e.ActorService, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
