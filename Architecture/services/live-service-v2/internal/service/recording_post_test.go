package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// fakePosts answers RecordingPost per stream; a stream with no entry has no
// post yet.
type fakePosts struct {
	posts map[uuid.UUID]RecordingPost
	errs  map[uuid.UUID]error
	calls map[uuid.UUID]int
	total int
	// deadlines records, per call, whether the context carried a deadline.
	deadlines []bool
}

func newFakePosts() *fakePosts {
	return &fakePosts{posts: map[uuid.UUID]RecordingPost{}, errs: map[uuid.UUID]error{}, calls: map[uuid.UUID]int{}}
}

func (f *fakePosts) RecordingPost(ctx context.Context, streamID uuid.UUID) (RecordingPost, error) {
	f.calls[streamID]++
	f.total++
	_, has := ctx.Deadline()
	f.deadlines = append(f.deadlines, has)
	if err := f.errs[streamID]; err != nil {
		return RecordingPost{}, err
	}
	if p, ok := f.posts[streamID]; ok {
		return p, nil
	}
	return RecordingPost{}, ErrRecordingPostNotYet
}

// recordedStream adds an ended stream whose recording import is done
// (vod_ready went out) to the rig.
func recordedStream(t *testing.T, r *rig, host uuid.UUID) *postgres.LiveStream {
	t.Helper()
	st := r.store.AddStreamStatus(host, stEnded)
	if _, err := r.store.SetRecording(ctx, st.ID, "https://s3/live-recordings/recordings/"+st.ID.String()+".mp4", 90,
		postgres.RecordingImport{Bucket: "live-recordings", ObjectKey: "recordings/" + st.ID.String() + ".mp4", DurationMs: 90000}); err != nil {
		t.Fatal(err)
	}
	if err := r.store.CompleteImport(ctx, st.ID, uuid.New(), func(*postgres.LiveStream, postgres.RecordingImport) ([]postgres.OutboxEvent, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	return r.store.Stream(st.ID)
}

func postRig(t *testing.T) (*rig, *fakePosts, uuid.UUID) {
	t.Helper()
	host := uuid.New()
	r := newRig(host)
	posts := newFakePosts()
	r.svc.posts = posts
	return r, posts, host
}

func lookupState(t *testing.T, r *rig, id uuid.UUID) string {
	t.Helper()
	row := r.store.PostLookup(id)
	if row == nil {
		t.Fatalf("stream %s has no lookup row", id)
	}
	return row.State
}

// TestRecordingPostStoredOnce: the sweeper stores the id and stops asking.
func TestRecordingPostStoredOnce(t *testing.T) {
	r, posts, host := postRig(t)
	st := recordedStream(t, r, host)
	post := uuid.New()
	posts.posts[st.ID] = RecordingPost{PostID: post, Visibility: "unlisted"}

	if err := r.svc.Sweep(ctx); err != nil { // the sweeper itself runs the lookups
		t.Fatal(err)
	}
	got := r.store.Stream(st.ID).RecordingPostID
	if got == nil || *got != post || lookupState(t, r, st.ID) != postgres.PostLookupFound {
		t.Fatalf("recording_post_id=%v state=%s", got, lookupState(t, r, st.ID))
	}
	// Settled: nobody asks again, however much later.
	posts.posts[st.ID] = RecordingPost{PostID: uuid.New()}
	r.clock.Advance(48 * time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	if _, err := r.svc.GetStream(ctx, st.ID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if posts.calls[st.ID] != 1 || *r.store.Stream(st.ID).RecordingPostID != post {
		t.Fatalf("calls=%d id=%v, want one call and the first id", posts.calls[st.ID], r.store.Stream(st.ID).RecordingPostID)
	}
}

// TestRecordingPostNotOverwritten: an id already on the row survives a
// lookup that answers a different one.
func TestRecordingPostNotOverwritten(t *testing.T) {
	r, posts, host := postRig(t)
	st := recordedStream(t, r, host)
	first, other := uuid.New(), uuid.New()
	r.store.Streams[st.ID].RecordingPostID = &first
	posts.posts[st.ID] = RecordingPost{PostID: other}

	r.svc.ResolveRecordingPosts(ctx)
	if got := r.store.Stream(st.ID).RecordingPostID; posts.calls[st.ID] != 1 || got == nil || *got != first {
		t.Fatalf("calls=%d recording_post_id=%v, want %s kept", posts.calls[st.ID], got, first)
	}
	if lookupState(t, r, st.ID) != postgres.PostLookupFound {
		t.Fatalf("state=%s", lookupState(t, r, st.ID))
	}
}

// TestRecordingPostNotYetIsRetried: a 404 stores nothing and is asked again
// after the backoff, not before.
func TestRecordingPostNotYetIsRetried(t *testing.T) {
	r, posts, host := postRig(t)
	st := recordedStream(t, r, host)

	r.svc.ResolveRecordingPosts(ctx)
	if posts.calls[st.ID] != 1 || r.store.Stream(st.ID).RecordingPostID != nil || lookupState(t, r, st.ID) != postgres.PostLookupPending {
		t.Fatalf("after a 404: calls=%d id=%v state=%s", posts.calls[st.ID], r.store.Stream(st.ID).RecordingPostID, lookupState(t, r, st.ID))
	}
	r.svc.ResolveRecordingPosts(ctx) // inside the backoff
	r.clock.Advance(postLookupBackoff(1) - time.Second)
	r.svc.ResolveRecordingPosts(ctx)
	if posts.calls[st.ID] != 1 {
		t.Fatalf("asked again inside the backoff: %d calls", posts.calls[st.ID])
	}
	r.clock.Advance(2 * time.Second)
	r.svc.ResolveRecordingPosts(ctx)
	if posts.calls[st.ID] != 2 || r.store.PostLookup(st.ID).Attempts != 2 {
		t.Fatalf("after the backoff: calls=%d attempts=%d", posts.calls[st.ID], r.store.PostLookup(st.ID).Attempts)
	}
	// The post appears: the next due lookup stores it.
	post := uuid.New()
	posts.posts[st.ID] = RecordingPost{PostID: post}
	r.clock.Advance(postLookupBackoff(2) + time.Second)
	r.svc.ResolveRecordingPosts(ctx)
	if got := r.store.Stream(st.ID).RecordingPostID; got == nil || *got != post {
		t.Fatalf("recording_post_id=%v", got)
	}
}

// TestRecordingPostErrorIsRetried: a failed lookup is not an answer. It is
// asked again, and it does not end the lookup at 24h the way a 404 does.
func TestRecordingPostErrorIsRetried(t *testing.T) {
	r, posts, host := postRig(t)
	st := recordedStream(t, r, host)
	posts.errs[st.ID] = errors.New("recording post: status 503")

	r.svc.ResolveRecordingPosts(ctx)
	r.clock.Advance(postLookupBackoff(1) + time.Second)
	r.svc.ResolveRecordingPosts(ctx)
	if posts.calls[st.ID] != 2 || r.store.Stream(st.ID).RecordingPostID != nil {
		t.Fatalf("calls=%d id=%v", posts.calls[st.ID], r.store.Stream(st.ID).RecordingPostID)
	}
	r.clock.Advance(postLookupGiveUp + time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	if lookupState(t, r, st.ID) != postgres.PostLookupPending {
		t.Fatalf("an erroring lookup ended at 24h: %s", lookupState(t, r, st.ID))
	}
	// post-service recovers: the stream is still backfilled.
	post := uuid.New()
	delete(posts.errs, st.ID)
	posts.posts[st.ID] = RecordingPost{PostID: post}
	r.clock.Advance(postLookupMaxBackoff + time.Second)
	r.svc.ResolveRecordingPosts(ctx)
	if got := r.store.Stream(st.ID).RecordingPostID; got == nil || *got != post {
		t.Fatalf("recording_post_id=%v", got)
	}

	// Nothing but errors for 72h does end it.
	st2 := recordedStream(t, r, host)
	posts.errs[st2.ID] = errors.New("recording post: status 500")
	r.clock.Advance(postLookupErrorGiveUp + time.Minute)
	r.svc.ResolveRecordingPosts(ctx)
	if lookupState(t, r, st2.ID) != postgres.PostLookupGaveUp {
		t.Fatalf("72h of errors: %s", lookupState(t, r, st2.ID))
	}
}

// TestRecordingPostDeletedIsNotStored: a deleted post is not offered and the
// stream is never asked about again — by the sweeper or by a read.
func TestRecordingPostDeletedIsNotStored(t *testing.T) {
	r, posts, host := postRig(t)
	st := recordedStream(t, r, host)
	posts.posts[st.ID] = RecordingPost{PostID: uuid.New(), Deleted: true}

	r.svc.ResolveRecordingPosts(ctx)
	if r.store.Stream(st.ID).RecordingPostID != nil || lookupState(t, r, st.ID) != postgres.PostLookupDeleted {
		t.Fatalf("id=%v state=%s", r.store.Stream(st.ID).RecordingPostID, lookupState(t, r, st.ID))
	}
	r.clock.Advance(time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	got, err := r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if err != nil || got.RecordingPostID != nil || posts.calls[st.ID] != 1 {
		t.Fatalf("after the stop: err=%v id=%v calls=%d", err, got.RecordingPostID, posts.calls[st.ID])
	}

	// Seen deleted on a detail read: the same.
	onRead := recordedStream(t, r, host)
	posts.posts[onRead.ID] = RecordingPost{PostID: uuid.New(), Deleted: true}
	if got, err := r.svc.GetStream(ctx, onRead.ID, uuid.Nil); err != nil || got.RecordingPostID != nil || lookupState(t, r, onRead.ID) != postgres.PostLookupDeleted {
		t.Fatalf("deleted on a read: %v id=%v state=%s", err, got.RecordingPostID, lookupState(t, r, onRead.ID))
	}

	// Two replicas: one stored the id a moment before the post was deleted,
	// the other's lookup (claimed before that) then sees it deleted. The id
	// is cleared.
	st2 := recordedStream(t, r, host)
	stale := uuid.New()
	posts.posts[st2.ID] = RecordingPost{PostID: stale}
	r.svc.ResolveRecordingPosts(ctx)
	if got := r.store.Stream(st2.ID).RecordingPostID; got == nil || *got != stale {
		t.Fatalf("setup: %v", got)
	}
	posts.posts[st2.ID] = RecordingPost{PostID: stale, Deleted: true}
	r.svc.lookupRecordingPost(ctx, postgres.PostLookup{StreamID: st2.ID}, true)
	if r.store.Stream(st2.ID).RecordingPostID != nil || lookupState(t, r, st2.ID) != postgres.PostLookupDeleted {
		t.Fatalf("a deleted post is still offered: id=%v state=%s", r.store.Stream(st2.ID).RecordingPostID, lookupState(t, r, st2.ID))
	}
}

// TestRecordingPostGivesUpAfter24h: 404s for a day end the lookup; a stream
// whose import finished long ago is still asked once (the backfill).
func TestRecordingPostGivesUpAfter24h(t *testing.T) {
	r, posts, host := postRig(t)
	st := recordedStream(t, r, host)

	for i := 0; i < 400 && lookupState(t, r, st.ID) == postgres.PostLookupPending; i++ {
		r.svc.ResolveRecordingPosts(ctx)
		if r.clock.Now().Sub(*r.store.Imports[st.ID].DoneAt) < postLookupGiveUp && lookupState(t, r, st.ID) != postgres.PostLookupPending {
			t.Fatalf("gave up before 24h (call %d)", posts.calls[st.ID])
		}
		r.clock.Advance(postLookupMaxBackoff + time.Second)
	}
	if lookupState(t, r, st.ID) != postgres.PostLookupGaveUp {
		t.Fatalf("state=%s after %d calls", lookupState(t, r, st.ID), posts.calls[st.ID])
	}
	asked := posts.calls[st.ID]
	if asked < 90 || asked > 120 { // 24h at a 15-minute cap, plus the ramp
		t.Fatalf("%d lookups in 24h", asked)
	}
	posts.posts[st.ID] = RecordingPost{PostID: uuid.New()}
	r.clock.Advance(48 * time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	if _, err := r.svc.GetStream(ctx, st.ID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if posts.calls[st.ID] != asked || r.store.Stream(st.ID).RecordingPostID != nil {
		t.Fatalf("asked after giving up: %d -> %d", asked, posts.calls[st.ID])
	}

	// Backfill: an import that finished 30h before the first lookup.
	old := recordedStream(t, r, host)
	oldPost := uuid.New()
	posts.posts[old.ID] = RecordingPost{PostID: oldPost}
	r.clock.Advance(30 * time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	if got := r.store.Stream(old.ID).RecordingPostID; got == nil || *got != oldPost {
		t.Fatalf("an old stream was not backfilled: %v", got)
	}
	// ...and with no post, that one lookup is its last.
	gone := recordedStream(t, r, host)
	r.clock.Advance(30 * time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	if posts.calls[gone.ID] != 1 || lookupState(t, r, gone.ID) != postgres.PostLookupGaveUp {
		t.Fatalf("old stream with no post: calls=%d state=%s", posts.calls[gone.ID], lookupState(t, r, gone.ID))
	}
}

// TestRecordingPostBoundedPerTick: one tick asks about at most
// postLookupBatch streams, and a claimed stream is not asked again by the
// next tick.
func TestRecordingPostBoundedPerTick(t *testing.T) {
	r, posts, host := postRig(t)
	if postLookupBatch > 20 { // 15s ticks: at most 80 lookups a minute
		t.Fatalf("postLookupBatch = %d: one tick may ask about at most 20 streams", postLookupBatch)
	}
	const n = 2*postLookupBatch + 5
	for i := 0; i < n; i++ {
		recordedStream(t, r, host)
	}
	r.svc.ResolveRecordingPosts(ctx)
	if posts.total != postLookupBatch {
		t.Fatalf("first tick: %d lookups, want %d", posts.total, postLookupBatch)
	}
	r.svc.ResolveRecordingPosts(ctx)
	r.svc.ResolveRecordingPosts(ctx)
	if posts.total != n {
		t.Fatalf("three ticks: %d lookups, want %d", posts.total, n)
	}
	for id, c := range posts.calls {
		if c != 1 {
			t.Fatalf("stream %s asked %d times", id, c)
		}
	}
	r.svc.ResolveRecordingPosts(ctx)
	if posts.total != n {
		t.Fatalf("a fourth tick asked again inside the backoff: %d", posts.total)
	}
}

// TestRecordingPostOnlyForFinishedImports: nothing is asked for a stream
// with no recording, a pending or failed import, or with no post-service.
func TestRecordingPostOnlyForFinishedImports(t *testing.T) {
	r, posts, host := postRig(t)
	plain := r.store.AddStreamStatus(host, stEnded)
	pending := r.store.AddStreamStatus(host, stEnded)
	failed := r.store.AddStreamStatus(host, stEnded)
	for _, st := range []*postgres.LiveStream{pending, failed} {
		if _, err := r.store.SetRecording(ctx, st.ID, "https://s3/x.mp4", 9, postgres.RecordingImport{Bucket: "b", ObjectKey: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.TerminateImport(ctx, failed.ID, nil, "failed", "x"); err != nil {
		t.Fatal(err)
	}
	r.svc.ResolveRecordingPosts(ctx)
	for _, st := range []*postgres.LiveStream{plain, pending, failed} {
		if _, err := r.svc.GetStream(ctx, st.ID, uuid.Nil); err != nil {
			t.Fatal(err)
		}
	}
	if posts.total != 0 {
		t.Fatalf("%d lookups for streams with no finished import", posts.total)
	}

	done := recordedStream(t, r, host)
	r.svc.posts = nil
	r.svc.ResolveRecordingPosts(ctx)
	if got, err := r.svc.GetStream(ctx, done.ID, uuid.Nil); err != nil || got.RecordingPostID != nil {
		t.Fatalf("no post-service: %v %v", got, err)
	}
	if posts.total != 0 || lookupState(t, r, done.ID) != postgres.PostLookupPending {
		t.Fatalf("no post-service: lookups=%d state=%s", posts.total, lookupState(t, r, done.ID))
	}
}

// TestRecordingPostOnDetailRead: the detail read of an ended stream with a
// recording asks once, bounded, and serves the id it found; a failure is
// ignored, and repeated reads do not repeat the lookup.
func TestRecordingPostOnDetailRead(t *testing.T) {
	r, posts, host := postRig(t)
	st := recordedStream(t, r, host)
	post := uuid.New()
	posts.posts[st.ID] = RecordingPost{PostID: post}

	got, err := r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if err != nil || got.RecordingPostID == nil || *got.RecordingPostID != post {
		t.Fatalf("detail read: %+v %v", got, err)
	}
	if len(posts.deadlines) != 1 || !posts.deadlines[0] {
		t.Fatalf("the read's lookup had no deadline: %v", posts.deadlines)
	}
	if stored := r.store.Stream(st.ID).RecordingPostID; stored == nil || *stored != post {
		t.Fatalf("not stored: %v", stored)
	}
	if _, err := r.svc.GetStream(ctx, st.ID, uuid.Nil); err != nil || posts.calls[st.ID] != 1 {
		t.Fatalf("a settled stream was asked again: %d %v", posts.calls[st.ID], err)
	}

	// Failure and "not yet": the read succeeds without the id, counts no
	// attempt, and reads inside the gap do not ask again.
	for name, lookupErr := range map[string]error{"error": errors.New("boom"), "not yet": nil} {
		st := recordedStream(t, r, host)
		if lookupErr != nil {
			posts.errs[st.ID] = lookupErr
		}
		for i := 0; i < 5; i++ {
			got, err := r.svc.GetStream(ctx, st.ID, uuid.Nil)
			if err != nil || got.RecordingPostID != nil {
				t.Fatalf("%s: read %d: %+v %v", name, i, got, err)
			}
		}
		row := r.store.PostLookup(st.ID)
		if posts.calls[st.ID] != 1 || row.State != postgres.PostLookupPending || row.Attempts != 0 {
			t.Fatalf("%s: calls=%d row=%+v", name, posts.calls[st.ID], row)
		}
		r.clock.Advance(postLookupReadGap + time.Second)
		if _, err := r.svc.GetStream(ctx, st.ID, uuid.Nil); err != nil || posts.calls[st.ID] != 2 {
			t.Fatalf("%s: after the gap: calls=%d %v", name, posts.calls[st.ID], err)
		}
	}

	// A reader who may not see the stream triggers nothing.
	hidden := recordedStream(t, r, host)
	r.store.Streams[hidden.ID].Visibility = visibilityFollowers
	if _, err := r.svc.GetStream(ctx, hidden.ID, uuid.New()); err == nil || posts.calls[hidden.ID] != 0 {
		t.Fatalf("hidden stream: err=%v calls=%d", err, posts.calls[hidden.ID])
	}
}

func TestPostLookupBackoff(t *testing.T) {
	want := []time.Duration{15 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for n, w := range want {
		if got := postLookupBackoff(n); got != w {
			t.Fatalf("backoff(%d) = %s, want %s", n, got, w)
		}
	}
	if got := postLookupBackoff(1000); got != postLookupMaxBackoff {
		t.Fatalf("backoff(1000) = %s", got)
	}
}

// TestHTTPRecordingPosts: the client's reading of post-service's answers.
func TestHTTPRecordingPosts(t *testing.T) {
	const key = "rp-test-key"
	stream, post := uuid.New(), uuid.New()
	status, body := http.StatusOK, ""
	var gotPath, gotKey, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath, gotKey, gotUser = req.URL.Path, req.Header.Get("X-Internal-Service-Key"), req.Header.Get("X-User-Id")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := NewHTTPRecordingPosts(srv.URL+"/", key)

	body = `{"data":{"post_id":"` + post.String() + `","visibility":"unlisted","deleted":false}}`
	got, err := c.RecordingPost(ctx, stream)
	if err != nil || got.PostID != post || got.Visibility != "unlisted" || got.Deleted {
		t.Fatalf("found: %+v %v", got, err)
	}
	if gotPath != "/v1/internal/posts/by-live-stream/"+stream.String() || gotKey != key || gotUser != "" {
		t.Fatalf("request: path=%q key set=%v user=%q", gotPath, gotKey == key, gotUser)
	}

	body = `{"data":{"post_id":"` + post.String() + `","visibility":"public","deleted":true}}`
	if got, err := c.RecordingPost(ctx, stream); err != nil || !got.Deleted {
		t.Fatalf("deleted: %+v %v", got, err)
	}

	status, body = http.StatusNotFound, `{"error":"not_found"}`
	if _, err := c.RecordingPost(ctx, stream); !errors.Is(err, ErrRecordingPostNotYet) {
		t.Fatalf("404: %v", err)
	}
	// Anything else is unknown, never "no post" and never a post.
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusServiceUnavailable, `{"error":"unresolved"}`},
		{http.StatusUnauthorized, `{}`},
		{http.StatusInternalServerError, ``},
		{http.StatusOK, `{"data":{}}`},
		{http.StatusOK, `{"data":{"post_id":"` + uuid.Nil.String() + `"}}`},
		{http.StatusOK, `not json`},
	} {
		status, body = tc.status, tc.body
		got, err := c.RecordingPost(ctx, stream)
		if err == nil || errors.Is(err, ErrRecordingPostNotYet) || got.PostID != uuid.Nil {
			t.Fatalf("status %d body %q: %+v %v", tc.status, tc.body, got, err)
		}
	}

	if NewHTTPRecordingPosts("  ", key) != nil {
		t.Fatal("an unconfigured client was built")
	}
	srv.Close()
	if _, err := c.RecordingPost(ctx, stream); err == nil || errors.Is(err, ErrRecordingPostNotYet) {
		t.Fatalf("network failure: %v", err)
	}
}
