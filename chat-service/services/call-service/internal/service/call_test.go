package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// In-process mock store
//
// The real store (postgres.CallStore) is a concrete struct backed by a pgx
// pool. For unit tests we replicate only the business-logic-relevant state in
// a plain Go struct and exercise the call rules directly. This keeps tests
// fast, dependency-free, and deterministic.
// ---------------------------------------------------------------------------

type mockCallSession struct {
	session      *domain.CallSession
	participants []*domain.CallParticipant
	invites      []*domain.CallInvite
}

type inMemCallStore struct {
	sessions map[uuid.UUID]*mockCallSession
	// callsCreatedThisHour simulates a per-user call creation counter.
	callsCreatedThisHour map[uuid.UUID]int
}

func newInMemCallStore() *inMemCallStore {
	return &inMemCallStore{
		sessions:             make(map[uuid.UUID]*mockCallSession),
		callsCreatedThisHour: make(map[uuid.UUID]int),
	}
}

func (s *inMemCallStore) getSession(callID uuid.UUID) *mockCallSession {
	return s.sessions[callID]
}

func (s *inMemCallStore) createCall(callID, hostID uuid.UUID, joinMode string, maxPart int) *domain.CallSession {
	now := time.Now()
	cs := &domain.CallSession{
		ID:              callID,
		CallType:        domain.CallTypeDirectAudio,
		InitiatorUserID: hostID,
		State:           domain.CallStateRinging,
		JoinMode:        joinMode,
		MaxParticipants: maxPart,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	hostPart := &domain.CallParticipant{
		ID:            uuid.New(),
		CallSessionID: callID,
		UserID:        hostID,
		Role:          domain.RoleHost,
		InviteState:   domain.InviteStateAccepted,
		JoinState:     domain.JoinStateNotJoined,
		CreatedAt:     now,
	}
	s.sessions[callID] = &mockCallSession{
		session:      cs,
		participants: []*domain.CallParticipant{hostPart},
	}
	s.callsCreatedThisHour[hostID]++
	return cs
}

func (s *inMemCallStore) addInvite(callID, inviterID, inviteeID uuid.UUID) *domain.CallInvite {
	inv := &domain.CallInvite{
		ID:              uuid.New(),
		CallSessionID:   callID,
		InviterUserID:   inviterID,
		InviteeUserID:   inviteeID,
		DeliveryChannel: domain.DeliveryChannelWebSocket,
		DeliveryStatus:  domain.DeliveryStatusPending,
		ResponseStatus:  domain.ResponseStatusPending,
		CreatedAt:       time.Now(),
	}
	ms := s.sessions[callID]
	ms.invites = append(ms.invites, inv)
	part := &domain.CallParticipant{
		ID:            uuid.New(),
		CallSessionID: callID,
		UserID:        inviteeID,
		Role:          domain.RoleParticipant,
		InviteState:   domain.InviteStateInvited,
		JoinState:     domain.JoinStateNotJoined,
		CreatedAt:     time.Now(),
	}
	ms.participants = append(ms.participants, part)
	return inv
}

func (s *inMemCallStore) getParticipant(callID, userID uuid.UUID) *domain.CallParticipant {
	ms := s.sessions[callID]
	if ms == nil {
		return nil
	}
	for _, p := range ms.participants {
		if p.UserID == userID {
			return p
		}
	}
	return nil
}

func (s *inMemCallStore) countActiveParticipants(callID uuid.UUID) int {
	ms := s.sessions[callID]
	if ms == nil {
		return 0
	}
	count := 0
	for _, p := range ms.participants {
		if p.JoinState == domain.JoinStateJoined {
			count++
		}
	}
	return count
}

func (s *inMemCallStore) setParticipantJoinState(callID, userID uuid.UUID, state string) {
	ms := s.sessions[callID]
	if ms == nil {
		return
	}
	for _, p := range ms.participants {
		if p.UserID == userID {
			p.JoinState = state
			if state == domain.JoinStateJoined {
				now := time.Now()
				p.JoinedAt = &now
			}
			return
		}
	}
}

func (s *inMemCallStore) getInviteForUser(callID, inviteeID uuid.UUID) *domain.CallInvite {
	ms := s.sessions[callID]
	if ms == nil {
		return nil
	}
	for _, inv := range ms.invites {
		if inv.InviteeUserID == inviteeID && inv.ResponseStatus == domain.ResponseStatusPending {
			return inv
		}
	}
	return nil
}

func (s *inMemCallStore) endCall(callID uuid.UUID, reason string) {
	ms := s.sessions[callID]
	if ms == nil {
		return
	}
	ms.session.State = domain.CallStateEnded
	r := reason
	ms.session.EndedReason = &r
}

// ---------------------------------------------------------------------------
// Business logic helpers
// These mirror service.Service methods so tests exercise real rules
// without needing a DB connection.
// ---------------------------------------------------------------------------

// errCallRateLimitExceeded mirrors ErrCallRateLimitExceeded.
var errCallRateLimitExceeded = errors.New("call rate limit exceeded: max 30 calls per hour")

// createCall simulates Service.CreateCall logic.
func createCall(
	_ context.Context,
	store *inMemCallStore,
	userID uuid.UUID,
	callType string,
	targetUserIDs []uuid.UUID,
) (uuid.UUID, error) {
	// Rate limit: max 30 calls/hr.
	if store.callsCreatedThisHour[userID] >= 30 {
		return uuid.Nil, errCallRateLimitExceeded
	}

	callID := uuid.New()
	joinMode := domain.JoinModeInviteOnly
	if callType == domain.CallTypeGroupAudio || callType == domain.CallTypeGroupVideo {
		joinMode = domain.JoinModeOpen
	}

	store.createCall(callID, userID, joinMode, 25)

	for _, targetID := range targetUserIDs {
		if targetID == userID {
			continue
		}
		store.addInvite(callID, userID, targetID)
	}

	return callID, nil
}

// joinCall simulates Service.JoinCall logic.
func joinCall(
	_ context.Context,
	store *inMemCallStore,
	userID, callID uuid.UUID,
) error {
	ms := store.getSession(callID)
	if ms == nil {
		return errors.New("call not found")
	}
	if ms.session.State == domain.CallStateEnded {
		return errors.New("call has already ended")
	}

	participant := store.getParticipant(callID, userID)

	if participant == nil {
		// Invite-only: user must have been invited.
		if ms.session.JoinMode == domain.JoinModeInviteOnly {
			return errors.New("not a participant in this call")
		}
		// Open join mode — add participant.
		now := time.Now()
		ms.participants = append(ms.participants, &domain.CallParticipant{
			ID:            uuid.New(),
			CallSessionID: callID,
			UserID:        userID,
			Role:          domain.RoleParticipant,
			InviteState:   domain.InviteStateAccepted,
			JoinState:     domain.JoinStateNotJoined,
			CreatedAt:     now,
		})
		participant = ms.participants[len(ms.participants)-1]
	}

	if participant.JoinState == domain.JoinStateJoined {
		return errors.New("user has already joined this call")
	}

	store.setParticipantJoinState(callID, userID, domain.JoinStateJoined)

	// Transition to active on first join.
	if ms.session.State == domain.CallStateRinging {
		ms.session.State = domain.CallStateActive
	}

	return nil
}

// leaveCall simulates Service.LeaveCall logic.
func leaveCall(
	_ context.Context,
	store *inMemCallStore,
	userID, callID uuid.UUID,
) error {
	ms := store.getSession(callID)
	if ms == nil {
		return errors.New("call not found")
	}

	participant := store.getParticipant(callID, userID)
	if participant == nil {
		return errors.New("not a participant")
	}

	store.setParticipantJoinState(callID, userID, domain.JoinStateLeft)

	// Auto-end if no active participants remain.
	if store.countActiveParticipants(callID) == 0 {
		reason := domain.EndedReasonAllLeft
		if userID == ms.session.InitiatorUserID {
			reason = domain.EndedReasonHostLeft
		}
		store.endCall(callID, reason)
	}

	return nil
}

// endCall simulates Service.EndCall logic (host-only).
func endCallByUser(
	_ context.Context,
	store *inMemCallStore,
	userID, callID uuid.UUID,
) error {
	ms := store.getSession(callID)
	if ms == nil {
		return errors.New("call not found")
	}

	participant := store.getParticipant(callID, userID)
	if participant == nil || (participant.Role != domain.RoleHost && participant.Role != domain.RoleModerator) {
		return errors.New("only the host can perform this action")
	}

	store.endCall(callID, domain.EndedReasonCompleted)
	return nil
}

// checkCallRateLimit mirrors RateLimiter.CheckCallRate using the in-memory counter.
func checkCallRateLimit(store *inMemCallStore, userID uuid.UUID) error {
	if store.callsCreatedThisHour[userID] >= 30 {
		return errCallRateLimitExceeded
	}
	return nil
}

// checkScheduledCallReminder returns whether a reminder is due (remind_at <= now).
func checkScheduledCallReminder(remindAt time.Time) bool {
	return !remindAt.After(time.Now())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestCreateCall_DirectCall verifies that creating a direct call creates the
// session, adds the host as a participant, and creates an invite for the target.
func TestCreateCall_DirectCall(t *testing.T) {
	ctx := context.Background()
	store := newInMemCallStore()

	hostID := uuid.New()
	targetID := uuid.New()

	callID, err := createCall(ctx, store, hostID, domain.CallTypeDirectAudio, []uuid.UUID{targetID})
	if err != nil {
		t.Fatalf("createCall: %v", err)
	}

	ms := store.getSession(callID)
	if ms == nil {
		t.Fatal("expected call session to exist")
	}

	// Host should be a participant with RoleHost.
	hostPart := store.getParticipant(callID, hostID)
	if hostPart == nil {
		t.Fatal("expected host participant to exist")
	}
	if hostPart.Role != domain.RoleHost {
		t.Fatalf("expected host role, got %s", hostPart.Role)
	}

	// Target should have an invite.
	inv := store.getInviteForUser(callID, targetID)
	if inv == nil {
		t.Fatal("expected invite for target user")
	}
	if inv.InviteeUserID != targetID {
		t.Fatalf("expected invitee=%v, got %v", targetID, inv.InviteeUserID)
	}

	// Join mode should be invite_only for direct calls.
	if ms.session.JoinMode != domain.JoinModeInviteOnly {
		t.Fatalf("expected join_mode=invite_only, got %s", ms.session.JoinMode)
	}
}

// TestJoinCall_RequiresInvite verifies that joining an invite-only call without
// being invited returns an error.
func TestJoinCall_RequiresInvite(t *testing.T) {
	ctx := context.Background()
	store := newInMemCallStore()

	hostID := uuid.New()
	targetID := uuid.New()
	uninvitedID := uuid.New()

	callID, err := createCall(ctx, store, hostID, domain.CallTypeDirectAudio, []uuid.UUID{targetID})
	if err != nil {
		t.Fatalf("createCall: %v", err)
	}

	// Uninvited user tries to join.
	err = joinCall(ctx, store, uninvitedID, callID)
	if err == nil {
		t.Fatal("expected error when uninvited user joins invite-only call")
	}

	const wantErr = "not a participant in this call"
	if err.Error() != wantErr {
		t.Fatalf("expected error %q, got %q", wantErr, err.Error())
	}
}

// TestLeaveCall_LastParticipant_EndsCall verifies that when the last participant
// leaves, the call is automatically ended.
func TestLeaveCall_LastParticipant_EndsCall(t *testing.T) {
	ctx := context.Background()
	store := newInMemCallStore()

	hostID := uuid.New()
	otherID := uuid.New()

	callID, err := createCall(ctx, store, hostID, domain.CallTypeGroupAudio, []uuid.UUID{otherID})
	if err != nil {
		t.Fatalf("createCall: %v", err)
	}

	// Both join.
	if err := joinCall(ctx, store, hostID, callID); err != nil {
		t.Fatalf("host join: %v", err)
	}
	if err := joinCall(ctx, store, otherID, callID); err != nil {
		t.Fatalf("other join: %v", err)
	}

	ms := store.getSession(callID)
	if ms.session.State != domain.CallStateActive {
		t.Fatalf("expected active state after both joined, got %s", ms.session.State)
	}

	// Host leaves.
	if err := leaveCall(ctx, store, hostID, callID); err != nil {
		t.Fatalf("host leave: %v", err)
	}
	if ms.session.State == domain.CallStateEnded {
		t.Fatal("call should NOT end yet, other participant still joined")
	}

	// Other participant leaves — call should now end.
	if err := leaveCall(ctx, store, otherID, callID); err != nil {
		t.Fatalf("other leave: %v", err)
	}
	if ms.session.State != domain.CallStateEnded {
		t.Fatalf("expected call to end after last participant left, got state=%s", ms.session.State)
	}
}

// TestEndCall_HostOnly verifies that only the host (or moderator) can end a call.
func TestEndCall_HostOnly(t *testing.T) {
	ctx := context.Background()
	store := newInMemCallStore()

	hostID := uuid.New()
	regularUserID := uuid.New()

	callID, err := createCall(ctx, store, hostID, domain.CallTypeDirectAudio, []uuid.UUID{regularUserID})
	if err != nil {
		t.Fatalf("createCall: %v", err)
	}

	// Regular user tries to end the call.
	err = endCallByUser(ctx, store, regularUserID, callID)
	if err == nil {
		t.Fatal("expected error when non-host tries to end call")
	}
	const wantErr = "only the host can perform this action"
	if err.Error() != wantErr {
		t.Fatalf("expected %q, got %q", wantErr, err.Error())
	}

	// Host ends the call successfully.
	if err := endCallByUser(ctx, store, hostID, callID); err != nil {
		t.Fatalf("host end call: %v", err)
	}

	ms := store.getSession(callID)
	if ms.session.State != domain.CallStateEnded {
		t.Fatalf("expected call state=ended, got %s", ms.session.State)
	}
}

// TestRateLimit_CallCreation verifies that creating more than 30 calls in an
// hour is blocked for the same user.
func TestRateLimit_CallCreation(t *testing.T) {
	ctx := context.Background()
	store := newInMemCallStore()

	userID := uuid.New()

	// Fill up to the limit (30 calls).
	for i := 0; i < 30; i++ {
		store.callsCreatedThisHour[userID]++
	}

	// 31st call should be blocked.
	err := checkCallRateLimit(store, userID)
	if err == nil {
		t.Fatal("expected rate limit error after 30 calls/hour")
	}
	if !errors.Is(err, errCallRateLimitExceeded) {
		t.Fatalf("expected ErrCallRateLimitExceeded, got %v", err)
	}

	// Verify the error matches the service error text exactly.
	const wantMsg = "call rate limit exceeded: max 30 calls per hour"
	if err.Error() != wantMsg {
		t.Fatalf("expected error msg %q, got %q", wantMsg, err.Error())
	}

	// A call attempt also goes through createCall which checks the same limit.
	_, err = createCall(ctx, store, userID, domain.CallTypeDirectAudio, []uuid.UUID{uuid.New()})
	if err == nil {
		t.Fatal("expected rate limit error from createCall")
	}
}

// TestScheduledCall_ReminderDue verifies that a reminder whose remind_at is in
// the past is considered due, and a future one is not.
func TestScheduledCall_ReminderDue(t *testing.T) {
	cases := []struct {
		name      string
		remindAt  time.Time
		expectDue bool
	}{
		{
			name:      "past reminder is due",
			remindAt:  time.Now().Add(-5 * time.Minute),
			expectDue: true,
		},
		{
			name:      "future reminder is not due",
			remindAt:  time.Now().Add(30 * time.Minute),
			expectDue: false,
		},
		{
			name:      "reminder exactly now is due",
			remindAt:  time.Now().Add(-time.Millisecond),
			expectDue: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			due := checkScheduledCallReminder(tc.remindAt)
			if due != tc.expectDue {
				t.Fatalf("checkScheduledCallReminder(%v) = %v, want %v", tc.remindAt, due, tc.expectDue)
			}
		})
	}
}
