// After-date check-ins (mechanic M14, DATING_DATE_CHECKIN_ENABLED).
//
// A few hours after a planned safe meet, both people are asked how it went
// (dating.date_checkin.due through notification-service). Anyone may also
// answer for a match of theirs unasked, from the match screen:
//
//	POST /v1/dating/matches/:id/date-feedback {met, again?, felt_safe?}
//	GET  /v1/dating/date-checkins   the asks still waiting for an answer
//
// met is yes, no or not_yet; again (yes, no, unsure) and felt_safe only make
// sense after meeting. felt_safe=false is recorded as a safety event and the
// answer offers the report flow; nothing is decided automatically.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

const (
	// DateCheckinAfter is how long after the planned time the ask goes out.
	DateCheckinAfter = 3 * time.Hour
	// DateCheckinWindow is how long a meet stays askable, and an ask open.
	DateCheckinWindow = 7 * 24 * time.Hour
	// maxDateFeedbackPerMatch caps one person's answers about one match.
	maxDateFeedbackPerMatch = 5
)

var (
	// ErrInvalidDateFeedback maps to 400 INVALID_DATE_FEEDBACK.
	ErrInvalidDateFeedback = refuseField("INVALID_DATE_FEEDBACK", "met",
		"met must be yes, no or not_yet; again must be yes, no or unsure and, like felt_safe, only follows met=yes",
		map[string]any{"met": []string{"yes", "no", "not_yet"}, "again": []string{"yes", "no", "unsure"}})
	// ErrDateFeedbackLimit maps to 429 DATE_FEEDBACK_LIMIT.
	ErrDateFeedbackLimit = errors.New("rate_limited: enough answers about this match")
)

// DateFeedbackInput is the POST body.
type DateFeedbackInput struct {
	Met      string  `json:"met"`
	Again    *string `json:"again,omitempty"`
	FeltSafe *bool   `json:"felt_safe,omitempty"`
}

// DateFeedbackView is the POST answer.
type DateFeedbackView struct {
	MatchID   uuid.UUID `json:"match_id"`
	Met       string    `json:"met"`
	Again     *string   `json:"again,omitempty"`
	FeltSafe  *bool     `json:"felt_safe,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// OfferReport: the person did not feel safe; the app offers to report.
	OfferReport bool `json:"offer_report"`
}

// DateCheckinItem is one row of GET /date-checkins.
type DateCheckinItem struct {
	MatchID uuid.UUID       `json:"match_id"`
	MeetID  uuid.UUID       `json:"meet_id"`
	Person  PastMatchPerson `json:"person"`
	AskedAt time.Time       `json:"asked_at"`
}

func validDateFeedback(in DateFeedbackInput) bool {
	switch in.Met {
	case "yes":
	case "no", "not_yet":
		if in.Again != nil || in.FeltSafe != nil {
			return false
		}
	default:
		return false
	}
	if in.Again != nil {
		switch *in.Again {
		case "yes", "no", "unsure":
		default:
			return false
		}
	}
	return true
}

// PostDateFeedback records the caller's answer about one of their matches.
func (s *Service) PostDateFeedback(ctx context.Context, userID, matchID uuid.UUID, in DateFeedbackInput) (*DateFeedbackView, error) {
	if !s.mechanics.DateCheckin {
		return nil, ErrMechanicDisabled
	}
	if !validDateFeedback(in) {
		return nil, ErrInvalidDateFeedback
	}
	m, err := s.store.GetMatch(ctx, matchID)
	if err != nil || m == nil || (m.UserA != userID && m.UserB != userID) {
		// Not a participant reads as not found. A blocked pair may still
		// answer: "I did not feel safe" often comes with a block.
		return nil, store.ErrMatchNotFound
	}
	other := m.UserA
	if other == userID {
		other = m.UserB
	}
	n, err := s.store.DateFeedbackCount(ctx, matchID, userID)
	if err != nil {
		return nil, err
	}
	if n >= maxDateFeedbackPerMatch {
		return nil, ErrDateFeedbackLimit
	}
	f, err := s.store.RecordDateFeedback(ctx, store.DateFeedback{MatchID: matchID, UserID: userID, OtherID: other, Met: in.Met, Again: in.Again, FeltSafe: in.FeltSafe})
	if err != nil {
		return nil, err
	}
	unsafe := in.FeltSafe != nil && !*in.FeltSafe
	if unsafe {
		if err := s.store.RecordSafetyEvent(ctx, userID, "date_felt_unsafe", map[string]any{"match_id": matchID.String(), "other_user_id": other.String()}); err != nil {
			slog.Error("date check-in: safety event not recorded", "match_id", matchID, "error", err)
		}
	}
	return &DateFeedbackView{MatchID: matchID, Met: f.Met, Again: f.Again, FeltSafe: f.FeltSafe, CreatedAt: f.CreatedAt.UTC(), OfferReport: unsafe}, nil
}

// DateCheckins lists the caller's unanswered check-ins.
func (s *Service) DateCheckins(ctx context.Context, userID uuid.UUID) ([]DateCheckinItem, error) {
	if !s.mechanics.DateCheckin {
		return nil, ErrMechanicDisabled
	}
	rows, err := s.store.PendingDateCheckins(ctx, userID, DateCheckinWindow)
	if err != nil {
		return nil, err
	}
	out := make([]DateCheckinItem, 0, len(rows))
	for _, r := range rows {
		item := DateCheckinItem{MatchID: r.MatchID, MeetID: r.MeetID, Person: PastMatchPerson{UserID: r.OtherID}, AskedAt: r.AskedAt.UTC()}
		if r.FirstName != nil {
			item.Person.FirstName = *r.FirstName
		}
		out = append(out, item)
	}
	return out, nil
}

// SendDueDateCheckins claims the meets whose check-in is due and asks both
// people. Without a producer it claims nothing.
func (s *Service) SendDueDateCheckins(ctx context.Context, limit int) (int, error) {
	if !s.mechanics.DateCheckin || s.producer == nil {
		return 0, nil
	}
	meets, err := s.store.ClaimMeetsForDateCheckin(ctx, DateCheckinAfter, DateCheckinWindow, limit)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, mt := range meets {
		m, err := s.store.GetMatchByUsers(ctx, mt.UserID, mt.WithUserID)
		if err != nil || m == nil {
			continue
		}
		for _, pair := range [][2]uuid.UUID{{mt.UserID, mt.WithUserID}, {mt.WithUserID, mt.UserID}} {
			name := ""
			if p, perr := s.store.GetProfile(ctx, pair[1]); perr == nil && p != nil && p.FirstName != nil {
				name = *p.FirstName
			}
			if perr := s.producer.PublishDateCheckinDue(ctx, pair[0], m.ID, mt.MeetID, name, mt.ScheduledAt.Add(DateCheckinAfter)); perr != nil {
				slog.Warn("date check-in: publish failed", "meet_id", mt.MeetID, "error", perr)
				continue
			}
			sent++
		}
	}
	return sent, nil
}
