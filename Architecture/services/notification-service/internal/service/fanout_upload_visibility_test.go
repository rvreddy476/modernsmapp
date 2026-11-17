package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/subscribers"
	"github.com/google/uuid"
)

// Upload fan-out against a fake post-service and graph-service (lv-1,
// lv-17, lv-18, lv-19, lv-56, lv-71; reels-30). The fake post-service
// answers the three routes the worker uses:
//
//	GET /v1/posts/:id                          as the author (X-User-Id)
//	GET /v1/posts/internal/moderation-subject/:id
//	GET /v1/internal/posts/:id/visibility[?viewer_id=]

type fakePostService struct {
	mu sync.Mutex
	// authorStatus/authorBody answer the author read; authorStatus 0 = 200.
	authorStatus int
	author       map[string]any
	// subjectStatus answers moderation-subject (0 = 200 with subjectDeleted).
	subjectStatus  int
	subjectDeleted bool
	// anonVisible answers the viewer-less visibility read; viewers the
	// per-viewer read (missing = false). visStatus overrides both (0 = 200).
	anonVisible bool
	viewers     map[uuid.UUID]bool
	visStatus   int
	// what was asked
	authorHeaders []string
	viewerAsks    int
}

func (p *fakePostService) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("internal key missing on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/posts/internal/moderation-subject/"):
			if p.subjectStatus != 0 {
				w.WriteHeader(p.subjectStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"deleted": p.subjectDeleted}})
		case strings.HasPrefix(r.URL.Path, "/v1/internal/posts/") && strings.HasSuffix(r.URL.Path, "/visibility"):
			if r.Header.Get("X-User-Id") != "" {
				t.Errorf("visibility read must not carry an identity header")
			}
			if p.visStatus != 0 {
				w.WriteHeader(p.visStatus)
				return
			}
			visible := p.anonVisible
			if raw := r.URL.Query().Get("viewer_id"); raw != "" {
				p.viewerAsks++
				visible = p.viewers[uuid.MustParse(raw)]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"visible": visible})
		case strings.HasPrefix(r.URL.Path, "/v1/posts/"):
			p.authorHeaders = append(p.authorHeaders, r.Header.Get("X-User-Id"))
			if p.authorStatus != 0 && p.authorStatus != http.StatusOK {
				w.WriteHeader(p.authorStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": p.author})
		case strings.HasPrefix(r.URL.Path, "/v1/channels/"):
			_, _ = w.Write([]byte(`{"data":{"name":"Cal B"}}`))
		default:
			t.Errorf("unexpected post-service call %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (p *fakePostService) set(fn func(p *fakePostService)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

// fakeGraph answers /v1/graph/relationship: follows for the listed users.
type fakeGraph struct {
	mu      sync.Mutex
	follows map[uuid.UUID]bool
	status  int
	// blocked: the author blocked this viewer; blockedBy: the viewer
	// blocked the author; muted: the viewer muted the author.
	blocked, blockedBy, muted map[uuid.UUID]bool
}

func (g *fakeGraph) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.status != 0 {
			w.WriteHeader(g.status)
			return
		}
		viewer := uuid.MustParse(r.URL.Query().Get("user_id"))
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"follows": g.follows[viewer], "blocked": g.blocked[viewer],
			"blocked_by": g.blockedBy[viewer], "is_muted": g.muted[viewer],
		}})
	})
}

func publicReady() map[string]any {
	return map[string]any{"visibility": "public", "review_status": "approved", "title": "From the post"}
}

type uploadRig struct {
	f        *SubscriberFanout
	post     *fakePostService
	graph    *fakeGraph
	store    *fakeStore
	notifier *fakeNotifier
	subs     []uuid.UUID
	job      *postgres.FanoutJob
}

