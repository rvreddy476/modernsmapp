package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Dating backend audit H2: a send in a dating conversation that chat has not
// closed yet still asks dating whether the match may chat, so a suspension,
// chat hold or deletion stops the chat without waiting for an event.

// stubDatingState answers every match with open (or err), counting calls.
type stubDatingState struct {
	mu    sync.Mutex
	open  bool
	hold  bool // not open for a reversible review hold
	err   error
	calls int
}

func (s *stubDatingState) DatingMatchChatState(context.Context, uuid.UUID) (datingVerdict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return datingVerdict{Open: s.open, OnHold: !s.open && s.hold}, s.err
}

func (s *stubDatingState) set(open bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open, s.hold, s.err = open, false, err
}

func (s *stubDatingState) setHold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open, s.hold, s.err = false, true, nil
}

func (s *stubDatingState) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func trySend(f *datingFixture, from uuid.UUID, key string) error {
	_, err := f.svc.SendMessage(context.Background(), from, f.convID, "text", "hello", nil, nil, nil, key)
	return err
}

func TestDatingSendRefusedWhenDatingSaysTheMatchIsClosed(t *testing.T) {
	f := newDatingFixture(t, "", true)
	st := &stubDatingState{open: false}
	f.svc.datingMatchState = st
	if err := trySend(f, f.userA, "h2-closed"); !errors.Is(err, ErrMatchClosed) {
		t.Fatalf("send with dating reporting the match closed: err = %v, want ErrMatchClosed", err)
	}
	if len(f.store.rows) != 0 {
		t.Fatal("a refused send recorded a first-message obligation")
	}
	if st.count() != 1 {
		t.Fatalf("dating asked %d times, want 1", st.count())
	}
}

func TestDatingSendFailsClosedWhenTheStateIsUnknown(t *testing.T) {
	f := newDatingFixture(t, "", true)
	f.svc.datingMatchState = &stubDatingState{err: errors.New("dating-service answered 503")}
	err := trySend(f, f.userA, "h2-unknown")
	if err == nil {
		t.Fatal("a send went through although dating could not be asked")
	}
	if errors.Is(err, ErrMatchClosed) {
		t.Fatalf("an outage must not look like a closed match (clients treat 409 as final): %v", err)
	}
	if !errors.Is(err, errDatingStateUnavailable) {
		t.Fatalf("err = %v, want errDatingStateUnavailable", err)
	}
	// And the failure is not cached: the next send asks again.
	f.svc.datingMatchState = &stubDatingState{open: true}
	if err := trySend(f, f.userA, "h2-unknown-2"); err != nil {
		t.Fatalf("send after dating recovered: %v", err)
	}
}

func TestDatingSendWithoutMatchIDRefused(t *testing.T) {
	f := newDatingFixture(t, "", true)
	f.store.meta[f.convID].MatchID = nil
	st := &stubDatingState{open: true}
	f.svc.datingMatchState = st
	if err := trySend(f, f.userA, "h2-nomatch"); !errors.Is(err, ErrMatchClosed) {
		t.Fatalf("dating conversation without a match id: err = %v, want ErrMatchClosed", err)
	}
	if st.count() != 0 {
		t.Fatal("asked dating about a nil match")
	}
}

func TestDatingSendSkipsDatingOnceChatIsClosed(t *testing.T) {
	f := newDatingFixture(t, "", true)
	closed := time.Now()
	f.store.meta[f.convID].ClosedAt = &closed
	st := &stubDatingState{open: true}
	f.svc.datingMatchState = st
	if err := trySend(f, f.userA, "h2-closed-at"); !errors.Is(err, ErrMatchClosed) {
		t.Fatalf("closed conversation: err = %v, want ErrMatchClosed", err)
	}
	if st.count() != 0 {
		t.Fatal("a conversation chat already closed needs no dating call")
	}
}

