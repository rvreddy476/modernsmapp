package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/livestream"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/subscribers"
	"github.com/google/uuid"
)

// "Creator is live" on the upload fan-out machinery. The fakes are the
// ones the upload tests use, plus a reminder source.

type fakeReminders struct {
	pages  []*livestream.Page
	calls  int
	afters []string
	limits []int
	err    error
	// repeat serves pages[0] for ever (a route whose token never moves).
	repeat bool
}

func (f *fakeReminders) ReminderUserIDs(_ context.Context, _ uuid.UUID, after string, limit int) (*livestream.Page, error) {
	f.afters = append(f.afters, after)
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return nil, f.err
	}
	if f.repeat {
		f.calls++
		return f.pages[0], nil
	}
	if f.calls >= len(f.pages) {
		return &livestream.Page{}, nil
	}
	p := f.pages[f.calls]
	f.calls++
	return p, nil
}

func liveJob(creator, channel uuid.UUID) *postgres.FanoutJob {
	stream := uuid.New()
	return &postgres.FanoutJob{
		PostID:        stream,
		ChannelID:     channel,
		AuthorID:      creator,
		ContentType:   LiveContentType,
		DeepLink:      LiveDeepLink("landscape", stream.String()),
		NotifType:     LiveNotifType,
		Visibility:    "public",
		PostCreatedAt: time.Now(),
		Title:         "Friday Q&A",
		ChannelName:   "Cal B",
		Phase:         postgres.FanoutPhaseReminders,
	}
}

func liveFanoutWith(n uploadNotifier, st fanoutStore, subs subscriberSource, rem reminderSource) *SubscriberFanout {
	f := newSubscriberFanoutWith(n, st, subs)
	f.reminders = rem
	return f
}

// The whole recipient rule in one walk: reminder holders and bell-on
// subscribers are both told, a person in both groups is told once, and
// the creator is never told however they got onto either list.
func TestLiveFanout_RemindersThenSubscribers_DedupedAndCreatorExcluded(t *testing.T) {
	creator, channel := uuid.New(), uuid.New()
	onlyReminder, both, onlySub := uuid.New(), uuid.New(), uuid.New()
	rem := &fakeReminders{pages: []*livestream.Page{
		{IDs: []uuid.UUID{onlyReminder, both, creator}},
	}}
	src := &fakeSource{pages: []*subscribers.Page{
		{IDs: []uuid.UUID{both, creator, onlySub}, NextAfter: onlySub},
	}}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := liveFanoutWith(notifier, store, src, rem)
	job := liveJob(creator, channel)

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(notifier.got) != 3 {
		t.Fatalf("delivery calls = %d, want 3 (one per person, the one in both groups once)", len(notifier.got))
	}
	got := notifier.recipients()
	for _, id := range []uuid.UUID{onlyReminder, both, onlySub} {
		if _, ok := got[id]; !ok {
			t.Fatalf("%s never notified", id)
		}
	}
	if _, ok := got[creator]; ok {
		t.Fatal("the creator was told about their own stream")
	}

	u := got[both]
	if u.NotifType != LiveNotifType || u.PostID != job.PostID || u.AuthorID != creator {
		t.Fatalf("notification: %+v", u)
	}
	if u.DeepLink != "/posttube/live/"+job.PostID.String() || u.Title != "Friday Q&A" || u.ChannelName != "Cal B" {
		t.Fatalf("render inputs: %+v", u)
	}
	// One notification per user per stream: the identity is stream + user.
	if want := job.PostID.String() + ":" + both.String() + ":" + LiveNotifType; u.Identity != want {
		t.Fatalf("identity = %q, want %q", u.Identity, want)
	}

	// The reminder walk ended by handing over to the subscriber phase.
	if n := len(store.reminderAdvances); n != 1 || !store.reminderAdvances[0].done || store.reminderAdvances[0].delta != 2 {
		t.Fatalf("reminder advances = %+v, want one {delta 2, done}", store.reminderAdvances)
	}
	if job.Phase != postgres.FanoutPhaseSubscribers {
		t.Fatalf("phase = %q after the reminders drained", job.Phase)
	}
	// Subscribers: `both` was already told, so only one new delivery.
	if len(store.deltas) != 1 || store.deltas[0] != 1 {
		t.Fatalf("subscriber delivered deltas = %v, want [1]", store.deltas)
	}
}

