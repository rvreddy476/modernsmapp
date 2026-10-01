package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/graph"
	"github.com/atpost/notification-service/internal/livestream"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/subscribers"
	"github.com/google/uuid"
)

// The follower phase of "creator is live" (founder decision, 2 Oct 2026):
// after the reminder holders and the bell-on subscribers, everyone who
// follows the creator is told, by the same durable job.

type fakeFollowers struct {
	pages   []*graph.FollowerPage
	calls   int
	cursors []string
	limits  []int
	users   []uuid.UUID
	err     error
	// repeat serves pages[0] for ever (a source whose token never moves).
	repeat bool
	// onCall runs before each fetch, with the number of fetches so far.
	onCall func(n int)
}

func (f *fakeFollowers) FollowerIDs(_ context.Context, user uuid.UUID, cursor string, limit int) (*graph.FollowerPage, error) {
	if f.onCall != nil {
		f.onCall(len(f.cursors))
	}
	f.cursors = append(f.cursors, cursor)
	f.limits = append(f.limits, limit)
	f.users = append(f.users, user)
	if f.err != nil {
		return nil, f.err
	}
	if f.repeat {
		f.calls++
		return f.pages[0], nil
	}
	if f.calls >= len(f.pages) {
		return &graph.FollowerPage{}, nil
	}
	p := f.pages[f.calls]
	f.calls++
	return p, nil
}

func liveFanoutWithFollowers(n uploadNotifier, st fanoutStore, subs subscriberSource, rem reminderSource, fol followerSource) *SubscriberFanout {
	f := liveFanoutWith(n, st, subs, rem)
	f.followers = fol
	return f
}

// The three walks run in order, each handing over to the next: reminder
// holders, subscribers, followers. A person in all three groups is told
// once, and the creator never, however they got onto any list.
func TestLiveFanout_ThreePhases_OrderHandOverDedupeAndCreatorExcluded(t *testing.T) {
	creator, channel := uuid.New(), uuid.New()
	onlyReminder, onlySub, onlyFollower, all := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: []uuid.UUID{onlyReminder, all, creator}}}}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: []uuid.UUID{all, creator, onlySub}, NextAfter: onlySub}}}
	fol := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: []uuid.UUID{creator, all, onlySub, onlyFollower}}}}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := liveFanoutWithFollowers(notifier, store, src, rem, fol)
	job := liveJob(creator, channel)

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}

	// One call per person: four people across three overlapping groups.
	if len(notifier.got) != 4 {
		t.Fatalf("delivery calls = %d, want 4 (the one in all three groups once)", len(notifier.got))
	}
	got := notifier.recipients()
	for _, id := range []uuid.UUID{onlyReminder, onlySub, onlyFollower, all} {
		if _, ok := got[id]; !ok {
			t.Fatalf("%s never notified", id)
		}
	}
	if _, ok := got[creator]; ok {
		t.Fatal("the creator was told about their own stream")
	}

	// Order: reminder holders first, then subscribers, then followers.
	order := make([]uuid.UUID, 0, len(notifier.got))
	for _, u := range notifier.got {
		order = append(order, u.RecipientID)
	}
	pos := func(id uuid.UUID) int {
		for i, o := range order {
			if o == id {
				return i
			}
		}
		return -1
	}
	if !(pos(onlyReminder) < pos(onlySub) && pos(all) < pos(onlySub) && pos(onlySub) < pos(onlyFollower)) {
		t.Fatalf("delivery order %v: want reminder holders, then subscribers, then followers", order)
	}
	if want := []string{"r", "s", "f"}; !reflect.DeepEqual(store.order, want) {
		t.Fatalf("cursor writes ran %v, want %v", store.order, want)
	}

	// Hand-over into the follower phase happened once, and the followers
	// asked about are the creator's, from the start.
	if store.followerBegins != 1 || job.Phase != postgres.FanoutPhaseFollowers {
		t.Fatalf("follower hand-overs = %d, phase = %q", store.followerBegins, job.Phase)
	}
	if len(fol.users) != 1 || fol.users[0] != creator || fol.cursors[0] != "" {
		t.Fatalf("follower fetches: users %v cursors %v", fol.users, fol.cursors)
	}
	// Only the one new person counts as delivered in the follower phase.
	if want := []followerAdvance{{"", 1}}; !reflect.DeepEqual(store.followerAdvances, want) {
		t.Fatalf("follower advances = %+v, want %+v", store.followerAdvances, want)
	}

	// Same type, copy inputs, deep link and identity as the other phases.
	u := got[onlyFollower]
	if u.NotifType != LiveNotifType || u.PostID != job.PostID || u.AuthorID != creator {
		t.Fatalf("notification: %+v", u)
	}
	if u.DeepLink != "/posttube/live/"+job.PostID.String() || u.Title != "Friday Q&A" || u.ChannelName != "Cal B" {
		t.Fatalf("render inputs: %+v", u)
	}
	if want := job.PostID.String() + ":" + onlyFollower.String() + ":" + LiveNotifType; u.Identity != want {
		t.Fatalf("identity = %q, want %q", u.Identity, want)
	}
}

