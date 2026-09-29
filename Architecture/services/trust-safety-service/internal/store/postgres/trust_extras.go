package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Structs ──────────────────────────────────────────────────────────────────
//
// ContentAppeal and the appeal methods live in appeals.go.

type KeywordFilter struct {
	ID        uuid.UUID  `json:"id"`
	Scope     string     `json:"scope"`
	ScopeID   *uuid.UUID `json:"scope_id,omitempty"`
	Keyword   string     `json:"keyword"`
	Action    string     `json:"action"`
	AddedBy   uuid.UUID  `json:"added_by"`
	CreatedAt time.Time  `json:"created_at"`
}

type TeenAccount struct {
	UserID           uuid.UUID  `json:"user_id"`
	GuardianID       *uuid.UUID `json:"guardian_id,omitempty"`
	GuardianApproved bool       `json:"guardian_approved"`
	DailyLimitMins   int        `json:"daily_limit_mins"`
	ContentFilter    string     `json:"content_filter"`
	DMRestricted     bool       `json:"dm_restricted"`
	FollowerApproval bool       `json:"follower_approval"`
	LocationHidden   bool       `json:"location_hidden"`
	CreatedAt        time.Time  `json:"created_at"`
}

type MediaLabel struct {
	ID           uuid.UUID `json:"id"`
	MediaAssetID uuid.UUID `json:"media_asset_id"`
	LabelType    string    `json:"label_type"`
	Confidence   float32   `json:"confidence"`
	Source       string    `json:"source"`
	LabeledAt    time.Time `json:"labeled_at"`
}

type UserStrike struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	Reason      string     `json:"reason"`
	ContentType *string    `json:"content_type,omitempty"`
	ContentID   *uuid.UUID `json:"content_id,omitempty"`
	Severity    string     `json:"severity"`
	// CaseID links the strike to the case that produced it; nil for a
	// strike issued by hand. StrikeGroup is a free label for policies that
	// count strikes per group.
	CaseID      *uuid.UUID `json:"case_id,omitempty"`
	StrikeGroup *string    `json:"strike_group,omitempty"`
	// PolicyVersion names the policy that set ExpiresAt (StrikePolicyVersion
	// for new rows; LegacyStrikePolicyVersion for rows backfilled by 011).
	PolicyVersion string `json:"policy_version"`
	// IdempotencyKey makes a retried issue return this row instead of a
	// second strike. Unique where set.
	IdempotencyKey *string `json:"idempotency_key,omitempty"`
	// ExpiresAt is always set: issued_at + the policy duration.
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	// CreatedAt is the issue time (issued_at on the wire).
	CreatedAt time.Time `json:"created_at"`
	// A voided strike stays in the table and never counts again.
	VoidedAt   *time.Time `json:"voided_at,omitempty"`
	VoidReason *string    `json:"void_reason,omitempty"`
	VoidedBy   *uuid.UUID `json:"voided_by,omitempty"`
}

// Active reports whether the strike counts at now: not voided, not expired.
func (st *UserStrike) Active(now time.Time) bool {
	return st.VoidedAt == nil && st.ExpiresAt.After(now)
}

type VerificationRequest struct {
	ID              uuid.UUID         `json:"id"`
	UserID          uuid.UUID         `json:"user_id"`
	Type            string            `json:"type"`
	Status          string            `json:"status"`
	SubmittedDocs   map[string]string `json:"submitted_docs,omitempty"`
	RejectionReason *string           `json:"rejection_reason,omitempty"`
	ReviewedBy      *uuid.UUID        `json:"reviewed_by,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// ─── Store ────────────────────────────────────────────────────────────────────

type TrustExtrasStore struct {
	db *pgxpool.Pool
	// eventsTopic is where strike events are enqueued for (see
	// WithEventsTopic).
	eventsTopic string
}

func NewExtrasStore(db *pgxpool.Pool) *TrustExtrasStore {
	return &TrustExtrasStore{db: db}
}

// ─── Keyword filter methods ───────────────────────────────────────────────────

func (s *TrustExtrasStore) CreateKeywordFilter(ctx context.Context, f *KeywordFilter) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.keyword_filters (id, scope, scope_id, keyword, action, added_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, f.ID, f.Scope, f.ScopeID, f.Keyword, f.Action, f.AddedBy, f.CreatedAt)
	return err
}

func (s *TrustExtrasStore) GetKeywordFilters(ctx context.Context, scope string, scopeID *uuid.UUID) ([]KeywordFilter, error) {
	var query string
	var args []interface{}

	if scopeID != nil {
		query = `
			SELECT id, scope, scope_id, keyword, action, added_by, created_at
			FROM trust.keyword_filters
			WHERE scope = $1 AND scope_id = $2
			ORDER BY created_at DESC`
		args = []interface{}{scope, scopeID}
	} else {
		query = `
			SELECT id, scope, scope_id, keyword, action, added_by, created_at
			FROM trust.keyword_filters
			WHERE scope = $1 AND scope_id IS NULL
			ORDER BY created_at DESC`
		args = []interface{}{scope}
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var filters []KeywordFilter
	for rows.Next() {
		var f KeywordFilter
		if err := rows.Scan(&f.ID, &f.Scope, &f.ScopeID, &f.Keyword, &f.Action, &f.AddedBy, &f.CreatedAt); err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	return filters, nil
}

func (s *TrustExtrasStore) DeleteKeywordFilter(ctx context.Context, id, addedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM trust.keyword_filters WHERE id = $1 AND added_by = $2
	`, id, addedBy)
	return err
}

