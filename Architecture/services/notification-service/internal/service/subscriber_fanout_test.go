package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/subscribers"
	"github.com/google/uuid"
)

// Fakes drive the whole fan-out pipeline without Postgres, Scylla, Redis
// or a subscriber service. They record exactly what the pipeline asked of
// them so the tests can pin the contract: every id delivered, cursor
// persisted per page, self never notified, render inputs passed through.

type fakeSource struct {
	pages   []*subscribers.Page
	calls   int
	afters  []uuid.UUID
	channel uuid.UUID
}

func (f *fakeSource) SubscriberIDs(_ context.Context, _ uuid.UUID, after uuid.UUID, _ int) (*subscribers.Page, error) {
	f.afters = append(f.afters, after)
	if f.calls >= len(f.pages) {
		return &subscribers.Page{}, nil
	}
	p := f.pages[f.calls]
	f.calls++
	return p, nil
}

func (f *fakeSource) ChannelByOwner(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.channel, nil
}

type fakeStore struct {
	mu        sync.Mutex
	delivered map[uuid.UUID]bool
	cursors   []uuid.UUID
	deltas    []int64
}

func newFakeStore() *fakeStore { return &fakeStore{delivered: map[uuid.UUID]bool{}} }

func (s *fakeStore) EnqueueFanoutJob(context.Context, *postgres.FanoutJob) error { return nil }
func (s *fakeStore) ClaimFanoutJobs(context.Context, time.Duration, int) ([]postgres.FanoutJob, error) {
	return nil, nil
}
func (s *fakeStore) AdvanceFanoutCursor(_ context.Context, _ uuid.UUID, cursor uuid.UUID, delta int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors = append(s.cursors, cursor)
	s.deltas = append(s.deltas, delta)
	return nil
}
func (s *fakeStore) CompleteFanoutJob(context.Context, uuid.UUID) error        { return nil }
func (s *fakeStore) ReleaseFanoutJob(context.Context, uuid.UUID, string) error { return nil }
func (s *fakeStore) FailFanoutJob(context.Context, uuid.UUID, string) error    { return nil }
func (s *fakeStore) CleanupFanoutRecords(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (s *fakeStore) AlreadyDelivered(_ context.Context, _ uuid.UUID, uid uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delivered[uid], nil
}
func (s *fakeStore) MarkDelivered(_ context.Context, _ uuid.UUID, uid uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.delivered[uid] {
		return false, nil
	}
	s.delivered[uid] = true
	return true, nil
}

type fakeNotifier struct {
	mu      sync.Mutex
	got     []UploadNotification
	failFor uuid.UUID
}

func (n *fakeNotifier) CreateUploadNotification(_ context.Context, u UploadNotification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if u.RecipientID == n.failFor {
		return errors.New("inbox write failed")
	}
	n.got = append(n.got, u)
	return nil
}

func (n *fakeNotifier) recipients() map[uuid.UUID]UploadNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := map[uuid.UUID]UploadNotification{}
	for _, u := range n.got {
		m[u.RecipientID] = u
	}
	return m
}

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func uploadJob(author, channel uuid.UUID) *postgres.FanoutJob {
	return &postgres.FanoutJob{
		PostID:        uuid.New(),
		ChannelID:     channel,
		AuthorID:      author,
		ContentType:   "long_video",
		DeepLink:      "/tube/watch/x",
		NotifType:     "creator_uploaded_video",
		Visibility:    "public",
		PostCreatedAt: time.Now(),
		Title:         "My video",
		ChannelName:   "Cal B",
	}
}

// Two pages from the source: every id must be notified exactly once and
// the cursor must be persisted after EACH page, so a crash between pages
// resumes rather than restarts.
func TestFanout_PagesToExhaustionAndAdvancesCursorPerPage(t *testing.T) {
	author, channel := uuid.New(), uuid.New()
	page1, page2 := ids(3), ids(2)
	src := &fakeSource{pages: []*subscribers.Page{
		{IDs: page1, NextAfter: page1[2], HasMore: true},
		{IDs: page2, NextAfter: page2[1], HasMore: false},
	}}
	store := newFakeStore()
	notifier := &fakeNotifier{}
	f := newSubscriberFanoutWith(notifier, store, src)

	if err := f.processJob(context.Background(), uploadJob(author, channel)); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	got := notifier.recipients()
	for _, id := range append(page1, page2...) {
		if _, ok := got[id]; !ok {
			t.Fatalf("subscriber %s never notified", id)
		}
	}
	if len(got) != 5 {
		t.Fatalf("notified %d recipients, want 5", len(got))
	}
	if len(store.cursors) != 2 || store.cursors[0] != page1[2] || store.cursors[1] != page2[1] {
		t.Fatalf("cursor advances = %v, want [%s %s]", store.cursors, page1[2], page2[1])
	}
	if store.deltas[0] != 3 || store.deltas[1] != 2 {
		t.Fatalf("delivered deltas = %v, want [3 2]", store.deltas)
	}
	// The second fetch must start where the first page ended.
	if len(src.afters) != 2 || src.afters[0] != uuid.Nil || src.afters[1] != page1[2] {
		t.Fatalf("source afters = %v", src.afters)
	}
}