// Followers are paged with graph-service's own token to exhaustion, at
// the page size graph-service serves, with no recipient cap, and the
// token is persisted after every page.
func TestLiveFanout_FollowerPagingPersistsTokenPerPage_NoCap(t *testing.T) {
	creator := uuid.New()
	const pages = 60 // 6,000 followers: past the old 5,000 cap
	var all []*graph.FollowerPage
	var wantAdv []followerAdvance
	for i := 1; i <= pages; i++ {
		p := &graph.FollowerPage{IDs: ids(100)}
		tok := "tok-" + strings.Repeat("x", i)
		if i < pages {
			p.NextCursor = tok
			wantAdv = append(wantAdv, followerAdvance{tok, 100})
		} else {
			// The last page: the token stays where it was.
			wantAdv = append(wantAdv, followerAdvance{"tok-" + strings.Repeat("x", i-1), 100})
		}
		all = append(all, p)
	}
	fol := &fakeFollowers{pages: all}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := liveFanoutWithFollowers(notifier, store, &fakeSource{}, &fakeReminders{}, fol)
	job := liveJob(creator, uuid.Nil)

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(notifier.got) != pages*100 {
		t.Fatalf("notified %d followers, want all %d", len(notifier.got), pages*100)
	}
	if fol.calls != pages {
		t.Fatalf("fetched %d pages, want %d", fol.calls, pages)
	}
	if !reflect.DeepEqual(store.followerAdvances, wantAdv) {
		t.Fatalf("follower advances differ: got %d writes, first %+v, last %+v",
			len(store.followerAdvances), store.followerAdvances[0], store.followerAdvances[len(store.followerAdvances)-1])
	}
	for i, l := range fol.limits {
		if l != graph.FollowerPageMax {
			t.Fatalf("page %d asked for %d followers, want graph-service's page maximum %d", i, l, graph.FollowerPageMax)
		}
	}
	// Each fetch used the token the previous page handed back.
	if fol.cursors[0] != "" || fol.cursors[1] != "tok-x" || fol.cursors[pages-1] != "tok-"+strings.Repeat("x", pages-1) {
		t.Fatalf("fetch tokens: %q, %q ... %q", fol.cursors[0], fol.cursors[1], fol.cursors[pages-1])
	}
}

// A failed follower pins the token and the phase: the page is re-walked on
// the next attempt, the walk does not run ahead, and neither the reminder
// holders nor the subscribers are paged again.
func TestLiveFanout_FailedFollowerPinsCursorAndPhase(t *testing.T) {
	creator, channel := uuid.New(), uuid.New()
	p1, p2, p3 := ids(3), ids(3), ids(2)
	newFol := func() *fakeFollowers {
		return &fakeFollowers{pages: []*graph.FollowerPage{
			{IDs: p1, NextCursor: "tok-1"},
			{IDs: p2, NextCursor: "tok-2"},
			{IDs: p3},
		}}
	}
	fol := newFol()
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: ids(1)}}}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: ids(1)}}}
	store := newFakeStore()
	notifier := &fakeNotifier{failFor: p2[1]}
	f := liveFanoutWithFollowers(notifier, store, src, rem, fol)
	job := liveJob(creator, channel)

	if err := f.processJob(context.Background(), job); err == nil {
		t.Fatal("expected an error so the job is released for retry")
	}
	// Page 1 moved the token to tok-1; page 2 wrote it back unchanged with
	// the two who did get through.
	if want := []followerAdvance{{"tok-1", 3}, {"tok-1", 2}}; !reflect.DeepEqual(store.followerAdvances, want) {
		t.Fatalf("follower advances = %+v, want %+v", store.followerAdvances, want)
	}
	if fol.calls != 2 {
		t.Fatalf("fetched %d follower pages, want 2 (must not run ahead of a failed page)", fol.calls)
	}
	if job.Phase != postgres.FanoutPhaseFollowers || job.FollowerCursor != "tok-1" {
		t.Fatalf("phase %q cursor %q, want followers pinned at tok-1", job.Phase, job.FollowerCursor)
	}
	if store.delivered[p2[1]] {
		t.Fatal("failed recipient must not be marked delivered")
	}

	// The retry, as the worker would see it after a re-claim: the stored
	// phase and token, and a source that answers by token.
	notifier.failFor = uuid.Nil
	remCalls, subCalls := len(rem.afters), len(src.afters)
	fol2 := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: p2, NextCursor: "tok-2"}, {IDs: p3}}}
	f.followers = fol2
	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if fol2.cursors[0] != "tok-1" {
		t.Fatalf("retry resumed from %q, want the pinned tok-1 (not the start)", fol2.cursors[0])
	}
	if len(rem.afters) != remCalls || len(src.afters) != subCalls {
		t.Fatal("the retry re-paged reminder holders or subscribers")
	}
	if store.followerBegins != 1 {
		t.Fatalf("follower hand-overs = %d, want 1 across the retry", store.followerBegins)
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
	if seen[p2[1]] != 1 || len(seen) != 10 {
		t.Fatalf("after retry %d people notified, want 10 including the failed one", len(seen))
	}
}