// ListUserHideKeywords returns the user's own self-service hide list —
// scope='user', scope_id=userID, action='hide' — in a stable order.
func (s *TrustExtrasStore) ListUserHideKeywords(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT keyword FROM trust.keyword_filters
		WHERE scope = 'user' AND scope_id = $1 AND action = 'hide'
		ORDER BY created_at ASC, keyword ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keywords := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keywords = append(keywords, k)
	}
	return keywords, rows.Err()
}

// ReplaceUserHideKeywords atomically replaces the user's self-service hide
// list (scope='user', action='hide') inside one transaction: either the new
// set is fully in place or the old set is untouched. Rows the user did not
// write (other scopes, other actions) are never touched.
func (s *TrustExtrasStore) ReplaceUserHideKeywords(ctx context.Context, userID uuid.UUID, keywords []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		DELETE FROM trust.keyword_filters
		WHERE scope = 'user' AND scope_id = $1 AND action = 'hide'
	`, userID); err != nil {
		return err
	}
	// NOW() is transaction-stable, so stagger created_at by the submission
	// index — the list read back in created_at order is the list submitted.
	for i, k := range keywords {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trust.keyword_filters (id, scope, scope_id, keyword, action, added_by, created_at)
			VALUES ($1, 'user', $2, $3, 'hide', $2, NOW() + ($4 * interval '1 microsecond'))
		`, uuid.New(), userID, k, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *TrustExtrasStore) MatchKeywords(ctx context.Context, scope string, scopeID *uuid.UUID, text string) ([]KeywordFilter, error) {
	filters, err := s.GetKeywordFilters(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(text)
	var matched []KeywordFilter
	for _, f := range filters {
		if strings.Contains(lower, strings.ToLower(f.Keyword)) {
			matched = append(matched, f)
		}
	}
	return matched, nil
}

// ─── Teen account methods ─────────────────────────────────────────────────────

func (s *TrustExtrasStore) UpsertTeenAccount(ctx context.Context, t *TeenAccount) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.teen_accounts
			(user_id, guardian_id, guardian_approved, daily_limit_mins, content_filter,
			 dm_restricted, follower_approval, location_hidden, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id) DO UPDATE SET
			guardian_id       = EXCLUDED.guardian_id,
			daily_limit_mins  = EXCLUDED.daily_limit_mins,
			content_filter    = EXCLUDED.content_filter,
			dm_restricted     = EXCLUDED.dm_restricted,
			follower_approval = EXCLUDED.follower_approval,
			location_hidden   = EXCLUDED.location_hidden
	`, t.UserID, t.GuardianID, t.GuardianApproved, t.DailyLimitMins, t.ContentFilter,
		t.DMRestricted, t.FollowerApproval, t.LocationHidden, t.CreatedAt)
	return err
}

func (s *TrustExtrasStore) GetTeenAccount(ctx context.Context, userID uuid.UUID) (*TeenAccount, error) {
	var t TeenAccount
	err := s.db.QueryRow(ctx, `
		SELECT user_id, guardian_id, guardian_approved, daily_limit_mins, content_filter,
		       dm_restricted, follower_approval, location_hidden, created_at
		FROM trust.teen_accounts WHERE user_id = $1
	`, userID).Scan(
		&t.UserID, &t.GuardianID, &t.GuardianApproved, &t.DailyLimitMins, &t.ContentFilter,
		&t.DMRestricted, &t.FollowerApproval, &t.LocationHidden, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *TrustExtrasStore) SetGuardianApproval(ctx context.Context, userID uuid.UUID, approved bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE trust.teen_accounts SET guardian_approved = $2 WHERE user_id = $1
	`, userID, approved)
	return err
}

