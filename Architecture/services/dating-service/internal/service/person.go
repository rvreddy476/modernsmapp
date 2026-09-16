// Compact person cards (lane D10).
//
// A match, an incoming spark and the person card all render the same small
// object: who this is (first name, age), one photo the viewer is allowed to
// see, and whether they carry a verified badge. It never carries religion,
// community, an exact location, last-active when the person hides it, or any
// D9-sealed field; the distance is the lane D7 bucket only.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// PersonCard is the compact person object.
type PersonCard struct {
	UserID    uuid.UUID `json:"user_id"`
	FirstName string    `json:"first_name"`
	Age       int       `json:"age"`
	// PrimaryPhotoID is the dating photo id (never a media id);
	// PrimaryPhotoURL is the image route for the variant this viewer may
	// have. Both are omitted when the person has no approved primary photo.
	PrimaryPhotoID  *uuid.UUID `json:"primary_photo_id,omitempty"`
	PrimaryPhotoURL string     `json:"primary_photo_url,omitempty"`
	// PhotoState is "full" or "blurred" — the lane D6 rule
	// (PhotoVariantFor): the full image only for the owner, an open match,
	// or a public photo the owner does not blur until match.
	PhotoState string `json:"photo_state"`
	// Verified is the badge: a selfie- or Aadhaar-verified trust tier.
	Verified  bool   `json:"verified"`
	TrustTier string `json:"trust_tier,omitempty"`
	// DistanceBucket / DistanceLabel are the lane D7 bucket on the snapped
	// points, present only when both sides have a location. Never a number.
	DistanceBucket string `json:"distance_bucket,omitempty"`
	DistanceLabel  string `json:"distance_label,omitempty"`
}

// verifiedTier reports whether a trust tier carries the verified badge.
func verifiedTier(tier string) bool {
	return tier == "selfie" || tier == "aadhaar"
}

// buildPersonCard renders one row for one viewer. matched says whether the
// viewer currently has an open match with this person, and viewer is the
// viewer's own profile (nil = no distance bucket).
func buildPersonCard(row *store.PersonRow, matched bool, viewer *store.Profile) *PersonCard {
	if row == nil {
		return nil
	}
	first := ""
	if row.FirstName != nil {
		first = *row.FirstName
	}
	variant := PhotoVariantFor(row.PrimaryPhotoVisibility, PhotoViewer{
		Matched:              matched,
		OwnerSparkedViewer:   row.SparkedViewer,
		OwnerBlursUntilMatch: row.BlurPhotosUntilMatch || row.BlurMode,
	})
	card := &PersonCard{
		UserID:     row.UserID,
		FirstName:  first,
		Age:        row.Age(),
		PhotoState: variant,
		Verified:   verifiedTier(row.TrustTier),
		TrustTier:  row.TrustTier,
	}
	if row.PrimaryPhotoID != nil {
		id := *row.PrimaryPhotoID
		card.PrimaryPhotoID = &id
		card.PrimaryPhotoURL = PhotoImagePath(id, variant)
	}
	if viewer != nil {
		if band, ok := store.DistanceBandBetween(viewer.Latitude, viewer.Longitude, row.Latitude, row.Longitude); ok {
			card.DistanceBucket, card.DistanceLabel = band.Code, band.Label
		}
	}
	return card
}

// personCards builds the cards for a set of user ids as one viewer sees
// them. Ids the viewer may not see (blocked either way, deleted, suspended)
// are absent from the result. Best effort: a lookup error yields no cards
// rather than failing the list the cards decorate.
func (s *Service) personCards(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) map[uuid.UUID]*PersonCard {
	out := map[uuid.UUID]*PersonCard{}
	if len(ids) == 0 {
		return out
	}
	rows, err := s.store.ListPeopleForViewer(ctx, viewerID, ids)
	if err != nil {
		slog.Warn("person cards lookup failed", "viewer_id", viewerID, "error", err)
		return out
	}
	matched, mErr := s.store.ListActiveMatchPartnerIDs(ctx, viewerID)
	if mErr != nil {
		slog.Warn("person cards matched-partners lookup failed", "viewer_id", viewerID, "error", mErr)
		matched = map[uuid.UUID]struct{}{}
	}
	viewer, _ := s.store.GetProfile(ctx, viewerID)
	for id, row := range rows {
		_, isMatched := matched[id]
		out[id] = buildPersonCard(row, isMatched, viewer)
	}
	return out
}

// ErrPersonNotVisible is the one refusal for a person card the viewer has no
// relationship with, has blocked (either way), or who is gone. Maps to 404
// CANDIDATE_UNAVAILABLE, exactly like every other lane D3 refusal, so it
// never reveals a block.
var ErrPersonNotVisible = ErrCandidateUnavailable