// Reminder holders are paged with the route's own token, to exhaustion,
// and the token is persisted after every page.
func TestLiveFanout_ReminderPagingPersistsTokenPerPage(t *testing.T) {
	creator := uuid.New()
	p1, p2 := ids(3), ids(2)
	rem := &fakeReminders{pages: []*livestream.Page{
		{IDs: p1, NextAfter: "tok-1", HasMore: true},
		{IDs: p2, NextAfter: "tok-2", HasMore: false},
	}}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := liveFanoutWith(notifier, store, &fakeSource{}, rem)

	if err := f.processJob(context.Background(), liveJob(creator, uuid.Nil)); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(notifier.recipients()) != 5 {
		t.Fatalf("notified %d, want 5", len(notifier.recipients()))
	}
	if len(rem.afters) != 2 || rem.afters[0] != "" || rem.afters[1] != "tok-1" {
		t.Fatalf("reminder afters = %q, want [\"\" tok-1]", rem.afters)
	}
	if rem.limits[0] != fanoutPageSize {
		t.Fatalf("page size = %d", rem.limits[0])
	}
	want := []reminderAdvance{{"tok-1", 3, false}, {"tok-1", 2, true}}
	if len(store.reminderAdvances) != 2 || store.reminderAdvances[0] != want[0] || store.reminderAdvances[1] != want[1] {
		t.Fatalf("reminder advances = %+v, want %+v", store.reminderAdvances, want)
	}
}

// A failed reminder recipient pins the token: the page is re-walked on the
// next attempt, the job stays in the reminder phase, and the subscriber
// walk does not start ahead of it.
func TestLiveFanout_FailedReminderRecipientPinsTokenAndPhase(t *testing.T) {
	creator, channel := uuid.New(), uuid.New()
	p1 := ids(3)
	rem := &fakeReminders{pages: []*livestream.Page{
		{IDs: p1, NextAfter: "tok-1", HasMore: true},
		{IDs: ids(2), NextAfter: "tok-2"},
	}}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: ids(2)}}}
	store := newFakeStore()
	notifier := &fakeNotifier{failFor: p1[1]}
	f := liveFanoutWith(notifier, store, src, rem)
	job := liveJob(creator, channel)

	if err := f.processJob(context.Background(), job); err == nil {
		t.Fatal("expected an error so the job is released for retry")
	}
	want := reminderAdvance{"", 2, false}
	if len(store.reminderAdvances) != 1 || store.reminderAdvances[0] != want {
		t.Fatalf("reminder advances = %+v, want one stay-put write %+v", store.reminderAdvances, want)
	}
	if rem.calls != 1 {
		t.Fatalf("fetched %d reminder pages, want 1 (must not run ahead of a failed page)", rem.calls)
	}
	if src.calls != 0 || len(src.afters) != 0 {
		t.Fatal("the subscriber walk started although the reminder phase failed")
	}
	if job.Phase != postgres.FanoutPhaseReminders {
		t.Fatalf("phase = %q, want reminders", job.Phase)
	}
	if store.delivered[p1[1]] {
		t.Fatal("failed recipient must not be marked delivered")
	}

	// The retry reaches the one who failed and nobody twice.
	notifier.failFor = uuid.Nil
	rem.calls = 0
	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("retry: %v", err)
	}
	seen := map[uuid.UUID]int{}
	for _, u := range notifier.got {
		seen[u.RecipientID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s notified %d times across the retry", id, n)
		}
	}
	if seen[p1[1]] != 1 || len(seen) != 7 {
		t.Fatalf("after retry %d people notified, want 7 including the failed one", len(seen))
	}
}

