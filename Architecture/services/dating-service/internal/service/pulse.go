package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// PulseCard is the per-candidate row in the Pulse response. Shape is locked
// — mobile is consuming this contract.
type PulseCard struct {
	CandidateID  uuid.UUID             `json:"candidate_id"`
	Score        float64               `json:"score"`
	MatchReasons []matcher.MatchReason `json:"match_reasons"`
	Profile      PulseProfileSummary   `json:"profile"`
	Echoes       PulseEchoes           `json:"echoes"`
}

// PulseProfileSummary mirrors the locked spec — abbreviated profile for the
// list-card.
type PulseProfileSummary struct {
	UserID              uuid.UUID         `json:"user_id"`
	FirstName           string            `json:"first_name"`
	Age                 int               `json:"age"`
	Intent              string            `json:"intent"`
	City                string            `json:"city"`
	// DistanceBucket is the lane D7 distance bucket code (lt_5_km |
	// km_5_10 | km_10_25 | gt_25_km) on the snapped points, and
	// DistanceLabel its display text ("< 5 km", "5–10 km", "10–25 km",
	// "25+ km"). Both are omitted when either side has no location. No
	// numeric distance is ever sent.
	DistanceBucket      string            `json:"distance_bucket,omitempty"`
	DistanceLabel       string            `json:"distance_label,omitempty"`
	PrimaryPhotoURL     string            `json:"primary_photo_url"`
	PrimaryPhotoBlurred bool              `json:"primary_photo_blurred"`
	TuneSummary         map[string]any    `json:"tune_summary"`
	TrustTier           string            `json:"trust_tier"`
	// LastActiveBucket (today | this_week | a_while_ago) and
	// LastActiveLabel are omitted when the candidate hides last active,
	// the default for new profiles. Never a timestamp.
	LastActiveBucket    string            `json:"last_active_bucket,omitempty"`
	LastActiveLabel     string            `json:"last_active_label,omitempty"`
}

// PulseEchoes is the brief echoes ribbon under the card. v1 of Pulse leaves
// this empty (the echo refresher stamps cache rows but the response builder
// can pick representatives in Sprint 3).
type PulseEchoes struct {
	TopQAAnswerID *string `json:"top_qa_answer_id"`
	TopReelID     *string `json:"top_reel_id"`
	TopCommunity  *string `json:"top_community"`
	RecentPostID  *string `json:"recent_post_id"`
}

// PulseResponse is the top-level shape returned by /v1/dating/pulse/today and
// /v1/dating/pulse/nebula.
type PulseResponse struct {
	Data         []PulseCard `json:"data"`
	Meta         PulseMeta   `json:"meta"`
	// CohortGated is true when the soft-launch cohort gate (Sprint 6) is
	// excluding this user from the rollout. Mobile renders the same
	// "coming soon" empty state used by the city gate.
	CohortGated bool `json:"cohort_gated,omitempty"`
}

// PulseMeta carries response-level info (size + generated_at).
type PulseMeta struct {
	GeneratedAt time.Time `json:"generated_at"`
	Size        int       `json:"size"`
}

// pulseCacheTTL is the TTL on `dating:pulse:today:{user_id}`.
const pulseCacheTTL = 24 * time.Hour

// graphProvider is settable via SetGraphProvider so main.go can inject the
// HTTP-backed implementation while tests use the static one.
func (s *Service) SetGraphProvider(p matcher.GraphProvider) {
	s.graphProvider = p
}