// A follower source that cannot answer releases the job for retry without
// moving the token; "could not ask" is never "nobody follows".
func TestLiveFanout_FollowerSourceErrorIsRetried(t *testing.T) {
	fol := &fakeFollowers{err: errors.New("graph-service down")}
	store := newFakeStore()
	f := liveFanoutWithFollowers(&fakeNotifier{}, store, &fakeSource{}, &fakeReminders{}, fol)
	job := liveJob(uuid.New(), uuid.Nil)
	err := f.processJob(context.Background(), job)
	if err == nil || !strings.Contains(err.Error(), "follower page") {
		t.Fatalf("err = %v, want the follower page error so the job is released", err)
	}
	if len(store.followerAdvances) != 0 || job.FollowerCursor != "" {
		t.Fatalf("a failed fetch moved the token: %+v", store.followerAdvances)
	}
	// The earlier phases are behind it for good.
	if job.Phase != postgres.FanoutPhaseFollowers || store.followerBegins != 1 {
		t.Fatalf("phase %q, hand-overs %d", job.Phase, store.followerBegins)
	}
}

// A creator without a channel: reminder holders, then followers. The
// subscriber source is never asked about the nil channel.
func TestLiveFanout_NoChannel_RemindersThenFollowers(t *testing.T) {
	holder, follower := uuid.New(), uuid.New()
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: []uuid.UUID{holder}}}}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: ids(3)}}}
	fol := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: []uuid.UUID{holder, follower}}}}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := liveFanoutWithFollowers(notifier, store, src, rem, fol)

	if err := f.processJob(context.Background(), liveJob(uuid.New(), uuid.Nil)); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(src.afters) != 0 {
		t.Fatal("subscriber source asked about a nil channel")
	}
	if len(notifier.got) != 2 || notifier.got[0].RecipientID != holder || notifier.got[1].RecipientID != follower {
		t.Fatalf("notified %+v, want the reminder holder then the follower", notifier.got)
	}
	if want := []string{"r", "f"}; !reflect.DeepEqual(store.order, want) {
		t.Fatalf("cursor writes ran %v, want %v", store.order, want)
	}
}

// A job already in the follower phase (a retry, a resumed crash) pages
// neither earlier group again and does not hand over a second time.
func TestLiveFanout_FollowerPhaseDoesNotRewalkEarlierPhases(t *testing.T) {
	rem := &fakeReminders{pages: []*livestream.Page{{IDs: ids(2)}}}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: ids(2)}}}
	follower := uuid.New()
	fol := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: []uuid.UUID{follower}}}}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := liveFanoutWithFollowers(notifier, store, src, rem, fol)
	job := liveJob(uuid.New(), uuid.New())
	job.Phase = postgres.FanoutPhaseFollowers
	job.FollowerCursor = "tok-9"

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(rem.afters) != 0 || len(src.afters) != 0 {
		t.Fatalf("follower phase re-paged reminders %d times, subscribers %d times", len(rem.afters), len(src.afters))
	}
	if store.followerBegins != 0 {
		t.Fatal("a job already in the follower phase handed over again")
	}
	if len(fol.cursors) != 1 || fol.cursors[0] != "tok-9" {
		t.Fatalf("resumed from %v, want the stored token", fol.cursors)
	}
	if len(notifier.got) != 1 || notifier.got[0].RecipientID != follower {
		t.Fatalf("notified %+v", notifier.got)
	}
}