// A failed recipient pins the cursor: the page is re-walked on the next
// attempt and the job is released with an error. Nobody is skipped.
func TestFanout_FailedRecipientBlocksCursorAdvancement(t *testing.T) {
	author, channel := uuid.New(), uuid.New()
	page1, page2 := ids(3), ids(2)
	src := &fakeSource{pages: []*subscribers.Page{
		{IDs: page1, NextAfter: page1[2], HasMore: true},
		{IDs: page2, NextAfter: page2[1], HasMore: false},
	}}
	store := newFakeStore()
	notifier := &fakeNotifier{failFor: page1[1]}
	f := newSubscriberFanoutWith(notifier, store, src)

	err := f.processJob(context.Background(), uploadJob(author, channel))
	if err == nil {
		t.Fatal("expected an error so the job is released for retry")
	}
	// The cursor was persisted at the page START (uuid.Nil), never past it.
	if len(store.cursors) != 1 || store.cursors[0] != uuid.Nil {
		t.Fatalf("cursor advances = %v, want a single stay-put write at uuid.Nil", store.cursors)
	}
	if store.deltas[0] != 2 {
		t.Fatalf("delivered delta = %d, want 2 (the two that succeeded)", store.deltas[0])
	}
	if src.calls != 1 {
		t.Fatalf("source fetched %d pages, want 1 (must not run ahead of a failed page)", src.calls)
	}
	// The failed recipient carries no delivered marker, so a retry reaches them.
	if store.delivered[page1[1]] {
		t.Fatal("failed recipient must not be marked delivered")
	}
}

// The author subscribed to their own channel never hears about their own
// upload, and their id does not stall the page either.
func TestFanout_SelfExclusionIsTerminal(t *testing.T) {
	author, channel := uuid.New(), uuid.New()
	others := ids(2)
	page := []uuid.UUID{others[0], author, others[1]}
	src := &fakeSource{pages: []*subscribers.Page{
		{IDs: page, NextAfter: others[1], HasMore: false},
	}}
	store := newFakeStore()
	notifier := &fakeNotifier{}
	f := newSubscriberFanoutWith(notifier, store, src)

	if err := f.processJob(context.Background(), uploadJob(author, channel)); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	got := notifier.recipients()
	if _, ok := got[author]; ok {
		t.Fatal("author was notified about their own upload")
	}
	if len(got) != 2 {
		t.Fatalf("notified %d, want 2", len(got))
	}
	if len(store.cursors) != 1 || store.cursors[0] != others[1] {
		t.Fatalf("cursor must advance past a page containing self: %v", store.cursors)
	}
}

// The notifier receives everything it needs to render without a lookup:
// the channel name and title from the job, the channel id for the collapse
// key, and a stable identity for the idempotent inbox write.
func TestFanout_NotifierReceivesRenderInputs(t *testing.T) {
	author, channel := uuid.New(), uuid.New()
	sub := uuid.New()
	src := &fakeSource{pages: []*subscribers.Page{
		{IDs: []uuid.UUID{sub}, NextAfter: sub, HasMore: false},
	}}
	notifier := &fakeNotifier{}
	f := newSubscriberFanoutWith(notifier, newFakeStore(), src)
	job := uploadJob(author, channel)

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	u, ok := notifier.recipients()[sub]
	if !ok {
		t.Fatal("subscriber not notified")
	}
	if u.AuthorID != author || u.ChannelID != channel || u.PostID != job.PostID {
		t.Fatalf("ids: %+v", u)
	}
	if u.ChannelName != "Cal B" || u.Title != "My video" || u.NotifType != "creator_uploaded_video" || u.DeepLink != "/tube/watch/x" {
		t.Fatalf("render inputs: %+v", u)
	}
	if u.Identity != job.PostID.String()+":"+sub.String()+":creator_uploaded_video" {
		t.Fatalf("identity = %q", u.Identity)
	}

	r := renderUpload(u)
	if r.Title != "Cal B uploaded: My video" {
		t.Fatalf("rendered title = %q", r.Title)
	}
	if r.Body != "My video" {
		t.Fatalf("rendered body = %q", r.Body)
	}
	if want := "channel:" + channel.String() + ":upload"; r.CollapseKey != want {
		t.Fatalf("collapse key = %q, want %q", r.CollapseKey, want)
	}
	if categoryForEvent(u.NotifType) != catNewVideos {
		t.Fatal("upload notifications must land in the new_videos preference bucket")
	}
}