func newUploadRig(t *testing.T, nSubs int, author map[string]any) *uploadRig {
	t.Helper()
	post := &fakePostService{author: author, anonVisible: true, viewers: map[uuid.UUID]bool{}}
	graph := &fakeGraph{follows: map[uuid.UUID]bool{}}
	postSrv := httptest.NewServer(post.handler(t))
	t.Cleanup(postSrv.Close)
	graphSrv := httptest.NewServer(graph.handler())
	t.Cleanup(graphSrv.Close)

	subs := ids(nSubs)
	r := &uploadRig{post: post, graph: graph, store: newFakeStore(), notifier: &fakeNotifier{}, subs: subs}
	r.job = uploadJob(uuid.New(), uuid.New())
	r.f = newSubscriberFanoutWith(r.notifier, r.store, &rerunSource{ids: subs})
	r.f.SetEligibilityDeps(graphSrv.URL, postSrv.URL, "k")
	fixed := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	r.f.now = func() time.Time { return fixed }
	return r
}

// pass runs one claim's worth of work and records its outcome.
func (r *uploadRig) pass() {
	r.job.Attempts++
	r.f.settle(context.Background(), r.job, r.f.processJob(context.Background(), r.job))
}

// rerunSource serves the same single page on every walk, like the real
// subscriber source does when a job starts again from its cursor.
type rerunSource struct{ ids []uuid.UUID }

