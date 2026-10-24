package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Trending sounds (2026-10-24; contract trending-sounds-contract.md).

	WHO DOES WHAT

	post-service owns the posts that use a sound and every rule about who may
	see a post, so it COUNTS: GET /v1/internal/sound-uses answers each
	sound's uses per day over the last 14 days, one use per (sound, author,
	day), on posts that play to everybody, never the sound's own source reel
	(post-service service/sound_uses.go).

	This service owns sounds, so it RANKS. Every 15 minutes one replica (a
	session advisory lock, and a run younger than the interval is not
	repeated by another replica) reads every page of those counts, keeps the
	sounds with at least 3 uses this week that everyone may hear, scores
	them, and stores the whole list in one transaction (migration 030).
	GET /v1/audio/trending serves from the stored list.

	THE NUMBERS

	  uses_7d       days 0-6 (day 0 = the last 24 h)
	  uses_prev_7d  days 7-13
	  score         3 x day 0 + 2 x (days 1-2) + days 3-6
	  appears       uses_7d >= 3
	  rising        uses_7d >= 3 and (uses_prev_7d = 0 or uses_7d >= 2 x uses_prev_7d)
	  order         score, then uses_7d, high first; then sound id

	WHICH SOUNDS

	A sound must be playable to everyone: ready, and
	  - a platform library sound whose library row is active, not withdrawn
	    and whose licence has not lapsed, or
	  - an extracted sound whose source video the signed-out stranger may
	    play (admitToTrack asked as uuid.Nil: the source follows its video,
	    and the widest audience there is is the one asked). A private or held
	    source is gone.
	The run checks this; every read checks it again for its viewer
	(admitToTrack as the viewer, plus the library rule against the clock),
	so a sound that went private after the run never reaches anyone.

	BLOCKS

	A sound whose creator (audio_tracks.creator_user_id) holds a block with
	the viewer, either way, is skipped for that viewer: graph-service's
	blocked-any, the lookup post-service makes for the same question. A
	lookup that does not answer is a 503, never an unfiltered list.
*/

const (
	// TrendingSoundsInterval is how often the ranking is computed.
	TrendingSoundsInterval = 15 * time.Minute
	// trendingFreshness is how much younger than the interval a stored run
	// may be for another replica's tick to leave it alone.
	trendingFreshness = time.Minute
	// TrendingMinUses is the fewest uses this week a trending sound has.
	TrendingMinUses = 3
	// MaxTrendingSounds caps the stored list.
	MaxTrendingSounds = 1000
	// DefaultTrendingPage and MaxTrendingPage bound GET /v1/audio/trending.
	DefaultTrendingPage = 20
	MaxTrendingPage     = 50
	// trendingDays is the window post-service counts over.
	trendingDays = 14
	// maxSoundUsesPages bounds one run's reads of post-service.
	maxSoundUsesPages = 1000
	// trendingReadRounds bounds how many stored batches one page reads to
	// fill itself when the viewer's checks leave sounds out.
	trendingReadRounds = 5
)

var (
	// ErrTrendingCursor: the cursor is not one this route issued.
	ErrTrendingCursor = errors.New("cursor is not a value this endpoint issued")
	// ErrTrendingFilter: filter is neither all nor rising.
	ErrTrendingFilter = errors.New("filter must be all or rising")
	// ErrTrendingUnavailable: the ranking is not wired in this process.
	ErrTrendingUnavailable = errors.New("trending sounds are unavailable")
)

// SoundDailyUses is one sound's uses per day as post-service counts them.
// DailyUses[0] is the 24 hours before the count was made.
type SoundDailyUses struct {
	AudioTrackID uuid.UUID
	DailyUses    []int
}

// SoundUsesPage is one page of post-service's counts.
type SoundUsesPage struct {
	AsOf      time.Time
	Sounds    []SoundDailyUses
	NextAfter *uuid.UUID
}

// SoundUsesSource reads post-service's counts (HTTPSoundUses in production).
type SoundUsesSource interface {
	SoundUses(ctx context.Context, after uuid.UUID) (*SoundUsesPage, error)
}

// BlockChecker answers which candidates hold a block with userID, either
// way (HTTPBlockChecker in production).
type BlockChecker interface {
	BlockedAny(ctx context.Context, userID uuid.UUID, candidates []uuid.UUID) (map[uuid.UUID]bool, error)
}

