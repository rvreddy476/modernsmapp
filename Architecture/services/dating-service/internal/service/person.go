// Compact person cards (lane D10).
//
// A match, an incoming spark and the person card all render the same small
// object: who this is (first name, age, city, what they are here for), one
// photo the viewer is allowed to see, and whether they carry a verified
// badge. It never carries religion, community, an exact location, last-active
// when the person hides it, or any D9-sealed field; the distance and the
// last-active are lane D7 buckets only.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
	// City is the coarse city string the profile stores, and Intent what
	// this person is here for — the same two the deck card shows. City is a
	// name only: no coordinates, no geohash, no exact distance.
	City   string `json:"city,omitempty"`
	Intent string `json:"intent,omitempty"`
	// LastActiveBucket (today | this_week | a_while_ago) and LastActiveLabel
	// are the lane D7 last-active bucket. BOTH are omitted when the owner
	// hides last active — the default for a new profile. Never a timestamp.
	LastActiveBucket string `json:"last_active_bucket,omitempty"`
	LastActiveLabel  string `json:"last_active_label,omitempty"`
	// Detail is the pre-match "enough to decide" block, present only on the
	// surfaces where the viewer is deciding about this person: the person
	// card itself and an incoming spark. The match list, the trusted-contact
	// list and the location-share lists stay compact — a safety surface has
	// no business carrying somebody's bio and gallery.
	Detail *ProfileDetail `json:"detail,omitempty"`
}

// verifiedTier reports whether a trust tier carries the verified badge.
func verifiedTier(tier string) bool {
	return tier == "selfie" || tier == "aadhaar"
}

// --- The pre-match detail block --------------------------------------------
//
// The founder's decision: a deck card shows the photo AND enough of the
// profile to decide — the person's own description, what they are into, and
// the rest of their photos to swipe through — then you send a spark, and only
// an accepted spark opens chat and calls.
//
// What this block deliberately does NOT carry, because it stays sealed until
// the two people match: religion, community, any other lane D9 sealed field,
// the exact location or coordinates (distance is the lane D7 bucket, and it
// lives on the card itself, not here), last-active when the owner hides it,
// and the Echoes ribbon of main-app activity. Every existing guard still runs
// first: a blocked pair either way never gets a row at all, each photo goes
// through the lane D6 variant rule on its OWN visibility, and a blurring owner
// blurs the whole gallery, not just the primary.

// CardPhoto is one photo in a card's swipeable gallery: the dating photo id
// and the image route for the variant THIS viewer may have. Never a media id,
// never a storage URL — the route re-decides on every fetch.
type CardPhoto struct {
	ID    uuid.UUID `json:"id"`
	URL   string    `json:"url"`
	// State is "full" or "blurred", the lane D6 variant for this viewer.
	State string `json:"state"`
}

