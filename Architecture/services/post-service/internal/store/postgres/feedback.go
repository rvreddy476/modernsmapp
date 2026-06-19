package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Feedback is a user-submitted product feedback note. It is distinct from a
// trust-safety Report (which flags policy violations): feedback is "help us
// improve the app / this recommendation" and carries no moderation workflow.
type Feedback struct {
	ID           uuid.UUID  `json:"id"`
	UserID       uuid.UUID  `json:"user_id"`
	FeedbackType string     `json:"feedback_type"`
	PostID       *uuid.UUID `json:"post_id,omitempty"`
	Message      string     `json:"message"`
	Context      string     `json:"context,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// CreateFeedback inserts a feedback row and populates id + created_at.
func (s *Store) CreateFeedback(ctx context.Context, f *Feedback) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO app_feedback (user_id, feedback_type, post_id, message, context)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`,
		f.UserID, f.FeedbackType, f.PostID, f.Message, f.Context,
	).Scan(&f.ID, &f.CreatedAt)
}
