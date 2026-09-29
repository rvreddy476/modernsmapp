package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Copyright case shell and restriction commands (migration 013) ───────────
//
// Copyright Match plan, sections 6.4 "restriction commands", 9.2 and 9.5.
// A case owns the hold post-service enforces; every transition writes the
// case change, ONE command row carrying a decision_id minted before the
// transaction, and its audit row together, so none of the three can exist
// without the others. The dispatcher (internal/restriction) sends the
// command afterwards and records the outcome on the row.

const (
	// CopyrightCaseHoldActive: the case expects post-service to hold the post.
	CopyrightCaseHoldActive = "hold_active"
	// CopyrightCaseHoldReleased: the case expects the hold to be released.
	CopyrightCaseHoldReleased = "hold_released"

	// CopyrightPolicyVersion is the policy new cases and commands carry.
	CopyrightPolicyVersion = "copyright-v1"
	// CopyrightSource is the only restriction source enabled (post-service
	// refuses "safety" with SOURCE_NOT_ENABLED).
	CopyrightSource = "copyright"

	RestrictionCommandPending    = "pending"
	RestrictionCommandAcked      = "acked"
	RestrictionCommandSuperseded = "superseded"
	RestrictionCommandParked     = "parked"

	RestrictionActionPlaceHold   = "place_hold"
	RestrictionActionReleaseHold = "release_hold"

	RestrictionExpectedAbsent   = "absent"
	RestrictionExpectedActive   = "active"
	RestrictionExpectedReleased = "released"

	// Audit rows on trust.admin_audit.
	AuditTargetCopyrightCase   = "copyright_case"
	AuditCopyrightHoldPlaced   = "copyright_case.hold_placed"
	AuditCopyrightHoldReleased = "copyright_case.hold_released"
)

var (
	// ErrCopyrightCaseNotFound: no such case.
	ErrCopyrightCaseNotFound = errors.New("copyright case not found")
	// ErrCopyrightCaseExists: a case with that id already exists (a pinned
	// id replayed).
	ErrCopyrightCaseExists = errors.New("copyright case already exists")
	// ErrCopyrightCaseInvalidInput: the store's own shape check failed.
	ErrCopyrightCaseInvalidInput = errors.New("invalid copyright case input")
	// ErrRestrictionCommandNotPending: the command's status is not pending,
	// so the outcome could not be recorded (a stale dispatcher).
	ErrRestrictionCommandNotPending = errors.New("restriction command is not pending")
)

