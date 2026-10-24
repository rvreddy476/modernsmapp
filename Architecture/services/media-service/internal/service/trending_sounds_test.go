package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Trending sounds (2026-10-24): the numbers, which sounds may trend, the
// leader-safe run, and the per-viewer read. The SQL is
// store/postgres/trending_sounds_integration_test.go; the wire is
// http/trending_sounds_handler_test.go.

var tsNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// ── fakes ───────────────────────────────────────────────────────────────

type fakeTrendingStore struct {
	candidates map[uuid.UUID]postgres.TrendingCandidate
	candErr    error
	rows       []postgres.TrendingEntry
	run        *postgres.TrendingRun
	runErr     error
	listErr    error
	replaced   [][]postgres.TrendingSound
	replaceErr error
	leaseTaken bool
	tracks     map[uuid.UUID]*postgres.AudioTrack
	listCalls  int
}

func (f *fakeTrendingStore) TrendingCandidates(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.TrendingCandidate, error) {
	if f.candErr != nil {
		return nil, f.candErr
	}
	out := map[uuid.UUID]postgres.TrendingCandidate{}
	for _, id := range ids {
		if c, ok := f.candidates[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

func (f *fakeTrendingStore) ReplaceTrendingSounds(_ context.Context, at time.Time, ranked []postgres.TrendingSound) error {
	if f.replaceErr != nil {
		return f.replaceErr
	}
	f.replaced = append(f.replaced, ranked)
	f.run = &postgres.TrendingRun{ComputedAt: at, SoundCount: len(ranked)}
	return nil
}

func (f *fakeTrendingStore) ListTrendingSounds(_ context.Context, rising bool, after, limit int) ([]postgres.TrendingEntry, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []postgres.TrendingEntry
	for _, e := range f.rows {
		if e.Rank <= after || (rising && !e.Rising) {
			continue
		}
		out = append(out, e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeTrendingStore) TrendingSoundByID(_ context.Context, id uuid.UUID) (*postgres.TrendingEntry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	for i := range f.rows {
		if f.rows[i].AudioTrackID == id {
			e := f.rows[i]
			return &e, nil
		}
	}
	return nil, nil
}

func (f *fakeTrendingStore) LatestTrendingRun(context.Context) (*postgres.TrendingRun, error) {
	return f.run, f.runErr
}

func (f *fakeTrendingStore) WithTrendingLease(ctx context.Context, fn func(context.Context) error) (bool, error) {
	if f.leaseTaken {
		return false, nil
	}
	return true, fn(ctx)
}

func (f *fakeTrendingStore) GetAudioTrack(_ context.Context, id uuid.UUID) (*postgres.AudioTrack, error) {
	if t, ok := f.tracks[id]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, pgx.ErrNoRows
}

// fakeAudience answers admitToTrack from a per-source rule.
type fakeAudience struct {
	rule  func(viewer uuid.UUID, track *postgres.AudioTrack) error
	asked []uuid.UUID // the viewer of every question
}

func (a *fakeAudience) admitToTrack(_ context.Context, viewer uuid.UUID, track *postgres.AudioTrack) error {
	a.asked = append(a.asked, viewer)
	if track.IsLibrarySound() {
		if track.Status != "ready" {
			return delivery.ErrDeliveryDenied
		}
		return nil
	}
	return a.rule(viewer, track)
}

type fakeUses struct {
	pages map[uuid.UUID]*SoundUsesPage // by `after`
	err   error
	calls int
}

func (u *fakeUses) SoundUses(_ context.Context, after uuid.UUID) (*SoundUsesPage, error) {
	u.calls++
	if u.err != nil {
		return nil, u.err
	}
	if p, ok := u.pages[after]; ok {
		return p, nil
	}
	return &SoundUsesPage{AsOf: tsNow}, nil
}

type fakeBlocks struct {
	blocked map[uuid.UUID]bool
	err     error
	asked   [][]uuid.UUID
	user    uuid.UUID
}

func (b *fakeBlocks) BlockedAny(_ context.Context, user uuid.UUID, candidates []uuid.UUID) (map[uuid.UUID]bool, error) {
	b.user = user
	b.asked = append(b.asked, append([]uuid.UUID(nil), candidates...))
	if b.err != nil {
		return nil, b.err
	}
	out := map[uuid.UUID]bool{}
	for _, c := range candidates {
		if b.blocked[c] {
			out[c] = true
		}
	}
	return out, nil
}

func newTestTrending(store *fakeTrendingStore, uses SoundUsesSource, aud *fakeAudience, blocks BlockChecker) *TrendingSounds {
	t := &TrendingSounds{store: store, uses: uses, blocks: blocks, now: func() time.Time { return tsNow },
		interval: TrendingSoundsInterval, log: slog.Default()}
	if aud != nil {
		t.audience = aud
	}
	return t
}

func days(vals ...int) []int {
	d := make([]int, trendingDays)
	copy(d, vals)
	return d
}

// ── the numbers ─────────────────────────────────────────────────────────

func TestSoundStatsWeightsAndWindows(t *testing.T) {
	for _, tc := range []struct {
		name              string
		daily             []int
		uses, prev, score int
		rising            bool
	}{
		// day 0 x3, days 1-2 x2, days 3-6 x1; days 7-13 are the week before.
		{"weights", days(2, 1, 1, 1, 1, 1, 1, 5), 8, 5, 2*3 + 2*2 + 4, false},
		{"only day 0", days(4), 4, 0, 12, true},
		{"only day 1", days(0, 4), 4, 0, 8, true},
		{"only day 2", days(0, 0, 4), 4, 0, 8, true},
		{"only day 3", days(0, 0, 0, 4), 4, 0, 4, true},
		{"only day 6", days(0, 0, 0, 0, 0, 0, 4), 4, 0, 4, true},
		{"day 7 is last week", days(0, 0, 0, 0, 0, 0, 0, 4), 0, 4, 0, false},
		{"day 13 is last week", days(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4), 0, 4, 0, false},
		{"rising: doubled", days(3, 0, 0, 0, 0, 0, 0, 1), 3, 1, 9, true},
		{"rising: exactly double", days(4, 0, 0, 0, 0, 0, 0, 2), 4, 2, 12, true},
		{"not rising: under double", days(3, 0, 0, 0, 0, 0, 0, 2), 3, 2, 9, false},
		{"not rising: under the minimum", days(2), 2, 0, 6, false},
		{"rising at the minimum, nothing before", days(1, 1, 1), 3, 0, 7, true},
		{"negative and short input", []int{-5, 3}, 3, 0, 6, true},
	} {
		st := soundStats(tc.daily)
		if st.Uses7d != tc.uses || st.UsesPrev7d != tc.prev || st.Score != tc.score || st.Rising != tc.rising {
			t.Errorf("%s: got %+v, want uses %d prev %d score %d rising %v", tc.name, st, tc.uses, tc.prev, tc.score, tc.rising)
		}
	}
}

func TestRankSoundsMinimumEligibilityAndOrder(t *testing.T) {
	a, b, c, d, e, f := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	stats := map[uuid.UUID]SoundStats{
		a: {Uses7d: 3, Score: 9},
		b: {Uses7d: 2, Score: 50}, // under the minimum
		c: {Uses7d: 5, Score: 5},  // ties e on score, more uses
		d: {Uses7d: 9, Score: 99}, // not eligible
		e: {Uses7d: 4, Score: 5},  // ties c, fewer uses
		f: {Uses7d: 3, Score: 20, Rising: true},
	}
	eligible := map[uuid.UUID]bool{a: true, b: true, c: true, e: true, f: true}
	ranked := rankSounds(stats, eligible)
	want := []uuid.UUID{f, a, c, e}
	if len(ranked) != len(want) {
		t.Fatalf("ranked %d sounds, want %d: %+v", len(ranked), len(want), ranked)
	}
	for i, id := range want {
		if ranked[i].AudioTrackID != id || ranked[i].Rank != i+1 {
			t.Fatalf("position %d: %+v, want %s at rank %d", i, ranked[i], id, i+1)
		}
	}
	if !ranked[0].Rising || ranked[0].Score != 20 || ranked[0].Uses7d != 3 {
		t.Fatalf("the stored row lost its numbers: %+v", ranked[0])
	}

	// Equal score and uses: the id decides, so a run is deterministic.
	x, y := uuid.MustParse("00000000-0000-4000-8000-000000000001"), uuid.MustParse("00000000-0000-4000-8000-000000000002")
	tie := rankSounds(map[uuid.UUID]SoundStats{y: {Uses7d: 3, Score: 3}, x: {Uses7d: 3, Score: 3}}, map[uuid.UUID]bool{x: true, y: true})
	if tie[0].AudioTrackID != x || tie[1].AudioTrackID != y {
		t.Fatalf("tie order %v", tie)
	}
}

func TestRankSoundsIsCapped(t *testing.T) {
	stats := map[uuid.UUID]SoundStats{}
	eligible := map[uuid.UUID]bool{}
	for i := 0; i < MaxTrendingSounds+5; i++ {
		id := uuid.New()
		stats[id] = SoundStats{Uses7d: 3 + i, Score: 3 + i}
		eligible[id] = true
	}
	ranked := rankSounds(stats, eligible)
	if len(ranked) != MaxTrendingSounds || ranked[len(ranked)-1].Rank != MaxTrendingSounds {
		t.Fatalf("stored %d rows, last rank %d", len(ranked), ranked[len(ranked)-1].Rank)
	}
}

func TestLibraryTrackEligible(t *testing.T) {
	past, future := tsNow.Add(-time.Hour), tsNow.Add(time.Hour)
	for _, tc := range []struct {
		name string
		f    postgres.LibraryFacts
		want bool
	}{
		{"active, no expiry", postgres.LibraryFacts{Present: true, IsActive: true}, true},
		{"active, licence ahead", postgres.LibraryFacts{Present: true, IsActive: true, LicenseExpiresAt: &future}, true},
		{"licence expired", postgres.LibraryFacts{Present: true, IsActive: true, LicenseExpiresAt: &past}, false},
		{"licence expires now", postgres.LibraryFacts{Present: true, IsActive: true, LicenseExpiresAt: &tsNow}, false},
		{"inactive", postgres.LibraryFacts{Present: true}, false},
		{"withdrawn", postgres.LibraryFacts{Present: true, IsActive: true, WithdrawnAt: &past}, false},
		{"library row gone", postgres.LibraryFacts{IsActive: true}, false},
	} {
		if got := libraryTrackEligible(tc.f, tsNow); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ── the run ─────────────────────────────────────────────────────────────

type runWorld struct {
	store                                     *fakeTrendingStore
	uses                                      *fakeUses
	aud                                       *fakeAudience
	public, sameSource, private, lib, expired uuid.UUID
	withdrawn, unready, few, orphan, missing  uuid.UUID
	publicMedia, privateMedia                 uuid.UUID
}

func newRunWorld() *runWorld {
	w := &runWorld{
		public: uuid.New(), sameSource: uuid.New(), private: uuid.New(), lib: uuid.New(), expired: uuid.New(),
		withdrawn: uuid.New(), unready: uuid.New(), few: uuid.New(), orphan: uuid.New(), missing: uuid.New(),
		publicMedia: uuid.New(), privateMedia: uuid.New(),
	}
	past := tsNow.Add(-time.Hour)
	extracted := func(id uuid.UUID, media *uuid.UUID, status string) postgres.TrendingCandidate {
		return postgres.TrendingCandidate{Track: postgres.AudioTrack{ID: id, SourceMediaID: media, Status: status}}
	}
	library := func(id uuid.UUID, f postgres.LibraryFacts) postgres.TrendingCandidate {
		lib := uuid.New()
		return postgres.TrendingCandidate{Track: postgres.AudioTrack{ID: id, LibraryID: &lib, Status: "ready"}, Library: f}
	}
	w.store = &fakeTrendingStore{candidates: map[uuid.UUID]postgres.TrendingCandidate{
		w.public:     extracted(w.public, &w.publicMedia, "ready"),
		w.sameSource: extracted(w.sameSource, &w.publicMedia, "ready"),
		w.private:    extracted(w.private, &w.privateMedia, "ready"),
		w.lib:        library(w.lib, postgres.LibraryFacts{Present: true, IsActive: true}),
		w.expired:    library(w.expired, postgres.LibraryFacts{Present: true, IsActive: true, LicenseExpiresAt: &past}),
		w.withdrawn:  library(w.withdrawn, postgres.LibraryFacts{Present: true, WithdrawnAt: &past}),
		w.unready:    extracted(w.unready, &w.publicMedia, "processing"),
		w.few:        extracted(w.few, &w.publicMedia, "ready"),
		w.orphan:     extracted(w.orphan, nil, "ready"),
	}}
	second := uuid.New()
	w.uses = &fakeUses{pages: map[uuid.UUID]*SoundUsesPage{
		uuid.Nil: {AsOf: tsNow, NextAfter: &second, Sounds: []SoundDailyUses{
			{w.public, days(3, 1)}, {w.sameSource, days(1, 1, 1)}, {w.private, days(9)}, {w.lib, days(1, 1, 1, 1)},
			{w.expired, days(5)},
		}},
		second: {AsOf: tsNow, Sounds: []SoundDailyUses{
			{w.withdrawn, days(5)}, {w.unready, days(5)}, {w.few, days(2)}, {w.orphan, days(5)}, {w.missing, days(5)},
		}},
	}}
	w.aud = &fakeAudience{rule: func(viewer uuid.UUID, track *postgres.AudioTrack) error {
		if track.SourceMediaID == nil {
			return delivery.ErrDeliveryDenied
		}
		if *track.SourceMediaID == w.publicMedia {
			return nil
		}
		return delivery.ErrDeliveryDenied // the private source plays for nobody signed out
	}}
	return w
}

func TestRunRanksOnlySoundsEveryoneMayHear(t *testing.T) {
	w := newRunWorld()
	ran, err := newTestTrending(w.store, w.uses, w.aud, nil).RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("ran %v err %v", ran, err)
	}
	if w.uses.calls != 2 {
		t.Fatalf("read %d pages of uses, want both", w.uses.calls)
	}
	if len(w.store.replaced) != 1 {
		t.Fatalf("stored %d runs", len(w.store.replaced))
	}
	got := map[uuid.UUID]postgres.TrendingSound{}
	for _, r := range w.store.replaced[0] {
		got[r.AudioTrackID] = r
	}
	for _, id := range []uuid.UUID{w.public, w.sameSource, w.lib} {
		if _, ok := got[id]; !ok {
			t.Errorf("sound %s missing from the ranking", id)
		}
	}
	for name, id := range map[string]uuid.UUID{
		"private source": w.private, "expired library track": w.expired, "withdrawn library track": w.withdrawn,
		"unready": w.unready, "two uses": w.few, "no source": w.orphan, "no row": w.missing,
	} {
		if _, ok := got[id]; ok {
			t.Errorf("%s sound is ranked", name)
		}
	}
	if len(got) != 3 || got[w.public].Rank != 1 || got[w.public].Score != 11 {
		t.Errorf("ranking %+v", w.store.replaced[0])
	}
	for _, v := range w.aud.asked {
		if v != uuid.Nil {
			t.Fatalf("the run asked the audience as %s, want the signed-out stranger", v)
		}
	}
	// public and sameSource share a source: asked once; private once.
	if len(w.aud.asked) != 2 {
		t.Errorf("audience asked %d times, want once per source (2)", len(w.aud.asked))
	}
	if w.store.run == nil || !w.store.run.ComputedAt.Equal(tsNow) {
		t.Errorf("run not recorded: %+v", w.store.run)
	}
}

// Every way a run cannot be made keeps the stored ranking.
func TestRunFailsClosed(t *testing.T) {
	for name, breakIt := range map[string]func(w *runWorld){
		"uses unavailable": func(w *runWorld) { w.uses.err = errors.New("post-service 503") },
		"audience unresolved": func(w *runWorld) {
			w.aud.rule = func(uuid.UUID, *postgres.AudioTrack) error { return delivery.ErrDeliveryUnresolved }
		},
		"audience fault": func(w *runWorld) {
			w.aud.rule = func(uuid.UUID, *postgres.AudioTrack) error { return errors.New("boom") }
		},
		"candidates unreadable": func(w *runWorld) { w.store.candErr = errors.New("db down") },
		"cursor stuck": func(w *runWorld) {
			stuck := uuid.New()
			w.uses.pages = map[uuid.UUID]*SoundUsesPage{
				uuid.Nil: {AsOf: tsNow, NextAfter: &stuck},
				stuck:    {AsOf: tsNow, NextAfter: &stuck},
			}
		},
		"store write fails": func(w *runWorld) { w.store.replaceErr = errors.New("db down") },
	} {
		t.Run(name, func(t *testing.T) {
			w := newRunWorld()
			breakIt(w)
			ran, err := newTestTrending(w.store, w.uses, w.aud, nil).RunOnce(context.Background())
			if err == nil || ran {
				t.Fatalf("ran %v err %v, want an error", ran, err)
			}
			if len(w.store.replaced) != 0 {
				t.Fatalf("a ranking was stored: %+v", w.store.replaced)
			}
			// A cursor that does not move stops at once, not at the page
			// ceiling.
			if name == "cursor stuck" && w.uses.calls != 2 {
				t.Fatalf("a stuck cursor was read %d times", w.uses.calls)
			}
		})
	}
}

func TestRunIsLeaderSafe(t *testing.T) {
	w := newRunWorld()
	w.store.leaseTaken = true
	if ran, err := newTestTrending(w.store, w.uses, w.aud, nil).RunOnce(context.Background()); ran || err != nil {
		t.Fatalf("another replica holds the lease: ran %v err %v", ran, err)
	}
	if w.uses.calls != 0 {
		t.Fatalf("post-service asked %d times without the lease", w.uses.calls)
	}

	w = newRunWorld()
	w.store.run = &postgres.TrendingRun{ComputedAt: tsNow.Add(-5 * time.Minute)}
	if ran, err := newTestTrending(w.store, w.uses, w.aud, nil).RunOnce(context.Background()); ran || err != nil {
		t.Fatalf("a fresh run exists: ran %v err %v", ran, err)
	}
	if w.uses.calls != 0 || len(w.store.replaced) != 0 {
		t.Fatalf("a fresh run was repeated")
	}

	w = newRunWorld()
	w.store.run = &postgres.TrendingRun{ComputedAt: tsNow.Add(-TrendingSoundsInterval + trendingFreshness)}
	if ran, err := newTestTrending(w.store, w.uses, w.aud, nil).RunOnce(context.Background()); !ran || err != nil {
		t.Fatalf("a run one interval old: ran %v err %v", ran, err)
	}

	w = newRunWorld()
	w.store.runErr = errors.New("db down")
	if _, err := newTestTrending(w.store, w.uses, w.aud, nil).RunOnce(context.Background()); err == nil {
		t.Fatal("an unreadable last run must not be read as none")
	}

	if _, err := newTestTrending(w.store, nil, w.aud, nil).RunOnce(context.Background()); !errors.Is(err, ErrTrendingUnavailable) {
		t.Fatalf("no uses source: err %v", err)
	}
}

// ── the read ────────────────────────────────────────────────────────────

type readWorld struct {
	store   *fakeTrendingStore
	aud     *fakeAudience
	blocks  *fakeBlocks
	viewer  uuid.UUID
	ids     []uuid.UUID // by rank, 1-based at index 0
	creator []uuid.UUID
	media   []uuid.UUID
	denied  map[uuid.UUID]bool // sources the viewer may not hear
}

// newReadWorld stores n sounds ranked 1..n, each over its own source by its
// own creator; even ranks are rising.
func newReadWorld(n int) *readWorld {
	w := &readWorld{viewer: uuid.New(), denied: map[uuid.UUID]bool{}, blocks: &fakeBlocks{blocked: map[uuid.UUID]bool{}}}
	w.store = &fakeTrendingStore{run: &postgres.TrendingRun{ComputedAt: tsNow.Add(-3 * time.Minute), SoundCount: n}}
	for i := 1; i <= n; i++ {
		id, creator, media := uuid.New(), uuid.New(), uuid.New()
		w.ids, w.creator, w.media = append(w.ids, id), append(w.creator, creator), append(w.media, media)
		w.store.rows = append(w.store.rows, postgres.TrendingEntry{
			TrendingSound: postgres.TrendingSound{AudioTrackID: id, Rank: i, Score: 100 - i, Uses7d: 10 * i, UsesPrev7d: i, Rising: i%2 == 0},
			Track:         postgres.AudioTrack{ID: id, SourceMediaID: &w.media[i-1], CreatorUserID: &w.creator[i-1], Status: "ready", Title: fmt.Sprint("sound ", i)},
		})
	}
	w.aud = &fakeAudience{rule: func(viewer uuid.UUID, track *postgres.AudioTrack) error {
		if w.denied[*track.SourceMediaID] {
			return delivery.ErrDeliveryDenied
		}
		return nil
	}}
	return w
}

func (w *readWorld) list(t *testing.T, filter, cursor string, limit int) *TrendingPage {
	t.Helper()
	page, err := newTestTrending(w.store, nil, w.aud, w.blocks).List(context.Background(), w.viewer, filter, cursor, limit)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return page
}

func ranks(p *TrendingPage) []int {
	out := make([]int, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it.Rank)
	}
	return out
}

func wantRanks(t *testing.T, what string, got []int, want ...int) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(append([]int{}, want...)) {
		t.Fatalf("%s: ranks %v, want %v", what, got, want)
	}
}

func TestTrendingListPages(t *testing.T) {
	w := newReadWorld(5)
	first := w.list(t, "", "", 2)
	wantRanks(t, "first page", ranks(first), 1, 2)
	if first.NextCursor == "" || first.UpdatedAt == nil || !first.UpdatedAt.Equal(tsNow.Add(-3*time.Minute)) {
		t.Fatalf("first page meta: cursor %q updated %v", first.NextCursor, first.UpdatedAt)
	}
	it := first.Items[1]
	if it.Sound.ID != w.ids[1] || it.Uses7d != 20 || it.UsesPrev7d != 2 || !it.Rising {
		t.Fatalf("item %+v", it)
	}
	second := w.list(t, "all", first.NextCursor, 2)
	wantRanks(t, "second page", ranks(second), 3, 4)
	last := w.list(t, "all", second.NextCursor, 2)
	wantRanks(t, "last page", ranks(last), 5)
	if last.NextCursor != "" {
		t.Fatalf("the last page names a next cursor %q", last.NextCursor)
	}
	// A page exactly at the end may name a cursor whose page is empty.
	exact := w.list(t, "", "", 5)
	wantRanks(t, "everything", ranks(exact), 1, 2, 3, 4, 5)
	if exact.NextCursor != "" {
		t.Fatalf("a page that read the whole list names a cursor %q", exact.NextCursor)
	}
	// A page filled exactly at a full batch may name a cursor whose page is
	// empty; that page names none.
	full := w.list(t, "", "", 1)
	after := w.list(t, "", encodeTrendingCursor(5), 1)
	wantRanks(t, "after the end", ranks(after))
	if full.NextCursor == "" || after.NextCursor != "" {
		t.Fatalf("cursors: full %q, after the end %q", full.NextCursor, after.NextCursor)
	}
}

func TestTrendingListRisingKeepsTheGlobalRank(t *testing.T) {
	w := newReadWorld(6)
	page := w.list(t, "rising", "", 20)
	wantRanks(t, "rising", ranks(page), 2, 4, 6)
	for _, it := range page.Items {
		if !it.Rising {
			t.Fatalf("a sound that is not rising is on the rising list: %+v", it)
		}
	}
	page = w.list(t, "RISING", "", 1)
	next := w.list(t, "rising", page.NextCursor, 1)
	wantRanks(t, "rising, second page", ranks(next), 4)
}

func TestTrendingListSkipsBlockedCreators(t *testing.T) {
	w := newReadWorld(4)
	// Rank 2's creator blocked the viewer or the viewer blocked them: graph
	// answers either direction as one list.
	w.blocks.blocked[w.creator[1]] = true
	// The viewer's own sound is never a block question.
	w.store.rows[3].Track.CreatorUserID = &w.viewer
	page := w.list(t, "", "", 20)
	wantRanks(t, "blocked creator skipped", ranks(page), 1, 3, 4)
	if w.blocks.user != w.viewer || len(w.blocks.asked) != 1 || len(w.blocks.asked[0]) != 3 {
		t.Fatalf("blocked-any asked as %s about %v", w.blocks.user, w.blocks.asked)
	}
	for _, c := range w.blocks.asked[0] {
		if c == w.viewer {
			t.Fatal("the viewer was asked about themselves")
		}
	}
}

func TestTrendingListBlockLookupFailsClosed(t *testing.T) {
	w := newReadWorld(3)
	w.blocks.err = errors.New("graph down")
	_, err := newTestTrending(w.store, nil, w.aud, w.blocks).List(context.Background(), w.viewer, "", "", 20)
	if !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("err %v, want unresolved", err)
	}
	_, err = newTestTrending(w.store, nil, w.aud, nil).List(context.Background(), w.viewer, "", "", 20)
	if !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("no block lookup wired: err %v, want unresolved", err)
	}
}

func TestTrendingListRechecksTheSoundForTheViewer(t *testing.T) {
	w := newReadWorld(4)
	w.denied[w.media[0]] = true // went private after the run
	past := tsNow.Add(-time.Minute)
	lib := uuid.New()
	w.store.rows[2].Track.LibraryID, w.store.rows[2].Track.SourceMediaID, w.store.rows[2].Track.CreatorUserID = &lib, nil, nil
	w.store.rows[2].Library = postgres.LibraryFacts{Present: true, IsActive: true, LicenseExpiresAt: &past} // licence lapsed since
	page := w.list(t, "", "", 20)
	wantRanks(t, "re-checked", ranks(page), 2, 4)
	for _, v := range w.aud.asked {
		if v != w.viewer {
			t.Fatalf("the read asked as %s, want the viewer", v)
		}
	}

	w = newReadWorld(2)
	w.aud.rule = func(uuid.UUID, *postgres.AudioTrack) error { return delivery.ErrDeliveryUnresolved }
	if _, err := newTestTrending(w.store, nil, w.aud, w.blocks).List(context.Background(), w.viewer, "", "", 20); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("audience unresolved: err %v", err)
	}
}

// A page filled past sounds the viewer may not have reads further batches,
// and stops after a bounded number of them.
func TestTrendingListFillsAcrossBatches(t *testing.T) {
	w := newReadWorld(10)
	for i := 0; i < 4; i++ {
		w.blocks.blocked[w.creator[i]] = true
	}
	page := w.list(t, "", "", 1) // two rows a batch
	wantRanks(t, "filled from the third batch", ranks(page), 5)
	if page.NextCursor == "" {
		t.Fatal("more rows remain but no cursor")
	}

	w = newReadWorld(30)
	for i := 0; i < 30; i++ {
		w.blocks.blocked[w.creator[i]] = true
	}
	page = w.list(t, "", "", 1)
	if len(page.Items) != 0 || page.NextCursor == "" || w.store.listCalls != trendingReadRounds {
		t.Fatalf("items %d cursor %q batches %d", len(page.Items), page.NextCursor, w.store.listCalls)
	}
	rank, err := decodeTrendingCursor(page.NextCursor)
	if err != nil || rank != 2*trendingReadRounds {
		t.Fatalf("cursor continues after rank %d (%v), want %d", rank, err, 2*trendingReadRounds)
	}
}

func TestTrendingListRefusals(t *testing.T) {
	w := newReadWorld(2)
	svc := newTestTrending(w.store, nil, w.aud, w.blocks)
	ctx := context.Background()
	if _, err := svc.List(ctx, w.viewer, "hot", "", 20); !errors.Is(err, ErrTrendingFilter) {
		t.Errorf("bad filter: %v", err)
	}
	for _, c := range []string{"nope", encodeTrendingCursor(0), "cjE6eA"} {
		if _, err := svc.List(ctx, w.viewer, "", c, 20); !errors.Is(err, ErrTrendingCursor) {
			t.Errorf("cursor %q: %v", c, err)
		}
	}
	if _, err := svc.List(ctx, uuid.Nil, "", "", 20); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Errorf("signed out: %v", err)
	}
	w.store.listErr = errors.New("db down")
	if _, err := svc.List(ctx, w.viewer, "", "", 20); err == nil {
		t.Error("a store fault answered a page")
	}
	var nilSvc *TrendingSounds
	if _, err := nilSvc.List(ctx, w.viewer, "", "", 20); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Errorf("unwired: %v", err)
	}
}