func (s *rerunSource) SubscriberIDs(_ context.Context, _ uuid.UUID, after uuid.UUID, _ int) (*subscribers.Page, error) {
	if after != uuid.Nil {
		return &subscribers.Page{}, nil
	}
	return &subscribers.Page{IDs: s.ids, NextAfter: s.ids[len(s.ids)-1], HasMore: false}, nil
}
func (s *rerunSource) ChannelByOwner(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (r *uploadRig) lastOutcome(t *testing.T) string {
	t.Helper()
	if len(r.store.outcomes) == 0 {
		t.Fatal("no job outcome recorded")
	}
	return r.store.outcomes[len(r.store.outcomes)-1]
}

// lv-1: a video still processing at create is not written off. The job
// waits without deciding anybody, and once the post is ready every
// subscriber is told exactly once; a later pass tells nobody again.
func TestUploadFanout_ProcessingAtCreateNotifiedOnceWhenReady(t *testing.T) {
	processing := publicReady()
	processing["review_status"] = "pending"
	processing["is_processing"] = true
	r := newUploadRig(t, 3, processing)

	r.pass()
	if got := r.lastOutcome(t); got != "deferred:review_pending" {
		t.Fatalf("outcome = %q, want deferred:review_pending", got)
	}
	if len(r.notifier.got) != 0 || len(r.store.delivered) != 0 {
		t.Fatalf("a waiting job decided recipients: notified %d, markers %d", len(r.notifier.got), len(r.store.delivered))
	}
	if want := r.f.clock().Add(30 * time.Second); !r.store.dueAt[0].Equal(want) {
		t.Fatalf("first wait due %v, want %v", r.store.dueAt[0], want)
	}
	// The job is read as its author, the viewer post-service never hides
	// a processing post from.
	if h := r.post.authorHeaders[0]; h != r.job.AuthorID.String() {
		t.Fatalf("post read as %q, want the author", h)
	}

	// Approved but the media overlay still says processing: still waits.
	r.post.set(func(p *fakePostService) { p.author["review_status"] = "approved" })
	r.pass()
	if got := r.lastOutcome(t); got != "deferred:processing" {
		t.Fatalf("outcome = %q, want deferred:processing", got)
	}

	r.post.set(func(p *fakePostService) { p.author = publicReady() })
	r.pass()
	if got := r.lastOutcome(t); got != "completed" {
		t.Fatalf("outcome = %q, want completed", got)
	}
	if got := r.notifier.recipients(); len(got) != 3 {
		t.Fatalf("notified %d, want 3", len(got))
	}

	// A repeat trigger re-running the job tells nobody twice.
	r.pass()
	if len(r.notifier.got) != 3 {
		t.Fatalf("second run sent %d notifications in all, want 3", len(r.notifier.got))
	}
}

// A job whose title was missing from its trigger gets it from the post.
func TestUploadFanout_TitleFilledFromPost(t *testing.T) {
	r := newUploadRig(t, 1, publicReady())
	r.job.Title = ""
	r.pass()
	if u := r.notifier.recipients()[r.subs[0]]; u.Title != "From the post" {
		t.Fatalf("title = %q", u.Title)
	}
}

// Deleted: the author read 404s and the moderation subject confirms it.
// Terminal, and nobody is told.
func TestUploadFanout_DeletedIsSkipped(t *testing.T) {
	for name, subject := range map[string]func(p *fakePostService){
		"row gone":    func(p *fakePostService) { p.subjectStatus = http.StatusNotFound },
		"soft delete": func(p *fakePostService) { p.subjectDeleted = true },
	} {
		t.Run(name, func(t *testing.T) {
			r := newUploadRig(t, 2, publicReady())
			r.post.set(func(p *fakePostService) { p.authorStatus = http.StatusNotFound; subject(p) })
			r.pass()
			if got := r.lastOutcome(t); got != "completed" {
				t.Fatalf("outcome = %q, want completed", got)
			}
			if len(r.notifier.got) != 0 {
				t.Fatal("a deleted post was announced")
			}
		})
	}
}

// An author read that 404s for a row that still exists (a post-service
// that stopped honouring X-User-Id) must wait, never drop.
func TestUploadFanout_Unconfirmed404Waits(t *testing.T) {
	r := newUploadRig(t, 2, publicReady())
	r.post.set(func(p *fakePostService) { p.authorStatus = http.StatusNotFound })
	r.pass()
	if got := r.lastOutcome(t); got != "deferred:not_readable" {
		t.Fatalf("outcome = %q, want deferred:not_readable", got)
	}
	if len(r.store.delivered) != 0 {
		t.Fatal("markers written for an unconfirmed 404")
	}
}

// lv-18: a followers-only upload reaches followers whom post-service lets
// read it, and nobody else.
func TestUploadFanout_FollowersOnlyReachesFollowers(t *testing.T) {
	post := publicReady()
	post["visibility"] = "followers"
	r := newUploadRig(t, 3, post)
	follower, stranger, followerPS := r.subs[0], r.subs[1], r.subs[2]
	r.graph.follows[follower] = true
	r.graph.follows[followerPS] = true
	r.post.set(func(p *fakePostService) {
		p.anonVisible = false
		p.viewers[follower] = true
		p.viewers[followerPS] = false // post-service says no (e.g. restricted)
		// Even if post-service said yes to a non-follower, the follow check
		// still holds: two independent gates.
		p.viewers[stranger] = true
	})

	r.pass()
	got := r.notifier.recipients()
	if _, ok := got[follower]; !ok || len(got) != 1 {
		t.Fatalf("notified %v, want only the follower", got)
	}
	if !r.store.delivered[stranger] || !r.store.delivered[followerPS] {
		t.Fatal("ineligible recipients must be decided (marked), not retried")
	}
}

// lv-18: a public upload by a private account is not readable by a
// stranger, so each recipient is asked about; approved followers hear.
func TestUploadFanout_PrivateAccountAsksPerRecipient(t *testing.T) {
	r := newUploadRig(t, 2, publicReady())
	r.post.set(func(p *fakePostService) {
		p.anonVisible = false
		p.viewers[r.subs[0]] = true
	})
	r.pass()
	got := r.notifier.recipients()
	if _, ok := got[r.subs[0]]; !ok || len(got) != 1 {
		t.Fatalf("notified %v, want only the approved follower", got)
	}
	if r.post.viewerAsks != 2 {
		t.Fatalf("per-viewer reads = %d, want 2", r.post.viewerAsks)
	}
}

// A public post anyone may read needs no per-recipient post read.
func TestUploadFanout_PublicPostSkipsPerViewerRead(t *testing.T) {
	r := newUploadRig(t, 4, publicReady())
	r.pass()
	if len(r.notifier.got) != 4 || r.post.viewerAsks != 0 {
		t.Fatalf("notified %d with %d per-viewer reads; want 4 and 0", len(r.notifier.got), r.post.viewerAsks)
	}
}

// lv-19: a post-service 5xx is a retry with backoff, not a completion and
// not a lost recipient; the retry later delivers.
func TestUploadFanout_TransientErrorRetriedLater(t *testing.T) {
	r := newUploadRig(t, 2, publicReady())
	r.post.set(func(p *fakePostService) { p.authorStatus = http.StatusBadGateway })
	r.pass()
	if got := r.lastOutcome(t); got != "released" {
		t.Fatalf("outcome = %q, want released", got)
	}
	if want := r.f.clock().Add(30 * time.Second); !r.store.dueAt[0].Equal(want) {
		t.Fatalf("retry due %v, want %v", r.store.dueAt[0], want)
	}
	if len(r.store.delivered) != 0 {
		t.Fatal("a failed read decided recipients")
	}

	// A graph outage mid-walk: recipients are held, the job is released.
	r.post.set(func(p *fakePostService) { p.authorStatus = 0 })
	r.graph.status = http.StatusServiceUnavailable
	r.pass()
	if got := r.lastOutcome(t); got != "released" || len(r.store.delivered) != 0 {
		t.Fatalf("outcome = %q markers %d; want released, none", got, len(r.store.delivered))
	}
	// The second retry waits longer than the first.
	if d := r.store.dueAt[1].Sub(r.f.clock()); d != time.Minute {
		t.Fatalf("second retry delay = %v, want 1m", d)
	}

	r.graph.status = 0
	r.pass()
	if got := r.lastOutcome(t); got != "completed" || len(r.notifier.got) != 2 {
		t.Fatalf("outcome = %q notified %d; want completed, 2", got, len(r.notifier.got))
	}
}

// lv-71: an 18+ upload is a deterministic decision, never a retry loop.
func TestUploadFanout_AgeRestrictedIsDeterministic(t *testing.T) {
	adult := publicReady()
	adult["age_restricted"] = true

	r := newUploadRig(t, 2, adult)
	r.pass()
	if got := r.lastOutcome(t); got != "completed" || len(r.notifier.got) != 2 {
		t.Fatalf("default policy: outcome %q notified %d; want completed, 2", got, len(r.notifier.got))
	}

	r = newUploadRig(t, 2, adult)
	r.f.SetUploadPolicy(UploadPolicy{NotifyMembersOnly: true, NotifyAgeRestricted: false})
	r.pass()
	if got := r.lastOutcome(t); got != "completed" || len(r.notifier.got) != 0 {
		t.Fatalf("age flag off: outcome %q notified %d; want completed, 0", got, len(r.notifier.got))
	}

	// An age refusal on the read itself (401/403) is decided too.
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		r = newUploadRig(t, 1, publicReady())
		r.post.set(func(p *fakePostService) { p.authorStatus = status })
		r.pass()
		if got := r.lastOutcome(t); got != "completed" || len(r.notifier.got) != 1 {
			t.Fatalf("read %d: outcome %q notified %d; want completed, 1", status, got, len(r.notifier.got))
		}
	}
}

