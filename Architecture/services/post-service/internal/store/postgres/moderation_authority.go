package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrModerationDecisionConflict = errors.New("moderation decision id was already used with different claims")
	ErrModerationTransition       = errors.New("moderation transition is not allowed")
	ErrModerationRevision         = errors.New("moderation subject revision changed")
)

type ModerationSubject struct {
	PostID       uuid.UUID `json:"post_id"`
	AuthorID     uuid.UUID `json:"author_id"`
	ReviewStatus string    `json:"review_status"`
	SearchRev    int64     `json:"content_revision"`
	Deleted      bool      `json:"deleted"`
}

type ModeratePostInput struct {
	DecisionID       uuid.UUID
	PostID           uuid.UUID
	ActorID          uuid.UUID
	Action           string
	Reason           string
	Source           string
	SourceRefID      *uuid.UUID
	ExpectedRevision int64
}

type ModerationDecision struct {
	DecisionID      uuid.UUID  `json:"decision_id"`
	PostID          uuid.UUID  `json:"post_id"`
	ActorID         uuid.UUID  `json:"actor_id"`
	Action          string     `json:"action"`
	Reason          string     `json:"reason"`
	Source          string     `json:"source"`
	SourceRefID     *uuid.UUID `json:"source_ref_id,omitempty"`
	PreviousStatus  string     `json:"previous_status"`
	ResultingStatus string     `json:"resulting_status"`
	Changed         bool       `json:"changed"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (s *Store) GetModerationSubject(ctx context.Context, postID uuid.UUID) (*ModerationSubject, error) {
	var subject ModerationSubject
	err := s.db.QueryRow(ctx, `
		SELECT id, author_id, review_status, search_rev, deleted_at IS NOT NULL
		FROM posts WHERE id=$1
	`, postID).Scan(&subject.PostID, &subject.AuthorID, &subject.ReviewStatus, &subject.SearchRev, &subject.Deleted)
	if err != nil {
		return nil, err
	}
	return &subject, nil
}

func moderationTarget(action string) string {
	switch action {
	case "approve":
		return "approved"
	case "reject":
		return "rejected"
	case "needs_changes":
		return "needs_changes"
	default:
		return ""
	}
}

func transitionAllowed(current, action string) bool {
	switch action {
	case "reject":
		return current == "approved" || current == "flagged" || current == "pending" || current == "needs_changes"
	case "approve":
		return current == "rejected" || current == "flagged" || current == "pending" || current == "needs_changes"
	case "needs_changes":
		return current == "flagged" || current == "pending" || current == "rejected"
	default:
		return false
	}
}

func (s *Store) ModeratePost(ctx context.Context, in ModeratePostInput) (*ModerationDecision, error) {
	in.Action = strings.TrimSpace(strings.ToLower(in.Action))
	in.Reason = strings.TrimSpace(in.Reason)
	if in.DecisionID == uuid.Nil || in.PostID == uuid.Nil || in.ActorID == uuid.Nil ||
		moderationTarget(in.Action) == "" || in.Reason == "" ||
		(in.Source != "admin" && in.Source != "appeal") {
		return nil, fmt.Errorf("invalid moderation decision input")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 707))`, in.DecisionID.String()); err != nil {
		return nil, err
	}

	existing, err := getModerationDecisionTx(ctx, tx, in.DecisionID)
	if err == nil {
		if !sameModerationClaims(existing, in) {
			return nil, ErrModerationDecisionConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	var current string
	var revision int64
	var deleted bool
	if err := tx.QueryRow(ctx, `
		SELECT review_status, search_rev, deleted_at IS NOT NULL
		FROM posts WHERE id=$1 FOR UPDATE
	`, in.PostID).Scan(&current, &revision, &deleted); err != nil {
		return nil, err
	}
	if deleted {
		return nil, fmt.Errorf("%w: deleted post", ErrModerationTransition)
	}
	if in.ExpectedRevision > 0 && revision != in.ExpectedRevision {
		return nil, ErrModerationRevision
	}
	if !transitionAllowed(current, in.Action) {
		return nil, fmt.Errorf("%w: %s cannot %s", ErrModerationTransition, current, in.Action)
	}

	resulting := moderationTarget(in.Action)
	decision := &ModerationDecision{
		DecisionID: in.DecisionID, PostID: in.PostID, ActorID: in.ActorID,
		Action: in.Action, Reason: in.Reason, Source: in.Source, SourceRefID: in.SourceRefID,
		PreviousStatus: current, ResultingStatus: resulting, Changed: current != resulting,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_moderation_decisions
			(decision_id, post_id, actor_id, action, reason, source, source_ref_id,
			 previous_status, resulting_status, changed)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, decision.DecisionID, decision.PostID, decision.ActorID, decision.Action,
		decision.Reason, decision.Source, decision.SourceRefID,
		decision.PreviousStatus, decision.ResultingStatus, decision.Changed); err != nil {
		return nil, err
	}

	if decision.Changed {
		tag, err := tx.Exec(ctx, `UPDATE posts SET review_status=$2, updated_at=NOW() WHERE id=$1 AND review_status=$3`,
			in.PostID, resulting, current)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrModerationTransition
		}
		if err := BumpSearchRevAndEmitTx(ctx, tx, in.PostID); err != nil {
			return nil, err
		}
	}

	if err := tx.QueryRow(ctx, `SELECT created_at FROM post_moderation_decisions WHERE decision_id=$1`, in.DecisionID).Scan(&decision.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return decision, nil
}

func getModerationDecisionTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*ModerationDecision, error) {
	var d ModerationDecision
	err := tx.QueryRow(ctx, `
		SELECT decision_id, post_id, actor_id, action, reason, source, source_ref_id,
		       previous_status, resulting_status, changed, created_at
		FROM post_moderation_decisions WHERE decision_id=$1
	`, id).Scan(&d.DecisionID, &d.PostID, &d.ActorID, &d.Action, &d.Reason, &d.Source,
		&d.SourceRefID, &d.PreviousStatus, &d.ResultingStatus, &d.Changed, &d.CreatedAt)
	return &d, err
}

func sameModerationClaims(d *ModerationDecision, in ModeratePostInput) bool {
	if d.PostID != in.PostID || d.ActorID != in.ActorID || d.Action != in.Action ||
		d.Reason != in.Reason || d.Source != in.Source {
		return false
	}
	if d.SourceRefID == nil || in.SourceRefID == nil {
		return d.SourceRefID == nil && in.SourceRefID == nil
	}
	return *d.SourceRefID == *in.SourceRefID
}