// canViewPerson is THE access rule for the person card: the viewer must have
// a current match with them, a live incoming spark from them, or have them
// in their current deck. Blocks are enforced by the person query itself
// (both directions) and by each of these three checks.
func (s *Service) canViewPerson(ctx context.Context, viewerID, targetID uuid.UUID) (bool, error) {
	if viewerID == uuid.Nil || targetID == uuid.Nil || viewerID == targetID {
		return false, nil
	}
	if blocked, err := s.store.IsBlockedEitherWay(ctx, viewerID, targetID); err != nil {
		return false, err
	} else if blocked {
		return false, nil
	}
	matched, err := s.store.HasOpenMatch(ctx, viewerID, targetID)
	if err != nil {
		return false, err
	}
	if matched {
		return true, nil
	}
	sparked, err := s.store.HasIncomingSparkFrom(ctx, viewerID, targetID)
	if err != nil {
		return false, err
	}
	if sparked {
		return true, nil
	}
	return s.inCurrentDeck(ctx, viewerID, targetID)
}

// inCurrentDeck reports whether targetID sits in the viewer's current deck.
// The cached deck answers when there is one; otherwise today's deck is
// computed (and cached) exactly as GET /pulse/today would.
func (s *Service) inCurrentDeck(ctx context.Context, viewerID, targetID uuid.UUID) (bool, error) {
	deck := s.readPulseCache(ctx, viewerID)
	if deck == nil {
		computed, err := s.GetPulseToday(ctx, viewerID)
		if err != nil {
			return false, err
		}
		deck = computed
	}
	for _, card := range deck.Data {
		if card.CandidateID == targetID {
			return true, nil
		}
	}
	return false, nil
}

// GetPersonCard returns the compact card for one person, or
// ErrCandidateUnavailable when the viewer has no current match with them, no
// incoming spark from them and they are not in the viewer's deck.
func (s *Service) GetPersonCard(ctx context.Context, viewerID, targetID uuid.UUID) (*PersonCard, error) {
	if viewerID == uuid.Nil || targetID == uuid.Nil {
		return nil, fmt.Errorf("invalid: viewer and user ids required")
	}
	if viewerID == targetID {
		return nil, ErrPersonNotVisible
	}
	// The viewer must themselves be a verified adult with a profile, the
	// same bar the deck and sparks apply.
	if err := s.requireAdult(ctx, viewerID); err != nil {
		return nil, err
	}
	allowed, err := s.canViewPerson(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrPersonNotVisible
	}
	row, err := s.store.GetPersonForViewer(ctx, viewerID, targetID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			return nil, ErrPersonNotVisible
		}
		return nil, err
	}
	matched, err := s.store.HasOpenMatch(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	viewer, _ := s.store.GetProfile(ctx, viewerID)
	return buildPersonCard(row, matched, viewer), nil
}

// MatchWithPerson is a match plus the other participant's compact card. The
// match's own fields are inlined, so the shape is the one clients already
// parse with one added "person" member.
type MatchWithPerson struct {
	*store.Match
	Person *PersonCard `json:"person,omitempty"`
}

// SparkWithPerson is an incoming spark plus the sender's compact card.
type SparkWithPerson struct {
	*store.Spark
	Person *PersonCard `json:"person,omitempty"`
}

// otherParticipant returns the match participant who is not userID.
func otherParticipant(m *store.Match, userID uuid.UUID) uuid.UUID {
	if m == nil {
		return uuid.Nil
	}
	if m.UserA == userID {
		return m.UserB
	}
	return m.UserA
}

// decorateMatches attaches the other participant's card to each match.
func (s *Service) decorateMatches(ctx context.Context, viewerID uuid.UUID, matches []*store.Match) []*MatchWithPerson {
	ids := make([]uuid.UUID, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, otherParticipant(m, viewerID))
	}
	cards := s.personCards(ctx, viewerID, ids)
	out := make([]*MatchWithPerson, 0, len(matches))
	for _, m := range matches {
		out = append(out, &MatchWithPerson{Match: m, Person: cards[otherParticipant(m, viewerID)]})
	}
	return out
}

// decorateIncomingSparks attaches each sender's card to their spark.
func (s *Service) decorateIncomingSparks(ctx context.Context, viewerID uuid.UUID, sparks []*store.Spark) []*SparkWithPerson {
	ids := make([]uuid.UUID, 0, len(sparks))
	for _, sp := range sparks {
		ids = append(ids, sp.FromUserID)
	}
	cards := s.personCards(ctx, viewerID, ids)
	out := make([]*SparkWithPerson, 0, len(sparks))
	for _, sp := range sparks {
		out = append(out, &SparkWithPerson{Spark: sp, Person: cards[sp.FromUserID]})
	}
	return out
}

// GetMatchViewForUser is GetMatchForUser plus the other participant's
// compact person card.
func (s *Service) GetMatchViewForUser(ctx context.Context, matchID, userID uuid.UUID) (*MatchWithPerson, error) {
	m, err := s.GetMatchForUser(ctx, matchID, userID)
	if err != nil {
		return nil, err
	}
	other := otherParticipant(m, userID)
	return &MatchWithPerson{Match: m, Person: s.personCards(ctx, userID, []uuid.UUID{other})[other]}, nil
}
