package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/atpost/dating-service/internal/mediaclient"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// fakeMediaClaims keeps claims per (kind, ref) like media-service, with
// failures to inject. Shared by the unit and integration tests here.
type fakeMediaClaims struct {
	mu                   sync.Mutex
	held                 map[string]map[uuid.UUID]bool
	gone                 map[uuid.UUID]bool
	claimErr, releaseErr error
	claims, releases     int
	ctxErrAtRelease      error
}

func newFakeMediaClaims() *fakeMediaClaims {
	return &fakeMediaClaims{held: map[string]map[uuid.UUID]bool{}, gone: map[uuid.UUID]bool{}}
}

func claimKey(kind, ref string) string { return kind + "|" + ref }

func (f *fakeMediaClaims) Claim(_ context.Context, kind, ref string, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if f.claimErr != nil {
		return f.claimErr
	}
	for _, id := range ids {
		if f.gone[id] {
			return mediaclient.ErrNotFound
		}
	}
	k := claimKey(kind, ref)
	if f.held[k] == nil {
		f.held[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[k][id] = true
	}
	return nil
}

func (f *fakeMediaClaims) Release(ctx context.Context, kind, ref string, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	f.ctxErrAtRelease = ctx.Err()
	if f.releaseErr != nil {
		return f.releaseErr
	}
	k := claimKey(kind, ref)
	if len(ids) == 0 {
		delete(f.held, k)
		return nil
	}
	for _, id := range ids {
		delete(f.held[k], id)
	}
	return nil
}

func (f *fakeMediaClaims) holds(kind, ref string, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[claimKey(kind, ref)][id]
}

func (f *fakeMediaClaims) count(kind, ref string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.held[claimKey(kind, ref)])
}

var (
	errTestNotFound    = errors.New("test: not found")
	errTestUnavailable = errors.New("test: unavailable")
)

func TestClaimMapsEachAnswerToThePathsOwnError(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	s := &Service{}
	if err := s.claim(ctx, "k", "r", []uuid.UUID{id}, errTestNotFound, errTestUnavailable); !errors.Is(err, errTestUnavailable) {
		t.Fatalf("no client = %v; a write that adds media must be refused", err)
	}
	f := newFakeMediaClaims()
	s.SetMediaClaims(f)
	if err := s.claim(ctx, "k", "r", nil, errTestNotFound, errTestUnavailable); err != nil || f.claims != 0 {
		t.Fatalf("nothing to claim: err=%v calls=%d", err, f.claims)
	}
	f.gone[id] = true
	if err := s.claim(ctx, "k", "r", []uuid.UUID{id}, errTestNotFound, errTestUnavailable); !errors.Is(err, errTestNotFound) {
		t.Fatalf("missing asset = %v, want the path's not-found", err)
	}
	f.gone[id] = false
	f.claimErr = mediaclient.ErrUnavailable
	if err := s.claim(ctx, "k", "r", []uuid.UUID{id}, errTestNotFound, errTestUnavailable); !errors.Is(err, errTestUnavailable) {
		t.Fatalf("media-service down = %v", err)
	}
	f.claimErr = nil
	if err := s.claim(ctx, "k", "r", []uuid.UUID{id}, errTestNotFound, errTestUnavailable); err != nil || !f.holds("k", "r", id) {
		t.Fatalf("claim = %v, held %v", err, f.held)
	}
}

