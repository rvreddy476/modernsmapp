// First move (mechanic M5, DATING_FIRST_MOVE_ENABLED).
//
// A per-user opt-in. When a match forms, everyone in it who opted in becomes
// a "first mover": until the first message, only a first mover may write
// (chat-service enforces it, from the list sent when the conversation is
// created), and the match expires after FirstMoveWindow instead of the usual
// seven days. A pair where nobody opted in is unchanged.
//
// The waiting person has two things: they may answer one of the first
// mover's opening questions (up to MaxOpeningQuestions, written by the first
// mover), which becomes the conversation's first message, and they may
// extend the match by FreeExtendBy once per rolling 24 hours, free.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/dating-service/internal/moderation"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// First move limits.
const (
	MaxOpeningQuestions   = 3
	MaxOpeningQuestionLen = 140
	MaxOpeningAnswerLen   = 500
	FirstMoveWindow       = 24 * time.Hour
	FreeExtendBy          = 24 * time.Hour
	FreeExtendsPerWindow  = 1
)

var (
	ErrOpeningQuestionsTooMany = fmt.Errorf("invalid: at most %d opening questions", MaxOpeningQuestions)
	ErrOpeningQuestionInvalid  = fmt.Errorf("invalid: an opening question is 1 to %d characters", MaxOpeningQuestionLen)
	ErrOpeningQuestionRefused  = errors.New("invalid: opening questions cannot contain phone numbers, email addresses or links")
	// ErrFirstMoveNotPending: the match is not waiting for an opening answer
	// from the caller (no rule, a first mover, already talking, expired).
	ErrFirstMoveNotPending = errors.New("conflict: this match is not waiting for an opening answer from you")
	// ErrOpeningQuestionUnknown: not a live question of this match's first mover.
	ErrOpeningQuestionUnknown = errors.New("not_found: opening question not found")
	ErrOpeningAnswerInvalid   = fmt.Errorf("invalid: an answer is 1 to %d characters", MaxOpeningAnswerLen)
	ErrOpeningAnswerRefused   = errors.New("invalid: answers cannot contain phone numbers, email addresses or links")
	// ErrChatUnavailable: chat-service could not take the message right now.
	ErrChatUnavailable = errors.New("chat is unavailable; try again shortly")
)

// ExtendLimitError is the spent free extend. Maps to 429 EXTEND_LIMIT_REACHED.
type ExtendLimitError struct {
	Limit    int
	ResetsAt *time.Time
}

func (e *ExtendLimitError) Error() string { return "you have used your free extend; try again later" }

// FirstMoveSettings is GET/PUT /v1/dating/first-move.
type FirstMoveSettings struct {
	Enabled      bool                    `json:"enabled"`
	Questions    []store.OpeningQuestion `json:"questions"`
	MaxQuestions int                     `json:"max_questions"`
	MaxLength    int                     `json:"max_length"`
}

// FirstMoveView is the "first_move" member of a match the caller sees while
// the match waits for its first message under the rule.
type FirstMoveView struct {
	// YouMoveFirst: the caller may write; otherwise they wait.
	YouMoveFirst bool       `json:"you_move_first"`
	Deadline     *time.Time `json:"deadline,omitempty"`
	// OpeningQuestions: the first mover's questions, for the waiting person.
	OpeningQuestions []store.OpeningQuestion `json:"opening_questions,omitempty"`
	// CanExtend: the waiting person has their free extend available.
	CanExtend bool `json:"can_extend"`
}

// OpeningAnswerResult is POST /v1/dating/matches/:id/opening-answer.
type OpeningAnswerResult struct {
	Sent           bool       `json:"sent"`
	ConversationID *uuid.UUID `json:"conversation_id,omitempty"`
}