// lv-56: members-only uploads keep today's behaviour by default; the flag
// turns them off.
func TestUploadFanout_MembersOnlyBehindFlag(t *testing.T) {
	gated := publicReady()
	gated["tier_required_id"] = uuid.New().String()

	r := newUploadRig(t, 2, gated)
	r.pass()
	if len(r.notifier.got) != 2 {
		t.Fatalf("default: notified %d, want 2 (today's behaviour)", len(r.notifier.got))
	}

	r = newUploadRig(t, 2, gated)
	r.f.SetUploadPolicy(UploadPolicy{NotifyMembersOnly: false, NotifyAgeRestricted: true})
	r.pass()
	if len(r.notifier.got) != 0 || r.lastOutcome(t) != "completed" {
		t.Fatalf("flag off: notified %d outcome %q", len(r.notifier.got), r.lastOutcome(t))
	}
}

// The creator's notify_subscribers=false holds even when the job came from
// the "post became visible" trigger, which does not carry it.
func TestUploadFanout_CreatorOptOutHonoured(t *testing.T) {
	post := publicReady()
	post["notify_subscribers"] = false
	r := newUploadRig(t, 2, post)
	r.pass()
	if len(r.notifier.got) != 0 || r.lastOutcome(t) != "completed" {
		t.Fatalf("notified %d outcome %q", len(r.notifier.got), r.lastOutcome(t))
	}
}

// A post that never becomes visible expires after the wait ceiling
// instead of polling for ever.
func TestUploadFanout_WaitExpires(t *testing.T) {
	post := publicReady()
	post["visibility"] = "private"
	r := newUploadRig(t, 1, post)
	r.job.Waits = fanoutMaxWaits
	r.pass()
	if got := r.lastOutcome(t); got != "expired:visibility_private" {
		t.Fatalf("outcome = %q", got)
	}
}