// An upload job never reaches the followers, whatever its row says:
// uploads go to channel subscribers only.
func TestLiveFanout_UploadJobsNeverEnterFollowerPhase(t *testing.T) {
	for _, phase := range []string{"", postgres.FanoutPhaseSubscribers, postgres.FanoutPhaseReminders, postgres.FanoutPhaseFollowers} {
		sub := uuid.New()
		src := &fakeSource{pages: []*subscribers.Page{{IDs: []uuid.UUID{sub}, NextAfter: sub}}}
		fol := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: ids(3)}}}
		store, notifier := newFakeStore(), &fakeNotifier{}
		f := liveFanoutWithFollowers(notifier, store, src, &fakeReminders{}, fol)
		job := uploadJob(uuid.New(), uuid.New())
		job.Phase = phase

		if err := f.processJob(context.Background(), job); err != nil {
			t.Fatalf("phase %q: processJob: %v", phase, err)
		}
		if len(fol.cursors) != 0 || store.followerBegins != 0 || len(store.followerAdvances) != 0 {
			t.Fatalf("phase %q: upload job paged followers %d times, hand-overs %d", phase, len(fol.cursors), store.followerBegins)
		}
		if len(notifier.got) != 1 || notifier.got[0].RecipientID != sub {
			t.Fatalf("phase %q: notified %+v, want the one subscriber", phase, notifier.got)
		}
	}
}

// A source whose token does not move must end the walk, not spin.
func TestLiveFanout_NonAdvancingFollowerTokenEndsTheWalk(t *testing.T) {
	for _, c := range [][2]string{{"", ""}, {"stuck", "stuck"}} {
		fol := &fakeFollowers{repeat: true, pages: []*graph.FollowerPage{{IDs: ids(2), NextCursor: c[1]}}}
		store := newFakeStore()
		f := liveFanoutWithFollowers(&fakeNotifier{}, store, &fakeSource{}, &fakeReminders{}, fol)
		job := liveJob(uuid.New(), uuid.Nil)
		job.Phase = postgres.FanoutPhaseFollowers
		job.FollowerCursor = c[0]

		done := make(chan error, 1)
		go func() { done <- f.processJob(context.Background(), job) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("at %q: %v", c[0], err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("at %q: the walk never ended", c[0])
		}
		if fol.calls != 1 {
			t.Fatalf("at %q: fetched %d pages, want 1", c[0], fol.calls)
		}
	}
}

// The 30-minute rule holds inside a long follower walk: once the stream
// started longer ago than the window, the remaining followers are not
// told it "is live", and the job finishes rather than retrying.
func TestLiveFanout_FollowerWalkStopsAtTheNotifyWindow(t *testing.T) {
	job := liveJob(uuid.New(), uuid.Nil)
	job.Phase = postgres.FanoutPhaseFollowers
	p1, p2 := ids(2), ids(2)
	fol := &fakeFollowers{pages: []*graph.FollowerPage{
		{IDs: p1, NextCursor: "tok-1"},
		{IDs: p2, NextCursor: "tok-2"},
		{IDs: ids(2)},
	}}
	// The window closes while the second page is being delivered.
	fol.onCall = func(n int) {
		if n == 1 {
			job.PostCreatedAt = time.Now().Add(-liveNotifyWindow - time.Minute)
		}
	}
	store, notifier := newFakeStore(), &fakeNotifier{}
	f := liveFanoutWithFollowers(notifier, store, &fakeSource{}, &fakeReminders{}, fol)

	if err := f.processJob(context.Background(), job); err != nil {
		t.Fatalf("a job past its window must complete, not retry: %v", err)
	}
	if fol.calls != 2 || len(notifier.got) != 4 {
		t.Fatalf("fetched %d pages and notified %d, want 2 pages / 4 people then stop", fol.calls, len(notifier.got))
	}
}

// Without a follower source wired (a partial deployment) the job still
// tells the first two groups and completes.
func TestLiveFanout_NoFollowerSourceStillCompletes(t *testing.T) {
	sub := uuid.New()
	src := &fakeSource{pages: []*subscribers.Page{{IDs: []uuid.UUID{sub}, NextAfter: sub}}}
	notifier := &fakeNotifier{}
	f := liveFanoutWith(notifier, newFakeStore(), src, &fakeReminders{})
	if err := f.processJob(context.Background(), liveJob(uuid.New(), uuid.New())); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if len(notifier.got) != 1 {
		t.Fatalf("notified %d, want the subscriber", len(notifier.got))
	}
}

// The follower probe: one id from the start, false for nobody, an error
// (never a quiet false) when graph-service cannot be asked.
func TestLiveProbes_Followers(t *testing.T) {
	ctx := context.Background()
	creator := uuid.New()

	fol := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: ids(1), NextCursor: "more"}}}
	f := liveFanoutWithFollowers(&fakeNotifier{}, newFakeStore(), &fakeSource{}, &fakeReminders{}, fol)
	if ok, err := f.HasFollowers(ctx, creator); err != nil || !ok {
		t.Fatalf("HasFollowers = %v, %v", ok, err)
	}
	if fol.limits[0] != 1 || fol.cursors[0] != "" || fol.users[0] != creator {
		t.Fatalf("probe asked limit %d cursor %q user %s", fol.limits[0], fol.cursors[0], fol.users[0])
	}

	empty := liveFanoutWithFollowers(&fakeNotifier{}, newFakeStore(), &fakeSource{}, &fakeReminders{}, &fakeFollowers{})
	if ok, err := empty.HasFollowers(ctx, creator); ok || err != nil {
		t.Fatalf("no followers reported as %v, %v", ok, err)
	}

	down := liveFanoutWithFollowers(&fakeNotifier{}, newFakeStore(), &fakeSource{}, &fakeReminders{},
		&fakeFollowers{err: errors.New("down")})
	if _, err := down.HasFollowers(ctx, creator); err == nil {
		t.Fatal("a failed probe must report its error")
	}

	// Not wired: nobody, quietly, like the other two probes.
	unwired := liveFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{}, &fakeReminders{})
	if ok, err := unwired.HasFollowers(ctx, creator); ok || err != nil {
		t.Fatalf("unwired probe = %v, %v", ok, err)
	}
	var nilClient *graph.Client
	unwired.SetFollowerSource(nilClient)
	if unwired.followers != nil {
		t.Fatal("a nil client became a non-nil follower source")
	}
}

