package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Channel feed (RSS publishing, 2026-09-29) over in-memory storage: the
// anonymous evaluation, the Go re-check of every row, the enclosure choice,
// the media-record call and its cache. The SQL predicate is proved against
// Postgres in store/postgres/channels_feed_integration_test.go.

// ── fakes ───────────────────────────────────────────────────────────────────

type fakeFeedStore struct {
	rows  []postgres.ChannelFeedVideo
	facts postgres.ChannelFeedFacts
	// what the service asked for
	owner    uuid.UUID
	category string
	limit    int
	calls    int
}

func (f *fakeFeedStore) ListChannelFeedVideos(_ context.Context, owner uuid.UUID, category string, limit int) ([]postgres.ChannelFeedVideo, error) {
	f.owner, f.category, f.limit = owner, category, limit
	f.calls++
	return append([]postgres.ChannelFeedVideo(nil), f.rows...), nil
}

func (f *fakeFeedStore) ChannelFeedFacts(context.Context, uuid.UUID) (postgres.ChannelFeedFacts, error) {
	return f.facts, nil
}

type fakeFeedMedia struct {
	mu       sync.Mutex
	records  map[uuid.UUID]*FeedMediaRecord
	errs     map[uuid.UUID]error
	calls    map[uuid.UUID]int
	inFlight int32
	maxSeen  int32
	delay    time.Duration
}

func newFakeFeedMedia() *fakeFeedMedia {
	return &fakeFeedMedia{records: map[uuid.UUID]*FeedMediaRecord{}, errs: map[uuid.UUID]error{}, calls: map[uuid.UUID]int{}}
}

func (f *fakeFeedMedia) MediaRecord(ctx context.Context, id uuid.UUID) (*FeedMediaRecord, error) {
	n := atomic.AddInt32(&f.inFlight, 1)
	defer atomic.AddInt32(&f.inFlight, -1)
	for {
		seen := atomic.LoadInt32(&f.maxSeen)
		if n <= seen || atomic.CompareAndSwapInt32(&f.maxSeen, seen, n) {
			break
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[id]++
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	rec, ok := f.records[id]
	if !ok {
		return nil, errFeedMediaUnavailable
	}
	return rec, nil
}

func (f *fakeFeedMedia) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func sizep(n int64) *int64 { return &n }

func readyRecord(variants ...FeedMediaVariant) *FeedMediaRecord {
	return &FeedMediaRecord{FileType: "video", MimeType: "video/quicktime", FileSizeBytes: 9000,
		ProcessingStatus: "ready", ModerationStatus: "passed", Variants: variants}
}

// ── the rig ────────────────────────────────────────────────────────────────

type feedRig struct {
	svc      *Service
	channels *fakeChannelStore
	store    *fakeFeedStore
	media    *fakeFeedMedia
	hidden   *fakeHiddenAuthors
	owner    uuid.UUID
	seen     []canRequest
	now      time.Time
}

// newFeedRig: one public owner with the channel "raghu.builds". The graph
// fake allows the owner and denies everybody else.
func newFeedRig(t *testing.T) *feedRig {
	t.Helper()
	r := &feedRig{
		channels: newFakeChannelStore(), store: &fakeFeedStore{}, media: newFakeFeedMedia(),
		hidden: &fakeHiddenAuthors{hidden: map[uuid.UUID]bool{}}, owner: uuid.New(),
		now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
	}
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{r.owner.String(): true}, &calls, &r.seen)
	t.Cleanup(srv.Close)
	avatar := uuid.New()
	ch := &postgres.Channel{UserID: r.owner, Name: "Raghu Builds", Handle: "raghu.builds", About: "Weekly builds",
		AvatarMediaID: &avatar, ContactEmail: "hello@example.com"}
	if err := r.channels.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	stored := r.channels.byUser[r.owner]
	stored.ContactEmail = "hello@example.com"
	stored.UpdatedAt = r.now.Add(-48 * time.Hour)
	r.svc = &Service{
		channels: r.channels, channelFeed: r.store, feedMedia: r.media,
		graphServiceURL: srv.URL, internalServiceKey: "test-key", httpClient: http.DefaultClient,
		now: func() time.Time { return r.now },
	}
	r.svc.hiddenAuthors = r.hidden
	return r
}

// video adds a feed-eligible row whose asset resolves to a 720p enclosure,
// published `age` ago, and returns the post and media ids.
func (r *feedRig) video(age time.Duration, mutate func(*postgres.ChannelFeedVideo)) (uuid.UUID, uuid.UUID) {
	postID, mediaID := uuid.New(), uuid.New()
	published := r.now.Add(-age)
	row := postgres.ChannelFeedVideo{
		Post: postgres.Post{ID: postID, AuthorID: r.owner, Visibility: "public", ContentType: "long_video",
			ReviewStatus: "approved", Title: "Episode", Text: "notes", Category: "podcasts", Language: "en",
			CreatedAt: published.Add(-time.Hour), UpdatedAt: published, PublishedAt: &published},
		MediaID: &mediaID, DurationMs: 61000, ProcessingStatus: "ready", ModerationStatus: "passed",
	}
	r.media.records[mediaID] = readyRecord(FeedMediaVariant{Name: "720p", Mime: "video/mp4", SizeBytes: sizep(7200), ObjectKey: "k/720p"})
	if mutate != nil {
		mutate(&row)
	}
	r.store.rows = append(r.store.rows, row)
	return postID, mediaID
}

func (r *feedRig) feed(t *testing.T, ref, category string, limit int) *ChannelFeed {
	t.Helper()
	f, err := r.svc.ChannelFeed(context.Background(), ref, category, limit)
	if err != nil {
		t.Fatalf("ChannelFeed(%q): %v", ref, err)
	}
	return f
}

func feedItemIDs(f *ChannelFeed) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(f.Items))
	for _, it := range f.Items {
		out = append(out, it.ID)
	}
	return out
}