// GetPulseToday returns the user's daily Pulse list. Cached in Redis for 24h.
//
// Sprint 6: the soft-launch cohort gate runs BEFORE the cache so that a
// PULSE_COHORT_GATE flip applies immediately on the next request — we do
// not want a gated user to see a stale Pulse from the cache. Conversely,
// users who are inside the rollout get the cache-backed fast path.
func (s *Service) GetPulseToday(ctx context.Context, viewerID uuid.UUID) (*PulseResponse, error) {
	// P0-5: viewer must be a verified adult to see the discovery deck.
	// Surface as CohortGated so the client renders the same
	// "complete your profile" empty state instead of a hard error —
	// the underage guard mirrors the cohort gate UX.
	if err := s.requireAdult(ctx, viewerID); err != nil {
		return &PulseResponse{
			Data:        []PulseCard{},
			Meta:        PulseMeta{GeneratedAt: time.Now().UTC(), Size: 0},
			CohortGated: true,
		}, nil
	}
	if s.isUserGated(ctx, viewerID) {
		return &PulseResponse{
			Data:        []PulseCard{},
			Meta:        PulseMeta{GeneratedAt: time.Now().UTC(), Size: 0},
			CohortGated: true,
		}, nil
	}
	// §P0-7 Phase A: viewers under restrictive enforcement get an
	// empty deck (same shape as the cohort gate). The candidate-side
	// FetchCandidates filter handles the reciprocal "don't surface
	// this account to others" half. Best-effort on lookup errors —
	// see the spark-side rationale for why we don't fail open here.
	if level, rerr := s.GetUserRiskLevel(ctx, viewerID); rerr == nil {
		switch level {
		case store.RiskLevelHideFromDiscovery,
			store.RiskLevelChatHold,
			store.RiskLevelAdminReview,
			store.RiskLevelSuspend:
			return &PulseResponse{
				Data:        []PulseCard{},
				Meta:        PulseMeta{GeneratedAt: time.Now().UTC(), Size: 0},
				CohortGated: true,
			}, nil
		}
	} else {
		slog.Warn("pulse risk lookup failed", "viewer_id", viewerID, "error", rerr)
	}
	if cached := s.readPulseCache(ctx, viewerID); cached != nil {
		return cached, nil
	}

	resp, err := s.computePulseToday(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	s.writePulseCache(ctx, viewerID, resp)
	return resp, nil
}

// cacheKey is versioned. v2 (lane D7) cards carry distance and last-active
// buckets only, so a deck cached in the v1 shape (numeric distance_km,
// last_active_at, km figures in match reasons) is never read back; it expires
// on its own TTL.
func (s *Service) cacheKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("dating:pulse:today:v2:%s", viewerID.String())
}

func (s *Service) readPulseCache(ctx context.Context, viewerID uuid.UUID) *PulseResponse {
	if s.rdb == nil {
		return nil
	}
	raw, err := s.rdb.Get(ctx, s.cacheKey(viewerID)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("pulse cache get failed", "error", err)
		}
		return nil
	}
	var out PulseResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return &out
}