func TestNonDatingSendNeverAsksDating(t *testing.T) {
	f := newDatingFixture(t, "", false)
	st := &stubDatingState{open: false}
	f.svc.datingMatchState = st
	if err := trySend(f, f.userA, "h2-direct"); err != nil {
		t.Fatalf("plain direct send: %v", err)
	}
	if st.count() != 0 {
		t.Fatal("a non-dating conversation asked dating")
	}
}

func TestDatingStateIsCachedAndInvalidated(t *testing.T) {
	f := newDatingFixture(t, "", true)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f.svc.SetClock(func() time.Time { return now })
	st := &stubDatingState{open: true}
	f.svc.datingMatchState = st

	for i, key := range []string{"c1", "c2", "c3"} {
		if err := trySend(f, f.userA, key); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if st.count() != 1 {
		t.Fatalf("three sends inside the TTL asked dating %d times, want 1", st.count())
	}

	// Dating suspends a member. Within the open TTL the cached answer
	// stands; once it lapses the next send is refused.
	st.set(false, nil)
	now = now.Add(datingStateOpenTTL)
	if err := trySend(f, f.userA, "c4"); !errors.Is(err, ErrMatchClosed) {
		t.Fatalf("send after the open TTL with the member suspended: err = %v, want ErrMatchClosed", err)
	}
	// The closed answer is cached too.
	calls := st.count()
	if err := trySend(f, f.userB, "c5"); !errors.Is(err, ErrMatchClosed) || st.count() != calls {
		t.Fatalf("cached closed answer: err=%v calls %d -> %d", err, calls, st.count())
	}
	// A closure event for the match drops the cache entry.
	st.set(true, nil)
	f.svc.InvalidateDatingMatchState(f.matchID)
	if err := trySend(f, f.userA, "c6"); err != nil || st.count() != calls+1 {
		t.Fatalf("after invalidation: err=%v calls %d -> %d, want a fresh dating answer", err, calls, st.count())
	}
}

func TestDatingStateCacheBounded(t *testing.T) {
	var c datingMatchStateCache
	now := time.Now()
	for i := 0; i < datingStateMaxEntries; i++ {
		c.put(uuid.New(), datingVerdict{Open: true}, now)
	}
	id := uuid.New()
	c.put(id, datingVerdict{}, now)
	if len(c.entries) > datingStateMaxEntries {
		t.Fatalf("cache grew to %d entries", len(c.entries))
	}
	if v, ok := c.get(id, now); !ok || v.Open {
		t.Fatalf("the newest entry must be kept: verdict=%+v ok=%v", v, ok)
	}
}

// The HTTP checker: the route, the credential, no user identity, and every
// answer mapped (200 open/closed, 404 closed, anything else an error).
func TestHTTPDatingMatchStateAnswers(t *testing.T) {
	matchID := uuid.New()
	var (
		mu     sync.Mutex
		status = http.StatusOK
		body   = `{"data":{"match_id":"x","open":true}}`
		got    *http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		got = r.Clone(context.Background())
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	svc := &Service{datingServiceURL: srv.URL, internalServiceKey: testInternalKey, httpClient: srv.Client()}
	check := httpDatingMatchState{s: svc}
	answer := func(st int, b string) (bool, error) {
		mu.Lock()
		status, body = st, b
		mu.Unlock()
		v, err := check.DatingMatchChatState(context.Background(), matchID)
		return v.Open, err
	}

	if open, err := answer(http.StatusOK, `{"data":{"open":true}}`); err != nil || !open {
		t.Fatalf("200 open: %v %v", open, err)
	}
	if got.Method != http.MethodGet || got.URL.Path != "/v1/dating/internal/matches/"+matchID.String()+"/chat-state" {
		t.Fatalf("request = %s %s", got.Method, got.URL.Path)
	}
	if got.Header.Get("X-Internal-Service-Key") != testInternalKey {
		t.Fatal("the internal service key was not sent")
	}
	for _, h := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes"} {
		if got.Header.Get(h) != "" {
			t.Fatalf("a service call carried user identity header %s", h)
		}
	}
	if open, err := answer(http.StatusOK, `{"data":{"open":false,"reason":"suspended"}}`); err != nil || open {
		t.Fatalf("200 closed: %v %v", open, err)
	}
	// Only reason pending_review is a hold; any other not-open is a closure.
	for reason, hold := range map[string]bool{"pending_review": true, "suspended": false, "restricted": false, "": false} {
		mu.Lock()
		status, body = http.StatusOK, `{"data":{"open":false,"reason":"`+reason+`"}}`
		mu.Unlock()
		v, err := check.DatingMatchChatState(context.Background(), matchID)
		if err != nil || v.Open || v.OnHold != hold {
			t.Fatalf("reason %q: verdict=%+v err=%v, want on-hold=%v", reason, v, err, hold)
		}
	}
	if open, err := answer(http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`); err != nil || open {
		t.Fatalf("404: %v %v, want closed", open, err)
	}
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusNotFound, `404 page not found`}, // a dating-service without the route
		{http.StatusOK, `{"data":{}}`},
		{http.StatusOK, `not json`},
		{http.StatusInternalServerError, `{}`},
		{http.StatusUnauthorized, `{}`},
		{http.StatusForbidden, `{}`},
	} {
		if open, err := answer(tc.status, tc.body); err == nil || open {
			t.Fatalf("%d %s: open=%v err=%v, want an error", tc.status, tc.body, open, err)
		}
	}
	unconfigured := httpDatingMatchState{s: &Service{}}
	if v, err := unconfigured.DatingMatchChatState(context.Background(), matchID); err == nil || v.Open {
		t.Fatalf("no DATING_SERVICE_URL: verdict=%+v err=%v, want an error", v, err)
	}
}

// The default checker is the HTTP one: a Service with only a dating URL
// asks dating-service and honours its answer.
func TestDatingSendUsesDatingServiceByDefault(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"open":false,"reason":"risk_hold"}}`))
	}))
	defer srv.Close()
	f := newDatingFixture(t, srv.URL, true)
	f.svc.datingMatchState = nil
	if err := trySend(f, f.userA, "h2-http"); !errors.Is(err, ErrMatchClosed) {
		t.Fatalf("err = %v, want ErrMatchClosed from dating's answer", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("dating called %d times, want 1", calls)
	}
}