// A subscriber page with a failed recipient behaves as an upload's does.
func TestLiveFanout_FailedSubscriberPinsCursor(t *testing.T) {
	creator, channel := uuid.New(), uuid.New()
	page := ids(3)
	src := &fakeSource{pages: []*subscribers.Page{{IDs: page, NextAfter: page[2], HasMore: true}}}
	store := newFakeStore()
	f := liveFanoutWith(&fakeNotifier{failFor: page[0]}, store, src, &fakeReminders{})

	if err := f.processJob(context.Background(), liveJob(creator, channel)); err == nil {
		t.Fatal("expected an error")
	}
	if len(store.cursors) != 1 || store.cursors[0] != uuid.Nil || store.deltas[0] != 2 {
		t.Fatalf("cursors %v deltas %v, want a stay-put write with delta 2", store.cursors, store.deltas)
	}
}

// A job already in the subscriber phase (a retry, or a resumed crash) does
// not page the reminder holders again.
func TestLiveFanout_SubscriberPhaseDoesNotRewalkReminders(t *testing.T) {
	creator, channel := uuid.New(), uuid.New()
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: ids(2)}}}
	sub := uuid.New()
	src := &fakeSource{pages: []*subscribers.Page{{IDs: []uuid.UUID{sub}, NextAfter: sub}}}
	notifier := &fakeNotifier{}
	f := liveFanoutWith(notifier, newFakeStore(), src, rem)
	job := liveJob(creator, channel)
	job.Phase = postgres.FanoutPhaseSubscribers

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(rem.afters) != 0 {
		t.Fatalf("reminders fetched %d times in the subscriber phase", len(rem.afters))
	}
	if len(notifier.got) != 1 || notifier.got[0].RecipientID != sub {
		t.Fatalf("notified %+v, want the one subscriber", notifier.got)
	}
}

// A creator without a channel has reminder holders only: the subscriber
// source is never asked about the nil channel.
func TestLiveFanout_NoChannelMeansRemindersOnly(t *testing.T) {
	holder := uuid.New()
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: []uuid.UUID{holder}}}}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: ids(3)}}}
	notifier := &fakeNotifier{}
	f := liveFanoutWith(notifier, newFakeStore(), src, rem)

	if err := f.processJob(context.Background(), liveJob(uuid.New(), uuid.Nil)); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(src.afters) != 0 {
		t.Fatal("subscriber source asked about a nil channel")
	}
	if len(notifier.got) != 1 || notifier.got[0].RecipientID != holder {
		t.Fatalf("notified %+v, want the reminder holder only", notifier.got)
	}
}

// Running the same job twice (a duplicate claim, a full retry) tells
// nobody a second time.
func TestLiveFanout_SecondRunNotifiesNobodyAgain(t *testing.T) {
	creator, channel := uuid.New(), uuid.New()
	holders, subs := ids(2), ids(2)
	store, notifier := newFakeStore(), &fakeNotifier{}
	run := func() {
		rem := &fakeReminders{pages: []*livestream.Page{{IDs: holders}}}
		src := &fakeSource{pages: []*subscribers.Page{{IDs: subs, NextAfter: subs[1]}}}
		job := liveJob(creator, channel)
		job.PostID = uuid.MustParse("7b0c7a0e-1111-4222-8333-444455556666")
		if err := liveFanoutWith(notifier, store, src, rem).processJob(context.Background(), job); err != nil {
			t.Fatalf("processJob: %v", err)
		}
	}
	run()
	run()
	if len(notifier.got) != 4 {
		t.Fatalf("delivery calls = %d after two runs, want 4", len(notifier.got))
	}
}

// "Is live" has a shelf life. A job that surfaces after the window is
// finished without telling anyone; one inside it is delivered.
func TestLiveFanout_StaleJobNotifiesNobody(t *testing.T) {
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: ids(2)}}}
	notifier := &fakeNotifier{}
	f := liveFanoutWith(notifier, newFakeStore(), &fakeSource{}, rem)
	job := liveJob(uuid.New(), uuid.Nil)
	job.PostCreatedAt = time.Now().Add(-liveNotifyWindow - time.Minute)

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("a stale job must complete, not retry: %v", err)
	}
	if len(notifier.got) != 0 || len(rem.afters) != 0 {
		t.Fatalf("stale job notified %d and paged %d", len(notifier.got), len(rem.afters))
	}

	now := time.Now()
	fresh := &postgres.FanoutJob{NotifType: LiveNotifType, PostCreatedAt: now.Add(-liveNotifyWindow + time.Minute)}
	if liveJobStale(fresh, now) {
		t.Fatal("a job inside the window is not stale")
	}
	old := &postgres.FanoutJob{NotifType: "creator_uploaded_video", PostCreatedAt: now.Add(-48 * time.Hour)}
	if liveJobStale(old, now) {
		t.Fatal("the window applies to live jobs only; an old upload still notifies")
	}
}

