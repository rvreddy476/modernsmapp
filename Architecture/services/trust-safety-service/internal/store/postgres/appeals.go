package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ─── Appeals (Copyright Match plan section 6.3, P-3) ─────────────────────────
//
// An appeal is bound to the decision it challenges: appealed_decision_id
// and appealed_revision are captured at submission and the overturn is
// signed with THAT revision. Every status change here locks the row, checks
// the precondition under the lock, and commits the update, its audit row
// and (for a terminal outcome) its outbox event together. The canonical
// approve is sent only from 'overturning', which this store enters and
// leaves under the same lock, so an uphold and an overturn can never both
// win.

var ErrActiveAppealExists = errors.New("an active appeal already exists for this content")

// Appeal statuses (trust.content_appeals.status).
const (
	AppealOpen        = "open"
	AppealUnderReview = "under_review"
	// AppealOverturning: the canonical approve is in flight under
	// overturn_decision_id. The appeal cannot be upheld from here.
	AppealOverturning = "overturning"
	AppealUpheld      = "upheld"
	AppealOverturned  = "overturned"
	// AppealSuperseded: a later decision on the post replaced the one
	// appealed. Closed without an outcome; never overturns.
	AppealSuperseded = "superseded"
	AppealExpired    = "expired"
)

// AppealResolvedEvent is the outbox event written when an appeal reaches a
// terminal status. Payload: AppealResolvedPayload. (Defined here until the
// shared events catalogue carries it.)
const AppealResolvedEvent = "AppealResolved"

// AppealResolvedPayload is one appeal's outcome as published.
type AppealResolvedPayload struct {
	AppealID           string    `json:"appeal_id"`
	UserID             string    `json:"user_id"`
	ContentType        string    `json:"content_type"`
	ContentID          string    `json:"content_id"`
	ActionTaken        string    `json:"action_taken"`
	Outcome            string    `json:"outcome"`
	AppealedDecisionID *string   `json:"appealed_decision_id,omitempty"`
	OverturnDecisionID *string   `json:"overturn_decision_id,omitempty"`
	ReviewedBy         *string   `json:"reviewed_by,omitempty"`
	ResolvedAt         time.Time `json:"resolved_at"`
}

// AuditAppealOverturnRefused is the audit action written when an overturn
// is refused because the post changed since submission; the appeal's
// status is untouched.
const AuditAppealOverturnRefused = "appeal.overturn_refused"

