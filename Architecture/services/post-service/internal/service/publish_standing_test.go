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

	"github.com/atpost/post-service/internal/standing"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Author standing at publication (Copyright Match plan 6.4, P-5), no
// database. Every interactive entry point is called on a Service whose
// store is nil: the standing refusal MUST come back before any store
// call, or the test panics — which is the point. The DB-bound paths
// (draft, scheduled and video publishes) are in
// publish_standing_integration_test.go.

// standingFixture is a fake trust-safety standing route that answers one
// body (or one status) and counts calls.
type standingFixture struct {
	srv    *httptest.Server
	body   atomic.Value
	status atomic.Int32
	calls  atomic.Int32
}

const (
	standingOKBody        = `{"data":{"standing":"ok","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`
	standingSuspendedBody = `{"data":{"standing":"suspended","policy_version":"standing-v1","suspended_until":"2026-12-27T09:30:00Z","active_strikes":[{"id":"0b6c1d4e-8f2a-4c3b-9d1e-2f3a4b5c6d7e","severity":"severe_strike","reason":"copyright: upheld case","case_id":null,"issued_at":"2026-09-28T09:30:00Z","expires_at":"2026-12-27T09:30:00Z"}]}}`
)

func newStandingFixture(t *testing.T, body string) *standingFixture {
	t.Helper()
	f := &standingFixture{}
	f.body.Store(body)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if !strings.HasPrefix(r.URL.Path, standing.Path) || !strings.HasPrefix(r.Header.Get(standing.HeaderServiceAuthorization), "Bearer ") {
			http.Error(w, `{"error":{"code":"UNAUTHORIZED"}}`, http.StatusUnauthorized)
			return
		}
		if st := int(f.status.Load()); st != 0 {
			http.Error(w, `{"error":{"code":"STANDING_UNAVAILABLE"}}`, st)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.body.Load().(string)))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// standingTestClient builds a real standing.Client against the fixture.