// Followers go through the same per-recipient gate as the other groups:
// a block in either direction suppresses, and on a followers-only stream
// only an actual follow relationship delivers.
func TestLiveFanout_FollowerPhaseHonoursBlocksAndVisibility(t *testing.T) {
	creator := uuid.New()
	plain, blocked, blockedBy, notFollowing := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rels := map[string]string{
		plain.String():        `{"data":{"follows":true}}`,
		blocked.String():      `{"data":{"follows":true,"blocked":true}}`,
		blockedBy.String():    `{"data":{"follows":true,"blocked_by":true}}`,
		notFollowing.String(): `{"data":{}}`,
	}
	graphSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("other_id") != creator.String() {
			t.Errorf("unexpected graph call %s", r.URL)
		}
		_, _ = w.Write([]byte(rels[r.URL.Query().Get("user_id")]))
	}))
	defer graphSrv.Close()

	for visibility, want := range map[string][]uuid.UUID{
		"public":    {plain, notFollowing},
		"followers": {plain},
	} {
		fol := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: []uuid.UUID{plain, blocked, blockedBy, notFollowing}}}}
		store, notifier := newFakeStore(), &fakeNotifier{}
		f := liveFanoutWithFollowers(notifier, store, &fakeSource{}, &fakeReminders{}, fol)
		f.SetEligibilityDeps(graphSrv.URL, "", "k")
		job := liveJob(creator, uuid.Nil)
		job.Visibility = visibility

		if err := f.processJob(context.Background(), job); err != nil {
			t.Fatalf("%s: processJob: %v", visibility, err)
		}
		got := notifier.recipients()
		if len(got) != len(want) {
			t.Fatalf("%s: notified %d, want %d", visibility, len(got), len(want))
		}
		for _, id := range want {
			if _, ok := got[id]; !ok {
				t.Fatalf("%s: %s not notified", visibility, id)
			}
		}
		if _, ok := got[blocked]; ok {
			t.Fatalf("%s: a follower the creator blocked was notified", visibility)
		}
		if _, ok := got[blockedBy]; ok {
			t.Fatalf("%s: a follower who blocked the creator was notified", visibility)
		}
	}
}

// A second run of a finished job (duplicate claim, full retry) tells no
// follower twice.
func TestLiveFanout_FollowersSecondRunNotifiesNobodyAgain(t *testing.T) {
	creator := uuid.New()
	followers := ids(3)
	store, notifier := newFakeStore(), &fakeNotifier{}
	stream := uuid.New()
	run := func() {
		fol := &fakeFollowers{pages: []*graph.FollowerPage{{IDs: followers}}}
		job := liveJob(creator, uuid.Nil)
		job.PostID = stream
		if err := liveFanoutWithFollowers(notifier, store, &fakeSource{}, &fakeReminders{}, fol).processJob(context.Background(), job); err != nil {
			t.Fatalf("processJob: %v", err)
		}
	}
	run()
	run()
	if len(notifier.got) != 3 {
		t.Fatalf("delivery calls = %d after two runs, want 3", len(notifier.got))
	}
}
