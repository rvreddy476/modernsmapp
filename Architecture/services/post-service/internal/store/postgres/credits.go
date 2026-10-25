package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Video credits — cast & crew on a long video (2026-10-23, migration 070).

	The service validates every value and decides, per credit, whether the tag
	may link an account at all (exists, no block either way: service/credits.go).
	This file owns everything that has to happen inside one transaction:

	  * the replace — the list is written whole, in order, but a tag whose
	    (user_id, role) is still present keeps its row id, status and answer
	    time (PlanCreditReplace), so an unrelated edit never re-asks anyone
	    and an accept / decline link keeps pointing at the same row;
	  * the uploader's tagging limit — at most CreditTagsPerDay NEW pending
	    tags per 24 hours, counted from post_credit_tag_events under a
	    per-uploader advisory lock so two concurrent edits cannot both spend
	    the last slot; a tag over the limit is stored as plain text;
	  * the post.credit.requested outbox event for every new pending tag, and
	    post.credit.accepted on the tagged person's accept.
*/

// Credit statuses (chk_post_credits_status).
const (
	CreditUnlinked = "unlinked"
	CreditPending  = "pending"
	CreditAccepted = "accepted"
	CreditDeclined = "declined"
)

// CreditTagsPerDay is how many NEW pending tags one uploader may create in
// any 24 hours. Self-credits and tags an edit kept do not count.
const CreditTagsPerDay = 20

// creditTagWindow is the window CreditTagsPerDay applies to.
const creditTagWindow = 24 * time.Hour

// ErrCreditNotFound: no credit with that id tags the caller (it does not
// exist, tags someone else, is plain text, or its post is deleted). One
// error for all of them, so the consent routes cannot probe other tags.
var ErrCreditNotFound = errors.New("credit not found")

// CreditInput is one credit as a client sends it (create, PATCH, drafts) and
// as a reel draft stores it (reel_drafts.credits).
type CreditInput struct {
	Name   string     `json:"name"`
	Role   string     `json:"role"`
	UserID *uuid.UUID `json:"user_id,omitempty"`
}

// PostCredit is one post_credits row.
type PostCredit struct {
	ID          uuid.UUID
	PostID      uuid.UUID
	Position    int
	Name        string
	Role        string
	UserID      *uuid.UUID
	Status      string
	CreatedAt   time.Time
	RespondedAt *time.Time
}

// CreditCandidate is one credit as the service resolved it for the write.
// UserID is set only when the tag may link that account; Self marks the
// uploader crediting themselves (accepted at once).
type CreditCandidate struct {
	Name   string
	Role   string
	UserID *uuid.UUID
	Self   bool
}

// CreditsWrite asks a post write (create or edit) to replace the post's
// credits inside its own transaction. PostTitle and UploaderName ride on the
// request events. Result is filled in by the store.
type CreditsWrite struct {
	Candidates   []CreditCandidate
	UploaderName string
	PostTitle    string
	Result       CreditReplaceResult
}

// CreditReplaceResult is what one replace did.
type CreditReplaceResult struct {
	Credits []PostCredit
	// NewPending are the tags this write created as pending (and announced).
	NewPending []PostCredit
	// Skipped is how many tags went over the uploader's daily limit and were
	// stored as plain text (meta.credit_tags_skipped).
	Skipped int
	// Changed reports that the visible list (names and roles, in order)
	// differs from what was stored, i.e. search must re-read the post.
	Changed bool
}

func creditKey(userID uuid.UUID, role string) string {
	return userID.String() + "|" + strings.ToLower(strings.TrimSpace(role))
}

