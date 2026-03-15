package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/atpost/chat-call-service/internal/sfu"
	"github.com/atpost/chat-call-service/internal/store/postgres"
	events "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

var (
	ErrCallNotFound        = errors.New("call not found")
	ErrInviteNotFound      = errors.New("invite not found")
	ErrNotParticipant      = errors.New("not a participant in this call")
	ErrNotHost             = errors.New("only the host can perform this action")
	ErrCallAlreadyEnded    = errors.New("call has already ended")
	ErrAlreadyInCall       = errors.New("user is already in an active call")
	ErrAlreadyJoined       = errors.New("user has already joined this call")
	ErrInviteNotPending    = errors.New("invite is not in pending state")
	ErrMaxInvitesPerCall   = errors.New("maximum 20 invites per call")
	ErrCannotInviteSelf    = errors.New("cannot invite yourself")
	ErrCallNotActive       = errors.New("call is not active")
	ErrAlreadyAudioVideo   = errors.New("call already has video enabled")
	ErrMaxParticipants     = errors.New("call has reached maximum participants")
)

// Service is the business logic layer for calls.
type Service struct {
	store       *postgres.CallStore
	sfuProvider sfu.SFUProvider
	rateLimiter *RateLimiter
	log         *slog.Logger

	signalingEndpoint     string
	reconnectGraceSeconds int
}

func New(
	store *postgres.CallStore,
	sfuProvider sfu.SFUProvider,
	rateLimiter *RateLimiter,
	log *slog.Logger,
	reconnectGraceSeconds int,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store:                 store,
		sfuProvider:           sfuProvider,
		rateLimiter:           rateLimiter,
		log:                   log,
		signalingEndpoint:     "/v1/ws/connect",
		reconnectGraceSeconds: reconnectGraceSeconds,
	}
}

// ---------------------------------------------------------------------------
// CreateCall
// ---------------------------------------------------------------------------

type CreateCallRequest struct {
	CallType      string      `json:"call_type"`
	SourceType    string      `json:"source_type"`
	SourceID      *uuid.UUID  `json:"source_id,omitempty"`
	TargetUserIDs []uuid.UUID `json:"target_user_ids"`
	AudioOnly     bool        `json:"audio_only"`
	MaxParticipants int       `json:"max_participants,omitempty"`
	IdempotencyKey  string    `json:"idempotency_key,omitempty"`
}