// ─── Media label methods ──────────────────────────────────────────────────────

func (s *TrustExtrasStore) CreateMediaLabel(ctx context.Context, l *MediaLabel) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.media_labels (id, media_asset_id, label_type, confidence, source, labeled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, l.ID, l.MediaAssetID, l.LabelType, l.Confidence, l.Source, l.LabeledAt)
	return err
}

func (s *TrustExtrasStore) GetMediaLabels(ctx context.Context, mediaAssetID uuid.UUID) ([]MediaLabel, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_asset_id, label_type, confidence, source, labeled_at
		FROM trust.media_labels WHERE media_asset_id = $1
		ORDER BY labeled_at DESC
	`, mediaAssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labels []MediaLabel
	for rows.Next() {
		var l MediaLabel
		if err := rows.Scan(&l.ID, &l.MediaAssetID, &l.LabelType, &l.Confidence, &l.Source, &l.LabeledAt); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, nil
}

// ─── Strike methods ───────────────────────────────────────────────────────────
//
// Strikes are issued and voided in one transaction with their audit row
// and outbox event (Copyright Match plan section 6.4). They are never
// deleted here; purge (purge.go) is the only DELETE, and that is the
// account-erasure path.

// StrikePolicyVersion is the policy new strikes carry. It fixes the
// duration: every severity expires StrikeDuration after issue.
const StrikePolicyVersion = "strike-v1"

// LegacyStrikePolicyVersion marks rows migration 011 gave a 90-day expiry
// (founder default F-15) because they had none.
const LegacyStrikePolicyVersion = "legacy-f15-90d"

// StrikeDuration is strike-v1's duration for every severity.
const StrikeDuration = 90 * 24 * time.Hour

// Audit target and actions for strikes.
const (
	AuditTargetStrike  = "strike"
	AuditStrikeIssued  = "strike.issued"
	AuditStrikeVoided  = "strike.voided"
	strikeStatusActive = "active"
	strikeStatusVoided = "voided"
)

// ErrStrikeNotFound is returned by VoidStrike when no strike has the id.
var ErrStrikeNotFound = errors.New("strike not found")

// WithEventsTopic sets the Kafka topic strike events are enqueued for
// (DefaultTrustEventsTopic when unset).
func (s *TrustExtrasStore) WithEventsTopic(topic string) *TrustExtrasStore {
	s.eventsTopic = strings.TrimSpace(topic)
	return s
}

func (s *TrustExtrasStore) topic() string {
	if s.eventsTopic == "" {
		return DefaultTrustEventsTopic
	}
	return s.eventsTopic
}

const strikeColumns = `id, user_id, reason, content_type, content_id, severity, case_id, strike_group,
	policy_version, idempotency_key, expires_at, created_by, created_at, voided_at, void_reason, voided_by`

func scanStrike(row pgx.Row) (*UserStrike, error) {
	var st UserStrike
	if err := row.Scan(&st.ID, &st.UserID, &st.Reason, &st.ContentType, &st.ContentID, &st.Severity,
		&st.CaseID, &st.StrikeGroup, &st.PolicyVersion, &st.IdempotencyKey, &st.ExpiresAt,
		&st.CreatedBy, &st.CreatedAt, &st.VoidedAt, &st.VoidReason, &st.VoidedBy); err != nil {
		return nil, err
	}
	return &st, nil
}

func strikeIssuedPayload(st *UserStrike) events.StrikeIssuedPayload {
	p := events.StrikeIssuedPayload{
		StrikeID:      st.ID.String(),
		UserID:        st.UserID.String(),
		Severity:      st.Severity,
		PolicyVersion: st.PolicyVersion,
		IssuedAt:      st.CreatedAt.UTC(),
		ExpiresAt:     st.ExpiresAt.UTC(),
	}
	if st.CaseID != nil {
		id := st.CaseID.String()
		p.CaseID = &id
	}
	return p
}

// IssueStrike inserts the strike, its strike.issued audit row and its
// StrikeIssued outbox event in ONE transaction. When st.IdempotencyKey
// names a strike that already exists, nothing is written and that row is
// returned with created=false: a retried issue is one strike.
//
// st.ID, st.CreatedAt and st.ExpiresAt are the caller's (the service sets
// them from one clock); meta.Actor must be a human admin.
func (s *TrustExtrasStore) IssueStrike(ctx context.Context, st *UserStrike, meta AuditMeta) (*UserStrike, bool, error) {
	if err := meta.Actor.Validate(); err != nil {
		return nil, false, err
	}
	if st == nil || st.ID == uuid.Nil || st.UserID == uuid.Nil || st.CreatedAt.IsZero() || !st.ExpiresAt.After(st.CreatedAt) {
		return nil, false, fmt.Errorf("issue strike: incomplete strike")
	}
	if st.PolicyVersion == "" {
		st.PolicyVersion = StrikePolicyVersion
	}
	var (
		stored  *UserStrike
		created bool
	)
	err := withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		// The partial unique index turns a repeated key into no row here.
		var insertedID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO trust.user_strikes
				(id, user_id, reason, content_type, content_id, severity, case_id, strike_group,
				 policy_version, idempotency_key, expires_at, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
			RETURNING id
		`, st.ID, st.UserID, st.Reason, st.ContentType, st.ContentID, st.Severity, st.CaseID, st.StrikeGroup,
			st.PolicyVersion, st.IdempotencyKey, st.ExpiresAt, st.CreatedBy, st.CreatedAt).Scan(&insertedID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Replay: hand back the strike the first call wrote.
			existing, err := scanStrike(tx.QueryRow(ctx, `SELECT `+strikeColumns+` FROM trust.user_strikes WHERE idempotency_key = $1`, st.IdempotencyKey))
			if err != nil {
				return fmt.Errorf("issue strike: read replay: %w", err)
			}
			stored = existing
			return nil
		}
		if err != nil {
			return fmt.Errorf("issue strike: insert: %w", err)
		}
		inserted, err := scanStrike(tx.QueryRow(ctx, `SELECT `+strikeColumns+` FROM trust.user_strikes WHERE id = $1`, insertedID))
		if err != nil {
			return fmt.Errorf("issue strike: read back: %w", err)
		}
		if err := insertAudit(ctx, tx, meta, auditChange{
			Action:     AuditStrikeIssued,
			TargetType: AuditTargetStrike,
			TargetID:   inserted.ID,
			NewStatus:  strikeStatusActive,
		}); err != nil {
			return err
		}
		ev, err := newEnvelopeEvent(s.topic(), events.StrikeIssued, inserted.UserID.String(), inserted.CreatedAt, strikeIssuedPayload(inserted))
		if err != nil {
			return err
		}
		if err := enqueueOutbox(ctx, tx, ev); err != nil {
			return err
		}
		stored, created = inserted, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return stored, created, nil
}