// PlanCreditReplace is the replace decided in memory (pure, apart from new
// row ids): existing is the post's current rows, candidates the new list in
// order, quota how many new pending tags the uploader may still create.
//
//   - a candidate without UserID is plain text;
//   - a candidate whose (user_id, role) matches an existing tagged row keeps
//     that row's id, status, created_at and responded_at — whatever the
//     status, so a declined tag stays declined and an accepted one accepted;
//   - otherwise a self-credit is accepted, and any other tag is pending
//     while quota lasts and plain text after it (skipped++);
//   - the same (user_id, role) twice in one list links once; the repeat is
//     plain text and costs nothing.
func PlanCreditReplace(existing []PostCredit, candidates []CreditCandidate, quota int, now time.Time) (rows, newPending []PostCredit, skipped int) {
	byKey := map[string][]PostCredit{}
	for _, e := range existing {
		if e.UserID == nil {
			continue
		}
		k := creditKey(*e.UserID, e.Role)
		byKey[k] = append(byKey[k], e)
	}
	seen := map[string]bool{}
	rows = make([]PostCredit, 0, len(candidates))
	for i, c := range candidates {
		row := PostCredit{ID: uuid.New(), Position: i, Name: c.Name, Role: c.Role, Status: CreditUnlinked, CreatedAt: now}
		if c.UserID != nil {
			k := creditKey(*c.UserID, c.Role)
			switch {
			case seen[k]:
				// Repeat in this list: plain text.
			case len(byKey[k]) > 0:
				kept := byKey[k][0]
				byKey[k] = byKey[k][1:]
				row.ID, row.UserID, row.Status = kept.ID, kept.UserID, kept.Status
				row.CreatedAt, row.RespondedAt = kept.CreatedAt, kept.RespondedAt
			case c.Self:
				uid := *c.UserID
				answered := now
				row.UserID, row.Status, row.RespondedAt = &uid, CreditAccepted, &answered
			case quota > 0:
				uid := *c.UserID
				row.UserID, row.Status = &uid, CreditPending
				quota--
				newPending = append(newPending, row)
			default:
				skipped++
			}
			seen[k] = true
		}
		rows = append(rows, row)
	}
	return rows, newPending, skipped
}

// sameCreditText reports whether two lists show the same names and roles in
// the same order (the part search indexes).
func sameCreditText(a, b []PostCredit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Role != b[i].Role {
			return false
		}
	}
	return true
}

const postCreditCols = `id, post_id, position, name, role, user_id, status, created_at, responded_at` //nolint:gosec // G101: SQL column list, not a credential