func TestTrendingListLimitsAndEmpty(t *testing.T) {
	if NormalizeTrendingLimit(0) != 20 || NormalizeTrendingLimit(-3) != 20 || NormalizeTrendingLimit(51) != 50 || NormalizeTrendingLimit(7) != 7 {
		t.Fatal("limit normalization")
	}
	w := newReadWorld(60)
	if got := len(w.list(t, "", "", 500).Items); got != MaxTrendingPage {
		t.Fatalf("limit 500 answered %d", got)
	}
	if got := len(w.list(t, "", "", 0).Items); got != DefaultTrendingPage {
		t.Fatalf("no limit answered %d", got)
	}
	empty := newReadWorld(0)
	empty.store.run = nil
	page := empty.list(t, "", "", 20)
	if page.Items == nil || len(page.Items) != 0 || page.UpdatedAt != nil || page.NextCursor != "" {
		t.Fatalf("before the first run: %+v", page)
	}
}

func TestTrendingCursorRoundTrip(t *testing.T) {
	for _, r := range []int{1, 7, 1000} {
		got, err := decodeTrendingCursor(encodeTrendingCursor(r))
		if err != nil || got != r {
			t.Errorf("rank %d: %d %v", r, got, err)
		}
	}
	if got, err := decodeTrendingCursor(""); got != 0 || err != nil {
		t.Errorf("empty cursor: %d %v", got, err)
	}
}