type ContentAppeal struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	ContentType    string     `json:"content_type"`
	ContentID      uuid.UUID  `json:"content_id"`
	ActionTaken    string     `json:"action_taken"`
	AppealReason   string     `json:"appeal_reason"`
	Status         string     `json:"status"`
	ReviewedBy     *uuid.UUID `json:"reviewed_by,omitempty"`
	ResolutionNote *string    `json:"resolution_note,omitempty"`
	SubmittedAt    time.Time  `json:"submitted_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	// AppealedDecisionID is post-service's base decision at submission; nil
	// while the moderation subject does not expose one (and for appeals
	// that pre-date migration 012). AppealedRevision is the post's content
	// revision at submission (bound at first adjudication for legacy rows).
	AppealedDecisionID *uuid.UUID `json:"appealed_decision_id,omitempty"`
	AppealedRevision   *int64     `json:"appealed_revision,omitempty"`
	// OverturnDecisionID is the moderationcap decision id the approve is
	// sent under (the appeal id) and OverturnStartedAt when the appeal
	// entered 'overturning', both set then; OverturnAppliedAt is when
	// post-service acknowledged the approve.
	OverturnDecisionID *uuid.UUID `json:"overturn_decision_id,omitempty"`
	OverturnStartedAt  *time.Time `json:"overturn_started_at,omitempty"`
	OverturnAppliedAt  *time.Time `json:"overturn_applied_at,omitempty"`
}

// AppealTerminal reports whether the status is final.
func AppealTerminal(status string) bool {
	switch status {
	case AppealUpheld, AppealOverturned, AppealSuperseded, AppealExpired:
		return true
	}
	return false
}

const appealColumns = `id, user_id, content_type, content_id, action_taken, appeal_reason,
	status, reviewed_by, resolution_note, submitted_at, resolved_at,
	appealed_decision_id, appealed_revision, overturn_decision_id, overturn_started_at, overturn_applied_at`

func scanAppeal(row pgx.Row) (*ContentAppeal, error) {
	var a ContentAppeal
	if err := row.Scan(&a.ID, &a.UserID, &a.ContentType, &a.ContentID, &a.ActionTaken, &a.AppealReason,
		&a.Status, &a.ReviewedBy, &a.ResolutionNote, &a.SubmittedAt, &a.ResolvedAt,
		&a.AppealedDecisionID, &a.AppealedRevision, &a.OverturnDecisionID, &a.OverturnStartedAt, &a.OverturnAppliedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *TrustExtrasStore) CreateAppeal(ctx context.Context, appeal *ContentAppeal) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.content_appeals
			(id, user_id, content_type, content_id, action_taken, appeal_reason, status, submitted_at,
			 appealed_decision_id, appealed_revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, appeal.ID, appeal.UserID, appeal.ContentType, appeal.ContentID,
		appeal.ActionTaken, appeal.AppealReason, appeal.Status, appeal.SubmittedAt,
		appeal.AppealedDecisionID, appeal.AppealedRevision)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_appeals_one_active_per_user_content" {
		return ErrActiveAppealExists
	}
	return err
}

// GetAppeal returns one appeal; pgx.ErrNoRows when there is none.
func (s *TrustExtrasStore) GetAppeal(ctx context.Context, id uuid.UUID) (*ContentAppeal, error) {
	return scanAppeal(s.db.QueryRow(ctx, `SELECT `+appealColumns+` FROM trust.content_appeals WHERE id = $1`, id))
}

func (s *TrustExtrasStore) ListAppeals(ctx context.Context, status string, limit, offset int) ([]ContentAppeal, error) {
	query := `SELECT ` + appealColumns + ` FROM trust.content_appeals`
	args := []interface{}{}
	if status != "" {
		query += " WHERE status = $1 ORDER BY submitted_at DESC LIMIT $2 OFFSET $3"
		args = append(args, status, limit, offset)
	} else {
		query += " ORDER BY submitted_at DESC LIMIT $1 OFFSET $2"
		args = append(args, limit, offset)
	}
	return s.queryAppeals(ctx, query, args...)
}

func (s *TrustExtrasStore) ListUserAppeals(ctx context.Context, userID uuid.UUID) ([]ContentAppeal, error) {
	return s.queryAppeals(ctx, `
		SELECT `+appealColumns+` FROM trust.content_appeals
		WHERE user_id = $1
		ORDER BY submitted_at DESC
	`, userID)
}

// ListOverturningAppeals lists appeals whose canonical approve has been in
// flight for at least olderThan (the sweeper's input), oldest first.
func (s *TrustExtrasStore) ListOverturningAppeals(ctx context.Context, olderThan time.Duration, limit int) ([]ContentAppeal, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.queryAppeals(ctx, `
		SELECT `+appealColumns+` FROM trust.content_appeals
		WHERE status = 'overturning'
		  AND overturn_started_at <= clock_timestamp() - ($1::bigint * interval '1 millisecond')
		ORDER BY overturn_started_at ASC
		LIMIT $2
	`, olderThan.Milliseconds(), limit)
}

func (s *TrustExtrasStore) queryAppeals(ctx context.Context, query string, args ...interface{}) ([]ContentAppeal, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var appeals []ContentAppeal
	for rows.Next() {
		a, err := scanAppeal(rows)
		if err != nil {
			return nil, err
		}
		appeals = append(appeals, *a)
	}
	return appeals, rows.Err()
}

// appealChange is one guarded status change.
type appealChange struct {
	from []string
	to   string
	note string
	// bindRevision, when > 0 and the appeal has no appealed_revision yet
	// (a legacy row), is recorded as it.
	bindRevision int64
	// beginOverturn fixes overturn_decision_id to the appeal id and the
	// reviewer whose claims will be signed; finishOverturn keeps them and
	// stamps overturn_applied_at (only on 'overturned').
	beginOverturn, finishOverturn bool
}

// changeAppeal applies ch under a row lock. It reports changed=false (and
// writes nothing) when the appeal is not in one of ch.from; the returned
// appeal is then the row as found. pgx.ErrNoRows when there is no appeal.
func (s *TrustExtrasStore) changeAppeal(ctx context.Context, id uuid.UUID, ch appealChange, meta AuditMeta) (*ContentAppeal, bool, error) {
	if err := meta.Actor.Validate(); err != nil {
		return nil, false, err
	}
	var reviewer *uuid.UUID
	if meta.Actor.UserID != uuid.Nil {
		rid := meta.Actor.UserID
		reviewer = &rid
	}
	var (
		result  *ContentAppeal
		changed bool
	)
	err := withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		current, err := scanAppeal(tx.QueryRow(ctx, `SELECT `+appealColumns+` FROM trust.content_appeals WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		allowed := false
		for _, f := range ch.from {
			if f == current.Status {
				allowed = true
				break
			}
		}
		if !allowed {
			result = current
			return nil
		}
		if ch.beginOverturn && reviewer == nil {
			return fmt.Errorf("begin overturn: a human reviewer is required")
		}
		updated, err := scanAppeal(tx.QueryRow(ctx, `
			UPDATE trust.content_appeals
			   SET status = $2,
			       resolution_note = CASE WHEN $6 THEN COALESCE(NULLIF($3, ''), resolution_note) ELSE NULLIF($3, '') END,
			       reviewed_by = CASE WHEN $6 THEN reviewed_by ELSE COALESCE($4, reviewed_by) END,
			       resolved_at = CASE WHEN $2 IN ('upheld', 'overturned', 'superseded', 'expired') THEN NOW() ELSE resolved_at END,
			       appealed_revision = CASE WHEN appealed_revision IS NULL AND $5 > 0 THEN $5 ELSE appealed_revision END,
			       overturn_decision_id = CASE WHEN $7 THEN COALESCE(overturn_decision_id, id) ELSE overturn_decision_id END,
			       overturn_started_at = CASE WHEN $7 THEN COALESCE(overturn_started_at, NOW()) ELSE overturn_started_at END,
			       overturn_applied_at = CASE WHEN $6 AND $2 = 'overturned' THEN COALESCE(overturn_applied_at, NOW()) ELSE overturn_applied_at END
			 WHERE id = $1
			RETURNING `+appealColumns, id, ch.to, ch.note, reviewer, ch.bindRevision, ch.finishOverturn, ch.beginOverturn))
		if err != nil {
			return fmt.Errorf("appeal %s: %w", ch.to, err)
		}
		prevNote := ""
		if current.ResolutionNote != nil {
			prevNote = *current.ResolutionNote
		}
		if err := insertAudit(ctx, tx, meta, auditChange{
			Action:         "appeal." + ch.to,
			TargetType:     AuditTargetAppeal,
			TargetID:       id,
			PrevStatus:     current.Status,
			NewStatus:      updated.Status,
			PrevAssignee:   current.ReviewedBy,
			NewAssignee:    updated.ReviewedBy,
			PrevResolution: prevNote,
			NewResolution:  ch.note,
		}); err != nil {
			return err
		}
		if AppealTerminal(updated.Status) {
			ev, err := newEnvelopeEvent(s.topic(), AppealResolvedEvent, updated.UserID.String(), *updated.ResolvedAt, appealResolvedPayload(updated))
			if err != nil {
				return err
			}
			if err := enqueueOutbox(ctx, tx, ev); err != nil {
				return err
			}
		}
		result, changed = updated, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return result, changed, nil
}