func scanPostCredits(rows pgx.Rows) ([]PostCredit, error) {
	var out []PostCredit
	for rows.Next() {
		var c PostCredit
		if err := rows.Scan(&c.ID, &c.PostID, &c.Position, &c.Name, &c.Role, &c.UserID, &c.Status, &c.CreatedAt, &c.RespondedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// replaceCreditsTx replaces the post's credits inside tx (create or edit).
func replaceCreditsTx(ctx context.Context, tx pgx.Tx, postID, uploaderID uuid.UUID, w *CreditsWrite) error {
	// One uploader's writes queue behind each other, so the limit below is
	// counted and spent atomically.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('post_credit_tags:' || $1::text, 0))`, uploaderID); err != nil {
		return fmt.Errorf("lock credit tags: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT `+postCreditCols+` FROM post_credits WHERE post_id = $1 ORDER BY position FOR UPDATE`, postID)
	if err != nil {
		return fmt.Errorf("load credits: %w", err)
	}
	existing, err := scanPostCredits(rows)
	rows.Close()
	if err != nil {
		return fmt.Errorf("load credits: %w", err)
	}

	quota := 0
	if needsCreditQuota(w.Candidates) {
		if _, err := tx.Exec(ctx, `DELETE FROM post_credit_tag_events WHERE uploader_id = $1 AND created_at < NOW() - $2::interval`,
			uploaderID, intervalArg(creditTagWindow)); err != nil {
			return fmt.Errorf("prune credit tags: %w", err)
		}
		var used int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM post_credit_tag_events WHERE uploader_id = $1 AND created_at >= NOW() - $2::interval`,
			uploaderID, intervalArg(creditTagWindow)).Scan(&used); err != nil {
			return fmt.Errorf("count credit tags: %w", err)
		}
		quota = CreditTagsPerDay - used
		if quota < 0 {
			quota = 0
		}
	}

	now := time.Now().UTC()
	planned, newPending, skipped := PlanCreditReplace(existing, w.Candidates, quota, now)
	if _, err := tx.Exec(ctx, `DELETE FROM post_credits WHERE post_id = $1`, postID); err != nil {
		return fmt.Errorf("clear credits: %w", err)
	}
	for i := range planned {
		planned[i].PostID = postID
		c := planned[i]
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_credits (`+postCreditCols+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			c.ID, postID, c.Position, c.Name, c.Role, c.UserID, c.Status, c.CreatedAt, c.RespondedAt); err != nil {
			return fmt.Errorf("insert credit: %w", err)
		}
	}
	for i := range newPending {
		newPending[i].PostID = postID
		c := newPending[i]
		if _, err := tx.Exec(ctx, `INSERT INTO post_credit_tag_events (uploader_id, credit_id, created_at) VALUES ($1, $2, $3)`,
			uploaderID, c.ID, now); err != nil {
			return fmt.Errorf("record credit tag: %w", err)
		}
		if err := InsertOutboxEventTx(ctx, tx, events.EventPostCreditRequested, "post", postID, events.PostCreditRequestedPayload{
			CreditID:     c.ID.String(),
			PostID:       postID.String(),
			UploaderID:   uploaderID.String(),
			TaggedUserID: c.UserID.String(),
			Name:         c.Name,
			Role:         c.Role,
			PostTitle:    w.PostTitle,
			UploaderName: w.UploaderName,
			CreatedAt:    now,
		}); err != nil {
			return fmt.Errorf("emit credit request: %w", err)
		}
	}
	w.Result = CreditReplaceResult{
		Credits:    planned,
		NewPending: newPending,
		Skipped:    skipped,
		Changed:    !sameCreditText(existing, planned),
	}
	return nil
}

// needsCreditQuota: only a list that may create a new pending tag reads the
// ledger.
func needsCreditQuota(cs []CreditCandidate) bool {
	for _, c := range cs {
		if c.UserID != nil && !c.Self {
			return true
		}
	}
	return false
}

func intervalArg(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d/time.Second))
}

// ListPostCredits returns a post's credits in order.
func (s *Store) ListPostCredits(ctx context.Context, postID uuid.UUID) ([]PostCredit, error) {
	rows, err := s.db.Query(ctx, `SELECT `+postCreditCols+` FROM post_credits WHERE post_id = $1 ORDER BY position`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostCredits(rows)
}

// Consent actions on a credit, by the tagged person.
const (
	CreditActionAccept  = "accept"
	CreditActionDecline = "decline"
	CreditActionRemove  = "remove"
)

// NextCreditStatus is the consent transition (pure). from is a linked status
// (pending, accepted, declined). announce is true only on the transition
// INTO accepted, so a repeated accept tells the uploader nothing twice.
//
//	accept   pending | declined | accepted → accepted
//	decline  pending | declined | accepted → declined (the credit stays text)
//	remove   any linked status            → unlinked (the link is gone, the
//	                                         name stays as plain text)
func NextCreditStatus(from, action string) (to string, announce bool, ok bool) {
	switch from {
	case CreditPending, CreditAccepted, CreditDeclined:
	default:
		return "", false, false
	}
	switch action {
	case CreditActionAccept:
		return CreditAccepted, from != CreditAccepted, true
	case CreditActionDecline:
		return CreditDeclined, false, true
	case CreditActionRemove:
		return CreditUnlinked, false, true
	}
	return "", false, false
}

// CreditAnswerAllowed is the consent routes' ownership rule (pure): only the
// account a live credit tags may answer it. A plain-text credit has nobody
// to answer, and a deleted post's credits are not answerable.
func CreditAnswerAllowed(taggedUserID *uuid.UUID, callerID uuid.UUID, postDeleted bool) bool {
	return taggedUserID != nil && callerID != uuid.Nil && *taggedUserID == callerID && !postDeleted
}

// RespondToCredit applies the tagged person's answer. Only the account the
// credit tags may answer; anyone else, a plain-text credit, a missing id and
// a deleted post are all ErrCreditNotFound. Idempotent: answering twice
// leaves the row as the first answer left it.
func (s *Store) RespondToCredit(ctx context.Context, creditID, callerID uuid.UUID, action string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		userID             *uuid.UUID
		status, name, role string
		postID, uploaderID uuid.UUID
		title              string
		deletedAt          *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT c.user_id, c.status, c.name, c.role, c.post_id, p.author_id, COALESCE(p.title, ''), p.deleted_at
		FROM post_credits c JOIN posts p ON p.id = c.post_id
		WHERE c.id = $1
		FOR UPDATE OF c`, creditID).
		Scan(&userID, &status, &name, &role, &postID, &uploaderID, &title, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrCreditNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load credit: %w", err)
	}
	if !CreditAnswerAllowed(userID, callerID, deletedAt != nil) {
		return "", ErrCreditNotFound
	}
	to, announce, ok := NextCreditStatus(status, action)
	if !ok {
		return "", ErrCreditNotFound
	}
	if to != status {
		now := time.Now().UTC()
		if to == CreditUnlinked {
			_, err = tx.Exec(ctx, `UPDATE post_credits SET status = $2, user_id = NULL, responded_at = $3 WHERE id = $1`, creditID, to, now)
		} else {
			_, err = tx.Exec(ctx, `UPDATE post_credits SET status = $2, responded_at = $3 WHERE id = $1`, creditID, to, now)
		}
		if err != nil {
			return "", fmt.Errorf("answer credit: %w", err)
		}
		if announce {
			if err := InsertOutboxEventTx(ctx, tx, events.EventPostCreditAccepted, "post", postID, events.PostCreditAcceptedPayload{
				CreditID:     creditID.String(),
				PostID:       postID.String(),
				UploaderID:   uploaderID.String(),
				TaggedUserID: callerID.String(),
				Name:         name,
				Role:         role,
				PostTitle:    title,
				AcceptedAt:   now,
			}); err != nil {
				return "", fmt.Errorf("emit credit accepted: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return to, nil
}

// CreditRequestRow is one of my pending requests, with the post it is on.
type CreditRequestRow struct {
	ID          uuid.UUID
	Name        string
	Role        string
	CreatedAt   time.Time
	PostID      uuid.UUID
	PostTitle   string
	CoverURL    *string
	UploaderID  uuid.UUID
	ChannelName *string
}

// CreditCursor is the (created_at, id) keyset both credit lists page by.
type CreditCursor struct {
	At time.Time
	ID uuid.UUID
}

// EncodeCreditCursor renders a cursor for meta.next_cursor.
func EncodeCreditCursor(c CreditCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.At.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()))
}

// ErrInvalidCreditCursor: a cursor these lists did not issue.
var ErrInvalidCreditCursor = errors.New("cursor is not a value this endpoint issued")

// DecodeCreditCursor parses a cursor; "" is the first page (nil).
func DecodeCreditCursor(raw string) (*CreditCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCreditCursor
	}
	at, id, found := strings.Cut(string(b), "|")
	if !found {
		return nil, ErrInvalidCreditCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, ErrInvalidCreditCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, ErrInvalidCreditCursor
	}
	return &CreditCursor{At: t, ID: u}, nil
}

// ListCreditRequests returns the caller's pending tags, newest first, on
// posts that are not deleted. limit+1 rows are read to know about a next
// page; the cursor names the last row returned.
func (s *Store) ListCreditRequests(ctx context.Context, userID uuid.UUID, cursor *CreditCursor, limit int) ([]CreditRequestRow, *CreditCursor, error) {
	args := []any{userID, limit + 1}
	keyset := ""
	if cursor != nil {
		keyset = `AND (c.created_at, c.id) < ($3, $4)`
		args = append(args, cursor.At, cursor.ID)
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.name, c.role, c.created_at, p.id, COALESCE(p.title, ''),
		       COALESCE(NULLIF(vm.thumbnail_url, ''),
		                CASE WHEN p.cover_media_id IS NOT NULL THEN '/v1/media/' || p.cover_media_id::text || '/serve' END),
		       p.author_id, ch.name
		FROM post_credits c
		JOIN posts p ON p.id = c.post_id
		LEFT JOIN video_metadata vm ON vm.post_id = p.id
		LEFT JOIN channels ch ON ch.user_id = p.author_id
		WHERE c.user_id = $1 AND c.status = 'pending' AND p.deleted_at IS NULL `+keyset+`
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []CreditRequestRow
	for rows.Next() {
		var r CreditRequestRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Role, &r.CreatedAt, &r.PostID, &r.PostTitle, &r.CoverURL, &r.UploaderID, &r.ChannelName); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *CreditCursor
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = &CreditCursor{At: last.CreatedAt, ID: last.ID}
	}
	return out, next, nil
}

// ListAppearancePostIDs returns, newest first, the ids of the public,
// published, approved long videos on which userID's credit is accepted —
// the candidates for "Appears in". Members-only, scheduled, deleted,
// unlisted / followers / friends / private and held posts never qualify
// here; the service then puts every id through the ordinary per-viewer gate
// (blocks, private and hidden authors, the age gate).
func (s *Store) ListAppearancePostIDs(ctx context.Context, userID uuid.UUID, cursor *CreditCursor, limit int) ([]uuid.UUID, *CreditCursor, error) {
	args := []any{userID, limit + 1}
	keyset := ""
	if cursor != nil {
		keyset = `AND (p.created_at, p.id) < ($3, $4)`
		args = append(args, cursor.At, cursor.ID)
	}
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.created_at
		FROM posts p
		WHERE p.deleted_at IS NULL
		  AND p.publish_at IS NULL
		  AND p.visibility = 'public'
		  AND p.content_type IN ('long_video', 'video')
		  AND p.tier_required_id IS NULL
		  AND `+viewerApproved("p")+`
		  AND EXISTS (SELECT 1 FROM post_credits c
		              WHERE c.post_id = p.id AND c.user_id = $1 AND c.status = 'accepted')
		  `+keyset+`
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	type row struct {
		id uuid.UUID
		at time.Time
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.at); err != nil {
			return nil, nil, err
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *CreditCursor
	if len(got) > limit {
		got = got[:limit]
		last := got[len(got)-1]
		next = &CreditCursor{At: last.at, ID: last.id}
	}
	ids := make([]uuid.UUID, len(got))
	for i, r := range got {
		ids[i] = r.id
	}
	return ids, next, nil
}

// IsLongVideoContentType: the kinds credits belong to (legacy "video"
// included).
func IsLongVideoContentType(ct string) bool {
	return ct == "long_video" || ct == "video"
}

// rowQuerier is what creditSearchFields reads through: a transaction or the
// pool.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// creditSearchFields is a long video's search projection of its credits:
// every name and role in order (plain text whatever the tag's status),
// genres, year and production house.
func creditSearchFields(ctx context.Context, q rowQuerier, postID uuid.UUID) (events.PostCreditSearchFields, error) {
	var f events.PostCreditSearchFields
	var year *int
	var house *string
	err := q.QueryRow(ctx, `
		SELECT COALESCE((SELECT array_agg(c.name ORDER BY c.position) FROM post_credits c WHERE c.post_id = p.id), '{}'),
		       COALESCE((SELECT array_agg(c.role ORDER BY c.position) FROM post_credits c WHERE c.post_id = p.id), '{}'),
		       p.genres, p.release_year, p.production_house
		FROM posts p WHERE p.id = $1`, postID).Scan(&f.CreditNames, &f.CreditRoles, &f.Genres, &year, &house)
	if err != nil {
		return f, fmt.Errorf("read credit search fields: %w", err)
	}
	if len(f.CreditNames) == 0 {
		f.CreditNames, f.CreditRoles = nil, nil
	}
	if len(f.Genres) == 0 {
		f.Genres = nil
	}
	if year != nil {
		f.ReleaseYear = *year
	}
	if house != nil {
		f.ProductionHouse = *house
	}
	return f, nil
}

// CreditSearchFields is creditSearchFields over the pool, for a PostCreated
// built from a stored post (the schedule worker).
// found is false for a post that is not stored (yet): a post being created
// carries its own fields.
func (s *Store) CreditSearchFields(ctx context.Context, postID uuid.UUID) (events.PostCreditSearchFields, bool, error) {
	f, err := creditSearchFields(ctx, s.db, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, false, nil
	}
	return f, err == nil, err
}

// marshalDraftCredits renders a draft's credits for reel_drafts.credits
// (nil = no credits saved: SQL NULL).
func marshalDraftCredits(cs []CreditInput) ([]byte, error) {
	if cs == nil {
		return nil, nil
	}
	return json.Marshal(cs)
}
