package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/trust-safety-service/internal/restriction"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Copyright case holds (Copyright Match plan, sections 6.4 "restriction
// commands", 9.2). A hold's owner is a trust.copyright_cases row; every
// transition mints ONE decision id, then commits the case change, the
// command row and the audit row together, then kicks the dispatcher. The
// decision id is therefore on disk before anything is sent, so a crash or
// a retry re-sends the same id, re-signed.

var (
	// ErrCopyrightUnavailable: no copyright store is configured.
	ErrCopyrightUnavailable = errors.New("copyright cases are unavailable")
	// ErrInvalidCopyrightReason: the reason code is outside the closed
	// action / reason table (moderationcap) for this transition.
	ErrInvalidCopyrightReason = errors.New("invalid reason code for this transition")
	// ErrInvalidCopyrightCase: a required id is missing.
	ErrInvalidCopyrightCase = errors.New("invalid copyright case")
)

// copyrightStore is what the service needs from postgres.CopyrightStore
// (an interface so the crash / retry tests can observe the writes).
type copyrightStore interface {
	CreateCaseAndPlaceHold(ctx context.Context, in postgres.CreateCaseInput, cmd postgres.NewCommand, meta postgres.AuditMeta) (*postgres.CopyrightCase, *postgres.RestrictionCommand, error)
	TransitionHold(ctx context.Context, caseID uuid.UUID, cmd postgres.NewCommand, meta postgres.AuditMeta) (*postgres.CopyrightCase, *postgres.RestrictionCommand, error)
	GetCase(ctx context.Context, caseID uuid.UUID) (*postgres.CopyrightCase, *postgres.RestrictionCommand, error)
}

// SetCopyrightStore installs the case store.
func (s *Service) SetCopyrightStore(store copyrightStore) { s.copyright = store }

// SetRestrictionKick installs the dispatcher's Kick, called after every
// committed transition so the command goes out without waiting for the
// next sweep.
func (s *Service) SetRestrictionKick(kick func()) { s.restrictionKick = kick }

// CopyrightHold is a case with its latest command: what the admin routes
// answer.
type CopyrightHold struct {
	Case        *postgres.CopyrightCase      `json:"case"`
	Enforcement *postgres.RestrictionCommand `json:"enforcement,omitempty"`
}

// CreateCopyrightHoldInput opens a case shell and places its first hold.
type CreateCopyrightHoldInput struct {
	SubjectPostID   uuid.UUID
	SubjectAuthorID uuid.UUID
	ReasonCode      string
	// CaseID is optional; the caller may pin one (a replayed console click
	// then fails on the primary key instead of opening a second case).
	CaseID uuid.UUID
}

func checkReason(action, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if !moderationcap.RestrictionReasonAllowed(action, reason) {
		return "", fmt.Errorf("%w: %s with %q", ErrInvalidCopyrightReason, action, reason)
	}
	return reason, nil
}

func humanActor(meta postgres.AuditMeta) error {
	if meta.Actor.UserID == uuid.Nil || meta.Actor.Validate() != nil {
		return ErrActorRequired
	}
	return nil
}

// newCommand mints the decision id and the digest the command row stores.
// It is the ONLY place a decision id is minted.
func newCommand(caseID uuid.UUID, cs postgres.CreateCaseInput, action, reason, expected string, actor uuid.UUID, revision int64) postgres.NewCommand {
	id := uuid.New()
	digest := restriction.Digest(restriction.Command{
		DecisionID: id, CaseID: caseID, CaseRevision: revision,
		Action: action, Source: postgres.CopyrightSource,
		SubjectPostID: cs.SubjectPostID, SubjectAuthorID: cs.SubjectAuthorID,
		ExpectedState: expected, ReasonCode: reason, PolicyVersion: cs.PolicyVersion, ActorID: actor,
	})
	return postgres.NewCommand{DecisionID: id, Action: action, ReasonCode: reason, ActorID: actor, ClaimsDigest: digest, ExpectedState: expected}
}

