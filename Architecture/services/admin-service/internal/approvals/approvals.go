// Package approvals is two-person approval for admin operations.
//
// A route declared two_person does not execute on the first admin's call.
// Submit stores the exact operation — app, operation, target and payload — with
// a hash over all of them, and answers pending. A different holder of the same
// permission approves; Approve re-checks the hash and runs the stored payload,
// once. The founder-alone exception: when identity reports no other
// TOTP-enrolled holder of the permission, Submit executes at once and says so.
package approvals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Statuses of an approval.
const (
	StatusPending  = "pending"
	StatusApproved = "approved" // claimed by an approver; execution in progress
	StatusRejected = "rejected"
	StatusExpired  = "expired"
	StatusExecuted = "executed"
)

// TTL is how long a pending approval waits for a second admin.
const TTL = 24 * time.Hour

// Approval is one row of admin.approvals.
type Approval struct {
	ID                 string          `json:"id"`
	App                string          `json:"app"`
	Operation          string          `json:"operation"`
	TargetType         string          `json:"target_type"`
	TargetID           string          `json:"target_id"`
	Payload            json.RawMessage `json:"payload"`
	PayloadHash        string          `json:"payload_hash"`
	Requester          string          `json:"requester"`
	RequesterReason    string          `json:"requester_reason"`
	RequiredPermission string          `json:"required_permission"`
	Status             string          `json:"status"`
	Approver           *string         `json:"approver"`
	DecisionReason     *string         `json:"decision_reason"`
	ResultStatus       *int            `json:"result_status"`
	ResultOutcome      *string         `json:"result_outcome"`
	CreatedAt          time.Time       `json:"created_at"`
	DecidedAt          *time.Time      `json:"decided_at"`
	ExecutedAt         *time.Time      `json:"executed_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
}

// Store persists approvals. Transitions are compare-and-set on status, so two
// approvers racing cannot both execute.
type Store interface {
	CreateApproval(ctx context.Context, a *Approval) error
	GetApproval(ctx context.Context, id string) (*Approval, error)
	// ClaimApproval moves a pending, unexpired approval to approved with the
	// approver recorded. ErrNotClaimable when it is not pending or has expired.
	ClaimApproval(ctx context.Context, id, approver, reason string) (*Approval, error)
	// FinishApproval moves an approved approval to executed with its result.
	FinishApproval(ctx context.Context, id string, resultStatus int, resultOutcome string) error
	// CloseApproval moves a pending or approved approval to rejected.
	CloseApproval(ctx context.Context, id, decider, reason string) (*Approval, error)
	// ExpireApproval moves a pending approval past its expiry to expired.
	ExpireApproval(ctx context.Context, id string) error
	// ListPendingApprovals lists unexpired pending approvals whose required
	// permission is one of perms and whose requester is not excludeRequester.
	ListPendingApprovals(ctx context.Context, perms []string, excludeRequester string, limit int) ([]Approval, error)
}

// Holders counts other TOTP-enrolled holders of a permission.
type Holders interface {
	OtherTOTPHolders(ctx context.Context, permission, excludeUserID string) (int, error)
}

// Result is what one execution returned downstream.
type Result struct {
	Data   []byte
	Status int
	Err    error
}

// Outcome is "success" for a 2xx and "failure" otherwise.
func (r Result) Outcome() string {
	if r.Err == nil && r.Status >= 200 && r.Status < 300 {
		return "success"
	}
	return "failure"
}

// Executor performs a stored operation as actorID with exactly payload.
type Executor func(ctx context.Context, actorID string, payload json.RawMessage) Result

// Errors.
var (
	ErrNotFound           = errors.New("approval not found")
	ErrNotClaimable       = errors.New("approval is not pending")
	ErrSelfApproval       = errors.New("the requester cannot decide their own request")
	ErrForbidden          = errors.New("caller does not hold the required permission")
	ErrExpired            = errors.New("approval has expired")
	ErrAlreadyDecided     = errors.New("approval has already been decided")
	ErrInProgress         = errors.New("approval is being executed")
	ErrPayloadTampered    = errors.New("stored payload does not match its hash")
	ErrHoldersUnavailable = errors.New("second-holder count could not be resolved")
	ErrNoExecutor         = errors.New("no executor registered for operation")
	// ErrRecordExecution: the operation ran, but marking the approval executed failed.
	ErrRecordExecution = errors.New("operation executed but the approval could not be marked executed")
)

// Request is one two-person operation as the first admin submitted it.
type Request struct {
	App                string
	Operation          string
	TargetType         string
	TargetID           string
	RequiredPermission string
	Requester          string
	Reason             string
	Payload            any
}

// Mode says how Submit handled a request.
type Mode string

const (
	ModePending    Mode = "pending"
	ModeSoleHolder Mode = "sole_holder"
)

// Submission is Submit's answer.
type Submission struct {
	Mode     Mode
	Approval *Approval // ModePending
	Result   Result    // ModeSoleHolder
}

// Service runs the approval flow.
type Service struct {
	store     Store
	holders   Holders
	executors map[string]Executor
}

func NewService(store Store, holders Holders) *Service {
	return &Service{store: store, holders: holders, executors: map[string]Executor{}}
}

func executorKey(app, operation string) string { return app + "/" + operation }

// Register installs the executor for app/operation. Registering twice panics:
// two routes claiming one operation is a programming error.
func (s *Service) Register(app, operation string, ex Executor) {
	k := executorKey(app, operation)
	if _, dup := s.executors[k]; dup {
		panic("approvals: executor registered twice for " + k)
	}
	s.executors[k] = ex
}

// HasExecutor reports whether app/operation can be executed.
func (s *Service) HasExecutor(app, operation string) bool {
	_, ok := s.executors[executorKey(app, operation)]
	return ok
}

// Canonical re-encodes v as JSON with sorted object keys and numbers kept
// verbatim, so the same payload always hashes the same whether it came from a
// Go value or back out of a JSONB column.
func Canonical(v any) ([]byte, error) {
	raw, ok := v.(json.RawMessage)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}

// Hash binds the payload to the operation and target it was approved for, so
// neither the payload nor the operation or target columns can be swapped.
func Hash(app, operation, targetType, targetID string, canonicalPayload []byte) string {
	h := sha256.New()
	for _, part := range []string{app, operation, targetType, targetID} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	h.Write(canonicalPayload)
	return hex.EncodeToString(h.Sum(nil))
}

// verifyHash recomputes a stored approval's hash.
func verifyHash(a *Approval) error {
	canon, err := Canonical(a.Payload)
	if err != nil {
		return ErrPayloadTampered
	}
	if Hash(a.App, a.Operation, a.TargetType, a.TargetID, canon) != a.PayloadHash {
		return ErrPayloadTampered
	}
	return nil
}

// Submit handles the first admin's call to a two_person route.
func (s *Service) Submit(ctx context.Context, r Request) (Submission, error) {
	ex, ok := s.executors[executorKey(r.App, r.Operation)]
	if !ok {
		return Submission{}, fmt.Errorf("%w: %s/%s", ErrNoExecutor, r.App, r.Operation)
	}
	canon, err := Canonical(r.Payload)
	if err != nil {
		return Submission{}, fmt.Errorf("encode payload: %w", err)
	}

	others, err := s.holders.OtherTOTPHolders(ctx, r.RequiredPermission, r.Requester)
	if err != nil {
		return Submission{}, fmt.Errorf("%w: %v", ErrHoldersUnavailable, err)
	}
	if others == 0 {
		// Founder-alone: nobody else could approve, so the requester acts.
		return Submission{Mode: ModeSoleHolder, Result: ex(ctx, r.Requester, canon)}, nil
	}

	a := &Approval{
		App:                r.App,
		Operation:          r.Operation,
		TargetType:         r.TargetType,
		TargetID:           r.TargetID,
		Payload:            canon,
		PayloadHash:        Hash(r.App, r.Operation, r.TargetType, r.TargetID, canon),
		Requester:          r.Requester,
		RequesterReason:    r.Reason,
		RequiredPermission: r.RequiredPermission,
		Status:             StatusPending,
	}
	if err := s.store.CreateApproval(ctx, a); err != nil {
		return Submission{}, fmt.Errorf("create approval: %w", err)
	}
	return Submission{Mode: ModePending, Approval: a}, nil
}

// Decision is Approve's answer. Replayed is true when the approval had
// already been executed and Result is the stored outcome, not a new call.
type Decision struct {
	Approval *Approval
	Result   Result
	Replayed bool
}

// load fetches an approval and applies the checks every decision shares.
func (s *Service) load(ctx context.Context, id, decider string, has func(string) bool) (*Approval, error) {
	a, err := s.store.GetApproval(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Requester == decider {
		return a, ErrSelfApproval
	}
	if !has(a.RequiredPermission) {
		return a, ErrForbidden
	}
	return a, nil
}

// Approve lets a second holder approve and execute an approval.
func (s *Service) Approve(ctx context.Context, id, approver, reason string, has func(string) bool) (Decision, error) {
	a, err := s.load(ctx, id, approver, has)
	if err != nil {
		return Decision{Approval: a}, err
	}
	if d, err, done := s.settled(ctx, a); done {
		return d, err
	}

	claimed, err := s.store.ClaimApproval(ctx, id, approver, reason)
	if errors.Is(err, ErrNotClaimable) {
		// Lost a race, or expired between the read and the claim.
		again, gerr := s.store.GetApproval(ctx, id)
		if gerr != nil {
			return Decision{Approval: a}, gerr
		}
		if d, err, done := s.settled(ctx, again); done {
			return d, err
		}
		return Decision{Approval: again}, ErrNotClaimable
	}
	if err != nil {
		return Decision{Approval: a}, err
	}

	if err := verifyHash(claimed); err != nil {
		if _, cerr := s.store.CloseApproval(ctx, id, approver, "payload hash mismatch"); cerr != nil {
			return Decision{Approval: claimed}, errors.Join(err, cerr)
		}
		return Decision{Approval: claimed}, err
	}
	ex, ok := s.executors[executorKey(claimed.App, claimed.Operation)]
	if !ok {
		return Decision{Approval: claimed}, fmt.Errorf("%w: %s/%s", ErrNoExecutor, claimed.App, claimed.Operation)
	}

	res := ex(ctx, approver, claimed.Payload)
	if err := s.store.FinishApproval(ctx, id, res.Status, res.Outcome()); err != nil {
		return Decision{Approval: claimed, Result: res}, fmt.Errorf("%w: %v", ErrRecordExecution, err)
	}
	status, outcome := res.Status, res.Outcome()
	claimed.Status = StatusExecuted
	claimed.ResultStatus, claimed.ResultOutcome = &status, &outcome
	return Decision{Approval: claimed, Result: res}, nil
}

// settled answers for an approval that is no longer pending. done is false
// when the approval is still pending and unexpired.
func (s *Service) settled(ctx context.Context, a *Approval) (Decision, error, bool) {
	switch a.Status {
	case StatusExecuted:
		r := Result{}
		if a.ResultStatus != nil {
			r.Status = *a.ResultStatus
		}
		return Decision{Approval: a, Result: r, Replayed: true}, nil, true
	case StatusApproved:
		return Decision{Approval: a}, ErrInProgress, true
	case StatusRejected:
		return Decision{Approval: a}, ErrAlreadyDecided, true
	case StatusExpired:
		return Decision{Approval: a}, ErrExpired, true
	case StatusPending:
		if !time.Now().Before(a.ExpiresAt) {
			if err := s.store.ExpireApproval(ctx, a.ID); err != nil {
				return Decision{Approval: a}, err, true
			}
			a.Status = StatusExpired
			return Decision{Approval: a}, ErrExpired, true
		}
		return Decision{}, nil, false
	}
	return Decision{Approval: a}, ErrNotClaimable, true
}

// Reject closes a pending approval without executing it.
func (s *Service) Reject(ctx context.Context, id, decider, reason string, has func(string) bool) (*Approval, error) {
	a, err := s.load(ctx, id, decider, has)
	if err != nil {
		return a, err
	}
	if _, err, done := s.settled(ctx, a); done {
		if err == nil { // executed
			err = ErrAlreadyDecided
		}
		return a, err
	}
	closed, err := s.store.CloseApproval(ctx, id, decider, reason)
	if errors.Is(err, ErrNotClaimable) {
		return a, ErrAlreadyDecided
	}
	return closed, err
}

// ListDecidable lists pending approvals the caller could decide: they hold the
// required permission and did not request it.
func (s *Service) ListDecidable(ctx context.Context, caller string, perms []string, limit int) ([]Approval, error) {
	if len(perms) == 0 {
		return []Approval{}, nil
	}
	return s.store.ListPendingApprovals(ctx, perms, caller, limit)
}