// ExtendResult is POST /v1/dating/matches/:id/extend.
type ExtendResult struct {
	Extended   bool       `json:"extended"`
	ExtraDays  int        `json:"extra_days,omitempty"`
	ExtraHours int        `json:"extra_hours"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// Free: the free 24-hour extend of a first-move match.
	Free bool `json:"free,omitempty"`
}

// OpeningAnswerSender is the chat call that posts a validated opening
// answer. The HTTP message client implements it.
type OpeningAnswerSender interface {
	SendOpeningAnswer(ctx context.Context, matchID, senderID uuid.UUID, text, idempotencyKey string) (*CreateConversationResponse, error)
}

// ErrOpeningAnswerRefusedByChat: chat refused the answer (409).
var ErrOpeningAnswerRefusedByChat = errors.New("chat refused the opening answer")

func checkFreeText(text string, refused error) error {
	verdict := moderation.ScanMessage(text)
	for _, p := range verdict.Patterns {
		switch p {
		case moderation.PatternPhone, moderation.PatternEmail, moderation.PatternURL:
			return refused
		}
	}
	if verdict.ActionTaken == "block" {
		return refused
	}
	return nil
}

func moverIn(movers []uuid.UUID, id uuid.UUID) bool {
	for _, m := range movers {
		if m == id {
			return true
		}
	}
	return false
}

// GetFirstMove returns the caller's setting and questions.
func (s *Service) GetFirstMove(ctx context.Context, userID uuid.UUID) (*FirstMoveSettings, error) {
	if !s.mechanics.FirstMove {
		return nil, ErrMechanicDisabled
	}
	st, err := s.store.GetFirstMoveSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &FirstMoveSettings{Enabled: st.Enabled, Questions: st.Questions, MaxQuestions: MaxOpeningQuestions, MaxLength: MaxOpeningQuestionLen}, nil
}

// PutFirstMove sets the opt-in and replaces the questions. questions nil
// leaves them as they are; an empty list removes them.
func (s *Service) PutFirstMove(ctx context.Context, userID uuid.UUID, enabled *bool, questions []string) (*FirstMoveSettings, error) {
	if !s.mechanics.FirstMove {
		return nil, ErrMechanicDisabled
	}
	var clean []string
	if questions != nil {
		if len(questions) > MaxOpeningQuestions {
			return nil, ErrOpeningQuestionsTooMany
		}
		clean = make([]string, 0, len(questions))
		for _, q := range questions {
			q = strings.TrimSpace(q)
			if q == "" || utf8.RuneCountInString(q) > MaxOpeningQuestionLen {
				return nil, ErrOpeningQuestionInvalid
			}
			if err := checkFreeText(q, ErrOpeningQuestionRefused); err != nil {
				return nil, err
			}
			clean = append(clean, q)
		}
	}
	if err := s.store.SetFirstMoveSettings(ctx, userID, enabled, clean); err != nil {
		return nil, err
	}
	return s.GetFirstMove(ctx, userID)
}

// firstMoversFor decides who moves first in a new match: everyone in the
// pair who opted in, while the mechanic is on. A failed lookup means no rule
// (the match behaves as before) rather than a failed match.
func (s *Service) firstMoversFor(ctx context.Context, a, b uuid.UUID) []uuid.UUID {
	if !s.mechanics.FirstMove {
		return nil
	}
	opted, err := s.store.FirstMoveOptedIn(ctx, []uuid.UUID{a, b})
	if err != nil {
		slog.Warn("first move: opt-in lookup failed; no first-move rule for this match", "error", err)
		return nil
	}
	var out []uuid.UUID
	for _, id := range []uuid.UUID{a, b} {
		if opted[id] {
			out = append(out, id)
		}
	}
	return out
}

// firstMovePending reports whether m is waiting for its first message under
// the rule.
func firstMovePending(m *store.Match) bool {
	return m != nil && len(m.FirstMoverIDs) > 0 && m.FirstMessageAt == nil && m.Status == "matched"
}

// firstMoveView builds the caller's view of a pending first-move match, or
// nil when there is nothing to show.
func (s *Service) firstMoveView(ctx context.Context, m *store.Match, viewerID uuid.UUID) *FirstMoveView {
	if !firstMovePending(m) {
		return nil
	}
	v := &FirstMoveView{YouMoveFirst: moverIn(m.FirstMoverIDs, viewerID), Deadline: m.ExpiresAt}
	if v.YouMoveFirst {
		return v
	}
	if qs, err := s.store.LiveOpeningQuestions(ctx, m.FirstMoverIDs); err == nil {
		for _, mover := range m.FirstMoverIDs {
			v.OpeningQuestions = append(v.OpeningQuestions, qs[mover]...)
		}
	} else {
		slog.Warn("first move: opening questions lookup failed", "match_id", m.ID, "error", err)
	}
	if used, _, err := s.store.FreeExtendUsage(ctx, viewerID); err == nil {
		v.CanExtend = used < FreeExtendsPerWindow
	}
	return v
}

// SendOpeningAnswer posts the waiting person's answer to one of the first
// mover's opening questions as the match's first message.
func (s *Service) SendOpeningAnswer(ctx context.Context, matchID, callerID, questionID uuid.UUID, answer string) (*OpeningAnswerResult, error) {
	if !s.mechanics.FirstMove {
		return nil, ErrMechanicDisabled
	}
	m, err := s.GetMatchForUser(ctx, matchID, callerID)
	if err != nil {
		return nil, err
	}
	if !firstMovePending(m) || moverIn(m.FirstMoverIDs, callerID) || (m.ExpiresAt != nil && !m.ExpiresAt.After(time.Now())) {
		return nil, ErrFirstMoveNotPending
	}
	q, err := s.store.GetLiveOpeningQuestion(ctx, questionID)
	if err != nil {
		if errors.Is(err, store.ErrOpeningQuestionNotFound) {
			return nil, ErrOpeningQuestionUnknown
		}
		return nil, err
	}
	if !moverIn(m.FirstMoverIDs, q.UserID) {
		return nil, ErrOpeningQuestionUnknown
	}
	answer = strings.TrimSpace(answer)
	if answer == "" || utf8.RuneCountInString(answer) > MaxOpeningAnswerLen {
		return nil, ErrOpeningAnswerInvalid
	}
	if err := checkFreeText(answer, ErrOpeningAnswerRefused); err != nil {
		return nil, err
	}
	sender, ok := s.msgClient.(OpeningAnswerSender)
	if !ok || s.msgClient == nil {
		return nil, ErrChatUnavailable
	}
	text := "“" + q.Text + "”\n" + answer
	// One answer per person per match: a retry is the same message.
	key := "dating-opening-answer:" + matchID.String() + ":" + callerID.String()
	if _, err := sender.SendOpeningAnswer(ctx, matchID, callerID, text, key); err != nil {
		if errors.Is(err, ErrOpeningAnswerRefusedByChat) {
			return nil, ErrFirstMoveNotPending
		}
		slog.Warn("first move: chat refused or failed the opening answer", "match_id", matchID, "error", err)
		return nil, ErrChatUnavailable
	}
	return &OpeningAnswerResult{Sent: true, ConversationID: m.ConversationID}, nil
}