// The wait curve covers long transcodes: about 72 h before expiry.
func TestFanoutBackoff_CurveAndCeiling(t *testing.T) {
	if fanoutBackoff(1) != 30*time.Second || fanoutBackoff(2) != time.Minute || fanoutBackoff(40) != 30*time.Minute {
		t.Fatalf("curve: %v %v %v", fanoutBackoff(1), fanoutBackoff(2), fanoutBackoff(40))
	}
	var total time.Duration
	for n := 1; n <= fanoutMaxWaits; n++ {
		total += fanoutBackoff(n)
	}
	if total < 72*time.Hour || total > 80*time.Hour {
		t.Fatalf("total wait %v, want about 72 h", total)
	}
	var retries time.Duration
	for n := 1; n <= fanoutMaxAttempts; n++ {
		retries += fanoutBackoff(n)
	}
	if retries < 3*time.Hour {
		t.Fatalf("retry budget %v; a post-service rollout must not lose an upload", retries)
	}
}

func TestClassifyUpload(t *testing.T) {
	def := DefaultUploadPolicy()
	deleted := "2026-11-17T00:00:00Z"
	off := false
	tier := uuid.New().String()
	cases := []struct {
		name   string
		v      authorView
		gate   uploadGate
		reason string
	}{
		{"public approved", authorView{Visibility: "public", ReviewStatus: "approved"}, gateReady, ""},
		{"legacy empty review", authorView{Visibility: "", ReviewStatus: ""}, gateReady, ""},
		{"followers", authorView{Visibility: "followers", ReviewStatus: "approved"}, gateReady, ""},
		{"deleted", authorView{Visibility: "public", ReviewStatus: "approved", DeletedAt: &deleted}, gateSkip, "deleted"},
		{"opted out", authorView{Visibility: "public", ReviewStatus: "approved", NotifySubscribers: &off}, gateSkip, "creator_opted_out"},
		{"pending", authorView{Visibility: "public", ReviewStatus: "pending"}, gateWait, "review_pending"},
		{"rejected", authorView{Visibility: "public", ReviewStatus: "rejected"}, gateWait, "review_rejected"},
		{"held", authorView{Visibility: "public", ReviewStatus: "approved", Restrictions: []json.RawMessage{[]byte(`{}`)}}, gateWait, "restricted"},
		{"processing", authorView{Visibility: "public", ReviewStatus: "approved", IsProcessing: true}, gateWait, "processing"},
		{"scheduled", authorView{Visibility: "public", ReviewStatus: "approved", IsScheduled: true}, gateWait, "scheduled"},
		{"private", authorView{Visibility: "private", ReviewStatus: "approved"}, gateWait, "visibility_private"},
		{"unlisted", authorView{Visibility: "unlisted", ReviewStatus: "approved"}, gateWait, "visibility_unlisted"},
		{"close friends", authorView{Visibility: "close_friends", ReviewStatus: "approved"}, gateSkip, "retired_audience"},
		{"members default", authorView{Visibility: "public", ReviewStatus: "approved", TierRequiredID: &tier}, gateReady, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := classifyUpload(c.v, def)
			if st.gate != c.gate || st.reason != c.reason {
				t.Fatalf("gate %v reason %q, want %v %q", st.gate, st.reason, c.gate, c.reason)
			}
		})
	}
}

// Blocks hold in both directions on a public upload, where no per-viewer
// post read is made; a mute is not a visibility rule, so a subscriber who
// muted the author is still told (the decision pinned here).
func TestUploadFanout_BlocksBothDirectionsMuteDoesNot(t *testing.T) {
	r := newUploadRig(t, 4, publicReady())
	blocked, blocker, muter, plain := r.subs[0], r.subs[1], r.subs[2], r.subs[3]
	r.graph.blocked = map[uuid.UUID]bool{blocked: true}
	r.graph.blockedBy = map[uuid.UUID]bool{blocker: true}
	r.graph.muted = map[uuid.UUID]bool{muter: true}

	r.pass()
	got := r.notifier.recipients()
	if _, ok := got[blocked]; ok {
		t.Fatal("a viewer the author blocked was told")
	}
	if _, ok := got[blocker]; ok {
		t.Fatal("a viewer who blocked the author was told")
	}
	if _, ok := got[muter]; !ok {
		t.Fatal("a subscriber who muted the author was dropped")
	}
	if _, ok := got[plain]; !ok || len(got) != 2 {
		t.Fatalf("notified %v, want the muter and the plain subscriber", got)
	}
	if !r.store.delivered[blocked] || !r.store.delivered[blocker] {
		t.Fatal("blocked recipients must be decided (marked), not retried")
	}
}