// A route that says has_more but hands back a token that does not move
// must end the walk, not spin on the same page.
func TestLiveFanout_NonAdvancingTokenEndsTheWalk(t *testing.T) {
	// {where the job is, what the route hands back}. The last one is a
	// token that goes BACK to the start, which would re-walk everyone.
	for _, c := range [][2]string{{"", ""}, {"stuck", "stuck"}, {"tok-3", ""}} {
		cursor, token := c[0], c[1]
		rem := &fakeReminders{repeat: true, pages: []*livestream.Page{
			{IDs: ids(2), NextAfter: token, HasMore: true},
		}}
		store := newFakeStore()
		f := liveFanoutWith(&fakeNotifier{}, store, &fakeSource{}, rem)
		job := liveJob(uuid.New(), uuid.Nil)
		job.ReminderCursor = cursor

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := f.processJob(ctx, job)
		cancel()
		if err != nil {
			t.Fatalf("token %q: %v", token, err)
		}
		if rem.calls != 1 {
			t.Fatalf("token %q: fetched %d pages, want 1", token, rem.calls)
		}
		if !store.reminderAdvances[len(store.reminderAdvances)-1].done {
			t.Fatalf("token %q: phase never handed over", token)
		}
	}
}

// A reminder route that cannot be reached is a retry, not "no reminders".
func TestLiveFanout_ReminderSourceErrorIsRetried(t *testing.T) {
	rem := &fakeReminders{err: errors.New("live-service-v2 down")}
	store := newFakeStore()
	f := liveFanoutWith(&fakeNotifier{}, store, &fakeSource{}, rem)
	job := liveJob(uuid.New(), uuid.New())
	if err := f.processJob(context.Background(), job); err == nil {
		t.Fatal("expected the job to be released for retry")
	}
	if len(store.reminderAdvances) != 0 || job.Phase != postgres.FanoutPhaseReminders {
		t.Fatal("a failed fetch must not move the phase")
	}
}

// An upload job is untouched by all of this: it never asks for reminders.
func TestLiveFanout_UploadJobsNeverPageReminders(t *testing.T) {
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: ids(2)}}}
	sub := uuid.New()
	src := &fakeSource{pages: []*subscribers.Page{{IDs: []uuid.UUID{sub}, NextAfter: sub}}}
	notifier := &fakeNotifier{}
	f := liveFanoutWith(notifier, newFakeStore(), src, rem)
	job := uploadJob(uuid.New(), uuid.New())
	job.Phase = postgres.FanoutPhaseReminders // even if a row said so

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(rem.afters) != 0 || len(notifier.got) != 1 {
		t.Fatalf("upload job paged reminders %d times, notified %d", len(rem.afters), len(notifier.got))
	}
}