// pending_review is a reversible hold: refused with ErrMatchOnHold (which is
// an ErrMatchClosed for every existing path), cached only for the short TTL,
// so chat resumes once dating reports the member cleared.
func TestDatingHoldIsRefusedAndNotSticky(t *testing.T) {
	f := newDatingFixture(t, "", true)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f.svc.SetClock(func() time.Time { return now })
	st := &stubDatingState{}
	st.setHold()
	f.svc.datingMatchState = st

	err := trySend(f, f.userA, "hold-1")
	if !errors.Is(err, ErrMatchOnHold) {
		t.Fatalf("send while a member is under review: err = %v, want ErrMatchOnHold", err)
	}
	if !errors.Is(err, ErrMatchClosed) {
		t.Fatal("ErrMatchOnHold must also be an ErrMatchClosed (opening answer, forward, parity paths)")
	}
	// Still held inside the TTL, from the cache.
	if err := trySend(f, f.userB, "hold-2"); !errors.Is(err, ErrMatchOnHold) || st.count() != 1 {
		t.Fatalf("cached hold: err=%v calls=%d", err, st.count())
	}
	// The moderator clears the member; after the short TTL chat resumes,
	// long before the 5-minute closed TTL.
	st.set(true, nil)
	now = now.Add(datingStateOpenTTL)
	if err := trySend(f, f.userA, "hold-3"); err != nil {
		t.Fatalf("send after the hold lifted: %v", err)
	}
	if datingStateTTL(datingVerdict{OnHold: true}) != datingStateOpenTTL || datingStateTTL(datingVerdict{}) != datingStateClosedTTL {
		t.Fatal("a hold must use the short TTL and a closure the long one")
	}
}