// CopyrightCase is one trust.copyright_cases row.
type CopyrightCase struct {
	ID              uuid.UUID `json:"id"`
	SubjectPostID   uuid.UUID `json:"subject_post_id"`
	SubjectAuthorID uuid.UUID `json:"subject_author_id"`
	Source          string    `json:"source"`
	State           string    `json:"state"`
	CaseRevision    int64     `json:"case_revision"`
	PolicyVersion   string    `json:"policy_version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// RestrictionCommand is one trust.restriction_commands row.
type RestrictionCommand struct {
	DecisionID      uuid.UUID       `json:"decision_id"`
	CaseID          uuid.UUID       `json:"case_id"`
	CaseRevision    int64           `json:"case_revision"`
	Action          string          `json:"action"`
	Source          string          `json:"source"`
	SubjectPostID   uuid.UUID       `json:"subject_post_id"`
	SubjectAuthorID uuid.UUID       `json:"subject_author_id"`
	ExpectedState   string          `json:"expected_state"`
	ReasonCode      string          `json:"reason_code"`
	PolicyVersion   string          `json:"policy_version"`
	ActorID         uuid.UUID       `json:"actor_id"`
	ClaimsDigest    []byte          `json:"-"`
	Status          string          `json:"status"`
	Attempts        int             `json:"attempts"`
	Requeues        int             `json:"requeues"`
	NextAttemptAt   time.Time       `json:"next_attempt_at"`
	ClaimedAt       *time.Time      `json:"claimed_at,omitempty"`
	LastStatusCode  *int            `json:"last_status_code,omitempty"`
	LastErrorCode   *string         `json:"last_error_code,omitempty"`
	LastError       *string         `json:"last_error,omitempty"`
	AckedAt         *time.Time      `json:"acked_at,omitempty"`
	Replayed        *bool           `json:"replayed,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	ParkedAt        *time.Time      `json:"parked_at,omitempty"`
	ParkReason      *string         `json:"park_reason,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// NewCommand is what a transition writes: the service mints DecisionID
// and computes ClaimsDigest before the transaction begins.
type NewCommand struct {
	DecisionID    uuid.UUID
	Action        string
	ReasonCode    string
	ActorID       uuid.UUID
	ClaimsDigest  []byte
	ExpectedState string
}

func (c NewCommand) validate() error {
	if c.DecisionID == uuid.Nil || c.ActorID == uuid.Nil || len(c.ClaimsDigest) != 32 ||
		strings.TrimSpace(c.ReasonCode) == "" {
		return fmt.Errorf("%w: command is incomplete", ErrCopyrightCaseInvalidInput)
	}
	if c.Action != RestrictionActionPlaceHold && c.Action != RestrictionActionReleaseHold {
		return fmt.Errorf("%w: action %q", ErrCopyrightCaseInvalidInput, c.Action)
	}
	switch c.ExpectedState {
	case RestrictionExpectedAbsent, RestrictionExpectedActive, RestrictionExpectedReleased:
	default:
		return fmt.Errorf("%w: expected_state %q", ErrCopyrightCaseInvalidInput, c.ExpectedState)
	}
	return nil
}

// CopyrightStore owns trust.copyright_cases and trust.restriction_commands.
type CopyrightStore struct {
	db *pgxpool.Pool
}

// NewCopyrightStore constructs a CopyrightStore.
func NewCopyrightStore(db *pgxpool.Pool) *CopyrightStore { return &CopyrightStore{db: db} }

const copyrightCaseCols = `id, subject_post_id, subject_author_id, source, state, case_revision, policy_version, created_at, updated_at`

func scanCopyrightCase(row pgx.Row) (*CopyrightCase, error) {
	var c CopyrightCase
	if err := row.Scan(&c.ID, &c.SubjectPostID, &c.SubjectAuthorID, &c.Source, &c.State, &c.CaseRevision,
		&c.PolicyVersion, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

const restrictionCommandCols = `decision_id, case_id, case_revision, action, source, subject_post_id, subject_author_id,
	expected_state, reason_code, policy_version, actor_id, claims_digest, status, attempts, requeues, next_attempt_at,
	claimed_at, last_status_code, last_error_code, last_error, acked_at, replayed, result, parked_at, park_reason,
	created_at, updated_at`

func scanRestrictionCommand(row pgx.Row) (*RestrictionCommand, error) {
	var k RestrictionCommand
	if err := row.Scan(&k.DecisionID, &k.CaseID, &k.CaseRevision, &k.Action, &k.Source, &k.SubjectPostID, &k.SubjectAuthorID,
		&k.ExpectedState, &k.ReasonCode, &k.PolicyVersion, &k.ActorID, &k.ClaimsDigest, &k.Status, &k.Attempts, &k.Requeues,
		&k.NextAttemptAt, &k.ClaimedAt, &k.LastStatusCode, &k.LastErrorCode, &k.LastError, &k.AckedAt, &k.Replayed,
		&k.Result, &k.ParkedAt, &k.ParkReason, &k.CreatedAt, &k.UpdatedAt); err != nil {
		return nil, err
	}
	return &k, nil
}

// insertCommand writes the command row inside tx, taking the subject from
// the case so a command can never name another post than its case.
func insertCommand(ctx context.Context, tx pgx.Tx, cs *CopyrightCase, in NewCommand) (*RestrictionCommand, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO trust.restriction_commands
			(decision_id, case_id, case_revision, action, source, subject_post_id, subject_author_id,
			 expected_state, reason_code, policy_version, actor_id, claims_digest)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+restrictionCommandCols,
		in.DecisionID, cs.ID, cs.CaseRevision, in.Action, cs.Source, cs.SubjectPostID, cs.SubjectAuthorID,
		in.ExpectedState, in.ReasonCode, cs.PolicyVersion, in.ActorID, in.ClaimsDigest)
	k, err := scanRestrictionCommand(row)
	if err != nil {
		return nil, fmt.Errorf("copyright: insert command: %w", err)
	}
	return k, nil
}

// CreateCaseInput opens a case shell.
type CreateCaseInput struct {
	CaseID          uuid.UUID
	SubjectPostID   uuid.UUID
	SubjectAuthorID uuid.UUID
	PolicyVersion   string
}

// CreateCaseAndPlaceHold inserts the case in hold_active at revision 1,
// the first place_hold command (expected_state absent) and the audit row
// in ONE transaction. meta.Actor must be a human admin.
func (s *CopyrightStore) CreateCaseAndPlaceHold(ctx context.Context, in CreateCaseInput, cmd NewCommand, meta AuditMeta) (*CopyrightCase, *RestrictionCommand, error) {
	if err := meta.Actor.Validate(); err != nil {
		return nil, nil, err
	}
	if meta.Actor.UserID == uuid.Nil {
		return nil, nil, ErrActorRequired
	}
	if in.CaseID == uuid.Nil || in.SubjectPostID == uuid.Nil || in.SubjectAuthorID == uuid.Nil || strings.TrimSpace(in.PolicyVersion) == "" {
		return nil, nil, fmt.Errorf("%w: case is incomplete", ErrCopyrightCaseInvalidInput)
	}
	if cmd.Action != RestrictionActionPlaceHold || cmd.ExpectedState != RestrictionExpectedAbsent {
		return nil, nil, fmt.Errorf("%w: a new case starts with place_hold on an absent restriction", ErrCopyrightCaseInvalidInput)
	}
	var (
		cs *CopyrightCase
		k  *RestrictionCommand
	)
	err := withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		var err error
		cs, err = scanCopyrightCase(tx.QueryRow(ctx, `
			INSERT INTO trust.copyright_cases (id, subject_post_id, subject_author_id, source, state, case_revision, policy_version)
			VALUES ($1, $2, $3, $4, $5, 1, $6)
			RETURNING `+copyrightCaseCols,
			in.CaseID, in.SubjectPostID, in.SubjectAuthorID, CopyrightSource, CopyrightCaseHoldActive, strings.TrimSpace(in.PolicyVersion)))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "copyright_cases_pkey" {
				return ErrCopyrightCaseExists
			}
			return fmt.Errorf("copyright: insert case: %w", err)
		}
		if k, err = insertCommand(ctx, tx, cs, cmd); err != nil {
			return err
		}
		return insertAudit(ctx, tx, meta, auditChange{
			Action: AuditCopyrightHoldPlaced, TargetType: AuditTargetCopyrightCase, TargetID: cs.ID,
			NewStatus: CopyrightCaseHoldActive, NewResolution: cmd.ReasonCode,
		})
	})
	if err != nil {
		return nil, nil, err
	}
	return cs, k, nil
}

// TransitionHold moves an existing case between hold_active and
// hold_released and writes the command and audit rows in ONE transaction.
// place_hold requires hold_released (expected_state released: a re-place);
// release_hold requires hold_active (expected_state active). The case row
// is locked, so two reviewers racing get one transition and one
// ErrInvalidTransition; the revision rises by exactly one.
func (s *CopyrightStore) TransitionHold(ctx context.Context, caseID uuid.UUID, cmd NewCommand, meta AuditMeta) (*CopyrightCase, *RestrictionCommand, error) {
	if err := meta.Actor.Validate(); err != nil {
		return nil, nil, err
	}
	if meta.Actor.UserID == uuid.Nil {
		return nil, nil, ErrActorRequired
	}
	if err := cmd.validate(); err != nil {
		return nil, nil, err
	}
	var from, to, expected, action string
	switch cmd.Action {
	case RestrictionActionPlaceHold:
		from, to, expected, action = CopyrightCaseHoldReleased, CopyrightCaseHoldActive, RestrictionExpectedReleased, AuditCopyrightHoldPlaced
	case RestrictionActionReleaseHold:
		from, to, expected, action = CopyrightCaseHoldActive, CopyrightCaseHoldReleased, RestrictionExpectedActive, AuditCopyrightHoldReleased
	}
	if cmd.ExpectedState != expected {
		return nil, nil, fmt.Errorf("%w: %s expects the restriction to be %s", ErrCopyrightCaseInvalidInput, cmd.Action, expected)
	}
	var (
		cs *CopyrightCase
		k  *RestrictionCommand
	)
	err := withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		current, err := scanCopyrightCase(tx.QueryRow(ctx, `SELECT `+copyrightCaseCols+` FROM trust.copyright_cases WHERE id = $1 FOR UPDATE`, caseID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCopyrightCaseNotFound
		}
		if err != nil {
			return fmt.Errorf("copyright: lock case: %w", err)
		}
		if current.State != from {
			return fmt.Errorf("%w: case is %s, %s needs %s", ErrInvalidTransition, current.State, cmd.Action, from)
		}
		cs, err = scanCopyrightCase(tx.QueryRow(ctx, `
			UPDATE trust.copyright_cases
			   SET state = $2, case_revision = case_revision + 1, updated_at = clock_timestamp()
			 WHERE id = $1 AND state = $3
			RETURNING `+copyrightCaseCols, caseID, to, from))
		if err != nil {
			return fmt.Errorf("copyright: transition case: %w", err)
		}
		if k, err = insertCommand(ctx, tx, cs, cmd); err != nil {
			return err
		}
		return insertAudit(ctx, tx, meta, auditChange{
			Action: action, TargetType: AuditTargetCopyrightCase, TargetID: cs.ID,
			PrevStatus: from, NewStatus: to, NewResolution: cmd.ReasonCode,
		})
	})
	if err != nil {
		return nil, nil, err
	}
	return cs, k, nil
}

// GetCase reads one case and its latest command (nil when none).
func (s *CopyrightStore) GetCase(ctx context.Context, caseID uuid.UUID) (*CopyrightCase, *RestrictionCommand, error) {
	cs, err := scanCopyrightCase(s.db.QueryRow(ctx, `SELECT `+copyrightCaseCols+` FROM trust.copyright_cases WHERE id = $1`, caseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrCopyrightCaseNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("copyright: read case: %w", err)
	}
	k, err := s.LatestCommand(ctx, caseID)
	if err != nil {
		return nil, nil, err
	}
	return cs, k, nil
}

// LatestCommand is the case's highest-revision command; nil when none.
func (s *CopyrightStore) LatestCommand(ctx context.Context, caseID uuid.UUID) (*RestrictionCommand, error) {
	k, err := scanRestrictionCommand(s.db.QueryRow(ctx, `
		SELECT `+restrictionCommandCols+` FROM trust.restriction_commands
		 WHERE case_id = $1 ORDER BY case_revision DESC LIMIT 1`, caseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("copyright: read latest command: %w", err)
	}
	return k, nil
}

// GetCommand reads one command by decision id.
func (s *CopyrightStore) GetCommand(ctx context.Context, decisionID uuid.UUID) (*RestrictionCommand, error) {
	k, err := scanRestrictionCommand(s.db.QueryRow(ctx, `SELECT `+restrictionCommandCols+` FROM trust.restriction_commands WHERE decision_id = $1`, decisionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pgx.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("copyright: read command: %w", err)
	}
	return k, nil
}

// ── Dispatcher side ─────────────────────────────────────────────────────────

// ClaimPendingCommands claims up to limit due commands with SKIP LOCKED and
// holds each for lease (its next_attempt_at moves past the lease and its
// attempts count rises BEFORE the send), so two dispatchers never send the
// same row at once and a dispatcher that dies mid-send leaves a row that
// becomes due again by itself. Per case, a command is claimable only when
// no lower-revision command of that case is still pending: commands reach
// post-service in revision order, which its compare-and-set requires.
func (s *CopyrightStore) ClaimPendingCommands(ctx context.Context, limit int, lease time.Duration) ([]RestrictionCommand, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if lease <= 0 {
		lease = time.Minute
	}
	rows, err := s.db.Query(ctx, `
		WITH picked AS (
			SELECT c.decision_id
			  FROM trust.restriction_commands c
			 WHERE c.status = 'pending'
			   AND c.next_attempt_at <= clock_timestamp()
			   AND NOT EXISTS (
				   SELECT 1 FROM trust.restriction_commands e
				    WHERE e.case_id = c.case_id AND e.status = 'pending' AND e.case_revision < c.case_revision)
			 ORDER BY c.created_at, c.case_revision
			 LIMIT $1
			 FOR UPDATE SKIP LOCKED
		)
		UPDATE trust.restriction_commands c
		   SET attempts = c.attempts + 1,
		       claimed_at = clock_timestamp(),
		       next_attempt_at = clock_timestamp() + ($2::bigint * interval '1 millisecond'),
		       updated_at = clock_timestamp()
		  FROM picked
		 WHERE c.decision_id = picked.decision_id
		RETURNING `+qualify("c", restrictionCommandCols), limit, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("copyright: claim commands: %w", err)
	}
	defer rows.Close()
	out := make([]RestrictionCommand, 0, limit)
	for rows.Next() {
		k, err := scanRestrictionCommand(rows)
		if err != nil {
			return nil, fmt.Errorf("copyright: scan claimed command: %w", err)
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// qualify prefixes every column in a comma list with alias.
func qualify(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func clip(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) > 2000 {
		s = s[:2000]
	}
	return &s
}

func nilIfZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

// MarkCommandAcked records a 2xx (or a replay). Only a pending row can be
// acked; anything else is ErrRestrictionCommandNotPending.
func (s *CopyrightStore) MarkCommandAcked(ctx context.Context, decisionID uuid.UUID, statusCode int, replayed bool, result json.RawMessage) error {
	if len(result) == 0 || !json.Valid(result) {
		result = json.RawMessage("null")
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE trust.restriction_commands
		   SET status = 'acked', acked_at = clock_timestamp(), replayed = $3, result = $4::jsonb,
		       last_status_code = $2, last_error_code = NULL, last_error = NULL, updated_at = clock_timestamp()
		 WHERE decision_id = $1 AND status = 'pending'
	`, decisionID, nilIfZero(statusCode), replayed, result)
	if err != nil {
		return fmt.Errorf("copyright: ack command: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRestrictionCommandNotPending
	}
	return nil
}