// Enqueue carries the phase to the store.
func TestEnqueue_CarriesPhase(t *testing.T) {
	store := newFakeStore()
	f := newSubscriberFanoutWith(&fakeNotifier{}, store, &fakeSource{})
	if err := f.Enqueue(context.Background(), EnqueueParams{
		PostID: uuid.New(), NotifType: LiveNotifType, Phase: postgres.FanoutPhaseReminders,
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.enqueued) != 1 || store.enqueued[0].Phase != postgres.FanoutPhaseReminders {
		t.Fatalf("enqueued = %+v", store.enqueued)
	}
}

func TestLiveProbes(t *testing.T) {
	ctx := context.Background()
	stream, channel := uuid.New(), uuid.New()

	rem := &fakeReminders{pages: []*livestream.Page{{IDs: ids(1)}}}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: ids(1)}}}
	f := liveFanoutWith(&fakeNotifier{}, newFakeStore(), src, rem)
	if ok, err := f.HasReminders(ctx, stream); err != nil || !ok {
		t.Fatalf("HasReminders = %v, %v", ok, err)
	}
	if rem.limits[0] != 1 || rem.afters[0] != "" {
		t.Fatalf("probe asked limit %d after %q, want 1 from the start", rem.limits[0], rem.afters[0])
	}
	if ok, err := f.HasSubscribers(ctx, channel); err != nil || !ok {
		t.Fatalf("HasSubscribers = %v, %v", ok, err)
	}

	empty := liveFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{}, &fakeReminders{})
	if ok, _ := empty.HasReminders(ctx, stream); ok {
		t.Fatal("no reminders reported as some")
	}
	if ok, _ := empty.HasSubscribers(ctx, channel); ok {
		t.Fatal("no subscribers reported as some")
	}

	// A nil channel never reaches the subscriber source.
	nilSrc := &fakeSource{pages: []*subscribers.Page{{IDs: ids(1)}}}
	g := liveFanoutWith(&fakeNotifier{}, newFakeStore(), nilSrc, &fakeReminders{})
	if ok, _ := g.HasSubscribers(ctx, uuid.Nil); ok || len(nilSrc.afters) != 0 {
		t.Fatal("a creator without a channel has no subscribers to probe")
	}

	// "Could not tell" is an error, never a quiet false.
	down := liveFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{}, &fakeReminders{err: errors.New("down")})
	if _, err := down.HasReminders(ctx, stream); err == nil {
		t.Fatal("a failed probe must report its error")
	}
}