// A follow removed between enqueue and delivery is read at delivery: the
// former follower is not told about a followers-only upload.
func TestUploadFanout_FollowRemovedBeforeDelivery(t *testing.T) {
	post := publicReady()
	post["visibility"] = "followers"
	r := newUploadRig(t, 1, post)
	sub := r.subs[0]
	r.post.set(func(p *fakePostService) { p.anonVisible = false; p.viewers[sub] = true })
	r.graph.follows[sub] = true
	r.graph.follows[sub] = false // unfollowed while the job waited

	r.pass()
	if len(r.notifier.got) != 0 {
		t.Fatal("a former follower was told about a followers-only upload")
	}
}

// channelSource is the rerunSource with a controllable channel lookup.
type channelSource struct {
	rerunSource
	mu      sync.Mutex
	channel uuid.UUID
	err     error
}

func (s *channelSource) ChannelByOwner(context.Context, uuid.UUID) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channel, s.err
}

// An upload job enqueued without its channel (the visible trigger's lookup
// failed) resolves it in the worker: a failure is a retry with backoff,
// never a completion, and the retry tells everyone; a clean "no channel"
// ends the job with nobody told.
func TestUploadFanout_ChannelResolvedByWorker(t *testing.T) {
	r := newUploadRig(t, 2, publicReady())
	channel := uuid.New()
	src := &channelSource{rerunSource: rerunSource{ids: r.subs}, err: errors.New("post-service 503")}
	r.f.subs = src
	r.job.ChannelID = uuid.Nil

	r.pass()
	if got := r.lastOutcome(t); got != "released" || len(r.notifier.got) != 0 {
		t.Fatalf("lookup failure: outcome %q notified %d; want released, 0", got, len(r.notifier.got))
	}

	src.mu.Lock()
	src.channel, src.err = channel, nil
	src.mu.Unlock()
	r.pass()
	if got := r.lastOutcome(t); got != "completed" || len(r.notifier.got) != 2 {
		t.Fatalf("after recovery: outcome %q notified %d; want completed, 2", got, len(r.notifier.got))
	}
	for _, u := range r.notifier.got {
		if u.ChannelID != channel {
			t.Fatalf("notification channel %s, want %s", u.ChannelID, channel)
		}
	}

	r = newUploadRig(t, 2, publicReady())
	r.f.subs = &channelSource{rerunSource: rerunSource{ids: r.subs}}
	r.job.ChannelID = uuid.Nil
	r.pass()
	if got := r.lastOutcome(t); got != "completed" || len(r.notifier.got) != 0 {
		t.Fatalf("no channel: outcome %q notified %d; want completed, 0", got, len(r.notifier.got))
	}
}

// A deactivated or deletion-scheduled recipient is told nothing; a
// suppression lookup that fails is an error (the fan-out holds the
// recipient and retries), never a silent drop that the fan-out would then
// mark as decided for good.
func TestCreateUploadNotification_SuppressionLookup(t *testing.T) {
	n := UploadNotification{RecipientID: uuid.New(), AuthorID: uuid.New(), NotifType: "creator_uploaded_video", PostID: uuid.New()}

	s := &Service{suppressionSeam: func(context.Context, uuid.UUID) (bool, error) { return true, nil }}
	if err := s.CreateUploadNotification(context.Background(), n); err != nil {
		t.Fatalf("suppressed recipient: %v, want a quiet nil", err)
	}

	s = &Service{suppressionSeam: func(context.Context, uuid.UUID) (bool, error) {
		return false, errors.New("postgres: connection reset")
	}}
	if err := s.CreateUploadNotification(context.Background(), n); err == nil {
		t.Fatal("a failed suppression lookup was swallowed; the fan-out would mark the recipient told")
	}
}