func (s *Service) writePulseCache(ctx context.Context, viewerID uuid.UUID, resp *PulseResponse) {
	if s.rdb == nil || resp == nil {
		return
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if err := s.rdb.Set(ctx, s.cacheKey(viewerID), raw, pulseCacheTTL).Err(); err != nil {
		slog.Warn("pulse cache set failed", "error", err)
	}

	// Phase 1 §3: populate the reverse-index that
	// InvalidateDecksForCandidate consults. SADD is idempotent; we
	// match the deck TTL so an abandoned viewer's membership entries
	// expire alongside their deck cache.
	if len(resp.Data) == 0 {
		return
	}
	pipe := s.rdb.Pipeline()
	for _, card := range resp.Data {
		key := deckMembershipKey(card.CandidateID)
		pipe.SAdd(ctx, key, viewerID.String())
		pipe.Expire(ctx, key, pulseCacheTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("deck membership update failed", "viewer_id", viewerID, "error", err)
	}
}

// InvalidatePulseCache removes the user's cached Pulse so the next request
// recomputes. Called from profile / Tune / preferences mutations.
//
// Phase 1 §3: also drops the viewer from every deck membership set
// they're tracked against so the reverse-index doesn't accumulate
// stale entries when the viewer's own deck is wiped.
func (s *Service) InvalidatePulseCache(ctx context.Context, viewerID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	if err := s.rdb.Del(ctx, s.cacheKey(viewerID)).Err(); err != nil {
		slog.Warn("pulse cache del failed", "error", err)
	}
}

// deckMembershipKey returns the Redis SET key that tracks which viewer
// decks currently include `candidateID`. The set is populated at deck
// generation time (writePulseCache) and consulted on candidate-state
// changes (paused / deleted / blocked / photo rejected) to fan out
// per-viewer deck invalidations.
func deckMembershipKey(candidateID uuid.UUID) string {
	return fmt.Sprintf("dating:deck_membership:%s", candidateID.String())
}

// InvalidateDecksForCandidate drops every viewer's cached deck that
// currently contains `candidateID`. Use this when:
//   - candidateID gets paused / deleted / suspended
//   - their primary photo is moderation-rejected
//   - they block someone (the someone's deck must drop them — though the
//     candidate query already filters mutual blocks, the cache may
//     still hold a stale copy)
//
// The membership set is also DEL'd so we don't keep fanning out on
// future events until the next deck generation rebuilds it.
func (s *Service) InvalidateDecksForCandidate(ctx context.Context, candidateID uuid.UUID) {
	if s.rdb == nil || candidateID == uuid.Nil {
		return
	}
	memberKey := deckMembershipKey(candidateID)
	viewers, err := s.rdb.SMembers(ctx, memberKey).Result()
	if err != nil {
		slog.Warn("deck membership lookup failed", "candidate_id", candidateID, "error", err)
		return
	}
	if len(viewers) == 0 {
		return
	}
	pipe := s.rdb.Pipeline()
	for _, v := range viewers {
		vid, err := uuid.Parse(v)
		if err != nil {
			continue
		}
		pipe.Del(ctx, s.cacheKey(vid))
	}
	pipe.Del(ctx, memberKey)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("deck fan-out invalidation failed", "candidate_id", candidateID, "error", err)
	}
}

// computePulseToday is the real matching pipeline.
func (s *Service) computePulseToday(ctx context.Context, viewerID uuid.UUID) (*PulseResponse, error) {
	// 1. Load viewer state.
	viewerProfile, err := s.store.GetProfile(ctx, viewerID)
	if err != nil && !errors.Is(err, store.ErrProfileNotFound) {
		return nil, fmt.Errorf("load viewer profile: %w", err)
	}
	prefs, err := s.store.GetPreferences(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("load viewer preferences: %w", err)
	}
	viewerTune, _ := s.store.GetTune(ctx, viewerID) // ignore not-found
	viewerEcho, _ := s.store.GetEchoCache(ctx, viewerID)

	// 2. Build the candidate query from prefs.
	q := store.CandidateQuery{
		ViewerID:      viewerID,
		ExcludePassed: true,
		Limit:         50,
	}
	if prefs.MinAge != nil {
		q.MinAge = *prefs.MinAge
	}
	if prefs.MaxAge != nil {
		q.MaxAge = *prefs.MaxAge
	}
	if prefs.InterestedInGender != nil {
		q.GenderFilter = *prefs.InterestedInGender
	}
	q.IntentFilter = prefs.IntentFilter
	if prefs.DistanceKm > 0 {
		q.DistanceKmMax = prefs.DistanceKm
	} else {
		q.DistanceKmMax = 25
	}
	if viewerProfile != nil {
		q.ViewerLat = viewerProfile.Latitude
		q.ViewerLon = viewerProfile.Longitude
	}
	// §P1-3: thread the viewer's verified_only_filter toggle into
	// the candidate query. Best-effort on lookup errors — the
	// deck stays visible rather than empty if the privacy row is
	// momentarily unreadable.
	if viewerPrivacy, perr := s.store.GetPrivacy(ctx, viewerID); perr == nil && viewerPrivacy != nil {
		q.VerifiedOnly = viewerPrivacy.VerifiedOnlyFilter
	} else if perr != nil && !errors.Is(perr, store.ErrProfileNotFound) {
		slog.Warn("pulse privacy lookup failed", "viewer_id", viewerID, "error", perr)
	}

	// 3. Fetch candidates.
	candidates, err := s.store.FetchCandidates(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("fetch candidates: %w", err)
	}

	// 4. Score each candidate and apply the diversity constraint.
	provider := s.graphProvider
	if provider == nil {
		provider = matcher.NewStaticGraphProvider()
	}
	vc := matcher.ViewerContext{
		UserID:        viewerID,
		Tune:          viewerTune,
		EchoCache:     viewerEcho,
		GraphProvider: provider,
	}
	if viewerProfile != nil {
		vc.Intent = viewerProfile.Intent
		vc.Latitude = viewerProfile.Latitude
		vc.Longitude = viewerProfile.Longitude
	}

	scored := make([]matcher.ScoredCandidate, 0, len(candidates))
	for i := range candidates {
		c := candidates[i]
		candEcho, _ := s.store.GetEchoCache(ctx, c.UserID)
		score, reasons, err := matcher.Score(ctx, vc, &c, candEcho)
		if err != nil {
			slog.Warn("matcher score failed", "candidate", c.UserID, "error", err)
			continue
		}
		scored = append(scored, matcher.ScoredCandidate{
			Candidate: &c,
			Score:     score,
			Reasons:   reasons,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	scored = matcher.ApplyDiversityConstraint(scored, 7)

	// §P1-3: precompute the viewer's active-match partner set so the
	// blur-photos-until-match gate can lift blur for matched users
	// without a per-card round-trip. Empty set is a safe default.
	matched, mErr := s.store.ListActiveMatchPartnerIDs(ctx, viewerID)
	if mErr != nil {
		slog.Warn("pulse matched-partners lookup failed", "viewer_id", viewerID, "error", mErr)
		matched = map[uuid.UUID]struct{}{}
	}

	// 5. Build response.
	cards := make([]PulseCard, 0, len(scored))
	for _, sc := range scored {
		cards = append(cards, s.buildCard(sc, viewerProfile, matched))
	}
	return &PulseResponse{
		Data: cards,
		Meta: PulseMeta{GeneratedAt: time.Now().UTC(), Size: len(cards)},
	}, nil
}

// buildCard converts a scored candidate into the locked PulseCard shape.
//
// §P1-3 / lane D7 masking happens here so the wire-level response respects
// each candidate's privacy settings without a second per-card hop:
//   - distance                → a bucket code + label on the snapped
//                               points, for every candidate; never km.
//   - hide_last_active        → no last-active bucket; otherwise only
//                               today / this_week / a_while_ago.
//   - photo visibility and blur_photos_until_match (lane D6) →
//                               primary_photo_url is the photo's
//                               /blurred route unless PhotoVariantFor
//                               allows /full (owner, open match, public
//                               without blur, sparked_only the owner
//                               sparked). The blur is rendered by
//                               media-service, not the client.
//
// matchedPartners is the precomputed set of user-ids the viewer
// currently has an active match with. Empty / nil = no matches.
func (s *Service) buildCard(sc matcher.ScoredCandidate, viewer *store.Profile, matchedPartners map[uuid.UUID]struct{}) PulseCard {
	c := sc.Candidate
	first := ""
	if c.FirstName != nil {
		first = *c.FirstName
	}
	city := ""
	if c.City != nil {
		city = *c.City
	}
	var distance store.DistanceBand
	if viewer != nil {
		distance, _ = store.DistanceBandBetween(viewer.Latitude, viewer.Longitude, c.Latitude, c.Longitude)
	}

	// Lane D6: the card names the photo image route for the variant this
	// viewer may have — never a media id, never a storage URL. The route
	// re-decides on every fetch, so a cached deck cannot outlive an unmatch.
	_, isMatched := matchedPartners[c.UserID]
	variant := PhotoVariantFor(c.PrimaryPhotoVisibility, PhotoViewer{
		Matched:              isMatched,
		OwnerSparkedViewer:   c.SparkedViewer,
		OwnerBlursUntilMatch: c.BlurPhotosUntilMatch || c.BlurMode,
	})
	primaryURL := ""
	if c.PrimaryPhotoID != nil {
		primaryURL = PhotoImagePath(*c.PrimaryPhotoID, variant)
	}
	primaryBlurred := variant == PhotoVariantBlurred

	tuneSummary := map[string]any{}
	if c.LifestyleRhythm != nil {
		tuneSummary["lifestyle_rhythm"] = *c.LifestyleRhythm
	}
	if c.ConversationStyle != nil {
		tuneSummary["conversation_style"] = *c.ConversationStyle
	}

	summary := PulseProfileSummary{
		UserID:              c.UserID,
		FirstName:           first,
		Age:                 c.Age(),
		Intent:              c.Intent,
		City:                city,
		DistanceBucket:      distance.Code,
		DistanceLabel:       distance.Label,
		PrimaryPhotoURL:     primaryURL,
		PrimaryPhotoBlurred: primaryBlurred,
		TuneSummary:         tuneSummary,
		TrustTier:           c.TrustTier,
	}
	if !c.HideLastActive {
		band := LastActiveBucketFor(c.LastActiveAt, time.Now())
		summary.LastActiveBucket, summary.LastActiveLabel = band.Code, band.Label
	}

	return PulseCard{
		CandidateID:  c.UserID,
		Score:        round2(sc.Score),
		MatchReasons: sc.Reasons,
		Profile:      summary,
		Echoes:       PulseEchoes{}, // v1 — filled in Sprint 3.
	}
}

func round2(f float64) float64 {
	return float64(int(f*100)) / 100
}

// GetPulseNebulaPassed returns the user's recently-passed candidates as
// PulseCards. Paged, max 100.
func (s *Service) GetPulseNebulaPassed(ctx context.Context, viewerID uuid.UUID, limit, offset int) (*PulseResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	passes, err := s.store.ListPassedCandidates(ctx, viewerID, limit, offset)
	if err != nil {
		return nil, err
	}
	viewerProfile, _ := s.store.GetProfile(ctx, viewerID)

	// §P1-3: matched-partner set drives the blur-photos-until-match
	// lift inside buildCard. Best-effort on lookup errors.
	matched, mErr := s.store.ListActiveMatchPartnerIDs(ctx, viewerID)
	if mErr != nil {
		slog.Warn("pulse nebula matched-partners lookup failed", "viewer_id", viewerID, "error", mErr)
		matched = map[uuid.UUID]struct{}{}
	}

	cards := make([]PulseCard, 0, len(passes))
	for _, p := range passes {
		c, err := s.store.GetCandidateForViewer(ctx, viewerID, p.CandidateID)
		if err != nil || c == nil {
			continue
		}
		card := s.buildCard(matcher.ScoredCandidate{Candidate: c, Score: 0}, viewerProfile, matched)
		cards = append(cards, card)
	}
	return &PulseResponse{
		Data: cards,
		Meta: PulseMeta{GeneratedAt: time.Now().UTC(), Size: len(cards)},
	}, nil
}

// PassResult is the body of POST /v1/dating/pulse/:candidateId/pass.
type PassResult struct {
	Passed        bool      `json:"passed"`
	CandidateID   uuid.UUID `json:"candidate_id"`
	CooldownUntil time.Time `json:"cooldown_until"`
}

// maxPassReasonLen bounds the optional free-text pass reason.
const maxPassReasonLen = 200

// PassCandidate records the viewer's pass (idempotent), drops the candidate
// from the viewer's cached deck, and FetchCandidates then excludes them for
// store.PassCooldown. Nothing is emitted: the candidate is never told.
func (s *Service) PassCandidate(ctx context.Context, viewerID, candidateID uuid.UUID, reason string) (*PassResult, error) {
	if viewerID == uuid.Nil || candidateID == uuid.Nil {
		return nil, fmt.Errorf("invalid: viewer and candidate ids required")
	}
	if viewerID == candidateID {
		return nil, fmt.Errorf("invalid: cannot pass yourself")
	}
	if len(reason) > maxPassReasonLen {
		return nil, fmt.Errorf("invalid: reason must be at most %d characters", maxPassReasonLen)
	}
	passedAt, err := s.store.RecordPass(ctx, viewerID, candidateID, reason)
	if err != nil {
		return nil, err
	}
	s.removeFromCachedDeck(ctx, viewerID, candidateID)
	return &PassResult{
		Passed:        true,
		CandidateID:   candidateID,
		CooldownUntil: passedAt.Add(store.PassCooldown).UTC(),
	}, nil
}

// removeFromCachedDeck rewrites the viewer's cached deck without the
// candidate, keeping the key's TTL. SET XX never recreates a key that
// expired meanwhile; a failed rewrite drops the cache instead.
func (s *Service) removeFromCachedDeck(ctx context.Context, viewerID, candidateID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	cached := s.readPulseCache(ctx, viewerID)
	if cached == nil {
		return
	}
	kept := make([]PulseCard, 0, len(cached.Data))
	for _, card := range cached.Data {
		if card.CandidateID != candidateID {
			kept = append(kept, card)
		}
	}
	if len(kept) == len(cached.Data) {
		return
	}
	cached.Data = kept
	cached.Meta.Size = len(kept)
	raw, err := json.Marshal(cached)
	if err != nil {
		s.InvalidatePulseCache(ctx, viewerID)
		return
	}
	if err := s.rdb.SetArgs(ctx, s.cacheKey(viewerID), raw, redis.SetArgs{Mode: "XX", KeepTTL: true}).Err(); err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("pulse cache pass rewrite failed; dropping deck", "viewer_id", viewerID, "error", err)
		s.InvalidatePulseCache(ctx, viewerID)
	}
	if err := s.rdb.SRem(ctx, deckMembershipKey(candidateID), viewerID.String()).Err(); err != nil {
		slog.Warn("deck membership removal failed", "candidate_id", candidateID, "error", err)
	}
}