// Without a channel name the push still reads sensibly rather than
// "uploaded: My video" with a dangling separator.
func TestRenderUpload_FallbacksWithoutChannelOrTitle(t *testing.T) {
	base := UploadNotification{NotifType: "creator_uploaded_flick", ChannelID: uuid.New()}

	n := base
	n.Title = "Sunset"
	if r := renderUpload(n); r.Title != "New upload: Sunset" || r.Body != "Sunset" {
		t.Fatalf("no channel: %+v", r)
	}
	n = base
	n.ChannelName = "Cal B"
	if r := renderUpload(n); r.Title != "Cal B uploaded a new flick" || r.Body == "" {
		t.Fatalf("no title: %+v", r)
	}
	n = base
	n.NotifType = "creator_uploaded_video"
	n.ChannelName = "Cal B"
	if r := renderUpload(n); r.Title != "Cal B uploaded a new video" {
		t.Fatalf("no title, video: %+v", r)
	}
}

func TestTemplates_UploadTypesRegistered(t *testing.T) {
	for _, ev := range []string{"creator_uploaded_video", "creator_uploaded_flick"} {
		tpl, ok := Templates[ev]
		if !ok {
			t.Fatalf("template %s missing", ev)
		}
		if !tpl.PushEligible || !tpl.CanAggregate || tpl.AggregateWindow != 30*time.Minute {
			t.Fatalf("template %s: %+v", ev, tpl)
		}
		if tpl.TitleTemplate != "{channel} uploaded: {title}" || tpl.AggregateTitle != "{count} new uploads from {channel}" {
			t.Fatalf("template %s text: %+v", ev, tpl)
		}
		if tpl.Icon != "video" || tpl.Priority != "medium" {
			t.Fatalf("template %s icon/priority: %+v", ev, tpl)
		}
		if categoryForEvent(ev) != catNewVideos {
			t.Fatalf("%s not mapped to new_videos", ev)
		}
	}
}

// The new_videos toggles gate both halves, and default on: a subscriber
// who never opened settings hears about every upload.
func TestResolveDecision_NewVideosCategory(t *testing.T) {
	for _, ev := range []string{"creator_uploaded_video", "creator_uploaded_flick"} {
		p := postgres.DefaultNotificationPreferences("u1")
		if !p.PushNewVideos || !p.InappNewVideos {
			t.Fatal("new_videos must default on for both channels")
		}
		if d := resolveDecision(ev, p, false); !d.CreateInbox || !d.SendPush {
			t.Fatalf("%s default: %+v", ev, d)
		}
		p.PushNewVideos = false
		if d := resolveDecision(ev, p, false); !d.CreateInbox || d.SendPush {
			t.Fatalf("%s push off: %+v", ev, d)
		}
		p.InappNewVideos = false
		p.PushNewVideos = true
		if d := resolveDecision(ev, p, false); d.CreateInbox || d.SendWebSocket || !d.SendPush {
			t.Fatalf("%s inapp off: %+v", ev, d)
		}
	}
}

func TestGetCollapseKey_UploadsCollapsePerChannel(t *testing.T) {
	cases := []struct {
		ev, target, recipient, want string
	}{
		{"creator_uploaded_video", "chan-1", "u1", "channel:chan-1:upload"},
		{"creator_uploaded_flick", "chan-1", "u1", "channel:chan-1:upload"},
		{"channel.update.published", "chan-1", "u1", "channel:chan-1:update"},
		{"missed_call", "call-1", "u1", "call:call-1"},
	}
	for _, c := range cases {
		if got := GetCollapseKey(c.ev, c.target, c.recipient); got != c.want {
			t.Fatalf("GetCollapseKey(%s) = %q, want %q", c.ev, got, c.want)
		}
	}
}
