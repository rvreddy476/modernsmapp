package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Trending sounds (2026-10-24): what counts as a use of a sound. The SQL
// half (source reel, content kinds, the window) is proved against Postgres
// in sound_uses_integration_test.go; here, the rule every row is judged by
// again, the one-per-author-per-day count and the fail-closed author gate.

var suNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

type fakeSoundUses struct {
	rows  []postgres.SoundUse
	next  *uuid.UUID
	err   error
	calls []soundUsesCall
}

type soundUsesCall struct {
	since, until time.Time
	after        uuid.UUID
	limit        int
}

func (f *fakeSoundUses) SoundUseRows(_ context.Context, since, until time.Time, after uuid.UUID, limit int) ([]postgres.SoundUse, *uuid.UUID, error) {
	f.calls = append(f.calls, soundUsesCall{since, until, after, limit})
	return f.rows, f.next, f.err
}

// use is a public, approved, live use of sound by author, published ago.
func use(sound, author uuid.UUID, ago time.Duration) postgres.SoundUse {
	return postgres.SoundUse{
		SoundID: sound, PostID: uuid.New(), AuthorID: author,
		PublishedAt: suNow.Add(-ago), Visibility: "public", ReviewStatus: "approved",
	}
}

func soundUsesService(store soundUsesStore) *Service {
	return &Service{soundUses: store, now: func() time.Time { return suNow }}
}

func dailyOf(t *testing.T, page *SoundUsesPage, sound uuid.UUID) []int {
	t.Helper()
	for _, s := range page.Sounds {
		if s.AudioTrackID == sound {
			if len(s.DailyUses) != SoundUsesDays {
				t.Fatalf("daily_uses has %d entries, want %d", len(s.DailyUses), SoundUsesDays)
			}
			return s.DailyUses
		}
	}
	return nil
}

func TestSoundUsesCountsOncePerAuthorPerDay(t *testing.T) {
	sound, a, b := uuid.New(), uuid.New(), uuid.New()
	store := &fakeSoundUses{rows: []postgres.SoundUse{
		// a: fifty reels in one day is one use...
		use(sound, a, 1*time.Hour), use(sound, a, 2*time.Hour), use(sound, a, 23*time.Hour),
		// ...and the next day is another.
		use(sound, a, 25*time.Hour),
		// b on day 0 is a second use that day.
		use(sound, b, 3*time.Hour),
		// a in the week before.
		use(sound, a, 8*24*time.Hour),
	}}
	page, err := soundUsesService(store).SoundUses(context.Background(), uuid.Nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := dailyOf(t, page, sound)
	want := make([]int, SoundUsesDays)
	want[0], want[1], want[8] = 2, 1, 1
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("daily uses = %v, want %v", got, want)
		}
	}
	if !page.AsOf.Equal(suNow) || page.Days != SoundUsesDays {
		t.Fatalf("as_of %v days %d", page.AsOf, page.Days)
	}
}

func TestSoundUsesWindowAndPaging(t *testing.T) {
	next := uuid.New()
	after := uuid.New()
	store := &fakeSoundUses{next: &next}
	page, err := soundUsesService(store).SoundUses(context.Background(), after, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("store called %d times", len(store.calls))
	}
	call := store.calls[0]
	if !call.until.Equal(suNow) || !call.since.Equal(suNow.Add(-14*24*time.Hour)) {
		t.Fatalf("window [%v, %v), want the 14 days before %v", call.since, call.until, suNow)
	}
	if call.after != after || call.limit != postgres.MaxSoundUsesPage {
		t.Fatalf("after %v limit %d", call.after, call.limit)
	}
	if page.NextAfter == nil || *page.NextAfter != next {
		t.Fatalf("next_after = %v, want %v", page.NextAfter, next)
	}
	if page.Sounds == nil || len(page.Sounds) != 0 {
		t.Fatalf("a page with no uses must list [] (got %#v)", page.Sounds)
	}
	if NormalizeSoundUsesLimit(0) != DefaultSoundUsesPage || NormalizeSoundUsesLimit(7) != 7 {
		t.Fatal("limit normalization")
	}
}

func TestSoundUseDayBoundaries(t *testing.T) {
	for _, tc := range []struct {
		ago  time.Duration
		want int
	}{
		{0, 0},
		{24*time.Hour - time.Second, 0},
		{24 * time.Hour, 1},
		{3*24*time.Hour - time.Second, 2},
		{3 * 24 * time.Hour, 3},
		{14*24*time.Hour - time.Second, 13},
		{14 * 24 * time.Hour, -1},
		{-time.Minute, -1}, // published after as_of
	} {
		if got := soundUseDay(suNow, suNow.Add(-tc.ago)); got != tc.want {
			t.Errorf("published %v before as_of: day %d, want %d", tc.ago, got, tc.want)
		}
	}
}

