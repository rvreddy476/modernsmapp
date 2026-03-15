package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrLinkNotFound is returned when a call link token is invalid or expired.
var ErrLinkNotFound = errors.New("call link not found or expired")

// ErrSummaryNotFound is returned when no summary exists for a call.
var ErrSummaryNotFound = errors.New("call summary not found")

// ---------------------------------------------------------------------------
// Call Links
// ---------------------------------------------------------------------------

// GenerateCallLink creates a shareable link for a call session.
// Only the call initiator may generate a link.
func (s *Service) GenerateCallLink(ctx context.Context, userID, callSessionID uuid.UUID, expiresInHours int, lobbyEnabled bool) (map[string]any, error) {
	session, err := s.store.GetCallSession(ctx, callSessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}
	if session.InitiatorUserID != userID {
		return nil, ErrNotHost
	}

	if expiresInHours <= 0 {
		expiresInHours = 24
	}
	expiresAt := time.Now().Add(time.Duration(expiresInHours) * time.Hour)

	token, err := s.store.CreateCallLink(ctx, callSessionID, expiresAt, lobbyEnabled)
	if err != nil {
		return nil, fmt.Errorf("create call link: %w", err)
	}

	return map[string]any{
		"link_token": token,
		"call_url":   "/call/" + token,
		"expires_at": expiresAt,
	}, nil
}

// JoinCallByLink allows a user to join a call using a shareable link token.
func (s *Service) JoinCallByLink(ctx context.Context, userID uuid.UUID, token string) (*JoinResponse, error) {
	session, err := s.store.GetCallSessionByLinkToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrLinkNotFound
	}

	return s.JoinCall(ctx, userID, session.ID)
}

// ---------------------------------------------------------------------------
// Scheduled Calls
// ---------------------------------------------------------------------------

// ScheduleCall creates a new call that is scheduled for a future time.
func (s *Service) ScheduleCall(ctx context.Context, userID uuid.UUID, callType, sourceType, sourceID string, scheduledAt time.Time, inviteUserIDs []uuid.UUID) (*CallResponse, error) {
	var srcID *uuid.UUID
	if sourceID != "" {
		id, err := uuid.Parse(sourceID)
		if err != nil {
			return nil, fmt.Errorf("invalid source_id: %w", err)
		}
		srcID = &id
	}

	resp, err := s.CreateCall(ctx, userID, CreateCallRequest{
		CallType:      callType,
		SourceType:    sourceType,
		SourceID:      srcID,
		TargetUserIDs: inviteUserIDs,
	})
	if err != nil {
		return nil, err
	}

	if err := s.store.SetCallScheduledAt(ctx, resp.ID, scheduledAt); err != nil {
		s.log.Warn("failed to set scheduled_at", "err", err, "call_id", resp.ID)
	}

	return resp, nil
}

// ListScheduledCalls returns upcoming scheduled calls for the given user.
func (s *Service) ListScheduledCalls(ctx context.Context, userID uuid.UUID, limit int) ([]CallResponse, error) {
	sessions, err := s.store.ListScheduledCalls(ctx, userID, time.Now(), limit)
	if err != nil {
		return nil, err
	}

	responses := make([]CallResponse, 0, len(sessions))
	for _, cs := range sessions {
		participants, _ := s.store.GetParticipants(ctx, cs.ID)
		partResponses := make([]ParticipantResponse, 0, len(participants))
		for _, p := range participants {
			partResponses = append(partResponses, toParticipantResponse(p))
		}
		responses = append(responses, CallResponse{
			ID:              cs.ID,
			CallType:        cs.CallType,
			SourceType:      cs.SourceType,
			SourceID:        cs.SourceID,
			InitiatorUserID: cs.InitiatorUserID,
			State:           cs.State,
			AudioOnly:       cs.AudioOnly,
			MaxParticipants: cs.MaxParticipants,
			JoinMode:        cs.JoinMode,
			Participants:    partResponses,
			StartedAt:       cs.StartedAt,
			AnsweredAt:      cs.AnsweredAt,
			EndedAt:         cs.EndedAt,
			EndedReason:     cs.EndedReason,
			CreatedAt:       cs.CreatedAt,
		})
	}

	return responses, nil
}

// ---------------------------------------------------------------------------
// Reminders
// ---------------------------------------------------------------------------

// SetCallReminder creates or updates a reminder for a user on a call.
func (s *Service) SetCallReminder(ctx context.Context, userID, callSessionID uuid.UUID, remindAt time.Time) (*postgres.CallReminder, error) {
	session, err := s.store.GetCallSession(ctx, callSessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}

	r := &postgres.CallReminder{
		CallSessionID: callSessionID,
		UserID:        userID,
		RemindAt:      remindAt,
	}
	return s.store.CreateCallReminder(ctx, r)
}

// DeleteCallReminder removes a reminder set by the user for a call.
func (s *Service) DeleteCallReminder(ctx context.Context, userID, callSessionID uuid.UUID) error {
	session, err := s.store.GetCallSession(ctx, callSessionID)
	if err != nil {
		return err
	}
	if session == nil {
		return ErrCallNotFound
	}
	return s.store.DeleteCallReminder(ctx, callSessionID, userID)
}

// ---------------------------------------------------------------------------
// Summaries
// ---------------------------------------------------------------------------

// GetCallSummary returns the AI summary for a completed call.
// The requesting user must be a participant.
func (s *Service) GetCallSummary(ctx context.Context, userID, callSessionID uuid.UUID) (*postgres.CallSummary, error) {
	session, err := s.store.GetCallSession(ctx, callSessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}

	participant, err := s.store.GetParticipant(ctx, callSessionID, userID)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, ErrNotParticipant
	}

	summary, err := s.store.GetCallSummary(ctx, callSessionID)
	if err != nil {
		return nil, err
	}
	if summary == nil {
		return nil, ErrSummaryNotFound
	}
	return summary, nil
}

// CreateCallSummary stores an AI-generated summary (called by an AI pipeline).
func (s *Service) CreateCallSummary(ctx context.Context, sum *postgres.CallSummary) (*postgres.CallSummary, error) {
	session, err := s.store.GetCallSession(ctx, sum.CallSessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}
	return s.store.UpsertCallSummary(ctx, sum)
}
