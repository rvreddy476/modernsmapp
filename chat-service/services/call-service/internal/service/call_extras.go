package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrLinkNotFound is returned when a call link token is invalid or expired.
var ErrLinkNotFound = errors.New("call link not found or expired")

// ErrSummaryNotFound is returned when no summary exists for a call.
var ErrSummaryNotFound = errors.New("call summary not found")

// ErrLobbyNotSupported refuses lobby_enabled=true (audit L3). Nothing
// enforces a lobby: a joiner by link is admitted directly. Accepting the flag
// would tell the host their call is gated when it is not.
var ErrLobbyNotSupported = errors.New("call lobbies are not supported")

// extrasStore is the slice of postgres.CallStore the extras use. It exists so
// the authorization rules here can be unit-tested without a database.
type extrasStore interface {
	GetCallSession(ctx context.Context, id uuid.UUID) (*domain.CallSession, error)
	GetParticipant(ctx context.Context, callID, userID uuid.UUID) (*domain.CallParticipant, error)
	CreateCallLink(ctx context.Context, callSessionID uuid.UUID, token string, expiresAt time.Time) (string, error)
	CreateCallReminder(ctx context.Context, r *postgres.CallReminder) (*postgres.CallReminder, error)
	DeleteCallReminder(ctx context.Context, callSessionID, userID uuid.UUID) error
	GetCallSummary(ctx context.Context, callSessionID uuid.UUID) (*postgres.CallSummary, error)
	UpsertCallSummary(ctx context.Context, sum *postgres.CallSummary) (*postgres.CallSummary, error)
}

// callLinkTokenBytes is the link token's entropy: 128 bits from crypto/rand
// (audit L3; the old token was 8 hex characters of a UUID, 32 bits).
const callLinkTokenBytes = 16

// callLinkTokenLen is the encoded length (unpadded base64url of 16 bytes).
var callLinkTokenLen = base64.RawURLEncoding.EncodedLen(callLinkTokenBytes)

// newCallLinkToken mints an unguessable, URL-safe link token.
func newCallLinkToken() (string, error) {
	buf := make([]byte, callLinkTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("call link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// wellFormedCallLinkToken reports whether token has the current format. A
// legacy 8-character token, or anything else, is refused before the database
// or the rate limiter is touched.
func wellFormedCallLinkToken(token string) bool {
	if len(token) != callLinkTokenLen {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == callLinkTokenBytes
}

// ---------------------------------------------------------------------------
// Call Links
// ---------------------------------------------------------------------------

// GenerateCallLink creates a shareable link for a call session.
// Only the call initiator may generate a link.
func (s *Service) GenerateCallLink(ctx context.Context, userID, callSessionID uuid.UUID, expiresInHours int, lobbyEnabled bool) (map[string]any, error) {
	if lobbyEnabled {
		return nil, ErrLobbyNotSupported
	}
	session, err := s.extras.GetCallSession(ctx, callSessionID)
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

	minted, err := newCallLinkToken()
	if err != nil {
		return nil, err
	}
	token, err := s.extras.CreateCallLink(ctx, callSessionID, minted, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("create call link: %w", err)
	}
	if token == "" {
		return nil, ErrCallNotFound
	}

	return map[string]any{
		"link_token": token,
		"call_url":   "/call/" + token,
		"expires_at": expiresAt,
	}, nil
}

// JoinCallByLink allows a user to join a call using a shareable link token.
func (s *Service) JoinCallByLink(ctx context.Context, userID uuid.UUID, token string) (*JoinResponse, error) {
	if !wellFormedCallLinkToken(token) {
		return nil, ErrLinkNotFound
	}
	// Per-user lookup limit (audit L3): CheckJoinRate is keyed per call, so
	// it never slowed a caller guessing tokens across calls.
	if err := s.rateLimiter.CheckLinkLookupRate(ctx, userID); err != nil {
		return nil, err
	}
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

// SetCallReminder creates or updates a reminder for a user on a call. Only a
// participant may set one (audit L4): any call id used to be accepted, so
// anyone could pile rows onto any call.
func (s *Service) SetCallReminder(ctx context.Context, userID, callSessionID uuid.UUID, remindAt time.Time) (*postgres.CallReminder, error) {
	session, err := s.extras.GetCallSession(ctx, callSessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}
	participant, err := s.extras.GetParticipant(ctx, callSessionID, userID)
	if err != nil {
		return nil, err
	}
	// A participant who was removed is no longer in the call.
	if participant == nil || participant.JoinState == domain.JoinStateRemoved {
		return nil, ErrNotParticipant
	}

	r := &postgres.CallReminder{
		CallSessionID: callSessionID,
		UserID:        userID,
		RemindAt:      remindAt,
	}
	return s.extras.CreateCallReminder(ctx, r)
}

// DeleteCallReminder removes a reminder set by the user for a call.
func (s *Service) DeleteCallReminder(ctx context.Context, userID, callSessionID uuid.UUID) error {
	session, err := s.extras.GetCallSession(ctx, callSessionID)
	if err != nil {
		return err
	}
	if session == nil {
		return ErrCallNotFound
	}
	// Deletes only the caller's own row, so no participant check is needed.
	return s.extras.DeleteCallReminder(ctx, callSessionID, userID)
}

// ---------------------------------------------------------------------------
// Summaries
// ---------------------------------------------------------------------------

// GetCallSummary returns the AI summary for a completed call.
// The requesting user must be a participant.
func (s *Service) GetCallSummary(ctx context.Context, userID, callSessionID uuid.UUID) (*postgres.CallSummary, error) {
	session, err := s.extras.GetCallSession(ctx, callSessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}

	participant, err := s.extras.GetParticipant(ctx, callSessionID, userID)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, ErrNotParticipant
	}

	summary, err := s.extras.GetCallSummary(ctx, callSessionID)
	if err != nil {
		return nil, err
	}
	if summary == nil {
		return nil, ErrSummaryNotFound
	}
	return summary, nil
}

// CreateCallSummary stores an AI-generated summary. Reached only through
// POST /internal/v1/calls/:callId/summary, which requires the internal
// service key (audit H1); no user-facing route writes summaries.
func (s *Service) CreateCallSummary(ctx context.Context, sum *postgres.CallSummary) (*postgres.CallSummary, error) {
	session, err := s.extras.GetCallSession(ctx, sum.CallSessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrCallNotFound
	}
	return s.extras.UpsertCallSummary(ctx, sum)
}
