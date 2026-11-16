package approvals

import (
	"context"
	"errors"
	"fmt"
)

// ErrSecondHolderRequired: the operation must be approved by a second person
// and nobody else holds the permission (with TOTP), so it cannot be requested.
var ErrSecondHolderRequired = errors.New("this operation needs a second holder of the permission and there is none")

// SubmitPending is Submit without the founder-alone exception: the request is
// ALWAYS stored for a second holder and never executed on the requester's own
// call. An operation whose owning service needs two distinct people (a
// Government invitation) uses it; when no other holder exists it refuses with
// ErrSecondHolderRequired instead of running as the sole holder.
func (s *Service) SubmitPending(ctx context.Context, r Request) (Submission, error) {
	if _, ok := s.executors[executorKey(r.App, r.Operation)]; !ok {
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
		return Submission{}, ErrSecondHolderRequired
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