func standingTestClient(t *testing.T, f *standingFixture, now func() time.Time) *standing.Client {
	t.Helper()
	_, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(standing.Issuer, "p1", priv)
	if err != nil {
		t.Fatal(err)
	}
	c, err := standing.New(standing.Config{BaseURL: f.srv.URL, InternalKey: "k", Signer: signer, Now: now,
		HTTPClient: &http.Client{Timeout: 2 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newStandingService(t *testing.T, body string) (*Service, *standingFixture) {
	t.Helper()
	f := newStandingFixture(t, body)
	return &Service{standing: standingTestClient(t, f, nil), requireStandingCheck: true}, f
}

var suspendedUntilFixture = time.Date(2026, 12, 27, 9, 30, 0, 0, time.UTC)

func assertSuspended(t *testing.T, err error) {
	t.Helper()
	var suspended *AuthorSuspendedError
	if !errors.As(err, &suspended) {
		t.Fatalf("want *AuthorSuspendedError, got %v", err)
	}
	if !errors.Is(err, ErrAuthorSuspended) {
		t.Fatal("errors.Is(err, ErrAuthorSuspended) must hold")
	}
	if errors.Is(err, ErrStandingUnknown) {
		t.Fatal("a refusal is not unknown")
	}
	if suspended.Standing != "suspended" || suspended.SuspendedUntil == nil || !suspended.SuspendedUntil.Equal(suspendedUntilFixture) {
		t.Fatalf("suspended=%+v", suspended)
	}
	if suspended.PolicyVersion != "standing-v1" {
		t.Fatalf("policy_version=%q", suspended.PolicyVersion)
	}
}

func assertUnknown(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrStandingUnknown) {
		t.Fatalf("want ErrStandingUnknown, got %v", err)
	}
	if errors.Is(err, ErrAuthorSuspended) {
		t.Fatal("unknown is not a refusal")
	}
}

// interactivePaths are the entry points that answer the author directly
// and need no store to reach the standing check.
func interactivePaths(t *testing.T, svc *Service, author uuid.UUID) map[string]func() error {
	t.Helper()
	return map[string]func() error{
		"CreatePost": func() error {
			_, err := svc.CreatePost(context.Background(), &CreatePostInput{AuthorID: author, Text: "hello"})
			return err
		},
		"CreateThread": func() error {
			_, err := svc.CreateThread(context.Background(), &CreateThreadInput{AuthorID: author, Entries: []ThreadEntryInput{{Text: "one"}, {Text: "two"}}})
			return err
		},
		"CreateRepost": func() error {
			_, err := svc.CreateRepost(context.Background(), CreateRepostInput{UserID: author, PostID: uuid.New(), Type: "plain"})
			return err
		},
		"CreateCrosspost": func() error {
			_, err := svc.CreateCrosspost(context.Background(), uuid.New(), author, "postbook")
			return err
		},
		"CreateStoryPending": func() error {
			_, err := svc.CreateStoryPending(context.Background(), &CreateStoryInput{AuthorID: author, MediaID: uuid.New(), MediaType: "image"})
			return err
		},
	}
}

func TestPublishStanding_EveryInteractivePathRefusesASuspendedAuthor(t *testing.T) {
	svc, f := newStandingService(t, standingSuspendedBody)
	author := uuid.New()
	for name, call := range interactivePaths(t, svc, author) {
		t.Run(name, func(t *testing.T) {
			assertSuspended(t, call())
		})
	}
	// One author, five paths, ONE call to trust-safety: the deny is cached.
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("trust-safety was asked %d times; the answer is cached for 60 s", got)
	}
}

func TestPublishStanding_EveryInteractivePathFailsClosedWhenUnknown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *standingFixture)
	}{
		{"trust-safety 503", func(f *standingFixture) { f.status.Store(http.StatusServiceUnavailable) }},
		{"credential refused 403", func(f *standingFixture) { f.status.Store(http.StatusForbidden) }},
		{"answer outside the enum", func(f *standingFixture) {
			f.body.Store(`{"data":{"standing":"good","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, f := newStandingService(t, standingOKBody)
			tc.setup(f)
			for name, call := range interactivePaths(t, svc, uuid.New()) {
				t.Run(name, func(t *testing.T) { assertUnknown(t, call()) })
			}
		})
	}
	t.Run("trust-safety unreachable", func(t *testing.T) {
		f := newStandingFixture(t, standingOKBody)
		f.srv.Close()
		svc := &Service{standing: standingTestClient(t, f, nil), requireStandingCheck: true}
		for name, call := range interactivePaths(t, svc, uuid.New()) {
			t.Run(name, func(t *testing.T) { assertUnknown(t, call()) })
		}
	})
	t.Run("no client configured", func(t *testing.T) {
		svc := &Service{requireStandingCheck: true}
		for name, call := range interactivePaths(t, svc, uuid.New()) {
			t.Run(name, func(t *testing.T) { assertUnknown(t, call()) })
		}
	})
}

func TestPublishStanding_RestrictedRefusesWithoutAnEnd(t *testing.T) {
	svc, _ := newStandingService(t, `{"data":{"standing":"restricted","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`)
	err := svc.requirePublishStanding(context.Background(), uuid.New())
	var suspended *AuthorSuspendedError
	if !errors.As(err, &suspended) || suspended.Standing != "restricted" || suspended.SuspendedUntil != nil {
		t.Fatalf("got %v", err)
	}
	if suspended.BlockReason() != BlockReasonAuthorSuspended {
		t.Fatalf("block reason=%q", suspended.BlockReason())
	}
}

func TestPublishStanding_OKAllows(t *testing.T) {
	svc, f := newStandingService(t, standingOKBody)
	author := uuid.New()
	for i := 0; i < 3; i++ {
		if err := svc.requirePublishStanding(context.Background(), author); err != nil {
			t.Fatalf("ok must allow: %v", err)
		}
	}
	if f.calls.Load() != 1 {
		t.Fatalf("allow must be cached: %d calls", f.calls.Load())
	}
}

func TestPublishStanding_DevOptOutOnlyWithoutAClient(t *testing.T) {
	// The opt-out covers a dev stack with NO key. A wired client is always
	// consulted, whatever the flag says.
	svc := &Service{requireStandingCheck: false}
	if err := svc.requirePublishStanding(context.Background(), uuid.New()); err != nil {
		t.Fatalf("opt-out without a client must allow: %v", err)
	}
	wired, _ := newStandingService(t, standingSuspendedBody)
	wired.requireStandingCheck = false
	assertSuspended(t, wired.requirePublishStanding(context.Background(), uuid.New()))
	// A nil author is never "ok".
	assertUnknown(t, wired.requirePublishStanding(context.Background(), uuid.Nil))
}

func TestPublishStanding_BlockReasonCarriesTheEnd(t *testing.T) {
	e := &AuthorSuspendedError{Standing: "suspended", SuspendedUntil: &suspendedUntilFixture}
	if got := e.BlockReason(); got != "author_suspended until 2026-12-27T09:30:00Z" {
		t.Fatalf("reason=%q", got)
	}
	if !strings.HasPrefix(e.BlockReason(), BlockReasonAuthorSuspended) {
		t.Fatal("the UI keys on the prefix")
	}
}

// ── live VOD (Kafka consumer, background policy) ─────────────────────

func liveVODInput(store *fakeLiveVODStore) (LiveVODInput, uuid.UUID) {
	stream, creator, media := uuid.New(), uuid.New(), uuid.New()
	store.media[media] = postgres.MediaOwnership{UploaderID: creator, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 90_000}
	store.bySuffix["live-recordings/"+stream.String()+".mp4"] = media
	return LiveVODInput{StreamID: stream, CreatorID: creator, RecordingURL: "http://minio:9000/bucket/live-recordings/" + stream.String() + ".mp4", Title: "t"}, creator
}

func TestCreateLiveVODPost_SuspendedCreatorIsSkippedNotRetried(t *testing.T) {
	store := newFakeLiveVODStore()
	svc, _ := newStandingService(t, standingSuspendedBody)
	svc.liveVOD = store
	in, _ := liveVODInput(store)
	out, err := svc.CreateLiveVODPost(context.Background(), in)
	if err != nil {
		t.Fatalf("a refusal is a skip, not a consumer retry: %v", err)
	}
	if out == nil || !strings.Contains(out.Skipped, BlockReasonAuthorSuspended) {
		t.Fatalf("outcome=%+v", out)
	}
	if len(store.inserts) != 0 {
		t.Fatal("no post may be inserted for a suspended creator")
	}
}

func TestCreateLiveVODPost_UnknownIsAnErrorForTheConsumerToRetry(t *testing.T) {
	store := newFakeLiveVODStore()
	svc, f := newStandingService(t, standingOKBody)
	f.status.Store(http.StatusInternalServerError)
	svc.liveVOD = store
	in, _ := liveVODInput(store)
	_, err := svc.CreateLiveVODPost(context.Background(), in)
	assertUnknown(t, err)
	if len(store.inserts) != 0 {
		t.Fatal("no post may be inserted through uncertainty")
	}
}

func TestCreateLiveVODPost_OKPublishes(t *testing.T) {
	store := newFakeLiveVODStore()
	svc, _ := newStandingService(t, standingOKBody)
	svc.liveVOD = store
	in, _ := liveVODInput(store)
	out, err := svc.CreateLiveVODPost(context.Background(), in)
	if err != nil || out == nil || !out.Created {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

// ── backoff (background paths) ───────────────────────────────────────

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func TestStandingBackoff_ExponentialCappedAndGivesUpAfter24h(t *testing.T) {
	ck := &fakeClock{t: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	b := newStandingBackoff(ck.now)
	key := "post_draft:x"
	if !b.due(key) {
		t.Fatal("an unseen item is due")
	}
	wantDelays := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for i, want := range wantDelays {
		giveUp, next := b.recordUnknown(key)
		if giveUp {
			t.Fatalf("attempt %d: gave up too early", i+1)
		}
		if got := next.Sub(ck.now()); got != want {
			t.Fatalf("attempt %d: delay=%s want %s", i+1, got, want)
		}
		if b.due(key) {
			t.Fatalf("attempt %d: must not be due right away", i+1)
		}
		ck.add(want - time.Second)
		if b.due(key) {
			t.Fatalf("attempt %d: not due one second early", i+1)
		}
		ck.add(time.Second)
		if !b.due(key) {
			t.Fatalf("attempt %d: due once the delay has passed", i+1)
		}
	}
	// Elapsed so far: ~2h03m. Keep failing hourly until 24 h since the
	// first unknown; only then does it give up.
	for !func() bool { giveUp, _ := b.recordUnknown(key); return giveUp }() {
		if ck.t.Sub(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)) > 25*time.Hour {
			t.Fatal("never gave up")
		}
		ck.add(time.Hour)
	}
	if since := ck.t.Sub(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)); since < standingBlockAfter || since > standingBlockAfter+time.Hour {
		t.Fatalf("gave up after %s, want ~%s", since, standingBlockAfter)
	}
	b.clear(key)
	if !b.due(key) {
		t.Fatal("cleared item is due again")
	}
	if giveUp, _ := b.recordUnknown(key); giveUp {
		t.Fatal("a cleared item starts its 24 h over")
	}
}

func TestBackgroundPublishStanding_Policy(t *testing.T) {
	ck := &fakeClock{t: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	svc, f := newStandingService(t, standingOKBody)
	svc.standing = standingTestClient(t, f, ck.now)
	svc.standingBackoff = newStandingBackoff(ck.now)
	author := uuid.New()
	key := standingKeyScheduledPost(uuid.New())

	// ok → proceed
	v, reason, err := svc.backgroundPublishStanding(context.Background(), key, author)
	if v != standingProceed || reason != "" || err != nil {
		t.Fatalf("ok: v=%d reason=%q err=%v", v, reason, err)
	}

	// unknown → retry later, and inside the window trust-safety is not asked
	f.status.Store(http.StatusServiceUnavailable)
	ck.add(2 * time.Minute) // past the cache
	v, _, err = svc.backgroundPublishStanding(context.Background(), key, author)
	if v != standingRetryLater {
		t.Fatalf("unknown: v=%d err=%v", v, err)
	}
	assertUnknown(t, err)
	calls := f.calls.Load()
	v, _, err = svc.backgroundPublishStanding(context.Background(), key, author)
	if v != standingRetryLater || f.calls.Load() != calls {
		t.Fatalf("inside the backoff window trust-safety must not be asked: v=%d calls %d→%d", v, calls, f.calls.Load())
	}
	assertUnknown(t, err)
	// A different item is not held back by this one's window.
	if v, _, _ := svc.backgroundPublishStanding(context.Background(), standingKeyScheduledPost(uuid.New()), author); v != standingRetryLater || f.calls.Load() == calls {
		t.Fatal("backoff is per item")
	}

	// 24 h of unknown → block with standing_unavailable
	ck.add(standingBlockAfter)
	v, reason, err = svc.backgroundPublishStanding(context.Background(), key, author)
	if v != standingBlock || reason != BlockReasonStandingUnavailable {
		t.Fatalf("after 24h: v=%d reason=%q err=%v", v, reason, err)
	}
	assertUnknown(t, err)

	// deny → block with the suspension reason, immediately (no backoff)
	f.status.Store(0)
	f.body.Store(standingSuspendedBody)
	svc.standing.(*standing.Client).Forget(author)
	v, reason, err = svc.backgroundPublishStanding(context.Background(), key, author)
	if v != standingBlock || reason != "author_suspended until 2026-12-27T09:30:00Z" {
		t.Fatalf("deny: v=%d reason=%q err=%v", v, reason, err)
	}
	assertSuspended(t, err)
	// And the item's backoff was cleared by the decision.
	if !svc.standingBackoffState().due(key) {
		t.Fatal("a decided item carries no backoff")
	}
}

// PublishScheduledPostDrafts and PublishScheduledDrafts keep their claim
// semantics; the standing decision for a claimed row is exercised against
// the real store in the integration test. Here: the worker-side helper on
// the composer draft releases or parks through the store seam, which is
// nil in this package's unit tests, so it must not be reached on an allow.
func TestDraftPublishStanding_AllowTouchesNoStore(t *testing.T) {
	svc, _ := newStandingService(t, standingOKBody)
	d := &postgres.PostDraft{ID: uuid.New(), AuthorID: uuid.New()}
	if err := svc.draftPublishStanding(context.Background(), d, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.draftPublishStanding(context.Background(), d, false); err != nil {
		t.Fatal(err)
	}
}