// Every way a post is not visible to everybody: none of these is a use.
func TestSoundUsesSkipPostsNotVisibleToEveryone(t *testing.T) {
	now := suNow
	tier := uuid.New()
	cases := map[string]func(u *postgres.SoundUse){
		"private":        func(u *postgres.SoundUse) { u.Visibility = "private" },
		"followers only": func(u *postgres.SoundUse) { u.Visibility = "followers" },
		"unlisted":       func(u *postgres.SoundUse) { u.Visibility = "unlisted" },
		"pending review": func(u *postgres.SoundUse) { u.ReviewStatus = "pending" },
		"held/restricted": func(u *postgres.SoundUse) {
			u.ActiveRestrictionCount = 1 // a copyright title hold or a restriction
		},
		"members-only": func(u *postgres.SoundUse) { u.TierRequiredID = &tier },
		"scheduled":    func(u *postgres.SoundUse) { u.PublishAt = &now },
		"deleted":      func(u *postgres.SoundUse) { u.DeletedAt = &now },
		"18+":          func(u *postgres.SoundUse) { u.AgeRestricted = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sound, kept, author := uuid.New(), uuid.New(), uuid.New()
			bad := use(sound, author, time.Hour)
			mutate(&bad)
			store := &fakeSoundUses{rows: []postgres.SoundUse{bad, use(kept, author, time.Hour)}}
			page, err := soundUsesService(store).SoundUses(context.Background(), uuid.Nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := dailyOf(t, page, sound); got != nil {
				t.Fatalf("%s post counted as a use: %v", name, got)
			}
			if got := dailyOf(t, page, kept); got == nil || got[0] != 1 {
				t.Fatalf("the visible use beside it was lost: %v", got)
			}
		})
	}
}

// A private account's or a hidden (deactivated / pending deletion)
// author's posts are not public uses.
func TestSoundUsesSkipPrivateAndHiddenAuthors(t *testing.T) {
	sound := uuid.New()
	public, private, hidden := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	var seen []canRequest
	graph := fakeGraphCan(t, map[string]bool{public.String(): true, hidden.String(): true}, &calls, &seen)
	defer graph.Close()

	svc := soundUsesService(&fakeSoundUses{rows: []postgres.SoundUse{
		use(sound, public, time.Hour), use(sound, private, time.Hour), use(sound, hidden, time.Hour),
	}})
	svc.graphServiceURL, svc.internalServiceKey, svc.httpClient = graph.URL, "test-key", http.DefaultClient
	svc.hiddenAuthors = &fakeHiddenAuthors{hidden: map[uuid.UUID]bool{hidden: true}}

	page, err := svc.SoundUses(context.Background(), uuid.Nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := dailyOf(t, page, sound); got == nil || got[0] != 1 {
		t.Fatalf("daily uses %v, want only the public author's one use", got)
	}
	if len(seen) == 0 || seen[0].ViewerID != uuid.Nil.String() {
		t.Fatalf("the author gate was not asked as the signed-out stranger: %+v", seen)
	}
}

// The author gate fails closed: an unanswered gate is no page at all, never
// a page in which those authors silently counted for nothing.
func TestSoundUsesUnavailableWhenTheAuthorGateCannotAnswer(t *testing.T) {
	sound := uuid.New()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer graph.Close()
	svc := soundUsesService(&fakeSoundUses{rows: []postgres.SoundUse{use(sound, uuid.New(), time.Hour)}})
	svc.graphServiceURL, svc.internalServiceKey, svc.httpClient = graph.URL, "test-key", http.DefaultClient
	if _, err := svc.SoundUses(context.Background(), uuid.Nil, 0); !errors.Is(err, ErrSoundUsesUnavailable) {
		t.Fatalf("err = %v, want ErrSoundUsesUnavailable", err)
	}

	svc = soundUsesService(&fakeSoundUses{rows: []postgres.SoundUse{use(sound, uuid.New(), time.Hour)}})
	svc.hiddenAuthors = &fakeHiddenAuthors{err: errors.New("down")}
	if _, err := svc.SoundUses(context.Background(), uuid.Nil, 0); !errors.Is(err, ErrSoundUsesUnavailable) {
		t.Fatalf("hidden-author lookup down: err = %v, want ErrSoundUsesUnavailable", err)
	}
}

func TestSoundUsesUnavailableWithoutAStore(t *testing.T) {
	if _, err := (&Service{}).SoundUses(context.Background(), uuid.Nil, 0); !errors.Is(err, ErrSoundUsesUnavailable) {
		t.Fatalf("no store: err = %v", err)
	}
	svc := soundUsesService(&fakeSoundUses{err: errors.New("db down")})
	if _, err := svc.SoundUses(context.Background(), uuid.Nil, 0); !errors.Is(err, ErrSoundUsesUnavailable) {
		t.Fatalf("store error: err = %v", err)
	}
}