// VoidStrike voids the strike (never deletes it) and writes the
// strike.voided audit row and the StrikeVoided outbox event in ONE
// transaction. An already-voided strike is returned unchanged with
// changed=false and nothing is written: a void replay is a no-op. A strike
// that does not exist, or (when userID is set) belongs to another user,
// is ErrStrikeNotFound. meta.Actor must be a human admin; they become
// voided_by.
func (s *TrustExtrasStore) VoidStrike(ctx context.Context, id uuid.UUID, userID *uuid.UUID, reason string, meta AuditMeta) (*UserStrike, bool, error) {
	if err := meta.Actor.Validate(); err != nil {
		return nil, false, err
	}
	if meta.Actor.UserID == uuid.Nil {
		return nil, false, ErrActorRequired
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, false, fmt.Errorf("void strike: a reason is required")
	}
	var (
		stored  *UserStrike
		changed bool
	)
	err := withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		current, err := scanStrike(tx.QueryRow(ctx, `SELECT `+strikeColumns+` FROM trust.user_strikes WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStrikeNotFound
		}
		if err != nil {
			return fmt.Errorf("void strike: lock: %w", err)
		}
		if userID != nil && current.UserID != *userID {
			return ErrStrikeNotFound
		}
		if current.VoidedAt != nil {
			stored = current
			return nil
		}
		voided, err := scanStrike(tx.QueryRow(ctx, `
			UPDATE trust.user_strikes
			   SET voided_at = NOW(), void_reason = $2, voided_by = $3
			 WHERE id = $1 AND voided_at IS NULL
			RETURNING `+strikeColumns, id, reason, meta.Actor.UserID))
		if err != nil {
			return fmt.Errorf("void strike: update: %w", err)
		}
		if err := insertAudit(ctx, tx, meta, auditChange{
			Action:        AuditStrikeVoided,
			TargetType:    AuditTargetStrike,
			TargetID:      voided.ID,
			PrevStatus:    strikeStatusActive,
			NewStatus:     strikeStatusVoided,
			NewResolution: reason,
		}); err != nil {
			return err
		}
		ev, err := newEnvelopeEvent(s.topic(), events.StrikeVoided, voided.UserID.String(), *voided.VoidedAt, events.StrikeVoidedPayload{
			StrikeIssuedPayload: strikeIssuedPayload(voided),
			VoidedAt:            voided.VoidedAt.UTC(),
			VoidReason:          reason,
		})
		if err != nil {
			return err
		}
		if err := enqueueOutbox(ctx, tx, ev); err != nil {
			return err
		}
		stored, changed = voided, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return stored, changed, nil
}

// GetStrike returns one strike, voided or not.
func (s *TrustExtrasStore) GetStrike(ctx context.Context, id uuid.UUID) (*UserStrike, error) {
	return scanStrike(s.db.QueryRow(ctx, `SELECT `+strikeColumns+` FROM trust.user_strikes WHERE id = $1`, id))
}

// GetActiveStrikes lists the user's strikes that count now: not voided,
// not expired. Newest first; never nil.
func (s *TrustExtrasStore) GetActiveStrikes(ctx context.Context, userID uuid.UUID) ([]UserStrike, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+strikeColumns+`
		FROM trust.user_strikes
		WHERE user_id = $1 AND voided_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC, id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	strikes := []UserStrike{}
	for rows.Next() {
		st, err := scanStrike(rows)
		if err != nil {
			return nil, err
		}
		strikes = append(strikes, *st)
	}
	return strikes, rows.Err()
}

func (s *TrustExtrasStore) CountActiveStrikes(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM trust.user_strikes
		WHERE user_id = $1 AND voided_at IS NULL AND expires_at > NOW()
	`, userID).Scan(&count)
	return count, err
}

// CountLegacyBackfilledStrikes counts the rows migration 011 gave a 90-day
// expiry (F-15); main logs it at boot.
func (s *TrustExtrasStore) CountLegacyBackfilledStrikes(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM trust.user_strikes WHERE policy_version = $1`, LegacyStrikePolicyVersion).Scan(&n)
	return n, err
}

// StandingSnapshot is everything the standing policy reads for one user,
// from one query at one instant.
type StandingSnapshot struct {
	// SuspendedUntil is trust.user_trust_state.suspended_until (nil when
	// unset or no row).
	SuspendedUntil *time.Time
	// ActiveStrikes are the strikes that count at the snapshot's instant:
	// not voided, expires_at after it. Newest first; never nil.
	ActiveStrikes []UserStrike
}

// StandingSnapshot reads the user's suspension and active strikes in ONE
// statement, evaluated at now (passed in, so the caller's clock and the
// filter agree). A user with no rows anywhere is an empty snapshot.
func (s *TrustExtrasStore) StandingSnapshot(ctx context.Context, userID uuid.UUID, now time.Time) (*StandingSnapshot, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ts.suspended_until,
		       st.id, st.user_id, st.reason, st.content_type, st.content_id, st.severity, st.case_id, st.strike_group,
		       st.policy_version, st.idempotency_key, st.expires_at, st.created_by, st.created_at,
		       st.voided_at, st.void_reason, st.voided_by
		  FROM (SELECT $1::uuid AS user_id) u
		  LEFT JOIN trust.user_trust_state ts ON ts.user_id = u.user_id
		  LEFT JOIN trust.user_strikes st
		         ON st.user_id = u.user_id AND st.voided_at IS NULL AND st.expires_at > $2
		 ORDER BY st.created_at DESC, st.id
	`, userID, now)
	if err != nil {
		return nil, fmt.Errorf("standing snapshot: %w", err)
	}
	defer rows.Close()

	snap := &StandingSnapshot{ActiveStrikes: []UserStrike{}}
	for rows.Next() {
		var (
			suspendedUntil *time.Time
			id             *uuid.UUID
			st             UserStrike
			user           *uuid.UUID
			reason         *string
			severity       *string
			policy         *string
			expiresAt      *time.Time
			createdAt      *time.Time
		)
		if err := rows.Scan(&suspendedUntil, &id, &user, &reason, &st.ContentType, &st.ContentID, &severity,
			&st.CaseID, &st.StrikeGroup, &policy, &st.IdempotencyKey, &expiresAt, &st.CreatedBy, &createdAt,
			&st.VoidedAt, &st.VoidReason, &st.VoidedBy); err != nil {
			return nil, fmt.Errorf("standing snapshot: scan: %w", err)
		}
		snap.SuspendedUntil = suspendedUntil
		if id == nil {
			continue // the LEFT JOIN's "no strike" row
		}
		st.ID, st.UserID, st.Reason, st.Severity, st.PolicyVersion, st.ExpiresAt, st.CreatedAt =
			*id, *user, *reason, *severity, *policy, *expiresAt, *createdAt
		snap.ActiveStrikes = append(snap.ActiveStrikes, st)
	}
	return snap, rows.Err()
}

// ─── Verification request methods ─────────────────────────────────────────────

func (s *TrustExtrasStore) CreateVerificationRequest(ctx context.Context, r *VerificationRequest) error {
	var docsJSON []byte
	if r.SubmittedDocs != nil {
		var err error
		docsJSON, err = json.Marshal(r.SubmittedDocs)
		if err != nil {
			return err
		}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.verification_requests
			(id, user_id, type, status, submitted_docs, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, r.ID, r.UserID, r.Type, r.Status, docsJSON, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *TrustExtrasStore) GetVerificationRequest(ctx context.Context, id uuid.UUID) (*VerificationRequest, error) {
	return s.scanVerificationRequest(ctx, `
		SELECT id, user_id, type, status, submitted_docs, rejection_reason, reviewed_by, created_at, updated_at
		FROM trust.verification_requests WHERE id = $1
	`, id)
}

func (s *TrustExtrasStore) GetUserVerificationRequest(ctx context.Context, userID uuid.UUID) (*VerificationRequest, error) {
	return s.scanVerificationRequest(ctx, `
		SELECT id, user_id, type, status, submitted_docs, rejection_reason, reviewed_by, created_at, updated_at
		FROM trust.verification_requests WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, userID)
}