// RecordCommandRetry keeps the row pending and holds it back for retryAfter.
func (s *CopyrightStore) RecordCommandRetry(ctx context.Context, decisionID uuid.UUID, statusCode int, errorCode, cause string, retryAfter time.Duration) error {
	if retryAfter < 0 {
		retryAfter = 0
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE trust.restriction_commands
		   SET next_attempt_at = clock_timestamp() + ($5::bigint * interval '1 millisecond'),
		       last_status_code = $2, last_error_code = $3, last_error = $4, updated_at = clock_timestamp()
		 WHERE decision_id = $1 AND status = 'pending'
	`, decisionID, nilIfZero(statusCode), clip(errorCode), clip(cause), retryAfter.Milliseconds())
	if err != nil {
		return fmt.Errorf("copyright: record retry: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRestrictionCommandNotPending
	}
	return nil
}

// MarkCommandSuperseded closes a pending command that post-service refused
// as stale ONLY when a newer command exists for the same case; it reports
// false (and changes nothing) otherwise, and the caller parks the row.
func (s *CopyrightStore) MarkCommandSuperseded(ctx context.Context, decisionID uuid.UUID, statusCode int, errorCode, cause string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE trust.restriction_commands c
		   SET status = 'superseded', last_status_code = $2, last_error_code = $3, last_error = $4, updated_at = clock_timestamp()
		 WHERE c.decision_id = $1 AND c.status = 'pending'
		   AND EXISTS (SELECT 1 FROM trust.restriction_commands n WHERE n.case_id = c.case_id AND n.case_revision > c.case_revision)
	`, decisionID, nilIfZero(statusCode), clip(errorCode), clip(cause))
	if err != nil {
		return false, fmt.Errorf("copyright: supersede command: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ParkCommand takes a pending command out of the queue with a reason. A
// parked command is never retried by the dispatcher; a human resolves it.
func (s *CopyrightStore) ParkCommand(ctx context.Context, decisionID uuid.UUID, reason string, statusCode int, errorCode, cause string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: park reason is required", ErrCopyrightCaseInvalidInput)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE trust.restriction_commands
		   SET status = 'parked', parked_at = clock_timestamp(), park_reason = $2,
		       last_status_code = $3, last_error_code = $4, last_error = $5, updated_at = clock_timestamp()
		 WHERE decision_id = $1 AND status = 'pending'
	`, decisionID, reason, nilIfZero(statusCode), clip(errorCode), clip(cause))
	if err != nil {
		return fmt.Errorf("copyright: park command: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRestrictionCommandNotPending
	}
	return nil
}

// RequeueAckedCommand puts an ACKED command back in the queue (the
// reconciliation sweep found post-service without the state that command
// produced). Only acked rows requeue: a pending row is on its way and a
// parked one waits for a human. It reports whether the row was requeued.
func (s *CopyrightStore) RequeueAckedCommand(ctx context.Context, decisionID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE trust.restriction_commands
		   SET status = 'pending', acked_at = NULL, replayed = NULL, requeues = requeues + 1,
		       next_attempt_at = clock_timestamp(), updated_at = clock_timestamp()
		 WHERE decision_id = $1 AND status = 'acked'
	`, decisionID)
	if err != nil {
		return false, fmt.Errorf("copyright: requeue command: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// OldestPendingCommandAge is the age of the oldest pending command (zero
// when the queue is drained): the plan's "not acked within 1 h → page".
func (s *CopyrightStore) OldestPendingCommandAge(ctx context.Context) (time.Duration, error) {
	var age *float64
	if err := s.db.QueryRow(ctx, `
		SELECT EXTRACT(EPOCH FROM clock_timestamp() - min(created_at))
		  FROM trust.restriction_commands WHERE status = 'pending'
	`).Scan(&age); err != nil {
		return 0, fmt.Errorf("copyright: oldest pending: %w", err)
	}
	if age == nil {
		return 0, nil
	}
	return time.Duration(*age * float64(time.Second)), nil
}

// CountParkedCommands is how many commands wait for a human.
func (s *CopyrightStore) CountParkedCommands(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM trust.restriction_commands WHERE status = 'parked'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("copyright: count parked: %w", err)
	}
	return n, nil
}

// ── Reconciliation side ─────────────────────────────────────────────────────

// ReconcileCase is one case with its latest command, as the sweep reads it.
type ReconcileCase struct {
	Case    CopyrightCase
	Command *RestrictionCommand
}

// ReconcilePage is a keyset cursor over ListCasesForReconcile.
type ReconcilePage struct {
	AfterCreatedAt time.Time
	AfterID        uuid.UUID
}

// ListCasesForReconcile pages, oldest first, over the cases whose hold
// state post-service should reflect: every hold_active case, and every
// hold_released case updated within releasedWindow (a release the sweep
// has verified once need not be verified forever). Each carries its
// latest command.
func (s *CopyrightStore) ListCasesForReconcile(ctx context.Context, after ReconcilePage, limit int, releasedWindow time.Duration) ([]ReconcileCase, ReconcilePage, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if releasedWindow <= 0 {
		releasedWindow = 24 * time.Hour
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+qualify("c", copyrightCaseCols)+`, k.decision_id
		  FROM trust.copyright_cases c
		  LEFT JOIN LATERAL (
			SELECT decision_id FROM trust.restriction_commands
			 WHERE case_id = c.id ORDER BY case_revision DESC LIMIT 1) k ON true
		 WHERE (c.state = $4 OR (c.state = $5 AND c.updated_at > clock_timestamp() - ($3::bigint * interval '1 millisecond')))
		   AND (c.created_at, c.id) > ($1, $2)
		 ORDER BY c.created_at, c.id
		 LIMIT $6
	`, after.AfterCreatedAt, after.AfterID, releasedWindow.Milliseconds(), CopyrightCaseHoldActive, CopyrightCaseHoldReleased, limit)
	if err != nil {
		return nil, after, fmt.Errorf("copyright: list for reconcile: %w", err)
	}
	defer rows.Close()
	var (
		out  []ReconcileCase
		ids  []uuid.UUID
		next = after
	)
	for rows.Next() {
		var (
			c   CopyrightCase
			dec *uuid.UUID
		)
		if err := rows.Scan(&c.ID, &c.SubjectPostID, &c.SubjectAuthorID, &c.Source, &c.State, &c.CaseRevision,
			&c.PolicyVersion, &c.CreatedAt, &c.UpdatedAt, &dec); err != nil {
			return nil, after, fmt.Errorf("copyright: scan reconcile row: %w", err)
		}
		out = append(out, ReconcileCase{Case: c})
		if dec != nil {
			ids = append(ids, *dec)
		}
		next = ReconcilePage{AfterCreatedAt: c.CreatedAt, AfterID: c.ID}
	}
	if err := rows.Err(); err != nil {
		return nil, after, err
	}
	if len(ids) > 0 {
		cmds, err := s.commandsByID(ctx, ids)
		if err != nil {
			return nil, after, err
		}
		for i := range out {
			if k, ok := cmds[out[i].Case.ID]; ok {
				out[i].Command = k
			}
		}
	}
	return out, next, nil
}

func (s *CopyrightStore) commandsByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*RestrictionCommand, error) {
	rows, err := s.db.Query(ctx, `SELECT `+restrictionCommandCols+` FROM trust.restriction_commands WHERE decision_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("copyright: read commands: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]*RestrictionCommand{}
	for rows.Next() {
		k, err := scanRestrictionCommand(rows)
		if err != nil {
			return nil, fmt.Errorf("copyright: scan command: %w", err)
		}
		out[k.CaseID] = k
	}
	return out, rows.Err()
}