// A release outlives the request that asked for it.
func TestReleaseRunsDetachedAndReportsTheOutcome(t *testing.T) {
	s := &Service{}
	if s.release(context.Background(), "k", "r", nil) {
		t.Fatal("no client reported a release")
	}
	f := newFakeMediaClaims()
	s.SetMediaClaims(f)
	f.held[claimKey("k", "r")] = map[uuid.UUID]bool{uuid.New(): true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !s.release(ctx, "k", "r", nil) || f.count("k", "r") != 0 || f.ctxErrAtRelease != nil {
		t.Fatalf("release on a cancelled request: held=%v ctxErr=%v", f.held, f.ctxErrAtRelease)
	}
	f.releaseErr = mediaclient.ErrUnavailable
	if s.release(context.Background(), "k", "r", nil) {
		t.Fatal("a failed release reported success")
	}
}

func TestMediaChange(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	cases := []struct {
		name       string
		prev, next *uuid.UUID
		add, drop  int
	}{
		{"set", nil, &a, 1, 0},
		{"same", &a, &a, 0, 0},
		{"replace", &a, &b, 1, 1},
		{"clear", &a, nil, 0, 1},
		{"none", nil, nil, 0, 0},
	}
	for _, tc := range cases {
		add, drop := mediaChange(tc.prev, tc.next)
		if len(add) != tc.add || len(drop) != tc.drop {
			t.Errorf("%s: add=%v drop=%v", tc.name, add, drop)
		}
	}
	if add, drop := mediaChange(&a, &b); add[0] != b || drop[0] != a {
		t.Fatalf("replace: add=%v drop=%v", add, drop)
	}
}

// A removal releases first and refuses when media-service does not confirm:
// the media purge would refuse a claimed asset, and once the row is gone no
// pass could find the claim again.
func TestReleaseBeforeDetachRefusesAnUnconfirmedRelease(t *testing.T) {
	ctx := context.Background()
	s := &Service{}
	if err := s.releaseBeforeDetach(ctx, "k", "r", errTestUnavailable); err != nil {
		t.Fatalf("no client: %v; a removal is never refused for want of one", err)
	}
	f := newFakeMediaClaims()
	s.SetMediaClaims(f)
	id := uuid.New()
	f.held[claimKey("k", "r")] = map[uuid.UUID]bool{id: true}
	f.releaseErr = mediaclient.ErrUnavailable
	if err := s.releaseBeforeDetach(ctx, "k", "r", errTestUnavailable); !errors.Is(err, errTestUnavailable) {
		t.Fatalf("failed release = %v, want the removal refused", err)
	}
	f.releaseErr = nil
	if err := s.releaseBeforeDetach(ctx, "k", "r", errTestUnavailable); err != nil || f.count("k", "r") != 0 {
		t.Fatalf("release = %v, held %v", err, f.held)
	}
	s.reclaim(ctx, "k", "r", id)
	if !f.holds("k", "r", id) {
		t.Fatal("reclaim did not take the claim back")
	}
}

func TestReleaseHoldersStopsAtTheFirstFailure(t *testing.T) {
	ctx := context.Background()
	user := uuid.New()
	hs := []store.MediaHolder{{Kind: mediaclient.KindPhoto, Ref: "p1"}, {Kind: mediaclient.KindPromptClip, Ref: "c1"}}
	if err := (&Service{}).releaseHolders(ctx, user, hs); err != nil {
		t.Fatalf("no client: %v; never claimed here, the purge proceeds", err)
	}
	s := &Service{}
	f := newFakeMediaClaims()
	s.SetMediaClaims(f)
	f.held[claimKey(mediaclient.KindPhoto, "p1")] = map[uuid.UUID]bool{uuid.New(): true}
	f.held[claimKey(mediaclient.KindPromptClip, "c1")] = map[uuid.UUID]bool{uuid.New(): true}
	f.releaseErr = mediaclient.ErrUnavailable
	if err := s.releaseHolders(ctx, user, hs); err == nil || f.releases != 1 {
		t.Fatalf("err=%v releases=%d; a purge must stop and be retried", err, f.releases)
	}
	f.releaseErr = nil
	if err := s.releaseHolders(ctx, user, hs); err != nil ||
		f.count(mediaclient.KindPhoto, "p1") != 0 || f.count(mediaclient.KindPromptClip, "c1") != 0 {
		t.Fatalf("err=%v held=%v", err, f.held)
	}
}

func TestVerificationVideoRelease(t *testing.T) {
	old, video := uuid.New(), uuid.New()
	added := []uuid.UUID{video}
	cases := []struct {
		name      string
		prev, cur *uuid.UUID
		add       []uuid.UUID
		want      *uuid.UUID // nil: release nothing
	}{
		{"written over an old video", &old, &video, added, &old},
		{"written, nothing before", nil, &video, added, nil},
		{"written, same video again", &video, &video, nil, nil},
		{"not written (a pass stays)", &old, &old, added, &video},
		{"not written, no row", nil, nil, added, &video},
	}
	for _, tc := range cases {
		got := verificationVideoRelease(tc.prev, tc.cur, video, tc.add)
		switch {
		case tc.want == nil && len(got) != 0:
			t.Errorf("%s: released %v, want nothing", tc.name, got)
		case tc.want != nil && (len(got) != 1 || got[0] != *tc.want):
			t.Errorf("%s: released %v, want %s", tc.name, got, *tc.want)
		}
		for _, id := range got {
			if tc.cur != nil && id == *tc.cur {
				t.Errorf("%s: released %s, which the row names now", tc.name, id)
			}
		}
	}
	// Defence in depth: never the id the row names, whatever add holds.
	if got := verificationVideoRelease(&old, &old, video, []uuid.UUID{old}); len(got) != 0 {
		t.Fatalf("released %v, which the row names", got)
	}
}

func TestPromptClipRefRoundTrips(t *testing.T) {
	u := uuid.New()
	ref := store.PromptClipRef(u, 7)
	gu, gp, err := store.ParsePromptClipRef(ref)
	if err != nil || gu != u || gp != 7 {
		t.Fatalf("%q -> %s %d %v", ref, gu, gp, err)
	}
	for _, bad := range []string{"", "x", u.String(), u.String() + ":x", "nope:1"} {
		if _, _, err := store.ParsePromptClipRef(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