// ── the enclosure choice ───────────────────────────────────────────────────

func TestPickFeedEnclosurePrefers720pThen480pThenOriginal(t *testing.T) {
	id := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	v720 := FeedMediaVariant{Name: "720p", Mime: "video/mp4", SizeBytes: sizep(7200), ObjectKey: "k/720p"}
	v480 := FeedMediaVariant{Name: "480p", Mime: "video/mp4", SizeBytes: sizep(4800), ObjectKey: "k/480p"}
	v1080 := FeedMediaVariant{Name: "1080p", Mime: "video/mp4", SizeBytes: sizep(10800), ObjectKey: "k/1080p"}
	base := "/v1/media/" + id.String() + "/serve"
	cases := []struct {
		name string
		rec  *FeedMediaRecord
		want ChannelFeedEnclosure
		ok   bool
	}{
		{"720p wins over everything", readyRecord(v1080, v480, v720), ChannelFeedEnclosure{"720p", base + "/720p", "video/mp4", 7200}, true},
		{"480p when there is no 720p", readyRecord(v1080, v480), ChannelFeedEnclosure{"480p", base + "/480p", "video/mp4", 4800}, true},
		{"original when neither rendition exists", readyRecord(v1080), ChannelFeedEnclosure{"original", base, "video/quicktime", 9000}, true},
		{"a 720p with no size falls through to 480p",
			readyRecord(FeedMediaVariant{Name: "720p", Mime: "video/mp4", ObjectKey: "k/720p"}, v480),
			ChannelFeedEnclosure{"480p", base + "/480p", "video/mp4", 4800}, true},
		{"a 720p with no object falls through to the original",
			readyRecord(FeedMediaVariant{Name: "720p", Mime: "video/mp4", SizeBytes: sizep(7200)}),
			ChannelFeedEnclosure{"original", base, "video/quicktime", 9000}, true},
		{"a rendition with no mime is an MP4",
			readyRecord(FeedMediaVariant{Name: "720p", SizeBytes: sizep(7200), ObjectKey: "k/720p"}),
			ChannelFeedEnclosure{"720p", base + "/720p", "video/mp4", 7200}, true},
		{"no size anywhere: no enclosure",
			&FeedMediaRecord{FileType: "video", MimeType: "video/mp4", ProcessingStatus: "ready", ModerationStatus: "passed"}, ChannelFeedEnclosure{}, false},
		{"still processing: no enclosure",
			&FeedMediaRecord{FileType: "video", FileSizeBytes: 9000, ProcessingStatus: "processing", ModerationStatus: "passed", Variants: []FeedMediaVariant{v720}}, ChannelFeedEnclosure{}, false},
		{"failed: no enclosure",
			&FeedMediaRecord{FileType: "video", FileSizeBytes: 9000, ProcessingStatus: "failed", ModerationStatus: "passed", Variants: []FeedMediaVariant{v720}}, ChannelFeedEnclosure{}, false},
		{"moderation not passed: no enclosure",
			&FeedMediaRecord{FileType: "video", FileSizeBytes: 9000, ProcessingStatus: "ready", ModerationStatus: "manual_review", Variants: []FeedMediaVariant{v720}}, ChannelFeedEnclosure{}, false},
		{"not a video: no enclosure",
			&FeedMediaRecord{FileType: "image", FileSizeBytes: 9000, ProcessingStatus: "ready", ModerationStatus: "passed"}, ChannelFeedEnclosure{}, false},
		{"no record: no enclosure", nil, ChannelFeedEnclosure{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickFeedEnclosure(id, tc.rec)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("got %+v %v, want %+v %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// ── the document ───────────────────────────────────────────────────────────

func TestChannelFeedListsEpisodesInStoreOrderWithTheirEnclosures(t *testing.T) {
	r := newFeedRig(t)
	newest, newestMedia := r.video(time.Hour, nil)
	cover := uuid.New()
	older, olderMedia := r.video(30*time.Hour, func(v *postgres.ChannelFeedVideo) {
		v.Post.CoverMediaID, v.Post.Hashtags, v.Post.PublishedAt = &cover, []string{"build"}, nil
	})
	r.media.records[olderMedia] = readyRecord() // no renditions: the original
	r.store.facts = postgres.ChannelFeedFacts{Language: "en", DominantCategory: "podcasts"}

	f := r.feed(t, "@Raghu.Builds", "", 0)
	if got := feedItemIDs(f); len(got) != 2 || got[0] != newest || got[1] != older {
		t.Fatalf("items = %v, want [%s %s]", got, newest, older)
	}
	if r.store.owner != r.owner || r.store.category != "" || r.store.limit != 50 {
		t.Fatalf("store asked for owner=%s category=%q limit=%d", r.store.owner, r.store.category, r.store.limit)
	}
	ch := f.Channel
	if ch.UserID != r.owner || ch.Handle != "raghu.builds" || ch.Name != "Raghu Builds" || ch.About != "Weekly builds" ||
		ch.ContactEmail != "hello@example.com" || ch.Language != "en" || ch.DominantCategory != "podcasts" {
		t.Fatalf("channel = %+v", ch)
	}
	if ch.AvatarMediaID == nil || ch.AvatarURL == nil || *ch.AvatarURL != "/v1/media/"+ch.AvatarMediaID.String()+"/serve" {
		t.Fatalf("avatar = %v %v", ch.AvatarMediaID, ch.AvatarURL)
	}
	first, second := f.Items[0], f.Items[1]
	if first.MediaID != newestMedia || first.DurationMs != 61000 || first.CoverMediaID != nil || first.Hashtags == nil || len(first.Hashtags) != 0 {
		t.Fatalf("first item = %+v", first)
	}
	if want := (ChannelFeedEnclosure{"720p", "/v1/media/" + newestMedia.String() + "/serve/720p", "video/mp4", 7200}); first.Enclosure != want {
		t.Fatalf("first enclosure = %+v", first.Enclosure)
	}
	if !first.PublishedAt.Equal(r.now.Add(-time.Hour)) {
		t.Fatalf("published_at = %s", first.PublishedAt)
	}
	if want := (ChannelFeedEnclosure{"original", "/v1/media/" + olderMedia.String() + "/serve", "video/quicktime", 9000}); second.Enclosure != want {
		t.Fatalf("second enclosure = %+v", second.Enclosure)
	}
	if second.CoverMediaID == nil || *second.CoverMediaID != cover || len(second.Hashtags) != 1 {
		t.Fatalf("second item = %+v", second)
	}
	// No published_at on the row: the creation time stands in.
	if !second.PublishedAt.Equal(r.now.Add(-31 * time.Hour)) {
		t.Fatalf("published_at fallback = %s", second.PublishedAt)
	}
	// The newest item is newer than the channel row.
	if !f.UpdatedAt.Equal(r.now.Add(-time.Hour)) {
		t.Fatalf("updated_at = %s", f.UpdatedAt)
	}
}

func TestChannelFeedWithNoVideosIsAnEmptyListNotNull(t *testing.T) {
	r := newFeedRig(t)
	f := r.feed(t, r.owner.String(), "", 0)
	if f.Items == nil || len(f.Items) != 0 {
		t.Fatalf("items = %#v", f.Items)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(b, &wire); err != nil || string(wire.Items) != "[]" {
		t.Fatalf("items on the wire = %s (%v)", wire.Items, err)
	}
	if !f.UpdatedAt.Equal(r.now.Add(-48 * time.Hour)) {
		t.Fatalf("updated_at of an empty feed = %s, want the channel's", f.UpdatedAt)
	}
}

func TestChannelFeedCategoryAndLimit(t *testing.T) {
	r := newFeedRig(t)
	f := r.feed(t, "raghu.builds", " Podcasts ", 7)
	if f.Category != "podcasts" || r.store.category != "podcasts" || r.store.limit != 7 {
		t.Fatalf("category=%q store.category=%q store.limit=%d", f.Category, r.store.category, r.store.limit)
	}
	r.feed(t, "raghu.builds", "", 500)
	if r.store.limit != 50 {
		t.Fatalf("limit 500 reached the store as %d, want the ceiling 50", r.store.limit)
	}
	for _, bad := range []string{"cooking-shows", "film & animation", "../x"} {
		calls := r.store.calls
		if _, err := r.svc.ChannelFeed(context.Background(), "raghu.builds", bad, 0); !errors.Is(err, ErrInvalidCategory) {
			t.Errorf("category %q: err = %v, want ErrInvalidCategory", bad, err)
		}
		if r.store.calls != calls {
			t.Errorf("category %q reached the store", bad)
		}
	}
	// The category is refused before the channel is looked up, so the
	// answer cannot tell an existing channel from a missing one.
	if _, err := r.svc.ChannelFeed(context.Background(), "nobody.here", "cooking-shows", 0); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("unknown channel + bad category: %v", err)
	}
}

// ── who may read it ─────────────────────────────────────────────────────────

func TestChannelFeedIsAskedAsTheAnonymousStranger(t *testing.T) {
	r := newFeedRig(t)
	r.video(time.Hour, nil)
	r.feed(t, "raghu.builds", "", 0)
	if len(r.seen) != 1 {
		t.Fatalf("graph asked %d times, want 1", len(r.seen))
	}
	q := r.seen[0]
	if q.ViewerID != uuid.Nil.String() || q.Action != "view_posts" || len(q.TargetIDs) != 1 || q.TargetIDs[0] != r.owner.String() {
		t.Fatalf("graph question = %+v, want the nil viewer asking view_posts of the owner", q)
	}
	if len(r.hidden.seen) != 1 || r.hidden.seen[0] != r.owner {
		t.Fatalf("hidden-author lookup = %v", r.hidden.seen)
	}
}

func TestChannelFeedOfAHiddenOrPrivateOwnerIsNotFound(t *testing.T) {
	missing := func(t *testing.T, r *feedRig) {
		t.Helper()
		_, err := r.svc.ChannelFeed(context.Background(), "raghu.builds", "", 0)
		if !errors.Is(err, ErrChannelNotFound) {
			t.Fatalf("err = %v, want ErrChannelNotFound", err)
		}
		_, none := r.svc.ChannelFeed(context.Background(), "nobody.here", "", 0)
		if !errors.Is(none, ErrChannelNotFound) || none.Error() != err.Error() {
			t.Fatalf("hidden owner answered %q, no channel answered %q: they must be the same", err, none)
		}
		if r.store.calls != 0 || r.media.totalCalls() != 0 {
			t.Fatalf("a refused feed read %d pages and %d media records", r.store.calls, r.media.totalCalls())
		}
	}
	t.Run("hidden (deactivated or pending deletion)", func(t *testing.T) {
		r := newFeedRig(t)
		r.video(time.Hour, nil)
		r.hidden.hidden[r.owner] = true
		missing(t, r)
	})
	t.Run("private account", func(t *testing.T) {
		r := newFeedRig(t)
		r.video(time.Hour, nil)
		var calls int32
		srv := fakeGraphCan(t, map[string]bool{}, &calls, nil)
		t.Cleanup(srv.Close)
		r.svc.graphServiceURL = srv.URL
		missing(t, r)
	})
	t.Run("graph unreachable fails closed", func(t *testing.T) {
		r := newFeedRig(t)
		r.video(time.Hour, nil)
		r.svc.graphServiceURL = "http://127.0.0.1:1"
		missing(t, r)
	})
	t.Run("hidden lookup failing fails closed", func(t *testing.T) {
		r := newFeedRig(t)
		r.video(time.Hour, nil)
		r.hidden.err = errors.New("db down")
		missing(t, r)
	})
}

// The store applies the predicate in SQL; every row it returns is judged
// again. Each case is a row a broken query could hand back.
func TestChannelFeedRechecksEveryRowTheStoreReturns(t *testing.T) {
	tier := uuid.New()
	past := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]func(v *postgres.ChannelFeedVideo){
		"unlisted":          func(v *postgres.ChannelFeedVideo) { v.Post.Visibility = "unlisted" },
		"followers":         func(v *postgres.ChannelFeedVideo) { v.Post.Visibility = "followers" },
		"private":           func(v *postgres.ChannelFeedVideo) { v.Post.Visibility = "private" },
		"blank visibility":  func(v *postgres.ChannelFeedVideo) { v.Post.Visibility = "" },
		"members-only":      func(v *postgres.ChannelFeedVideo) { v.Post.TierRequiredID = &tier },
		"scheduled":         func(v *postgres.ChannelFeedVideo) { v.Post.PublishAt = &past },
		"age-restricted":    func(v *postgres.ChannelFeedVideo) { v.Post.AgeRestricted = true },
		"soft-deleted":      func(v *postgres.ChannelFeedVideo) { v.Post.DeletedAt = &past },
		"rejected":          func(v *postgres.ChannelFeedVideo) { v.Post.ReviewStatus = "rejected" },
		"pending review":    func(v *postgres.ChannelFeedVideo) { v.Post.ReviewStatus = "pending" },
		"restricted (hold)": func(v *postgres.ChannelFeedVideo) { v.Post.ActiveRestrictionCount = 1 },
		"a flick":           func(v *postgres.ChannelFeedVideo) { v.Post.ContentType = "flick" },
		"a plain post":      func(v *postgres.ChannelFeedVideo) { v.Post.ContentType = "post" },
		"another author":    func(v *postgres.ChannelFeedVideo) { v.Post.AuthorID = uuid.New() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := newFeedRig(t)
			good, _ := r.video(time.Hour, nil)
			bad, badMedia := r.video(2*time.Hour, mutate)
			f := r.feed(t, "raghu.builds", "", 0)
			if got := feedItemIDs(f); len(got) != 1 || got[0] != good {
				t.Fatalf("items = %v: %s row %s must be dropped, %s kept", got, name, bad, good)
			}
			if r.media.calls[badMedia] != 0 {
				t.Fatalf("the media record of a refused row was read")
			}
		})
	}
}

func TestChannelFeedOmitsItemsWithoutAPlayableEnclosure(t *testing.T) {
	r := newFeedRig(t)
	good, _ := r.video(time.Hour, nil)

	_, failed := r.video(2*time.Hour, nil)
	r.media.errs[failed] = errors.New("media-service 503")
	_, unknown := r.video(3*time.Hour, nil)
	delete(r.media.records, unknown)
	_, processing := r.video(4*time.Hour, nil)
	r.media.records[processing] = &FeedMediaRecord{FileType: "video", FileSizeBytes: 9000, ProcessingStatus: "processing", ModerationStatus: "passed"}
	_, sizeless := r.video(5*time.Hour, nil)
	r.media.records[sizeless] = &FeedMediaRecord{FileType: "video", ProcessingStatus: "ready", ModerationStatus: "passed"}
	// The row itself says the asset is not ready: omitted with no call.
	_, pipeline := r.video(6*time.Hour, func(v *postgres.ChannelFeedVideo) { v.ProcessingStatus = "processing" })
	_, unscanned := r.video(7*time.Hour, func(v *postgres.ChannelFeedVideo) { v.ModerationStatus = "pending" })
	r.video(8*time.Hour, func(v *postgres.ChannelFeedVideo) { v.MediaID = nil }) // no video attached at all

	f := r.feed(t, "raghu.builds", "", 0)
	if got := feedItemIDs(f); len(got) != 1 || got[0] != good {
		t.Fatalf("items = %v, want only %s", got, good)
	}
	if r.media.calls[pipeline] != 0 || r.media.calls[unscanned] != 0 {
		t.Fatalf("an asset the row already calls unready was looked up: %v", r.media.calls)
	}
}

// ── the media-record call and its cache ────────────────────────────────────

func TestChannelFeedCachesResolvedEnclosuresForTenMinutes(t *testing.T) {
	r := newFeedRig(t)
	_, ok := r.video(time.Hour, nil)
	_, failing := r.video(2*time.Hour, nil)
	r.media.errs[failing] = errors.New("blip")
	_, processing := r.video(3*time.Hour, nil)
	r.media.records[processing] = &FeedMediaRecord{FileType: "video", FileSizeBytes: 9000, ProcessingStatus: "processing", ModerationStatus: "passed"}

	if n := len(r.feed(t, "raghu.builds", "", 0).Items); n != 1 {
		t.Fatalf("first build: %d items", n)
	}
	r.now = r.now.Add(9*time.Minute + 59*time.Second)
	// The failure clears and the transcode finishes: neither was cached.
	delete(r.media.errs, failing)
	r.media.records[processing] = readyRecord()
	if n := len(r.feed(t, "raghu.builds", "", 0).Items); n != 3 {
		t.Fatalf("second build: %d items, want 3 (failures and not-ready must not be cached)", n)
	}
	if r.media.calls[ok] != 1 {
		t.Fatalf("resolved enclosure read %d times inside the TTL, want 1", r.media.calls[ok])
	}
	if r.media.calls[failing] != 2 || r.media.calls[processing] != 2 {
		t.Fatalf("unresolved lookups = %d, %d; want 2, 2", r.media.calls[failing], r.media.calls[processing])
	}
	r.now = r.now.Add(2 * time.Second) // 10 min 1 s after the first build
	r.feed(t, "raghu.builds", "", 0)
	if r.media.calls[ok] != 2 {
		t.Fatalf("after the TTL the record was read %d times, want 2", r.media.calls[ok])
	}
}

func TestChannelFeedReadsMediaRecordsEightAtATime(t *testing.T) {
	r := newFeedRig(t)
	r.media.delay = 20 * time.Millisecond
	for i := 0; i < 50; i++ {
		r.video(time.Duration(i+1)*time.Hour, nil)
	}
	f := r.feed(t, "raghu.builds", "", 0)
	if len(f.Items) != 50 {
		t.Fatalf("%d items, want 50", len(f.Items))
	}
	if got := atomic.LoadInt32(&r.media.maxSeen); got > channelFeedMediaConcurrency || got < 2 {
		t.Fatalf("peak concurrent media reads = %d, want 2..%d", got, channelFeedMediaConcurrency)
	}
}

func TestChannelFeedBoundsEachMediaRead(t *testing.T) {
	if channelFeedMediaConcurrency != 8 || channelFeedMediaTimeout != 3*time.Second || channelFeedEnclosureTTL != 10*time.Minute {
		t.Fatalf("plan constants changed: concurrency=%d timeout=%s ttl=%s", channelFeedMediaConcurrency, channelFeedMediaTimeout, channelFeedEnclosureTTL)
	}
	r := newFeedRig(t)
	_, slow := r.video(time.Hour, nil)
	var deadline time.Duration
	r.svc.feedMedia = feedMediaFunc(func(ctx context.Context, id uuid.UUID) (*FeedMediaRecord, error) {
		if d, ok := ctx.Deadline(); ok {
			deadline = time.Until(d)
		}
		return r.media.MediaRecord(ctx, id)
	})
	r.feed(t, "raghu.builds", "", 0)
	if r.media.calls[slow] != 1 || deadline <= 0 || deadline > channelFeedMediaTimeout {
		t.Fatalf("media read deadline = %s, want within %s", deadline, channelFeedMediaTimeout)
	}
}

type feedMediaFunc func(ctx context.Context, id uuid.UUID) (*FeedMediaRecord, error)

func (f feedMediaFunc) MediaRecord(ctx context.Context, id uuid.UUID) (*FeedMediaRecord, error) {
	return f(ctx, id)
}

// The production source against a fake media-service: the record route, the
// internal key, and no viewer.
func TestChannelFeedAsksMediaServiceAsATrustedServiceWithNoViewer(t *testing.T) {
	r := newFeedRig(t)
	_, mediaID := r.video(time.Hour, nil)
	_, gone := r.video(2*time.Hour, nil)
	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.Method+" "+req.URL.Path)
		mu.Unlock()
		if got := req.Header.Get("X-Internal-Service-Key"); got != "test-key" {
			t.Errorf("internal key header = %q", got)
		}
		if got := req.Header.Get("X-User-Id"); got != "" {
			t.Errorf("the record read carried a viewer: X-User-Id = %q", got)
		}
		if req.URL.Path != "/v1/media/"+mediaID.String() {
			// Refused, with a body that would read as a playable record if
			// the status were ignored.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"data":{"file_type":"video","mime_type":"video/mp4","file_size_bytes":9000,
				"processing_status":"ready","moderation_status":"passed"},"error":{"code":"NOT_FOUND"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"` + mediaID.String() + `","uploader_id":"` + r.owner.String() + `","file_type":"video",
			"mime_type":"video/quicktime","file_size_bytes":9000,"storage_bucket":"b","storage_key":"user/x/original",
			"processing_status":"ready","moderation_status":"passed","variants":[
			{"media_asset_id":"` + mediaID.String() + `","variant":"480p","size_bytes":4800,"mime":"video/mp4","object_key":"user/x/480p"},
			{"media_asset_id":"` + mediaID.String() + `","variant":"720p","size_bytes":7200,"mime":"video/mp4","object_key":"user/x/720p"}]},
			"meta":{}}`))
	}))
	t.Cleanup(srv.Close)
	r.svc.feedMedia = nil // the HTTP source
	r.svc.mediaServiceURL = srv.URL + "/"

	f := r.feed(t, "raghu.builds", "", 0)
	if len(f.Items) != 1 || f.Items[0].MediaID != mediaID {
		t.Fatalf("items = %+v (the 404 asset %s must be omitted)", f.Items, gone)
	}
	if want := (ChannelFeedEnclosure{"720p", "/v1/media/" + mediaID.String() + "/serve/720p", "video/mp4", 7200}); f.Items[0].Enclosure != want {
		t.Fatalf("enclosure = %+v", f.Items[0].Enclosure)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("media-service calls = %v", paths)
	}
	for _, p := range paths {
		if p != "GET /v1/media/"+mediaID.String() && p != "GET /v1/media/"+gone.String() {
			t.Fatalf("unexpected media-service call %q", p)
		}
	}
}

func TestChannelFeedWithoutMediaServiceListsNothing(t *testing.T) {
	r := newFeedRig(t)
	r.video(time.Hour, nil)
	r.svc.feedMedia = nil
	r.svc.mediaServiceURL = ""
	if f := r.feed(t, "raghu.builds", "", 0); len(f.Items) != 0 {
		t.Fatalf("items = %+v, want none when no enclosure can be resolved", f.Items)
	}
}
