// Past matches (mechanic M19, DATING_PAST_MATCH_REPORT_ENABLED).
//
// GET /v1/dating/past-matches lists the caller's matches that ended in the
// last PastMatchWindow — unmatched, expired or blocked — with the other
// person's first name, so they can still be reported through the usual
// POST /v1/dating/safety/report {target_id}. Which side ended the match is
// not said.
package service

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PastMatchWindow is how far back the list reaches.
const PastMatchWindow = 30 * 24 * time.Hour

// PastMatchPerson is the other person on a past match: their id (the report
// target) and first name, nothing else.
type PastMatchPerson struct {
	UserID    uuid.UUID `json:"user_id"`
	FirstName string    `json:"first_name,omitempty"`
}

// PastMatchItem is one row of GET /past-matches.
type PastMatchItem struct {
	MatchID   uuid.UUID       `json:"match_id"`
	Person    PastMatchPerson `json:"person"`
	MatchedAt time.Time       `json:"matched_at"`
	EndedAt   time.Time       `json:"ended_at"`
	// How it ended: unmatched, blocked, expired or closed.
	Ended    string `json:"ended"`
	Reported bool   `json:"reported"`
}

// PastMatchesMeta is the meta of GET /past-matches.
type PastMatchesMeta struct {
	WindowDays int `json:"window_days"`
}

// PastMatchesResponse is GET /past-matches.
type PastMatchesResponse struct {
	Data []PastMatchItem `json:"data"`
	Meta PastMatchesMeta `json:"meta"`
}

func endedHow(status string, reason *string) string {
	if status == "expired" {
		return "expired"
	}
	if reason != nil {
		switch *reason {
		case "unmatch":
			return "unmatched"
		case "block":
			return "blocked"
		}
	}
	return "closed"
}

// PastMatches lists the caller's recently ended matches.
func (s *Service) PastMatches(ctx context.Context, userID uuid.UUID) (*PastMatchesResponse, error) {
	if !s.mechanics.PastMatchReport {
		return nil, ErrMechanicDisabled
	}
	rows, err := s.store.PastMatches(ctx, userID, time.Now().Add(-PastMatchWindow), 50)
	if err != nil {
		return nil, err
	}
	out := &PastMatchesResponse{Data: make([]PastMatchItem, 0, len(rows)), Meta: PastMatchesMeta{WindowDays: int(PastMatchWindow.Hours() / 24)}}
	for _, r := range rows {
		item := PastMatchItem{
			MatchID: r.MatchID, Person: PastMatchPerson{UserID: r.OtherID},
			MatchedAt: r.MatchedAt.UTC(), EndedAt: r.ClosedAt.UTC(), Ended: endedHow(r.Status, r.CloseReason), Reported: r.Reported,
		}
		if r.FirstName != nil {
			item.Person.FirstName = *r.FirstName
		}
		out.Data = append(out.Data, item)
	}
	return out, nil
}