func (s *TrustExtrasStore) scanVerificationRequest(ctx context.Context, query string, arg interface{}) (*VerificationRequest, error) {
	var r VerificationRequest
	var docsRaw []byte
	err := s.db.QueryRow(ctx, query, arg).Scan(
		&r.ID, &r.UserID, &r.Type, &r.Status,
		&docsRaw, &r.RejectionReason, &r.ReviewedBy,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if docsRaw != nil {
		_ = json.Unmarshal(docsRaw, &r.SubmittedDocs)
	}
	return &r, nil
}

func (s *TrustExtrasStore) ListVerificationRequests(ctx context.Context, status string, limit, offset int) ([]VerificationRequest, error) {
	query := `
		SELECT id, user_id, type, status, submitted_docs, rejection_reason, reviewed_by, created_at, updated_at
		FROM trust.verification_requests
	`
	args := []interface{}{}
	if status != "" {
		query += " WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3"
		args = append(args, status, limit, offset)
	} else {
		query += " ORDER BY created_at DESC LIMIT $1 OFFSET $2"
		args = append(args, limit, offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []VerificationRequest
	for rows.Next() {
		var r VerificationRequest
		var docsRaw []byte
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.Type, &r.Status,
			&docsRaw, &r.RejectionReason, &r.ReviewedBy,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if docsRaw != nil {
			_ = json.Unmarshal(docsRaw, &r.SubmittedDocs)
		}
		requests = append(requests, r)
	}
	return requests, nil
}

func (s *TrustExtrasStore) HasPendingVerification(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM trust.verification_requests
			WHERE user_id = $1 AND status IN ('pending','more_info_needed')
		)
	`, userID).Scan(&exists)
	return exists, err
}

func (s *TrustExtrasStore) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, status, rejectionReason string, reviewedBy *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE trust.verification_requests
		SET status = $2,
		    rejection_reason = NULLIF($3, ''),
		    reviewed_by = $4,
		    updated_at = NOW()
		WHERE id = $1
	`, id, status, rejectionReason, reviewedBy)
	return err
}