// trendingStore is the store slice of the ranking.
type trendingStore interface {
	TrendingCandidates(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.TrendingCandidate, error)
	ReplaceTrendingSounds(ctx context.Context, computedAt time.Time, ranked []postgres.TrendingSound) error
	ListTrendingSounds(ctx context.Context, risingOnly bool, afterRank, limit int) ([]postgres.TrendingEntry, error)
	TrendingSoundByID(ctx context.Context, audioID uuid.UUID) (*postgres.TrendingEntry, error)
	LatestTrendingRun(ctx context.Context) (*postgres.TrendingRun, error)
	WithTrendingLease(ctx context.Context, fn func(ctx context.Context) error) (bool, error)
	GetAudioTrack(ctx context.Context, id uuid.UUID) (*postgres.AudioTrack, error)
}

// trackAudience is the one audience decision of a sound (audio_reads.go).
type trackAudience interface {
	admitToTrack(ctx context.Context, viewerID uuid.UUID, track *postgres.AudioTrack) error
}

// TrendingSounds computes, stores and serves the ranking.
type TrendingSounds struct {
	store    trendingStore
	uses     SoundUsesSource
	audience trackAudience
	blocks   BlockChecker
	now      func() time.Time
	interval time.Duration
	log      *slog.Logger
}

// NewTrendingSounds wires the ranking. uses may be nil (no run is made; the
// stored list is served as it is); a nil blocks answers every list 503.
func NewTrendingSounds(store trendingStore, uses SoundUsesSource, audience *RecordReads, blocks BlockChecker) *TrendingSounds {
	t := &TrendingSounds{
		store: store, uses: uses, blocks: blocks,
		now: time.Now, interval: TrendingSoundsInterval,
		log: slog.Default().With("component", "trending-sounds"),
	}
	if audience != nil {
		t.audience = audience
	}
	return t
}

// ─── The numbers ────────────────────────────────────────────────────

// SoundStats is one sound's week as the ranking reads it.
type SoundStats struct {
	Uses7d     int
	UsesPrev7d int
	Score      int
	Rising     bool
}

// soundStats derives a sound's numbers from its daily uses (day 0 first).
// Days past the window are ignored; missing days count nothing.
func soundStats(daily []int) SoundStats {
	day := func(i int) int {
		if i < len(daily) && daily[i] > 0 {
			return daily[i]
		}
		return 0
	}
	var st SoundStats
	for i := 0; i < 7; i++ {
		st.Uses7d += day(i)
	}
	for i := 7; i < trendingDays; i++ {
		st.UsesPrev7d += day(i)
	}
	st.Score = 3*day(0) + 2*(day(1)+day(2))
	for i := 3; i < 7; i++ {
		st.Score += day(i)
	}
	st.Rising = st.Uses7d >= TrendingMinUses && (st.UsesPrev7d == 0 || st.Uses7d >= 2*st.UsesPrev7d)
	return st
}

// rankSounds orders the sounds that qualify (TrendingMinUses, eligible) and
// numbers them from 1, at most MaxTrendingSounds.
func rankSounds(stats map[uuid.UUID]SoundStats, eligible map[uuid.UUID]bool) []postgres.TrendingSound {
	ranked := make([]postgres.TrendingSound, 0, len(stats))
	for id, st := range stats {
		if st.Uses7d < TrendingMinUses || !eligible[id] {
			continue
		}
		ranked = append(ranked, postgres.TrendingSound{
			AudioTrackID: id, Score: st.Score, Uses7d: st.Uses7d, UsesPrev7d: st.UsesPrev7d, Rising: st.Rising,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Uses7d != b.Uses7d {
			return a.Uses7d > b.Uses7d
		}
		return a.AudioTrackID.String() < b.AudioTrackID.String()
	})
	if len(ranked) > MaxTrendingSounds {
		ranked = ranked[:MaxTrendingSounds]
	}
	for i := range ranked {
		ranked[i].Rank = i + 1
	}
	return ranked
}

// libraryTrackEligible: the curated library row may still be offered.
func libraryTrackEligible(f postgres.LibraryFacts, now time.Time) bool {
	if !f.Present || !f.IsActive || f.WithdrawnAt != nil {
		return false
	}
	return f.LicenseExpiresAt == nil || f.LicenseExpiresAt.After(now)
}

// ─── The run ────────────────────────────────────────────────────────

// Start runs the ranking now and every interval until ctx ends. Without a
// counts source there is nothing to run.
func (t *TrendingSounds) Start(ctx context.Context) {
	if t == nil || t.uses == nil || t.store == nil {
		return
	}
	go func() {
		t.tick(ctx)
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.tick(ctx)
			}
		}
	}()
}