// Per-recipient eligibility for a live job: no post lookup (the stream id
// is not a post), blocks in both directions, and a followers-only stream
// only for people who follow.
func TestLiveEligible(t *testing.T) {
	creator := uuid.New()
	var rel atomic.Value
	rel.Store(`{"data":{}}`)
	var postHits int32
	graphSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/graph/relationship") || r.URL.Query().Get("other_id") != creator.String() {
			t.Errorf("unexpected graph call %s", r.URL)
		}
		_, _ = w.Write([]byte(rel.Load().(string)))
	}))
	defer graphSrv.Close()
	postSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&postHits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer postSrv.Close()

	f := newSubscriberFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{})
	f.SetEligibilityDeps(graphSrv.URL, postSrv.URL, "k")

	cases := []struct {
		name, visibility, rel string
		want                  bool
	}{
		{"public, no relationship", "public", `{"data":{}}`, true},
		{"public, creator blocked viewer", "public", `{"data":{"blocked":true}}`, false},
		{"public, viewer blocked creator", "public", `{"data":{"blocked_by":true}}`, false},
		{"followers, follows", "followers", `{"data":{"follows":true}}`, true},
		{"followers, does not follow", "followers", `{"data":{}}`, false},
		{"paid", "paid", `{"data":{"follows":true}}`, false},
		{"private", "private", `{"data":{"follows":true}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rel.Store(c.rel)
			job := liveJob(creator, uuid.New())
			job.Visibility = c.visibility
			got, err := f.eligible(context.Background(), job, uuid.New())
			if err != nil {
				t.Fatalf("eligible: %v", err)
			}
			if got != c.want {
				t.Fatalf("eligible = %v, want %v", got, c.want)
			}
		})
	}
	if n := atomic.LoadInt32(&postHits); n != 0 {
		t.Fatalf("post-service asked %d times about a stream id", n)
	}
}

// A graph failure is a retry for that recipient, never a silent delivery.
func TestLiveEligible_GraphFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	f := newSubscriberFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{})
	f.SetEligibilityDeps(srv.URL, "", "k")
	if ok, err := f.eligible(context.Background(), liveJob(uuid.New(), uuid.New()), uuid.New()); err == nil || ok {
		t.Fatalf("eligible = %v, %v; want an error", ok, err)
	}
}

func TestLiveVisibilityNotifies(t *testing.T) {
	for v, want := range map[string]bool{
		"public": true, "followers": true,
		"paid": false, "private": false, "unlisted": false, "": false, "PUBLIC": false, "subscribers": false,
	} {
		if got := LiveVisibilityNotifies(v); got != want {
			t.Fatalf("LiveVisibilityNotifies(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestLiveDeepLink_ByOrientation(t *testing.T) {
	for orientation, want := range map[string]string{
		"landscape": "/posttube/live/s1",
		"portrait":  "/reels/live/s1",
		"":          "/posttube/live/s1", // events written before the field existed
		"square":    "/posttube/live/s1",
	} {
		if got := LiveDeepLink(orientation, "s1"); got != want {
			t.Fatalf("LiveDeepLink(%q) = %q, want %q", orientation, got, want)
		}
	}
}

func TestRenderLive(t *testing.T) {
	stream, recipient := uuid.New(), uuid.New()
	base := UploadNotification{NotifType: LiveNotifType, PostID: stream, ChannelID: uuid.New(), RecipientID: recipient}

	n := base
	n.ChannelName, n.Title = "Cal B", "Friday Q&A"
	r := renderLive(n)
	if r.Title != "Cal B is live: Friday Q&A" {
		t.Fatalf("title = %q", r.Title)
	}
	if r.Body != "Tap to watch live" {
		t.Fatalf("body = %q", r.Body)
	}
	if want := "live:" + stream.String(); r.CollapseKey != want {
		t.Fatalf("collapse key = %q, want %q", r.CollapseKey, want)
	}

	n = base
	n.Title = "Friday Q&A"
	if r := renderLive(n); r.Title != "A creator you follow is live: Friday Q&A" {
		t.Fatalf("no name: %q", r.Title)
	}
	n = base
	n.ChannelName = "Cal B"
	if r := renderLive(n); r.Title != "Cal B is live" {
		t.Fatalf("no title: %q", r.Title)
	}
	if r := renderLive(base); r.Title != "A creator you follow is live" {
		t.Fatalf("no name, no title: %q", r.Title)
	}
}

// The delivery call files a live notice under live_stream/{streamID} with
// the live copy, and leaves an upload exactly as it was.
func TestFanoutDelivery_EntityAndCopyByType(t *testing.T) {
	stream, channel := uuid.New(), uuid.New()
	entity, r := fanoutDelivery(UploadNotification{
		NotifType: LiveNotifType, PostID: stream, ChannelID: channel, ChannelName: "Cal B", Title: "Friday Q&A",
	})
	if entity != "live_stream" || r.Title != "Cal B is live: Friday Q&A" || r.CollapseKey != "live:"+stream.String() {
		t.Fatalf("live: %q %+v", entity, r)
	}
	entity, r = fanoutDelivery(UploadNotification{
		NotifType: "creator_uploaded_video", PostID: stream, ChannelID: channel, ChannelName: "Cal B", Title: "My video",
	})
	if entity != "post" || r.Title != "Cal B uploaded: My video" || r.CollapseKey != "channel:"+channel.String()+":upload" {
		t.Fatalf("upload: %q %+v", entity, r)
	}
}

// The device push carries exactly what a client needs to open the room.
func TestLivePushData(t *testing.T) {
	stream, recipient := uuid.New(), uuid.New()
	link := LiveDeepLink("portrait", stream.String())
	entity, render := fanoutDelivery(UploadNotification{
		NotifType: LiveNotifType, PostID: stream, RecipientID: recipient, ChannelName: "Cal B", Title: "Friday Q&A",
	})
	title, body, data := buildPushData(recipient, LiveNotifType, entity, stream, link, render)
	if title != "Cal B is live: Friday Q&A" || body != "Tap to watch live" {
		t.Fatalf("copy = %q / %q", title, body)
	}
	want := map[string]string{
		"type":         "creator_went_live",
		"entity_type":  "live_stream",
		"entity_id":    stream.String(),
		"deep_link":    "/reels/live/" + stream.String(),
		"title":        title,
		"body":         body,
		"collapse_key": "live:" + stream.String(),
	}
	if len(data) != len(want) {
		t.Fatalf("push data = %v", data)
	}
	for k, v := range want {
		if data[k] != v {
			t.Fatalf("push data[%s] = %q, want %q", k, data[k], v)
		}
	}
}

// The live toggles gate both halves and default on.
func TestResolveDecision_LiveCategory(t *testing.T) {
	p := postgres.DefaultNotificationPreferences("u1")
	if !p.PushLive || !p.InappLive {
		t.Fatal("live must default on for both channels")
	}
	if d := resolveDecision(LiveNotifType, p, false); !d.CreateInbox || !d.SendWebSocket || !d.SendPush {
		t.Fatalf("default: %+v", d)
	}
	p.PushLive = false
	if d := resolveDecision(LiveNotifType, p, false); !d.CreateInbox || d.SendPush {
		t.Fatalf("push off: %+v", d)
	}
	p.InappLive, p.PushLive = false, true
	if d := resolveDecision(LiveNotifType, p, false); d.CreateInbox || d.SendWebSocket || !d.SendPush {
		t.Fatalf("in-app off: %+v", d)
	}
	p.PushLive = false
	if d := resolveDecision(LiveNotifType, p, false); d.CreateInbox || d.SendWebSocket || d.SendPush {
		t.Fatalf("both off: %+v", d)
	}
	// Turning uploads off does not silence live, and the reverse.
	q := postgres.DefaultNotificationPreferences("u2")
	q.PushNewVideos, q.InappNewVideos = false, false
	if d := resolveDecision(LiveNotifType, q, false); !d.CreateInbox || !d.SendPush {
		t.Fatalf("new_videos off must not gate live: %+v", d)
	}
}

func TestTemplates_LiveCopy(t *testing.T) {
	tpl := Templates[LiveNotifType]
	if tpl.TitleTemplate != "{creator} is live: {title}" || tpl.BodyTemplate == "" || tpl.CanAggregate || !tpl.PushEligible {
		t.Fatalf("template: %+v", tpl)
	}
	if GetCollapseKey(LiveNotifType, "s1", "u1") != "live:s1" {
		t.Fatalf("collapse key = %q", GetCollapseKey(LiveNotifType, "s1", "u1"))
	}
}

// A creator with no channel name is named from their profile, once per job
// attempt; a profile that cannot be read leaves neutral copy.
func TestLiveFanout_NameFallsBackToProfileThenNeutral(t *testing.T) {
	creator := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/profiles/batch" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"` + creator.String() + `":{"username":"calb","display_name":"Cal Bee"}}`))
	}))
	defer srv.Close()
	svc := &Service{}
	svc.SetProfileServiceURL(srv.URL)
	if got := svc.CreatorDisplayName(context.Background(), creator); got != "Cal Bee" {
		t.Fatalf("display name = %q", got)
	}
	if got := (&Service{}).CreatorDisplayName(context.Background(), creator); got != "" {
		t.Fatalf("no profile service: %q", got)
	}

	holder := uuid.New()
	run := func(names creatorNamer) UploadNotification {
		rem := &fakeReminders{pages: []*livestream.Page{{IDs: []uuid.UUID{holder}}}}
		notifier := &fakeNotifier{}
		f := liveFanoutWith(notifier, newFakeStore(), &fakeSource{}, rem)
		f.names = names
		job := liveJob(creator, uuid.Nil)
		job.ChannelName = ""
		if err := f.processJob(context.Background(), job); err != nil {
			t.Fatalf("processJob: %v", err)
		}
		return notifier.got[0]
	}
	if r := renderLive(run(svc)); r.Title != "Cal Bee is live: Friday Q&A" {
		t.Fatalf("profile name: %q", r.Title)
	}
	if r := renderLive(run(nil)); r.Title != "A creator you follow is live: Friday Q&A" {
		t.Fatalf("neutral: %q", r.Title)
	}
}

// Without a reminder source wired (a partial deployment) the job does not
// stall in the reminder phase: it hands over and the subscribers are told.
func TestLiveFanout_NoReminderSourceStillNotifiesSubscribers(t *testing.T) {
	sub := uuid.New()
	src := &fakeSource{pages: []*subscribers.Page{{IDs: []uuid.UUID{sub}, NextAfter: sub}}}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := newSubscriberFanoutWith(notifier, store, src)
	job := liveJob(uuid.New(), uuid.New())

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(store.reminderAdvances) != 1 || !store.reminderAdvances[0].done {
		t.Fatalf("reminder advances = %+v, want one hand-over", store.reminderAdvances)
	}
	if len(notifier.got) != 1 || notifier.got[0].RecipientID != sub {
		t.Fatalf("notified %+v", notifier.got)
	}
}