// CreateCopyrightHold opens the case shell in hold_active and queues the
// first place_hold (expected_state absent).
func (s *Service) CreateCopyrightHold(ctx context.Context, in CreateCopyrightHoldInput, meta postgres.AuditMeta) (*CopyrightHold, error) {
	if s.copyright == nil {
		return nil, ErrCopyrightUnavailable
	}
	if err := humanActor(meta); err != nil {
		return nil, err
	}
	if in.SubjectPostID == uuid.Nil || in.SubjectAuthorID == uuid.Nil {
		return nil, fmt.Errorf("%w: subject_post_id and subject_author_id are required", ErrInvalidCopyrightCase)
	}
	reason, err := checkReason(postgres.RestrictionActionPlaceHold, in.ReasonCode)
	if err != nil {
		return nil, err
	}
	caseID := in.CaseID
	if caseID == uuid.Nil {
		caseID = uuid.New()
	}
	cs := postgres.CreateCaseInput{CaseID: caseID, SubjectPostID: in.SubjectPostID, SubjectAuthorID: in.SubjectAuthorID, PolicyVersion: postgres.CopyrightPolicyVersion}
	cmd := newCommand(caseID, cs, postgres.RestrictionActionPlaceHold, reason, postgres.RestrictionExpectedAbsent, meta.Actor.UserID, 1)
	meta.Reason = reason
	c, k, err := s.copyright.CreateCaseAndPlaceHold(ctx, cs, cmd, meta)
	if err != nil {
		return nil, err
	}
	s.kickRestrictions()
	return &CopyrightHold{Case: c, Enforcement: k}, nil
}

// PlaceHold re-places the hold of a released case (expected_state
// released). ErrInvalidTransition when the case is not hold_released.
func (s *Service) PlaceHold(ctx context.Context, caseID uuid.UUID, reasonCode string, meta postgres.AuditMeta) (*CopyrightHold, error) {
	return s.transition(ctx, caseID, postgres.RestrictionActionPlaceHold, postgres.RestrictionExpectedReleased, reasonCode, meta)
}

// ReleaseHold releases the hold of an active case (expected_state active).
// ErrInvalidTransition when the case is not hold_active.
func (s *Service) ReleaseHold(ctx context.Context, caseID uuid.UUID, reasonCode string, meta postgres.AuditMeta) (*CopyrightHold, error) {
	return s.transition(ctx, caseID, postgres.RestrictionActionReleaseHold, postgres.RestrictionExpectedActive, reasonCode, meta)
}

func (s *Service) transition(ctx context.Context, caseID uuid.UUID, action, expected, reasonCode string, meta postgres.AuditMeta) (*CopyrightHold, error) {
	if s.copyright == nil {
		return nil, ErrCopyrightUnavailable
	}
	if err := humanActor(meta); err != nil {
		return nil, err
	}
	if caseID == uuid.Nil {
		return nil, fmt.Errorf("%w: case id is required", ErrInvalidCopyrightCase)
	}
	reason, err := checkReason(action, reasonCode)
	if err != nil {
		return nil, err
	}
	// The digest covers the subject and the revision, which only the locked
	// case row knows: read it first, then let the store's compare-and-set
	// on the state (and the revision bump) decide. A concurrent transition
	// between the read and the lock is refused by the state check, never
	// applied under a stale revision.
	current, _, err := s.copyright.GetCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	cs := postgres.CreateCaseInput{CaseID: caseID, SubjectPostID: current.SubjectPostID, SubjectAuthorID: current.SubjectAuthorID, PolicyVersion: current.PolicyVersion}
	nextRevision := current.CaseRevision + 1
	cmd := newCommand(caseID, cs, action, reason, expected, meta.Actor.UserID, nextRevision)
	meta.Reason = reason
	c, k, err := s.copyright.TransitionHold(ctx, caseID, cmd, meta)
	if err != nil {
		return nil, err
	}
	if k.CaseRevision != nextRevision {
		// Cannot happen while the state check holds (a state flip is one
		// revision); refusing here keeps the stored digest honest.
		return nil, fmt.Errorf("copyright: revision moved under the transition (want %d, stored %d)", nextRevision, k.CaseRevision)
	}
	s.kickRestrictions()
	return &CopyrightHold{Case: c, Enforcement: k}, nil
}

// GetCopyrightHold reads a case and its latest command.
func (s *Service) GetCopyrightHold(ctx context.Context, caseID uuid.UUID) (*CopyrightHold, error) {
	if s.copyright == nil {
		return nil, ErrCopyrightUnavailable
	}
	c, k, err := s.copyright.GetCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	return &CopyrightHold{Case: c, Enforcement: k}, nil
}

func (s *Service) kickRestrictions() {
	if s.restrictionKick != nil {
		s.restrictionKick()
	}
}