func (t *TrendingSounds) tick(ctx context.Context) {
	ran, err := t.RunOnce(ctx)
	if err != nil {
		// The stored list stays: every read re-checks it, and an old list
		// is better than none.
		t.log.Warn("trending sounds run failed; keeping the stored ranking", "error", err)
		return
	}
	if !ran {
		t.log.Debug("trending sounds run skipped: another replica holds or just made it")
	}
}

// RunOnce computes and stores the ranking unless another replica holds the
// run or made one less than an interval ago. It reports whether it ran.
func (t *TrendingSounds) RunOnce(ctx context.Context) (bool, error) {
	if t == nil || t.store == nil || t.uses == nil {
		return false, ErrTrendingUnavailable
	}
	computed := false
	held, err := t.store.WithTrendingLease(ctx, func(ctx context.Context) error {
		last, err := t.store.LatestTrendingRun(ctx)
		if err != nil {
			return fmt.Errorf("read last run: %w", err)
		}
		if last != nil && t.now().Sub(last.ComputedAt) < t.interval-trendingFreshness {
			return nil
		}
		computed = true
		return t.compute(ctx)
	})
	if err != nil {
		return false, err
	}
	return held && computed, nil
}

func (t *TrendingSounds) compute(ctx context.Context) error {
	stats := map[uuid.UUID]SoundStats{}
	after := uuid.Nil
	for pages := 0; ; pages++ {
		if pages >= maxSoundUsesPages {
			return fmt.Errorf("sound uses: more than %d pages", maxSoundUsesPages)
		}
		page, err := t.uses.SoundUses(ctx, after)
		if err != nil {
			return fmt.Errorf("sound uses: %w", err)
		}
		for _, s := range page.Sounds {
			stats[s.AudioTrackID] = soundStats(s.DailyUses)
		}
		if page.NextAfter == nil {
			break
		}
		if *page.NextAfter == after {
			return errors.New("sound uses: the cursor did not move")
		}
		after = *page.NextAfter
	}

	qualifying := make([]uuid.UUID, 0, len(stats))
	for id, st := range stats {
		if st.Uses7d >= TrendingMinUses {
			qualifying = append(qualifying, id)
		}
	}
	now := t.now()
	eligible := make(map[uuid.UUID]bool, len(qualifying))
	decided := map[uuid.UUID]bool{} // per source video
	for start := 0; start < len(qualifying); start += MaxTrendingSounds {
		end := start + MaxTrendingSounds
		if end > len(qualifying) {
			end = len(qualifying)
		}
		candidates, err := t.store.TrendingCandidates(ctx, qualifying[start:end])
		if err != nil {
			return fmt.Errorf("load candidates: %w", err)
		}
		for id, c := range candidates {
			ok, err := t.everyoneMayHear(ctx, c, now, decided)
			if err != nil {
				return fmt.Errorf("sound %s: %w", id, err)
			}
			eligible[id] = ok
		}
	}

	ranked := rankSounds(stats, eligible)
	if err := t.store.ReplaceTrendingSounds(ctx, now, ranked); err != nil {
		return fmt.Errorf("store ranking: %w", err)
	}
	t.log.Info("trending sounds ranked", "sounds_used", len(stats), "qualifying", len(qualifying), "ranked", len(ranked))
	return nil
}

// everyoneMayHear is the run's eligibility of one sound. decided caches the
// answer per source video.
func (t *TrendingSounds) everyoneMayHear(ctx context.Context, c postgres.TrendingCandidate, now time.Time, decided map[uuid.UUID]bool) (bool, error) {
	track := c.Track
	if track.Status != "ready" {
		return false, nil
	}
	if track.IsLibrarySound() {
		return libraryTrackEligible(c.Library, now), nil
	}
	if track.SourceMediaID == nil || *track.SourceMediaID == uuid.Nil {
		return false, nil
	}
	if ok, known := decided[*track.SourceMediaID]; known {
		return ok, nil
	}
	ok, err := t.admit(ctx, uuid.Nil, &track)
	if err != nil {
		return false, err
	}
	decided[*track.SourceMediaID] = ok
	return ok, nil
}