func appealResolvedPayload(a *ContentAppeal) AppealResolvedPayload {
	p := AppealResolvedPayload{
		AppealID: a.ID.String(), UserID: a.UserID.String(), ContentType: a.ContentType,
		ContentID: a.ContentID.String(), ActionTaken: a.ActionTaken, Outcome: a.Status,
	}
	if a.ResolvedAt != nil {
		p.ResolvedAt = a.ResolvedAt.UTC()
	}
	if a.AppealedDecisionID != nil {
		v := a.AppealedDecisionID.String()
		p.AppealedDecisionID = &v
	}
	if a.OverturnDecisionID != nil {
		v := a.OverturnDecisionID.String()
		p.OverturnDecisionID = &v
	}
	if a.ReviewedBy != nil {
		v := a.ReviewedBy.String()
		p.ReviewedBy = &v
	}
	return p
}

// TransitionAppeal moves an appeal whose status is in from to a
// NON-terminal status (under_review) and writes the audit row in the same
// transaction. It reports false (and writes nothing) when the appeal is
// missing or not in an allowed state. Terminal outcomes go through
// ResolveAppeal, BeginOverturn and FinishOverturn.
func (s *TrustExtrasStore) TransitionAppeal(ctx context.Context, id uuid.UUID, from []string, status, note string, meta AuditMeta) (bool, error) {
	if AppealTerminal(status) || status == AppealOverturning {
		return false, fmt.Errorf("transition appeal: %s is not a plain transition", status)
	}
	_, changed, err := s.changeAppeal(ctx, id, appealChange{from: from, to: status, note: note}, meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return changed, err
}

// ResolveAppeal closes an appeal that is in one of from as outcome
// ('upheld' or 'superseded'; never 'overturned', which only FinishOverturn
// may write) with its audit row and AppealResolved outbox event in ONE
// transaction. changed=false when the appeal is not in one of from; the
// row as found is returned so the caller can name the state.
func (s *TrustExtrasStore) ResolveAppeal(ctx context.Context, id uuid.UUID, from []string, outcome, note string, meta AuditMeta) (*ContentAppeal, bool, error) {
	if outcome != AppealUpheld && outcome != AppealSuperseded {
		return nil, false, fmt.Errorf("resolve appeal: %q is not an outcome this path may write", outcome)
	}
	return s.changeAppeal(ctx, id, appealChange{from: from, to: outcome, note: note}, meta)
}

// BeginOverturn moves an appeal in one of from to 'overturning' under the
// row lock: the reviewer (meta's human actor) and note are fixed here
// because the canonical approve's claims are built from them, and a replay
// must send identical claims. bindRevision is recorded as the appealed
// revision only when the row has none (legacy appeals). changed=false when
// the appeal is not in one of from.
func (s *TrustExtrasStore) BeginOverturn(ctx context.Context, id uuid.UUID, from []string, note string, bindRevision int64, meta AuditMeta) (*ContentAppeal, bool, error) {
	return s.changeAppeal(ctx, id, appealChange{from: from, to: AppealOverturning, note: note, bindRevision: bindRevision, beginOverturn: true}, meta)
}

// FinishOverturn leaves 'overturning' for outcome ('overturned' when
// post-service acknowledged the approve, 'superseded' when it refused it as
// stale). The reviewer and note fixed by BeginOverturn are kept; a note
// here is added only when one was given. changed=false when the appeal is
// no longer 'overturning' (another finisher won); the row is returned so
// the caller can tell 'overturned' (a replay) from anything else.
func (s *TrustExtrasStore) FinishOverturn(ctx context.Context, id uuid.UUID, outcome, note string, meta AuditMeta) (*ContentAppeal, bool, error) {
	if outcome != AppealOverturned && outcome != AppealSuperseded {
		return nil, false, fmt.Errorf("finish overturn: %q is not an outcome this path may write", outcome)
	}
	return s.changeAppeal(ctx, id, appealChange{from: []string{AppealOverturning}, to: outcome, note: note, finishOverturn: true}, meta)
}

// NoteOverturnRefused records, in the audit log only, that an overturn was
// refused because the post changed since submission. The appeal's status
// is untouched; nothing is written when the appeal does not exist.
func (s *TrustExtrasStore) NoteOverturnRefused(ctx context.Context, id uuid.UUID, note string, meta AuditMeta) error {
	if err := meta.Actor.Validate(); err != nil {
		return err
	}
	return withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM trust.content_appeals WHERE id = $1 FOR SHARE`, id).Scan(&status); err != nil {
			return err
		}
		return insertAudit(ctx, tx, meta, auditChange{
			Action: AuditAppealOverturnRefused, TargetType: AuditTargetAppeal, TargetID: id,
			PrevStatus: status, NewStatus: status, NewResolution: note,
		})
	})
}

// HasOpenAppeal checks if an open appeal already exists for the same user+content.
func (s *TrustExtrasStore) HasOpenAppeal(ctx context.Context, userID, contentID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM trust.content_appeals
			WHERE user_id = $1 AND content_id = $2 AND status IN ('open', 'under_review', 'overturning')
		)
	`, userID, contentID).Scan(&exists)
	return exists, err
}