// ── the sound page ──────────────────────────────────────────────────────

func TestTrendingBadge(t *testing.T) {
	w := newReadWorld(3)
	svc := newTestTrending(w.store, nil, w.aud, w.blocks)
	ctx := context.Background()
	badge, err := svc.Badge(ctx, w.ids[1])
	if err != nil || badge == nil || badge.Rank != 2 || !badge.Rising {
		t.Fatalf("badge %+v err %v", badge, err)
	}
	if badge, err := svc.Badge(ctx, uuid.New()); badge != nil || err != nil {
		t.Fatalf("an unranked sound: %+v %v", badge, err)
	}
	past := tsNow.Add(-time.Minute)
	lib := uuid.New()
	w.store.rows[0].Track.LibraryID = &lib
	w.store.rows[0].Library = postgres.LibraryFacts{Present: true, IsActive: true, LicenseExpiresAt: &past}
	if badge, _ := svc.Badge(ctx, w.ids[0]); badge != nil {
		t.Fatalf("an expired library track kept its badge: %+v", badge)
	}
	w.store.rows[2].Track.Status = "deleted"
	if badge, _ := svc.Badge(ctx, w.ids[2]); badge != nil {
		t.Fatalf("a deleted sound kept its badge: %+v", badge)
	}
}

func TestSoundExtras(t *testing.T) {
	w := newReadWorld(2)
	cover := "https://cdn.example.com/art.jpg"
	lib := uuid.New()
	w.store.tracks = map[uuid.UUID]*postgres.AudioTrack{
		w.ids[1]: {ID: w.ids[1], SourceMediaID: &w.media[1], Status: "ready"},
	}
	libID := uuid.New()
	w.store.tracks[libID] = &postgres.AudioTrack{ID: libID, LibraryID: &lib, LibraryCoverURL: &cover, Status: "ready"}
	svc := newTestTrending(w.store, nil, w.aud, w.blocks)
	ctx := context.Background()

	got, err := svc.Extras(ctx, w.ids[1])
	if err != nil || got.Trending == nil || got.Trending.Rank != 2 || got.CoverURL == nil ||
		*got.CoverURL != "/v1/media/"+w.media[1].String()+"/serve/thumb_150" {
		t.Fatalf("extras %+v err %v", got, err)
	}
	got, err = svc.Extras(ctx, libID)
	if err != nil || got.Trending != nil || got.CoverURL == nil || *got.CoverURL != cover {
		t.Fatalf("library extras %+v err %v", got, err)
	}
	if _, err := svc.Extras(ctx, uuid.New()); !errors.Is(err, ErrSoundNotFound) {
		t.Fatalf("missing sound: %v", err)
	}
	// A badge that cannot be read costs the badge, not the cover.
	w.store.listErr = errors.New("db down")
	got, err = svc.Extras(ctx, w.ids[1])
	if err != nil || got.Trending != nil || got.CoverURL == nil {
		t.Fatalf("badge fault: %+v %v", got, err)
	}
}

func TestSoundCoverURL(t *testing.T) {
	media, lib := uuid.New(), uuid.New()
	httpsArt, keyArt := "https://cdn.example.com/a.jpg", "audio-library/a.jpg"
	for _, tc := range []struct {
		name  string
		track postgres.AudioTrack
		want  string
	}{
		{"extracted", postgres.AudioTrack{SourceMediaID: &media}, "/v1/media/" + media.String() + "/serve/thumb_150"},
		{"library https", postgres.AudioTrack{LibraryID: &lib, LibraryCoverURL: &httpsArt}, httpsArt},
		{"library storage key", postgres.AudioTrack{LibraryID: &lib, LibraryCoverURL: &keyArt}, ""},
		{"library without art", postgres.AudioTrack{LibraryID: &lib, SourceMediaID: &media}, ""},
		{"neither", postgres.AudioTrack{}, ""},
	} {
		got := ""
		if c := tc.track.CoverURL(); c != nil {
			got = *c
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
