// Daily picks (mechanic M7, DATING_PICKS_ENABLED).
//
// Up to MaxDailyPicks profiles a day, chosen apart from the deck: the
// best-scored candidates under the viewer's preferences (and their pass
// filters, while they hold a pass), verified profiles first, and never
// someone in the deck's current batch. The selection is made once per local
// day (the client's IANA time zone, Asia/Kolkata by default) and refreshed
// at local midnight. Every read re-checks visibility, so a block, a pause or
// an action takes a pick out at once. Acting on a pick spends no deck card.
package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo; picks need it

	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// MaxDailyPicks is the most picks a day holds.
const MaxDailyPicks = 10

// DefaultPicksTimezone is used when the client sends none.
const DefaultPicksTimezone = "Asia/Kolkata"

// verifiedPickBonus lifts a verified profile above an unverified one with a
// similar score.
const verifiedPickBonus = 0.2

// ErrInvalidTimezone maps to 400 INVALID_TIMEZONE.
var ErrInvalidTimezone = errors.New("invalid: tz must be an IANA time zone name")

// PicksMeta is the meta of GET /v1/dating/picks.
type PicksMeta struct {
	Date     string    `json:"date"`
	Timezone string    `json:"timezone"`
	ResetsAt time.Time `json:"resets_at"`
	Size     int       `json:"size"`
}

// PicksResponse is GET /v1/dating/picks.
type PicksResponse struct {
	Data []PulseCard `json:"data"`
	Meta PicksMeta   `json:"meta"`
}

// picksLocation resolves the client's time zone.
func picksLocation(tz string) (*time.Location, string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		tz = DefaultPicksTimezone
	}
	if strings.EqualFold(tz, "local") {
		return nil, "", ErrInvalidTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, "", ErrInvalidTimezone
	}
	return loc, tz, nil
}

// GetDailyPicks returns today's picks for the viewer.
func (s *Service) GetDailyPicks(ctx context.Context, viewerID uuid.UUID, tz string) (*PicksResponse, error) {
	if !s.mechanics.Picks {
		return nil, ErrMechanicDisabled
	}
	loc, name, err := picksLocation(tz)
	if err != nil {
		return nil, err
	}
	now := time.Now().In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	out := &PicksResponse{Data: []PulseCard{}, Meta: PicksMeta{
		Date: day.Format("2006-01-02"), Timezone: name, ResetsAt: day.AddDate(0, 0, 1).UTC(),
	}}
	// The same gates as the deck: a verified adult, inside the rollout, not
	// held by the risk sweeper.
	if err := s.requireAdult(ctx, viewerID); err != nil {
		return out, nil
	}
	if s.isUserGated(ctx, viewerID) {
		return out, nil
	}
	if level, rerr := s.GetUserRiskLevel(ctx, viewerID); rerr == nil {
		switch level {
		case store.RiskLevelHideFromDiscovery, store.RiskLevelChatHold, store.RiskLevelAdminReview, store.RiskLevelSuspend:
			return out, nil
		}
	}

	made, err := s.store.HasDailyPicks(ctx, viewerID, day)
	if err != nil {
		return nil, err
	}
	if !made {
		ids, err := s.choosePicks(ctx, viewerID)
		if err != nil {
			return nil, err
		}
		if err := s.store.SaveDailyPicks(ctx, viewerID, day, ids); err != nil {
			return nil, err
		}
	}
	ids, err := s.store.DailyPicks(ctx, viewerID, day)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	visible, err := s.store.FetchCandidates(ctx, store.CandidateQuery{
		ViewerID: viewerID, OnlyIDs: ids, ExcludePassed: true, ExcludeActed: true, Limit: MaxDailyPicks,
	})
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*store.CandidateProfile, len(visible))
	for i := range visible {
		byID[visible[i].UserID] = &visible[i]
	}
	viewer, _ := s.store.GetProfile(ctx, viewerID)
	matched, err := s.store.ListActiveMatchPartnerIDs(ctx, viewerID)
	if err != nil {
		matched = map[uuid.UUID]struct{}{}
	}
	keep := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if byID[id] != nil {
			keep = append(keep, id)
		}
	}
	prompts, photos := s.detailFor(ctx, keep)
	for _, id := range keep {
		card := s.buildCard(matcher.ScoredCandidate{Candidate: byID[id], Reasons: []matcher.MatchReason{}}, viewer, matched, prompts[id], photos[id])
		out.Data = append(out.Data, card)
	}
	out.Meta.Size = len(out.Data)
	return out, nil
}