// admit is the audience decision as a yes/no; anything but a resolved
// answer is an error.
func (t *TrendingSounds) admit(ctx context.Context, viewerID uuid.UUID, track *postgres.AudioTrack) (bool, error) {
	if t.audience == nil {
		return false, fmt.Errorf("%w: no audience decision wired", delivery.ErrDeliveryUnresolved)
	}
	err := t.audience.admitToTrack(ctx, viewerID, track)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, delivery.ErrDeliveryDenied):
		return false, nil
	case errors.Is(err, delivery.ErrDeliveryUnresolved):
		return false, err
	default:
		return false, fmt.Errorf("%w: %v", delivery.ErrDeliveryUnresolved, err)
	}
}

// ─── The reads ──────────────────────────────────────────────────────

// TrendingItem is one row of GET /v1/audio/trending.
type TrendingItem struct {
	Rank       int                 `json:"rank"`
	Sound      postgres.AudioTrack `json:"sound"`
	Uses7d     int                 `json:"uses_7d"`
	UsesPrev7d int                 `json:"uses_prev_7d"`
	Rising     bool                `json:"rising"`
}

// TrendingPage is one page of the list.
type TrendingPage struct {
	Items []TrendingItem
	// NextCursor is "" on the last page.
	NextCursor string
	// UpdatedAt is the stored run's time; nil before the first run.
	UpdatedAt *time.Time
}

// TrendingBadge is `trending` on the sound page.
type TrendingBadge struct {
	Rank   int  `json:"rank"`
	Rising bool `json:"rising"`
}

// NormalizeTrendingLimit resolves the caller's page size.
func NormalizeTrendingLimit(limit int) int {
	if limit <= 0 {
		return DefaultTrendingPage
	}
	if limit > MaxTrendingPage {
		return MaxTrendingPage
	}
	return limit
}

const trendingCursorPrefix = "r1:"

func encodeTrendingCursor(rank int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(trendingCursorPrefix + strconv.Itoa(rank)))
}

// decodeTrendingCursor is the rank the page starts after; 0 for none.
func decodeTrendingCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), trendingCursorPrefix) {
		return 0, ErrTrendingCursor
	}
	rank, err := strconv.Atoi(strings.TrimPrefix(string(raw), trendingCursorPrefix))
	if err != nil || rank < 1 {
		return 0, ErrTrendingCursor
	}
	return rank, nil
}

// parseTrendingFilter: "" and "all" are the whole list, "rising" the rising
// sounds.
func parseTrendingFilter(filter string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "", "all":
		return false, nil
	case "rising":
		return true, nil
	}
	return false, ErrTrendingFilter
}

// List is GET /v1/audio/trending for a signed-in viewer. rank is the
// sound's place in the whole ranking, on the rising list too.
func (t *TrendingSounds) List(ctx context.Context, viewerID uuid.UUID, filter, cursor string, limit int) (*TrendingPage, error) {
	if t == nil || t.store == nil {
		return nil, fmt.Errorf("%w: %v", delivery.ErrDeliveryUnresolved, ErrTrendingUnavailable)
	}
	if viewerID == uuid.Nil {
		return nil, delivery.ErrDeliveryDenied
	}
	rising, err := parseTrendingFilter(filter)
	if err != nil {
		return nil, err
	}
	after, err := decodeTrendingCursor(cursor)
	if err != nil {
		return nil, err
	}
	limit = NormalizeTrendingLimit(limit)

	page := &TrendingPage{Items: make([]TrendingItem, 0, limit)}
	run, err := t.store.LatestTrendingRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("read trending run: %w", err)
	}
	if run != nil {
		at := run.ComputedAt.UTC()
		page.UpdatedAt = &at
	}

	fetch := 2 * limit
	more := false
	last := after
	now := t.now()
	for round := 0; round < trendingReadRounds; round++ {
		batch, err := t.store.ListTrendingSounds(ctx, rising, last, fetch)
		if err != nil {
			return nil, fmt.Errorf("list trending sounds: %w", err)
		}
		if len(batch) == 0 {
			more = false
			break
		}
		keep, err := t.visibleTo(ctx, viewerID, batch, now)
		if err != nil {
			return nil, err
		}
		full := false
		for i, e := range batch {
			last = e.Rank
			if keep[i] {
				page.Items = append(page.Items, TrendingItem{
					Rank: e.Rank, Sound: e.Track, Uses7d: e.Uses7d, UsesPrev7d: e.UsesPrev7d, Rising: e.Rising,
				})
			}
			if len(page.Items) == limit {
				full = true
				more = i < len(batch)-1 || len(batch) == fetch
				break
			}
		}
		if full {
			break
		}
		more = len(batch) == fetch
		if !more {
			break
		}
	}
	if more {
		page.NextCursor = encodeTrendingCursor(last)
	}
	return page, nil
}