func (s *Service) CreateCall(ctx context.Context, userID uuid.UUID, req CreateCallRequest) (*CallResponse, error) {
	// Rate limit
	if err := s.rateLimiter.CheckCallRate(ctx, userID); err != nil {
		return nil, err
	}

	// Check user not already in active call
	existing, err := s.store.GetActiveCallForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check active call: %w", err)
	}
	if existing != nil {
		return nil, ErrAlreadyInCall
	}

	// Anti-spam check for direct calls
	isDirect := req.CallType == domain.CallTypeDirectAudio || req.CallType == domain.CallTypeDirectVideo
	if isDirect && len(req.TargetUserIDs) == 1 {
		if err := s.rateLimiter.CheckRingAntiSpam(ctx, userID, req.TargetUserIDs[0]); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	callID := uuid.New()
	roomID := uuid.New()
	roomKey := fmt.Sprintf("call-%s", callID)

	maxPart := req.MaxParticipants
	if maxPart <= 0 {
		if isDirect {
			maxPart = 2
		} else {
			maxPart = 25
		}
	}

	joinMode := domain.JoinModeInviteOnly
	if !isDirect {
		joinMode = domain.JoinModeOpen
	}

	// Create SFU room
	providerRoomName, err := s.sfuProvider.CreateRoom(ctx, roomKey, maxPart)
	if err != nil {
		return nil, fmt.Errorf("create SFU room: %w", err)
	}

	// Persist room
	room := &domain.CallRoom{
		ID:               roomID,
		RoomKey:          roomKey,
		Provider:         s.sfuProvider.ProviderName(),
		ProviderRoomName: providerRoomName,
		RegionCode:       "ap-south-1",
		Status:           domain.RoomStatusAllocated,
		MaxParticipants:  maxPart,
		CreatedAt:        now,
	}
	if err := s.store.CreateRoom(ctx, room); err != nil {
		return nil, fmt.Errorf("persist room: %w", err)
	}

	// Persist call session
	session := &domain.CallSession{
		ID:              callID,
		CallType:        req.CallType,
		SourceType:      req.SourceType,
		SourceID:        req.SourceID,
		InitiatorUserID: userID,
		RoomID:          &roomID,
		State:           domain.CallStateRinging,
		AudioOnly:       req.AudioOnly,
		MaxParticipants: maxPart,
		JoinMode:        joinMode,
		StartedAt:       &now,
		CreatedAt:       now,
	}
	if err := s.store.CreateCallSession(ctx, session); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}

	// Add initiator as host participant
	hostParticipant := &domain.CallParticipant{
		ID:            uuid.New(),
		CallSessionID: callID,
		UserID:        userID,
		Role:          domain.RoleHost,
		InviteState:   domain.InviteStateAccepted,
		JoinState:     domain.JoinStateNotJoined,
		CreatedAt:     now,
	}
	if err := s.store.AddParticipant(ctx, hostParticipant); err != nil {
		return nil, fmt.Errorf("add host participant: %w", err)
	}

	// Outbox: CallCreated
	_ = s.store.InsertOutboxEvent(ctx, events.CallCreated, events.CallCreatedPayload{
		CallID:          callID.String(),
		CallType:        req.CallType,
		SourceType:      req.SourceType,
		SourceID:        uuidPtrToString(req.SourceID),
		InitiatorUserID: userID.String(),
		AudioOnly:       req.AudioOnly,
		CreatedAt:       now,
	})

	// Create invites for target users
	for _, targetID := range req.TargetUserIDs {
		if targetID == userID {
			continue
		}
		inviteID := uuid.New()
		invite := &domain.CallInvite{
			ID:              inviteID,
			CallSessionID:   callID,
			InviterUserID:   userID,
			InviteeUserID:   targetID,
			DeliveryChannel: domain.DeliveryChannelWebSocket,
			DeliveryStatus:  domain.DeliveryStatusPending,
			ResponseStatus:  domain.ResponseStatusPending,
			CreatedAt:       now,
		}
		if err := s.store.CreateInvite(ctx, invite); err != nil {
			s.log.Warn("failed to create invite", "err", err, "target_user_id", targetID)
			continue
		}

		// Add as participant
		participant := &domain.CallParticipant{
			ID:            uuid.New(),
			CallSessionID: callID,
			UserID:        targetID,
			Role:          domain.RoleParticipant,
			InviteState:   domain.InviteStateInvited,
			JoinState:     domain.JoinStateNotJoined,
			CreatedAt:     now,
		}
		if err := s.store.AddParticipant(ctx, participant); err != nil {
			s.log.Warn("failed to add participant", "err", err, "target_user_id", targetID)
			continue
		}

		// Outbox: CallInvited
		_ = s.store.InsertOutboxEvent(ctx, events.CallInvited, events.CallInvitedPayload{
			CallID:        callID.String(),
			InviteID:      inviteID.String(),
			InviterUserID: userID.String(),
			InviteeUserID: targetID.String(),
			CallType:      req.CallType,
			CreatedAt:     now,
		})
	}

	return s.buildCallResponse(ctx, callID)
}

// ---------------------------------------------------------------------------
// GetCall
// ---------------------------------------------------------------------------

func (s *Service) GetCall(ctx context.Context, userID, callID uuid.UUID) (*CallResponse, error) {
	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}

	// Verify user is a participant
	participant, err := s.store.GetParticipant(ctx, callID, userID)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, ErrNotParticipant
	}

	return s.buildCallResponse(ctx, callID)
}

// ---------------------------------------------------------------------------
// JoinCall
// ---------------------------------------------------------------------------

