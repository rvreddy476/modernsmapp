package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Action button links (migration 072). See the migration for the states;
// service/action_links.go owns the rules and the read shape.

// Action link status values (the wire and the column agree).
const (
	ActionStatusActive      = "active"
	ActionStatusBlocked     = "blocked"
	ActionStatusUnderReview = "under_review"
	ActionStatusRemoved     = "removed"
)

// Why a link is not active (status_reason).
const (
	ActionReasonUnsafe    = "unsafe"
	ActionReasonModerator = "moderator"
	ActionReasonReports   = "reports"
)

// Safety verdicts (safety_status).
const (
	ActionSafetyUnchecked = "unchecked"
	ActionSafetySafe      = "safe"
	ActionSafetyUnsafe    = "unsafe"
)

var (
	// ErrActionLinkNotFound: no live link with that id (or none on the
	// target).
	ErrActionLinkNotFound = errors.New("action link not found")
	// ErrActionLinkTransition: a moderator decision the link's state does
	// not admit (a removed link, a restore of an active one).
	ErrActionLinkTransition = errors.New("action link is not in a state this decision applies to")
)

// ActionLink is one action_links row.
type ActionLink struct {
	ID              uuid.UUID
	PostID          *uuid.UUID
	ProfileOwnerID  *uuid.UUID
	AuthorID        uuid.UUID
	URL             string
	Domain          string
	Label           string
	Status          string
	StatusReason    *string
	SafetyStatus    string
	SafetyCheckedAt *time.Time
	ModeratedAt     *time.Time
	ClicksTotal     int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ActionLinkWrite is a validated, checked action to store: the service ran
// the label, URL, eligibility and safety rules before building it.
type ActionLinkWrite struct {
	// ID is the new link's id when the caller needs it before the insert
	// (the create answer); uuid.Nil = a fresh one.
	ID              uuid.UUID
	Label           string
	URL             string
	Domain          string
	SafetyStatus    string
	SafetyCheckedAt *time.Time
}

// ActionView is the `action` object on a post row or a channel.
//
// Viewers get id, label, label_text, domain and href — never the raw URL —
// and only for an active link. The author additionally gets url, status
// (active | blocked | under_review) and, when it is not active,
// status_reason (unsafe | moderator | reports) so the composer can say
// "Link blocked: unsafe".
type ActionView struct {
	ID           uuid.UUID `json:"id"`
	Label        string    `json:"label"`
	LabelText    string    `json:"label_text"`
	Domain       string    `json:"domain"`
	Href         string    `json:"href"`
	URL          string    `json:"url,omitempty"`
	Status       string    `json:"status,omitempty"`
	StatusReason string    `json:"status_reason,omitempty"`
}

const actionLinkCols = `id, post_id, profile_owner_id, author_id, url, domain, label,
	status, status_reason, safety_status, safety_checked_at, moderated_at,
	clicks_total, created_at, updated_at`

func scanActionLink(row pgx.Row) (*ActionLink, error) {
	var l ActionLink
	if err := row.Scan(&l.ID, &l.PostID, &l.ProfileOwnerID, &l.AuthorID, &l.URL, &l.Domain, &l.Label,
		&l.Status, &l.StatusReason, &l.SafetyStatus, &l.SafetyCheckedAt, &l.ModeratedAt,
		&l.ClicksTotal, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	return &l, nil
}

func scanActionLinkRows(rows pgx.Rows) ([]*ActionLink, error) {
	defer rows.Close()
	var out []*ActionLink
	for rows.Next() {
		l, err := scanActionLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// insertActionLinkTx writes a new live link for a post (postID) or a
// profile (ownerID), inside tx.
func insertActionLinkTx(ctx context.Context, tx pgx.Tx, postID, ownerID *uuid.UUID, authorID uuid.UUID, w *ActionLinkWrite) (*ActionLink, error) {
	id := w.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	return scanActionLink(tx.QueryRow(ctx, `
		INSERT INTO action_links (id, post_id, profile_owner_id, author_id, url, domain, label,
			status, safety_status, safety_checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, $9)
		RETURNING `+actionLinkCols,
		id, postID, ownerID, authorID, w.URL, w.Domain, w.Label, w.SafetyStatus, w.SafetyCheckedAt))
}

// replaceLiveLinkTx applies w to the target's current live link (current,
// already locked; nil when there is none):
//
//   - w nil: the current link becomes removed;
//   - same URL: label and safety verdict updated in place, same id. A link
//     the safety check had blocked becomes active again on a fresh safe
//     verdict; a moderator's block and a report hold stay;
//   - otherwise: the current link becomes removed and a new id is inserted,
//     so a shared /l/<old id> stops resolving.
func replaceLiveLinkTx(ctx context.Context, tx pgx.Tx, current *ActionLink, postID, ownerID *uuid.UUID, authorID uuid.UUID, w *ActionLinkWrite) (*ActionLink, error) {
	if current != nil && (w == nil || current.URL != w.URL) {
		if _, err := tx.Exec(ctx, `
			UPDATE action_links SET status = 'removed', removed_at = NOW(), updated_at = NOW(),
				recheck_claimed_until = NULL
			WHERE id = $1`, current.ID); err != nil {
			return nil, fmt.Errorf("remove action link: %w", err)
		}
	}
	if w == nil {
		return nil, nil
	}
	if current != nil && current.URL == w.URL {
		return scanActionLink(tx.QueryRow(ctx, `
			UPDATE action_links SET label = $2, safety_status = $3, safety_checked_at = $4,
				status = CASE WHEN status = 'blocked' AND status_reason = 'unsafe' AND $3 = 'safe' THEN 'active' ELSE status END,
				status_reason = CASE WHEN status = 'blocked' AND status_reason = 'unsafe' AND $3 = 'safe' THEN NULL ELSE status_reason END,
				updated_at = NOW()
			WHERE id = $1
			RETURNING `+actionLinkCols, current.ID, w.Label, w.SafetyStatus, w.SafetyCheckedAt))
	}
	return insertActionLinkTx(ctx, tx, postID, ownerID, authorID, w)
}

// ReplacePostActionLink sets (w) or removes (nil) the action on the caller's
// post. Returns the live link afterwards (nil when removed). pgx.ErrNoRows
// for a post that does not exist or is deleted, ErrPostEditNotOwned for
// someone else's.
func (s *Store) ReplacePostActionLink(ctx context.Context, postID, authorID uuid.UUID, w *ActionLinkWrite) (*ActionLink, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var owner uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, postID).Scan(&owner); err != nil {
		return nil, err
	}
	if owner != authorID {
		return nil, ErrPostEditNotOwned
	}
	current, err := scanActionLink(tx.QueryRow(ctx, `
		SELECT `+actionLinkCols+` FROM action_links
		WHERE post_id = $1 AND status <> 'removed' FOR UPDATE`, postID))
	if errors.Is(err, pgx.ErrNoRows) {
		current, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	pid := postID
	link, err := replaceLiveLinkTx(ctx, tx, current, &pid, nil, authorID, w)
	if err != nil {
		return nil, err
	}
	return link, tx.Commit(ctx)
}

// ReplaceProfileActionLink sets (w) or removes (nil) the caller's profile
// action. Serialised per owner with a transaction advisory lock, so two
// concurrent PATCHes cannot both insert.
func (s *Store) ReplaceProfileActionLink(ctx context.Context, ownerID uuid.UUID, w *ActionLinkWrite) (*ActionLink, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('action_links:profile:' || $1::text, 0))`, ownerID); err != nil {
		return nil, err
	}
	current, err := scanActionLink(tx.QueryRow(ctx, `
		SELECT `+actionLinkCols+` FROM action_links
		WHERE profile_owner_id = $1 AND status <> 'removed' FOR UPDATE`, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		current, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	owner := ownerID
	link, err := replaceLiveLinkTx(ctx, tx, current, nil, &owner, ownerID, w)
	if err != nil {
		return nil, err
	}
	return link, tx.Commit(ctx)
}

// ActionLinksForPosts returns each post's live (non-removed) link.
func (s *Store) ActionLinksForPosts(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*ActionLink, error) {
	out := map[uuid.UUID]*ActionLink{}
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+actionLinkCols+` FROM action_links
		WHERE post_id = ANY($1) AND status <> 'removed'`, postIDs)
	if err != nil {
		return nil, err
	}
	links, err := scanActionLinkRows(rows)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.PostID != nil {
			out[*l.PostID] = l
		}
	}
	return out, nil
}

// ActionLinkForProfile returns the owner's live profile link, nil for none.
func (s *Store) ActionLinkForProfile(ctx context.Context, ownerID uuid.UUID) (*ActionLink, error) {
	l, err := scanActionLink(s.db.QueryRow(ctx, `
		SELECT `+actionLinkCols+` FROM action_links
		WHERE profile_owner_id = $1 AND status <> 'removed'`, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

// ResolvableActionLink is a link as GET /v1/links/:id may answer it: the
// row, plus the post it is on (deleted posts are never resolvable) and its
// members-only gate.
type ResolvableActionLink struct {
	Link         *ActionLink
	PostTierID   *uuid.UUID
	PostAuthorID uuid.UUID
}

// GetResolvableActionLink loads an ACTIVE link by id. A post link resolves
// only while its post exists, is not deleted and is approved; nil when the
// link is not resolvable for any reason (one answer for all of them).
func (s *Store) GetResolvableActionLink(ctx context.Context, id uuid.UUID) (*ResolvableActionLink, error) {
	var out ResolvableActionLink
	var l ActionLink
	var tier *uuid.UUID
	var postAuthor *uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT l.id, l.post_id, l.profile_owner_id, l.author_id, l.url, l.domain, l.label,
			l.status, l.status_reason, l.safety_status, l.safety_checked_at, l.moderated_at,
			l.clicks_total, l.created_at, l.updated_at,
			p.tier_required_id, p.author_id
		FROM action_links l
		LEFT JOIN posts p ON p.id = l.post_id
		WHERE l.id = $1 AND l.status = 'active'
		  AND (l.post_id IS NULL OR (p.id IS NOT NULL AND p.deleted_at IS NULL
		       AND COALESCE(p.review_status, 'approved') = 'approved'))`, id).Scan(
		&l.ID, &l.PostID, &l.ProfileOwnerID, &l.AuthorID, &l.URL, &l.Domain, &l.Label,
		&l.Status, &l.StatusReason, &l.SafetyStatus, &l.SafetyCheckedAt, &l.ModeratedAt,
		&l.ClicksTotal, &l.CreatedAt, &l.UpdatedAt, &tier, &postAuthor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.Link = &l
	out.PostTierID = tier
	if postAuthor != nil {
		out.PostAuthorID = *postAuthor
	}
	return &out, nil
}

// RecordActionLinkClick counts one (already deduplicated) tap on an active
// link: clicks_total and the day's bucket, in one statement.
func (s *Store) RecordActionLinkClick(ctx context.Context, id uuid.UUID, day time.Time) error {
	_, err := s.db.Exec(ctx, `
		WITH bumped AS (
			UPDATE action_links SET clicks_total = clicks_total + 1
			WHERE id = $1 AND status = 'active'
			RETURNING id
		)
		INSERT INTO action_link_clicks_daily (link_id, day, clicks)
		SELECT id, $2::date, 1 FROM bumped
		ON CONFLICT (link_id, day) DO UPDATE SET clicks = action_link_clicks_daily.clicks + 1`,
		id, day.UTC().Format("2006-01-02"))
	return err
}

// ActionLinkStats sums the taps of every link a post (postID) or a profile
// (ownerID) has had — replaced links included, so a creator's count does
// not reset when they fix a typo in the URL. last7 counts days >= since.
func (s *Store) ActionLinkStats(ctx context.Context, postID, ownerID *uuid.UUID, since time.Time) (total, last7 int64, err error) {
	err = s.db.QueryRow(ctx, `
		WITH ls AS (
			SELECT id, clicks_total FROM action_links
			WHERE ($1::uuid IS NOT NULL AND post_id = $1) OR ($2::uuid IS NOT NULL AND profile_owner_id = $2)
		)
		SELECT COALESCE((SELECT SUM(clicks_total) FROM ls), 0)::bigint,
		       COALESCE((SELECT SUM(d.clicks) FROM action_link_clicks_daily d
		                 WHERE d.link_id IN (SELECT id FROM ls) AND d.day >= $3::date), 0)::bigint`,
		postID, ownerID, since.UTC().Format("2006-01-02")).Scan(&total, &last7)
	return total, last7, err
}

// ClaimActionLinksForRecheck claims up to limit live links whose last
// verdict is older than staleBefore (or that were never checked), oldest
// first, until claimUntil. FOR UPDATE SKIP LOCKED plus the claim column make
// it safe in every replica at once: two workers never take the same row,
// and a link whose check failed is not retried before its claim lapses.
func (s *Store) ClaimActionLinksForRecheck(ctx context.Context, now, staleBefore, claimUntil time.Time, limit int) ([]*ActionLink, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE action_links SET recheck_claimed_until = $3
		WHERE id IN (
			SELECT id FROM action_links
			WHERE status IN ('active', 'under_review')
			  AND (safety_checked_at IS NULL OR safety_checked_at < $2)
			  AND (recheck_claimed_until IS NULL OR recheck_claimed_until < $1)
			ORDER BY safety_checked_at NULLS FIRST
			LIMIT $4
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+actionLinkCols, now, staleBefore, claimUntil, limit)
	if err != nil {
		return nil, err
	}
	return scanActionLinkRows(rows)
}

// RecordActionLinkVerdict stores a recheck verdict and releases the claim.
// An unsafe verdict on a live link blocks it (status_reason unsafe). Returns
// the row afterwards; ErrActionLinkNotFound when it was removed meanwhile.
func (s *Store) RecordActionLinkVerdict(ctx context.Context, id uuid.UUID, safety string, checkedAt time.Time) (*ActionLink, error) {
	l, err := scanActionLink(s.db.QueryRow(ctx, `
		UPDATE action_links SET
			safety_status = $2, safety_checked_at = $3, recheck_claimed_until = NULL,
			status_reason = CASE WHEN $2 = 'unsafe' AND status IN ('active', 'under_review') THEN 'unsafe' ELSE status_reason END,
			updated_at    = CASE WHEN $2 = 'unsafe' AND status IN ('active', 'under_review') THEN NOW() ELSE updated_at END,
			status        = CASE WHEN $2 = 'unsafe' AND status IN ('active', 'under_review') THEN 'blocked' ELSE status END
		WHERE id = $1 AND status <> 'removed'
		RETURNING `+actionLinkCols, id, safety, checkedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrActionLinkNotFound
	}
	return l, err
}

// ActionLinkReportInput is one scam_link report from trust-safety.
type ActionLinkReportInput struct {
	// TargetType is "post" or "user" (a profile).
	TargetType string
	TargetID   uuid.UUID
	ReporterID uuid.UUID
	ReportID   uuid.UUID
	ReportedAt time.Time
	Now        time.Time
	Window     time.Duration
	Threshold  int
}

// ActionLinkReportOutcome is what one report did.
type ActionLinkReportOutcome struct {
	// Found: the target has a live link.
	Found bool
	// Counted: the report entered the ledger (not the author's own, not
	// older than the link).
	Counted bool
	// Reporters is the distinct reporters inside the window since the link
	// was created or a moderator last decided on it.
	Reporters int
	// Held: this report put the link under review.
	Held   bool
	LinkID uuid.UUID
	PostID *uuid.UUID
}

// ApplyActionLinkReport counts a scam_link report against the target's live
// link and, at Threshold distinct reporters within Window, moves an active
// link to under_review (status_reason reports). Idempotent per (link,
// reporter). Reports filed before the link existed (they were about a link
// since replaced) and reports older than a moderator's last decision do
// not count, so a restored link needs Threshold NEW reporters.
func (s *Store) ApplyActionLinkReport(ctx context.Context, in ActionLinkReportInput) (ActionLinkReportOutcome, error) {
	var out ActionLinkReportOutcome
	col := "post_id"
	switch in.TargetType {
	case "post":
	case "user":
		col = "profile_owner_id"
	default:
		return out, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	link, err := scanActionLink(tx.QueryRow(ctx, `
		SELECT `+actionLinkCols+` FROM action_links
		WHERE `+col+` = $1 AND status <> 'removed' FOR UPDATE`, in.TargetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Found, out.LinkID, out.PostID = true, link.ID, link.PostID
	if in.ReporterID == link.AuthorID || in.ReportedAt.Before(link.CreatedAt) {
		return out, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO action_link_reports (link_id, reporter_id, report_id, reported_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (link_id, reporter_id) DO UPDATE SET
			report_id   = CASE WHEN EXCLUDED.reported_at > action_link_reports.reported_at THEN EXCLUDED.report_id ELSE action_link_reports.report_id END,
			reported_at = GREATEST(action_link_reports.reported_at, EXCLUDED.reported_at)`,
		link.ID, in.ReporterID, in.ReportID, in.ReportedAt); err != nil {
		return out, fmt.Errorf("record action link report: %w", err)
	}
	out.Counted = true
	since := in.Now.Add(-in.Window)
	if link.ModeratedAt != nil && link.ModeratedAt.After(since) {
		since = *link.ModeratedAt
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM action_link_reports WHERE link_id = $1 AND reported_at > $2`,
		link.ID, since).Scan(&out.Reporters); err != nil {
		return out, err
	}
	if out.Reporters >= in.Threshold && link.Status == ActionStatusActive {
		if _, err := tx.Exec(ctx, `
			UPDATE action_links SET status = 'under_review', status_reason = 'reports', updated_at = NOW()
			WHERE id = $1`, link.ID); err != nil {
			return out, err
		}
		out.Held = true
	}
	return out, tx.Commit(ctx)
}

// Moderator decisions on a link.
const (
	ActionDecisionRestore = "restore"
	ActionDecisionBlock   = "block"
)

// ReviewActionLink applies a moderator's decision, audited in
// post_admin_audit in the same transaction: restore (blocked or
// under_review → active) or block (active or under_review → blocked,
// status_reason moderator). Either stamps moderated_at, which resets the
// report count. ErrActionLinkNotFound for an unknown or removed link,
// ErrActionLinkTransition for a decision its state does not admit.
func (s *Store) ReviewActionLink(ctx context.Context, id, actor uuid.UUID, decision, note string) (*ActionLink, error) {
	if actor == uuid.Nil {
		return nil, ErrAdminAuditActor
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	cur, err := scanActionLink(tx.QueryRow(ctx, `SELECT `+actionLinkCols+` FROM action_links WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.Status == ActionStatusRemoved) {
		return nil, ErrActionLinkNotFound
	}
	if err != nil {
		return nil, err
	}
	var next string
	var reason *string
	switch decision {
	case ActionDecisionRestore:
		if cur.Status != ActionStatusBlocked && cur.Status != ActionStatusUnderReview {
			return nil, ErrActionLinkTransition
		}
		next = ActionStatusActive
	case ActionDecisionBlock:
		if cur.Status != ActionStatusActive && cur.Status != ActionStatusUnderReview {
			return nil, ErrActionLinkTransition
		}
		next = ActionStatusBlocked
		r := ActionReasonModerator
		reason = &r
	default:
		return nil, ErrActionLinkTransition
	}
	l, err := scanActionLink(tx.QueryRow(ctx, `
		UPDATE action_links SET status = $2, status_reason = $3, moderated_at = NOW(), moderated_by = $4, updated_at = NOW()
		WHERE id = $1
		RETURNING `+actionLinkCols, id, next, reason, actor))
	if err != nil {
		return nil, err
	}
	if err := insertAdminAudit(ctx, tx, actor, "action_link.review", "action_link", id, cur.Status, next, note); err != nil {
		return nil, err
	}
	return l, tx.Commit(ctx)
}

// ListActionLinksByStatus is the moderation queue: links in status, most
// recently changed first, before the cursor (zero = from the top).
func (s *Store) ListActionLinksByStatus(ctx context.Context, status string, before time.Time, limit int) ([]*ActionLink, error) {
	if before.IsZero() {
		before = time.Now().Add(time.Hour)
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+actionLinkCols+` FROM action_links
		WHERE status = $1 AND updated_at < $2
		ORDER BY updated_at DESC LIMIT $3`, status, before, limit)
	if err != nil {
		return nil, err
	}
	return scanActionLinkRows(rows)
}