// visibleTo reports, per row, whether viewerID gets it: a library sound
// still on offer, no block either way with its creator, and a sound the
// viewer may hear (the source follows its video, asked as the viewer).
func (t *TrendingSounds) visibleTo(ctx context.Context, viewerID uuid.UUID, batch []postgres.TrendingEntry, now time.Time) ([]bool, error) {
	creators := make([]uuid.UUID, 0, len(batch))
	seen := map[uuid.UUID]bool{}
	for _, e := range batch {
		if c := e.Track.CreatorUserID; c != nil && *c != uuid.Nil && *c != viewerID && !seen[*c] {
			seen[*c] = true
			creators = append(creators, *c)
		}
	}
	blocked := map[uuid.UUID]bool{}
	if len(creators) > 0 {
		if t.blocks == nil {
			return nil, fmt.Errorf("%w: no block lookup wired", delivery.ErrDeliveryUnresolved)
		}
		var err error
		blocked, err = t.blocks.BlockedAny(ctx, viewerID, creators)
		if err != nil {
			return nil, fmt.Errorf("%w: blocked-any: %v", delivery.ErrDeliveryUnresolved, err)
		}
	}

	keep := make([]bool, len(batch))
	decided := map[uuid.UUID]bool{} // per source video
	for i := range batch {
		track := &batch[i].Track
		if c := track.CreatorUserID; c != nil && blocked[*c] {
			continue
		}
		if track.IsLibrarySound() && !libraryTrackEligible(batch[i].Library, now) {
			continue
		}
		var source uuid.UUID
		if track.SourceMediaID != nil {
			source = *track.SourceMediaID
		}
		if ok, known := decided[source]; known && source != uuid.Nil {
			keep[i] = ok
			continue
		}
		ok, err := t.admit(ctx, viewerID, track)
		if err != nil {
			return nil, err
		}
		if source != uuid.Nil {
			decided[source] = ok
		}
		keep[i] = ok
	}
	return keep, nil
}

// Badge is `trending` on the sound page: the sound's place in the stored
// ranking, or nil when it has none (or a library sound no longer offered).
// The page itself has already decided the viewer may hear the sound.
func (t *TrendingSounds) Badge(ctx context.Context, audioID uuid.UUID) (*TrendingBadge, error) {
	if t == nil || t.store == nil {
		return nil, ErrTrendingUnavailable
	}
	e, err := t.store.TrendingSoundByID(ctx, audioID)
	if err != nil || e == nil {
		return nil, err
	}
	if e.Track.Status != "ready" {
		return nil, nil
	}
	if e.Track.IsLibrarySound() && !libraryTrackEligible(e.Library, t.now()) {
		return nil, nil
	}
	return &TrendingBadge{Rank: e.Rank, Rising: e.Rising}, nil
}

// ErrSoundNotFound: no sound with that id.
var ErrSoundNotFound = errors.New("sound not found")

// SoundExtras is what post-service adds to the sound of its sound page
// (GET /v1/posts/by-sound/:id): the badge and the cover.
type SoundExtras struct {
	Trending *TrendingBadge `json:"trending"`
	CoverURL *string        `json:"cover_url"`
}

// Extras is GET /v1/media/internal/sounds/:audioId/extras. The caller
// (post-service) has already decided its viewer may hear the sound; this
// answers no audience question. A badge that cannot be read is null; a
// missing sound is ErrSoundNotFound.
func (t *TrendingSounds) Extras(ctx context.Context, audioID uuid.UUID) (*SoundExtras, error) {
	if t == nil || t.store == nil {
		return nil, ErrTrendingUnavailable
	}
	track, err := t.store.GetAudioTrack(ctx, audioID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && track == nil) {
		return nil, ErrSoundNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load sound: %w", err)
	}
	out := &SoundExtras{CoverURL: track.CoverURL()}
	badge, err := t.Badge(ctx, audioID)
	if err != nil {
		t.log.Warn("trending sounds: badge unavailable", "audio_track_id", audioID, "error", err)
	} else {
		out.Trending = badge
	}
	return out, nil
}
