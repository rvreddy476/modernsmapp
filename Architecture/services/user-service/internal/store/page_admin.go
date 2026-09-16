package store

// Platform decisions on business pages and their verification documents,
// each written with its page_admin_audit row in ONE transaction
// (database/migrations/009_page_admin_audit.sql). Both entry points use these:
// the admin-service token family and the legacy PAGES_ADMIN_USER_IDS routes.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/user-service/internal/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audit actions (page_admin_audit.action).
const (
	PageActionApprove     = "page.approve"
	PageActionReject      = "page.reject"
	PageActionSuspend     = "page.suspend"
	PageActionDisable     = "page.disable"
	DocumentActionApprove = "document.approve"
	DocumentActionReject  = "document.reject"
)

// Audit entry points (page_admin_audit.via).
const (
	ViaAdminService   = "admin_service"
	ViaPagesAllowlist = "pages_allowlist"
)

var (
	// ErrPageStatusChanged: the page is no longer in the status the caller
	// decided on (and authorized against). Answer 409.
	ErrPageStatusChanged = errors.New("PAGE_STATUS_CHANGED")
	// ErrIllegalPageTransition: the state machine forbids this move. 409.
	ErrIllegalPageTransition = errors.New("ILLEGAL_PAGE_TRANSITION")
	// ErrPageDocumentNotFound: no such document (on that page). 404.
	ErrPageDocumentNotFound = errors.New("PAGE_DOCUMENT_NOT_FOUND")
	// ErrAdminActorRequired: an audited decision needs a real actor.
	ErrAdminActorRequired = errors.New("ADMIN_ACTOR_REQUIRED")
)

// PageStatusDecision is one admin lifecycle decision on a page.
type PageStatusDecision struct {
	PageID uuid.UUID
	// From is the status the caller read and authorized against. The row is
	// locked and must still be in it.
	From      string
	To        string
	Action    string
	Actor     uuid.UUID
	Via       string
	Reason    string
	RequestID string
}

// PageDocumentDecision is one admin decision on a verification document.
type PageDocumentDecision struct {
	DocumentID uuid.UUID
	// PageID, when set, must own the document; uuid.Nil skips the check
	// (the legacy route never checked it).
	PageID    uuid.UUID
	Status    string // approved | rejected
	Action    string
	Actor     uuid.UUID
	Via       string
	Reason    string
	RequestID string
}

// AdminSetPageStatus applies a decision and its audit row atomically.
func (s *Store) AdminSetPageStatus(ctx context.Context, d PageStatusDecision) error {
	if d.Actor == uuid.Nil {
		return ErrAdminActorRequired
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var current string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM business_pages WHERE id=$1 FOR UPDATE`, d.PageID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPageNotFound
		}
		return err
	}
	if current != d.From {
		return ErrPageStatusChanged
	}
	if !pages.CanTransition(current, d.To) {
		return ErrIllegalPageTransition
	}
	if err := applyPageStatus(ctx, tx, d.PageID, d.To, d.Actor, d.Reason); err != nil {
		return err
	}
	if err := insertPageAdminAudit(ctx, tx, d.Actor, d.Via, d.Action, d.PageID, nil, current, d.To, d.Reason, d.RequestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminDecidePageDocument sets a document's review status and writes its
// audit row atomically.
func (s *Store) AdminDecidePageDocument(ctx context.Context, d PageDocumentDecision) error {
	if d.Actor == uuid.Nil {
		return ErrAdminActorRequired
	}
	if d.Status != "approved" && d.Status != "rejected" {
		return fmt.Errorf("UNSUPPORTED_DOCUMENT_STATUS")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var pageID uuid.UUID
	var current string
	if err := tx.QueryRow(ctx,
		`SELECT page_id, status FROM page_verification_documents WHERE id=$1 FOR UPDATE`,
		d.DocumentID).Scan(&pageID, &current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPageDocumentNotFound
		}
		return err
	}
	if d.PageID != uuid.Nil && d.PageID != pageID {
		return ErrPageDocumentNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE page_verification_documents
		 SET status=$2, reviewed_by_user_id=$3, reviewed_at=NOW(), rejection_reason=$4
		 WHERE id=$1`, d.DocumentID, d.Status, d.Actor, d.Reason); err != nil {
		return err
	}
	docID := d.DocumentID
	if err := insertPageAdminAudit(ctx, tx, d.Actor, d.Via, d.Action, pageID, &docID, current, d.Status, d.Reason, d.RequestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertPageAdminAudit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, via, action string, pageID uuid.UUID,
	documentID *uuid.UUID, prev, next, reason, requestID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO page_admin_audit
			(actor_user_id, via, action, page_id, document_id, prev_status, new_status, reason, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''))`,
		actor, via, action, pageID, documentID, prev, next, strings.TrimSpace(reason), requestID)
	if err != nil {
		return fmt.Errorf("page admin audit: %w", err)
	}
	return nil
}

// AdminListPendingPages is the review queue: pages awaiting a decision,
// oldest submission first.
func (s *Store) AdminListPendingPages(ctx context.Context, limit, offset int) ([]BusinessPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `SELECT `+pageSelectCols+` FROM business_pages
		WHERE status = 'pending_review'
		ORDER BY submitted_at ASC NULLS LAST, created_at ASC, id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BusinessPage
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// PageAdminStats are the business-pages counts for the Content dashboard.
type PageAdminStats struct {
	PendingReview    int64     `json:"pending_review"`
	Approved7d       int64     `json:"approved_7d"`
	Rejected7d       int64     `json:"rejected_7d"`
	Suspended7d      int64     `json:"suspended_7d"`
	DocumentsPending int64     `json:"documents_pending"`
	WindowDays       int       `json:"window_days"`
	AsOf             time.Time `json:"as_of"`
}

// PageAdminStats counts the review queue and the last seven days' decisions.
// Decisions are read from the pages' own decision timestamps, so decisions
// taken before the audit table existed count too; a page approved and then
// suspended inside the window counts in both.
func (s *Store) PageAdminStats(ctx context.Context) (*PageAdminStats, error) {
	st := &PageAdminStats{WindowDays: 7}
	err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM business_pages WHERE status = 'pending_review'),
			(SELECT COUNT(*) FROM business_pages WHERE approved_at  >= now() - interval '7 days'),
			(SELECT COUNT(*) FROM business_pages WHERE rejected_at  >= now() - interval '7 days'),
			(SELECT COUNT(*) FROM business_pages WHERE suspended_at >= now() - interval '7 days'),
			(SELECT COUNT(*) FROM page_verification_documents d
			   JOIN business_pages p ON p.id = d.page_id
			  WHERE d.status = 'pending' AND p.status <> 'disabled'),
			now()`).Scan(&st.PendingReview, &st.Approved7d, &st.Rejected7d, &st.Suspended7d, &st.DocumentsPending, &st.AsOf)
	if err != nil {
		return nil, err
	}
	return st, nil
}