// choosePicks makes the day's selection.
func (s *Service) choosePicks(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	viewerProfile, err := s.store.GetProfile(ctx, viewerID)
	if err != nil && !errors.Is(err, store.ErrProfileNotFound) {
		return nil, err
	}
	prefs, err := s.store.GetPreferences(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	q := store.CandidateQuery{ViewerID: viewerID, ExcludePassed: true, ExcludeActed: true, Limit: 50}
	if prefs.MinAge != nil {
		q.MinAge = *prefs.MinAge
	}
	if prefs.MaxAge != nil {
		q.MaxAge = *prefs.MaxAge
	}
	if prefs.InterestedInGender != nil && *prefs.InterestedInGender != InterestedInEveryone {
		q.GenderFilter = *prefs.InterestedInGender
	}
	if viewerProfile != nil && viewerProfile.Gender != nil {
		q.ViewerGender = *viewerProfile.Gender
	}
	q.IntentFilter = prefs.IntentFilter
	q.DistanceKmMax = prefs.DistanceKm
	if q.DistanceKmMax <= 0 {
		q.DistanceKmMax = 25
	}
	if viewerProfile != nil {
		q.ViewerLat, q.ViewerLon = viewerProfile.Latitude, viewerProfile.Longitude
	}
	privacyVerifiedOnly := false
	if p, perr := s.store.GetPrivacy(ctx, viewerID); perr == nil && p != nil {
		privacyVerifiedOnly = p.VerifiedOnlyFilter
	}
	s.applyDeckFilters(ctx, viewerID, &q, privacyVerifiedOnly)

	candidates, err := s.store.FetchCandidates(ctx, q)
	if err != nil {
		return nil, err
	}
	// Keep picks apart from the deck's current batch.
	inDeck := map[uuid.UUID]bool{}
	if deck := s.readPulseCache(ctx, viewerID); deck != nil {
		for _, c := range deck.Data {
			inDeck[c.CandidateID] = true
		}
	}
	provider := s.graphProvider
	if provider == nil {
		provider = matcher.NewStaticGraphProvider()
	}
	viewerTune, _ := s.store.GetTune(ctx, viewerID)
	viewerEcho, _ := s.store.GetEchoCache(ctx, viewerID)
	vc := matcher.ViewerContext{UserID: viewerID, Tune: viewerTune, EchoCache: viewerEcho, GraphProvider: provider}
	if viewerProfile != nil {
		vc.Intent, vc.Latitude, vc.Longitude = viewerProfile.Intent, viewerProfile.Latitude, viewerProfile.Longitude
	}
	type scoredPick struct {
		id    uuid.UUID
		score float64
	}
	scored := make([]scoredPick, 0, len(candidates))
	for i := range candidates {
		c := candidates[i]
		if inDeck[c.UserID] {
			continue
		}
		candEcho, _ := s.store.GetEchoCache(ctx, c.UserID)
		score, _, err := matcher.Score(ctx, vc, &c, candEcho)
		if err != nil {
			continue
		}
		if verifiedTier(c.TrustTier) {
			score += verifiedPickBonus
		}
		scored = append(scored, scoredPick{c.UserID, score})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if len(scored) > MaxDailyPicks {
		scored = scored[:MaxDailyPicks]
	}
	out := make([]uuid.UUID, len(scored))
	for i, p := range scored {
		out[i] = p.id
	}
	return out, nil
}