func (s *Service) JoinCall(ctx context.Context, userID, callID uuid.UUID) (*JoinResponse, error) {
	if err := s.rateLimiter.CheckJoinRate(ctx, userID, callID); err != nil {
		return nil, err
	}

	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}

	if session.State == domain.CallStateEnded || session.State == domain.CallStateCanceled ||
		session.State == domain.CallStateFailed || session.State == domain.CallStateExpired {
		return nil, ErrCallAlreadyEnded
	}

	participant, err := s.store.GetParticipant(ctx, callID, userID)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		// If open join mode, allow joining without invite
		if session.JoinMode == domain.JoinModeOpen {
			activeCount, err := s.store.CountActiveParticipants(ctx, callID)
			if err != nil {
				return nil, err
			}
			if activeCount >= session.MaxParticipants {
				return nil, ErrMaxParticipants
			}
			participant = &domain.CallParticipant{
				ID:            uuid.New(),
				CallSessionID: callID,
				UserID:        userID,
				Role:          domain.RoleParticipant,
				InviteState:   domain.InviteStateAccepted,
				JoinState:     domain.JoinStateNotJoined,
				CreatedAt:     time.Now(),
			}
			if err := s.store.AddParticipant(ctx, participant); err != nil {
				return nil, err
			}
		} else {
			return nil, ErrNotParticipant
		}
	}

	if participant.JoinState == domain.JoinStateJoined {
		return nil, ErrAlreadyJoined
	}

	// Get room for SFU token
	if session.RoomID == nil {
		return nil, errors.New("call has no room assigned")
	}
	room, err := s.store.GetRoom(ctx, *session.RoomID)
	if err != nil || room == nil {
		return nil, errors.New("room not found")
	}

	// Generate SFU token
	token, err := s.sfuProvider.GenerateToken(ctx, room.ProviderRoomName, userID.String(), true)
	if err != nil {
		return nil, fmt.Errorf("generate SFU token: %w", err)
	}

	// Update participant state
	if err := s.store.UpdateParticipantJoinState(ctx, callID, userID, domain.JoinStateJoined); err != nil {
		return nil, err
	}

	// Transition call to active if first non-initiator joins
	if session.State == domain.CallStateRinging || session.State == domain.CallStateInitiated {
		_ = s.store.UpdateCallState(ctx, callID, domain.CallStateActive, nil)
		_ = s.store.UpdateRoomStatus(ctx, room.ID, domain.RoomStatusActive)
	}

	// Outbox: CallJoined
	_ = s.store.InsertOutboxEvent(ctx, events.CallJoined, events.CallJoinedPayload{
		CallID:   callID.String(),
		UserID:   userID.String(),
		JoinedAt: time.Now(),
	})

	return &JoinResponse{
		CallID:                callID,
		SFUToken:              token,
		SFURoomName:           room.ProviderRoomName,
		ICEServers:            s.sfuProvider.GetICEServers(),
		SignalingEndpoint:     s.signalingEndpoint,
		ReconnectGraceSeconds: s.reconnectGraceSeconds,
	}, nil
}

// ---------------------------------------------------------------------------
// AcceptInvite
// ---------------------------------------------------------------------------

func (s *Service) AcceptInvite(ctx context.Context, userID, callID, inviteID uuid.UUID) error {
	invite, err := s.store.GetInvite(ctx, inviteID)
	if err != nil {
		return err
	}
	if invite == nil {
		return ErrInviteNotFound
	}
	if invite.CallSessionID != callID || invite.InviteeUserID != userID {
		return ErrInviteNotFound
	}
	if invite.ResponseStatus != domain.ResponseStatusPending {
		return ErrInviteNotPending
	}

	if err := s.store.UpdateInviteResponse(ctx, inviteID, domain.ResponseStatusAccepted); err != nil {
		return err
	}
	if err := s.store.UpdateParticipantInviteState(ctx, callID, userID, domain.InviteStateAccepted); err != nil {
		return err
	}

	// Clear anti-spam ring counter on accept
	session, _ := s.store.GetCallSession(ctx, callID)
	if session != nil {
		s.rateLimiter.ClearRingCounter(ctx, session.InitiatorUserID, userID)
	}

	_ = s.store.InsertOutboxEvent(ctx, events.CallAccepted, events.CallAcceptedPayload{
		CallID:     callID.String(),
		InviteID:   inviteID.String(),
		UserID:     userID.String(),
		AcceptedAt: time.Now(),
	})

	return nil
}

// ---------------------------------------------------------------------------
// DeclineInvite
// ---------------------------------------------------------------------------