// PromptAnswer is one catalog question and this person's answer to it —
// the "what she is interested in" content. The question text is resolved from
// the v1 catalog so the client does not have to carry it.
type PromptAnswer struct {
	PromptID int    `json:"prompt_id"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// ProfileDetail is the pre-match block described above. Every member is
// omitted when empty, and the whole block is omitted when a person has
// written nothing at all.
type ProfileDetail struct {
	// Bio is dating_profiles.bio — the description the owner wrote.
	Bio     string         `json:"bio,omitempty"`
	Prompts []PromptAnswer `json:"prompts,omitempty"`
	// Languages is dating_profiles.language_prefs. It is the only
	// interests/tags-shaped list a dating profile stores today; the prompt
	// answers above are where the real interests live.
	Languages []string `json:"languages,omitempty"`
	// Photos is the whole approved gallery, primary first, so the card can
	// be swiped through. Each entry carries its own variant.
	Photos []CardPhoto `json:"photos,omitempty"`
}

// cardPhotos applies the lane D6 rule to each photo with that photo's OWN
// visibility, so a public photo and a match_only one in the same gallery get
// the variant each deserves.
func cardPhotos(refs []store.PhotoRef, v PhotoViewer) []CardPhoto {
	if len(refs) == 0 {
		return nil
	}
	out := make([]CardPhoto, 0, len(refs))
	for _, ref := range refs {
		variant := PhotoVariantFor(ref.Visibility, v)
		out = append(out, CardPhoto{ID: ref.ID, URL: PhotoImagePath(ref.ID, variant), State: variant})
	}
	return out
}

// promptAnswers pairs each stored answer with its catalog question. An answer
// whose prompt id is no longer in the catalog is dropped rather than shown
// without its question.
func promptAnswers(ps []store.Prompt) []PromptAnswer {
	if len(ps) == 0 {
		return nil
	}
	out := make([]PromptAnswer, 0, len(ps))
	for _, p := range ps {
		question, ok := promptQuestion(p.PromptID)
		if !ok {
			continue
		}
		out = append(out, PromptAnswer{PromptID: p.PromptID, Question: question, Answer: p.Answer})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildProfileDetail assembles the block, or nil when there is nothing in it.
func buildProfileDetail(bio string, languages []string, prompts []store.Prompt, photos []store.PhotoRef, v PhotoViewer) *ProfileDetail {
	d := &ProfileDetail{
		Bio:       bio,
		Prompts:   promptAnswers(prompts),
		Languages: languages,
		Photos:    cardPhotos(photos, v),
	}
	if d.Bio == "" && len(d.Prompts) == 0 && len(d.Languages) == 0 && len(d.Photos) == 0 {
		return nil
	}
	return d
}

// buildPersonCard renders one compact row for one viewer. matched says
// whether the viewer currently has an open match with this person, and viewer
// is the viewer's own profile (nil = no distance bucket).
func buildPersonCard(row *store.PersonRow, matched bool, viewer *store.Profile) *PersonCard {
	return buildPersonCardDetail(row, matched, viewer, nil, nil, false)
}

// buildPersonCardDetail is buildPersonCard plus the pre-match detail block.
// withDetail is the switch, so a caller that must stay compact (the match
// list, trusted contacts, location shares) cannot grow the block by accident.
func buildPersonCardDetail(row *store.PersonRow, matched bool, viewer *store.Profile,
	prompts []store.Prompt, photos []store.PhotoRef, withDetail bool) *PersonCard {
	if row == nil {
		return nil
	}
	first := ""
	if row.FirstName != nil {
		first = *row.FirstName
	}
	photoViewer := PhotoViewer{
		Matched:              matched,
		OwnerSparkedViewer:   row.SparkedViewer,
		OwnerBlursUntilMatch: row.BlurPhotosUntilMatch || row.BlurMode,
	}
	variant := PhotoVariantFor(row.PrimaryPhotoVisibility, photoViewer)
	city := ""
	if row.City != nil {
		city = *row.City
	}
	card := &PersonCard{
		UserID:     row.UserID,
		FirstName:  first,
		Age:        row.Age(),
		PhotoState: variant,
		Verified:   verifiedTier(row.TrustTier),
		TrustTier:  row.TrustTier,
		City:       city,
		Intent:     row.Intent,
	}
	// Lane D7: the owner's hide_last_active wins. When they hide it the card
	// carries neither the bucket nor the label — exactly as the deck card
	// does it — and the timestamp itself never crosses either way.
	if !row.HideLastActive {
		band := LastActiveBucketFor(row.LastActiveAt, time.Now())
		card.LastActiveBucket, card.LastActiveLabel = band.Code, band.Label
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
	if withDetail {
		card.Detail = buildProfileDetail(row.Bio, row.LanguagePrefs, prompts, photos, photoViewer)
	}
	return card
}

// personCards builds the compact cards for a set of user ids as one viewer
// sees them. Ids the viewer may not see (blocked either way, deleted,
// suspended) are absent from the result. Best effort: a lookup error yields no
// cards rather than failing the list the cards decorate.
func (s *Service) personCards(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) map[uuid.UUID]*PersonCard {
	return s.personCardsDetail(ctx, viewerID, ids, false)
}

// personCardsDetail is personCards with the pre-match detail block switched
// on. The prompts and the galleries are fetched in one query each, so a list
// of cards costs two more round-trips rather than two per card.
func (s *Service) personCardsDetail(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID, withDetail bool) map[uuid.UUID]*PersonCard {
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

	// Only the ids that survived the visibility query are looked up, so a
	// blocked or deleted person's prompts and photos are never even read.
	var prompts map[uuid.UUID][]store.Prompt
	var photos map[uuid.UUID][]store.PhotoRef
	if withDetail {
		visible := make([]uuid.UUID, 0, len(rows))
		for id := range rows {
			visible = append(visible, id)
		}
		prompts, photos = s.detailFor(ctx, visible)
	}
	for id, row := range rows {
		_, isMatched := matched[id]
		out[id] = buildPersonCardDetail(row, isMatched, viewer, prompts[id], photos[id], withDetail)
	}
	return out
}

// detailFor bulk-loads the prompt answers and the approved photo galleries
// for a set of people. Best effort on either lookup: a card without its
// prompts or its gallery is better than a list that fails to render.
func (s *Service) detailFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]store.Prompt, map[uuid.UUID][]store.PhotoRef) {
	if len(ids) == 0 {
		return nil, nil
	}
	prompts, err := s.store.ListPromptsForUsers(ctx, ids)
	if err != nil {
		slog.Warn("card prompts lookup failed", "error", err)
		prompts = nil
	}
	photos, err := s.store.ListApprovedPhotosForUsers(ctx, ids)
	if err != nil {
		slog.Warn("card photos lookup failed", "error", err)
		photos = nil
	}
	return prompts, photos
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
	// The person card is where the viewer decides, so it carries the
	// pre-match detail block.
	prompts, photos := s.detailFor(ctx, []uuid.UUID{targetID})
	return buildPersonCardDetail(row, matched, viewer, prompts[targetID], photos[targetID], true), nil
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

// decorateIncomingSparks attaches each sender's card to their spark. This is
// the accept-or-ignore decision, so the sender's card carries the pre-match
// detail block — the same thing the viewer would have seen in the deck.
func (s *Service) decorateIncomingSparks(ctx context.Context, viewerID uuid.UUID, sparks []*store.Spark) []*SparkWithPerson {
	ids := make([]uuid.UUID, 0, len(sparks))
	for _, sp := range sparks {
		ids = append(ids, sp.FromUserID)
	}
	cards := s.personCardsDetail(ctx, viewerID, ids, true)
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