func (s *Service) DeclineInvite(ctx context.Context, userID, callID, inviteID uuid.UUID) error {
	invite, err := s.store.GetInvite(ctx, inviteID)
	if err != nil {
		return err
	}
	if invite == nil {
		return ErrInviteNotFound
	}
	if invite.CallSessionID != callID || invite.InviteeUserID != userID {
		return ErrInviteNotFound
	}
	if invite.ResponseStatus != domain.ResponseStatusPending {
		return ErrInviteNotPending
	}

	if err := s.store.UpdateInviteResponse(ctx, inviteID, domain.ResponseStatusDeclined); err != nil {
		return err
	}
	if err := s.store.UpdateParticipantInviteState(ctx, callID, userID, domain.InviteStateDeclined); err != nil {
		return err
	}

	_ = s.store.InsertOutboxEvent(ctx, events.CallDeclined, events.CallDeclinedPayload{
		CallID:     callID.String(),
		InviteID:   inviteID.String(),
		UserID:     userID.String(),
		DeclinedAt: time.Now(),
	})

	// If all invitees declined in a direct call, auto-end
	session, _ := s.store.GetCallSession(ctx, callID)
	if session != nil && session.IsDirectCall() {
		activeCount, _ := s.store.CountActiveParticipants(ctx, callID)
		if activeCount == 0 {
			s.endCallInternal(ctx, callID, session.InitiatorUserID, domain.EndedReasonMissed)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// LeaveCall
// ---------------------------------------------------------------------------

func (s *Service) LeaveCall(ctx context.Context, userID, callID uuid.UUID) error {
	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil {
		return err
	}
	if session == nil {
		return ErrCallNotFound
	}

	participant, err := s.store.GetParticipant(ctx, callID, userID)
	if err != nil {
		return err
	}
	if participant == nil {
		return ErrNotParticipant
	}

	if err := s.store.UpdateParticipantJoinState(ctx, callID, userID, domain.JoinStateLeft); err != nil {
		return err
	}

	// Calculate duration
	if participant.JoinedAt != nil {
		dur := int(time.Since(*participant.JoinedAt).Seconds())
		_ = s.store.SetParticipantDuration(ctx, callID, userID, dur)
	}

	_ = s.store.InsertOutboxEvent(ctx, events.CallLeft, events.CallLeftPayload{
		CallID: callID.String(),
		UserID: userID.String(),
		LeftAt: time.Now(),
	})

	// Check if all participants have left → auto-end
	activeCount, _ := s.store.CountActiveParticipants(ctx, callID)
	if activeCount == 0 {
		reason := domain.EndedReasonAllLeft
		if userID == session.InitiatorUserID {
			reason = domain.EndedReasonHostLeft
		}
		s.endCallInternal(ctx, callID, userID, reason)
	}

	return nil
}

// ---------------------------------------------------------------------------
// EndCall
// ---------------------------------------------------------------------------

func (s *Service) EndCall(ctx context.Context, userID, callID uuid.UUID) error {
	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil {
		return err
	}
	if session == nil {
		return ErrCallNotFound
	}
	if session.InitiatorUserID != userID {
		// Allow host or moderator
		participant, err := s.store.GetParticipant(ctx, callID, userID)
		if err != nil {
			return err
		}
		if participant == nil || (participant.Role != domain.RoleHost && participant.Role != domain.RoleModerator) {
			return ErrNotHost
		}
	}

	s.endCallInternal(ctx, callID, userID, domain.EndedReasonCompleted)
	return nil
}

func (s *Service) endCallInternal(ctx context.Context, callID uuid.UUID, endedBy uuid.UUID, reason string) {
	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil || session == nil {
		return
	}
	if session.State == domain.CallStateEnded {
		return
	}

	endedReason := reason
	_ = s.store.UpdateCallState(ctx, callID, domain.CallStateEnded, &endedReason)
	_ = s.store.MarkAllParticipantsLeft(ctx, callID)
	_ = s.store.ExpirePendingInvitesForCall(ctx, callID)

	// Close SFU room
	if session.RoomID != nil {
		room, err := s.store.GetRoom(ctx, *session.RoomID)
		if err == nil && room != nil {
			_ = s.sfuProvider.CloseRoom(ctx, room.ProviderRoomName)
			_ = s.store.UpdateRoomStatus(ctx, room.ID, domain.RoomStatusClosed)
		}
	}

	// Calculate call duration
	durationSeconds := 0
	if session.AnsweredAt != nil {
		durationSeconds = int(time.Since(*session.AnsweredAt).Seconds())
	} else if session.StartedAt != nil {
		durationSeconds = int(time.Since(*session.StartedAt).Seconds())
	}

	_ = s.store.InsertOutboxEvent(ctx, events.CallEnded, events.CallEndedPayload{
		CallID:          callID.String(),
		InitiatorUserID: session.InitiatorUserID.String(),
		EndedBy:         endedBy.String(),
		EndedReason:     reason,
		DurationSeconds: durationSeconds,
		SourceType:      session.SourceType,
		SourceID:        uuidPtrToString(session.SourceID),
		EndedAt:         time.Now(),
	})
}

// ---------------------------------------------------------------------------
// InviteParticipants
// ---------------------------------------------------------------------------

type InviteParticipantsRequest struct {
	UserIDs []uuid.UUID `json:"user_ids"`
}

func (s *Service) InviteParticipants(ctx context.Context, userID, callID uuid.UUID, req InviteParticipantsRequest) (*InviteResponse, error) {
	if len(req.UserIDs) > 20 {
		return nil, ErrMaxInvitesPerCall
	}

	if err := s.rateLimiter.CheckInviteRate(ctx, userID); err != nil {
		return nil, err
	}

	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}
	if session.State != domain.CallStateActive && session.State != domain.CallStateRinging {
		return nil, ErrCallNotActive
	}

	// Verify user is a participant
	participant, err := s.store.GetParticipant(ctx, callID, userID)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, ErrNotParticipant
	}

	now := time.Now()
	sent := 0
	for _, targetID := range req.UserIDs {
		if targetID == userID {
			continue
		}

		// Check if already invited
		existing, _ := s.store.GetInviteByCallAndUser(ctx, callID, targetID)
		if existing != nil {
			continue
		}

		inviteID := uuid.New()
		invite := &domain.CallInvite{
			ID:              inviteID,
			CallSessionID:   callID,
			InviterUserID:   userID,
			InviteeUserID:   targetID,
			DeliveryChannel: domain.DeliveryChannelWebSocket,
			DeliveryStatus:  domain.DeliveryStatusPending,
			ResponseStatus:  domain.ResponseStatusPending,
			CreatedAt:       now,
		}
		if err := s.store.CreateInvite(ctx, invite); err != nil {
			s.log.Warn("failed to create invite", "err", err, "target_user_id", targetID)
			continue
		}

		newParticipant := &domain.CallParticipant{
			ID:            uuid.New(),
			CallSessionID: callID,
			UserID:        targetID,
			Role:          domain.RoleParticipant,
			InviteState:   domain.InviteStateInvited,
			JoinState:     domain.JoinStateNotJoined,
			CreatedAt:     now,
		}
		_ = s.store.AddParticipant(ctx, newParticipant)

		_ = s.store.InsertOutboxEvent(ctx, events.CallInvited, events.CallInvitedPayload{
			CallID:        callID.String(),
			InviteID:      inviteID.String(),
			InviterUserID: userID.String(),
			InviteeUserID: targetID.String(),
			CallType:      session.CallType,
			CreatedAt:     now,
		})
		sent++
	}

	return &InviteResponse{CallID: callID, InvitesSent: sent}, nil
}

// ---------------------------------------------------------------------------
// MuteParticipant
// ---------------------------------------------------------------------------

func (s *Service) MuteParticipant(ctx context.Context, userID, callID, targetUserID uuid.UUID) error {
	// Verify caller is host or moderator
	caller, err := s.store.GetParticipant(ctx, callID, userID)
	if err != nil {
		return err
	}
	if caller == nil {
		return ErrNotParticipant
	}
	if caller.Role != domain.RoleHost && caller.Role != domain.RoleModerator {
		return ErrNotHost
	}

	target, err := s.store.GetParticipant(ctx, callID, targetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrNotParticipant
	}

	if err := s.store.UpdateMediaState(ctx, callID, targetUserID, true, target.VideoMuted); err != nil {
		return err
	}

	_ = s.store.InsertOutboxEvent(ctx, events.CallParticipantMuted, events.CallParticipantMutedPayload{
		CallID:       callID.String(),
		TargetUserID: targetUserID.String(),
		MutedBy:      userID.String(),
		MutedAt:      time.Now(),
	})

	return nil
}

// ---------------------------------------------------------------------------
// RemoveParticipant
// ---------------------------------------------------------------------------

func (s *Service) RemoveParticipant(ctx context.Context, userID, callID, targetUserID uuid.UUID) error {
	caller, err := s.store.GetParticipant(ctx, callID, userID)
	if err != nil {
		return err
	}
	if caller == nil {
		return ErrNotParticipant
	}
	if caller.Role != domain.RoleHost && caller.Role != domain.RoleModerator {
		return ErrNotHost
	}

	target, err := s.store.GetParticipant(ctx, callID, targetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrNotParticipant
	}
	// Can't remove host
	if target.Role == domain.RoleHost {
		return errors.New("cannot remove the host")
	}

	if err := s.store.UpdateParticipantJoinState(ctx, callID, targetUserID, domain.JoinStateRemoved); err != nil {
		return err
	}

	_ = s.store.InsertOutboxEvent(ctx, events.CallParticipantRemoved, events.CallParticipantRemovedPayload{
		CallID:       callID.String(),
		TargetUserID: targetUserID.String(),
		RemovedBy:    userID.String(),
		RemovedAt:    time.Now(),
	})

	return nil
}

// ---------------------------------------------------------------------------
// GetCallHistory
// ---------------------------------------------------------------------------

func (s *Service) GetCallHistory(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]CallHistoryItem, string, error) {
	sessions, nextCursor, err := s.store.ListCallHistory(ctx, userID, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	items := make([]CallHistoryItem, 0, len(sessions))
	for _, cs := range sessions {
		participants, _ := s.store.GetParticipants(ctx, cs.ID)

		durationSeconds := 0
		if cs.AnsweredAt != nil && cs.EndedAt != nil {
			durationSeconds = int(cs.EndedAt.Sub(*cs.AnsweredAt).Seconds())
		}

		isMissed := cs.EndedReason != nil && *cs.EndedReason == domain.EndedReasonMissed
		isIncoming := cs.InitiatorUserID != userID

		partResponses := make([]ParticipantResponse, 0, len(participants))
		for _, p := range participants {
			partResponses = append(partResponses, toParticipantResponse(p))
		}

		items = append(items, CallHistoryItem{
			ID:              cs.ID,
			CallType:        cs.CallType,
			SourceType:      cs.SourceType,
			SourceID:        cs.SourceID,
			InitiatorUserID: cs.InitiatorUserID,
			State:           cs.State,
			AudioOnly:       cs.AudioOnly,
			EndedReason:     cs.EndedReason,
			DurationSeconds: durationSeconds,
			IsMissed:        isMissed,
			IsIncoming:      isIncoming,
			Participants:    partResponses,
			CreatedAt:       cs.CreatedAt,
			EndedAt:         cs.EndedAt,
		})
	}

	return items, nextCursor, nil
}

// ---------------------------------------------------------------------------
// UpgradeCall
// ---------------------------------------------------------------------------

func (s *Service) UpgradeCall(ctx context.Context, userID, callID uuid.UUID) error {
	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil {
		return err
	}
	if session == nil {
		return ErrCallNotFound
	}
	if session.State != domain.CallStateActive {
		return ErrCallNotActive
	}
	if !session.AudioOnly {
		return ErrAlreadyAudioVideo
	}

	// Verify participant
	participant, err := s.store.GetParticipant(ctx, callID, userID)
	if err != nil {
		return err
	}
	if participant == nil {
		return ErrNotParticipant
	}

	// For now, any participant can request upgrade. The other side confirms via signaling.
	_ = s.store.InsertOutboxEvent(ctx, events.CallUpgraded, events.CallUpgradedPayload{
		CallID:     callID.String(),
		UpgradedBy: userID.String(),
		FromType:   session.CallType,
		ToType:     domain.CallTypeDirectVideo,
		UpgradedAt: time.Now(),
	})

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Service) buildCallResponse(ctx context.Context, callID uuid.UUID) (*CallResponse, error) {
	session, err := s.store.GetCallSession(ctx, callID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}

	participants, err := s.store.GetParticipants(ctx, callID)
	if err != nil {
		return nil, err
	}

	partResponses := make([]ParticipantResponse, 0, len(participants))
	for _, p := range participants {
		partResponses = append(partResponses, toParticipantResponse(p))
	}

	return &CallResponse{
		ID:              session.ID,
		CallType:        session.CallType,
		SourceType:      session.SourceType,
		SourceID:        session.SourceID,
		InitiatorUserID: session.InitiatorUserID,
		State:           session.State,
		AudioOnly:       session.AudioOnly,
		MaxParticipants: session.MaxParticipants,
		JoinMode:        session.JoinMode,
		Participants:    partResponses,
		StartedAt:       session.StartedAt,
		AnsweredAt:      session.AnsweredAt,
		EndedAt:         session.EndedAt,
		EndedReason:     session.EndedReason,
		CreatedAt:       session.CreatedAt,
	}, nil
}

func toParticipantResponse(p domain.CallParticipant) ParticipantResponse {
	return ParticipantResponse{
		ID:              p.ID,
		UserID:          p.UserID,
		Role:            p.Role,
		InviteState:     p.InviteState,
		JoinState:       p.JoinState,
		AudioMuted:      p.AudioMuted,
		VideoMuted:      p.VideoMuted,
		HandRaised:      p.HandRaised,
		IsScreenSharing: p.IsScreenSharing,
		JoinedAt:        p.JoinedAt,
		LeftAt:          p.LeftAt,
		DurationSeconds: p.DurationSeconds,
	}
}

func uuidPtrToString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
