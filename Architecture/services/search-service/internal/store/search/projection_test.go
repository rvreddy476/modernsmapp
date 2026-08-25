package search

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Re-review P0-3 — the revision domain must be ENFORCED, not documented.
//
// The previous version wrote "no ordinary event can ever reach fenceRev"
// in a comment and validated nothing, which left three holes: a malformed
// event above the fence could overwrite a permanent erasure tombstone;
// AutoRev on a document near MaxInt64 overflowed into a negative revision
// and turned a safety removal into a no-op; and nothing stopped future
// code from using the reserved range.
//
// These cover the Go half. The Painless half — overflow pinning and the
// author_erased rejection — can only be proven against live OpenSearch.

func TestProjectionValidate_RejectsEligibleWritesOutsideTheDomain(t *testing.T) {
	cases := []struct {
		name string
		rev  int64
	}{
		{"zero", 0},
		{"negative", -1},
		{"min_int64", math.MinInt64},
		{"at_the_fence", fenceRev},
		{"above_the_fence", fenceRev + 1},
		{"max_int64", math.MaxInt64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := PostProjection{PostID: "p1", Rev: tc.rev, Doc: PostDoc{PostID: "p1"}}
			err := p.validate()
			if err == nil {
				t.Fatalf("revision %d was accepted for an eligible write; a value in the "+
					"reserved range can overwrite a permanent erasure tombstone", tc.rev)
			}
			if !errors.Is(err, ErrRevisionOutOfDomain) {
				t.Fatalf("error should be ErrRevisionOutOfDomain, got %v", err)
			}
		})
	}
}

func TestProjectionValidate_AcceptsTheOrdinaryDomain(t *testing.T) {
	for _, rev := range []int64{1, 2, 1000, maxOrdinaryRev} {
		p := PostProjection{PostID: "p1", Rev: rev, Doc: PostDoc{PostID: "p1"}}
		if err := p.validate(); err != nil {
			t.Errorf("ordinary revision %d rejected: %v", rev, err)
		}
	}
}

// Removals are deliberately permissive. Refusing to index is recoverable;
// refusing to REMOVE is not, so a removal must never be blocked by domain
// validation — including the fence write itself.
func TestProjectionValidate_RemovalsAreNotBlockedByTheDomain(t *testing.T) {
	for _, rev := range []int64{0, 1, maxOrdinaryRev, fenceRev, math.MaxInt64} {
		p := PostProjection{PostID: "p1", Rev: rev, Removed: true}
		if err := p.validate(); err != nil {
			t.Errorf("removal at revision %d was rejected: %v — a removal must always "+
				"be expressible", rev, err)
		}
	}
	// A negative removal revision is still nonsense and is refused.
	p := PostProjection{PostID: "p1", Rev: -5, Removed: true}
	if err := p.validate(); err == nil {
		t.Error("a negative removal revision should be rejected")
	}
}

// AutoRev delegates the revision to OpenSearch, so there is nothing for
// Go to validate — and validating would wrongly reject takedowns.
func TestProjectionValidate_AutoRevSkipsValidation(t *testing.T) {
	for _, p := range []PostProjection{
		{PostID: "p1", AutoRev: true, Removed: true},
		{PostID: "p1", AutoRev: true, Rev: math.MaxInt64, Removed: true},
	} {
		if err := p.validate(); err != nil {
			t.Errorf("AutoRev projection rejected: %v", err)
		}
	}
}

// The fence must sit above every ordinary revision with room to spare, and
// maxOrdinaryRev must be exactly one below it.
func TestRevisionDomainConstants(t *testing.T) {
	if maxOrdinaryRev != fenceRev-1 {
		t.Fatalf("maxOrdinaryRev (%d) must be fenceRev-1 (%d)", maxOrdinaryRev, fenceRev-1)
	}
	if fenceRev <= 0 {
		t.Fatal("fenceRev must be positive")
	}
	// Headroom above the fence so AutoRev pinning has somewhere to sit
	// without approaching overflow.
	if math.MaxInt64-fenceRev < 1_000_000 {
		t.Fatalf("fenceRev (%d) leaves too little headroom below MaxInt64", fenceRev)
	}
	if FenceRevision() != fenceRev {
		t.Fatal("FenceRevision() must expose fenceRev")
	}
}

// The domain check is worthless if ApplyPostProjection does not call it.
// This drives the real method against a recording server and asserts that
// an out-of-domain eligible write never reaches OpenSearch at all.
func TestApplyPostProjection_RefusesOutOfDomainBeforeSendingAnything(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			atomic.AddInt32(&requests, 1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	store, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	atomic.StoreInt32(&requests, 0)

	err = store.ApplyPostProjection(context.Background(), PostProjection{
		PostID: "p1",
		Rev:    fenceRev + 1, // reserved for permanent erasure
		Doc:    PostDoc{PostID: "p1", Visibility: "public", ReviewStatus: "approved"},
	})
	if !errors.Is(err, ErrRevisionOutOfDomain) {
		t.Fatalf("expected ErrRevisionOutOfDomain, got %v", err)
	}
	if n := atomic.LoadInt32(&requests); n != 0 {
		t.Fatalf("%d request(s) were sent for an out-of-domain projection; the "+
			"domain check must run before anything reaches OpenSearch", n)
	}

	// A valid one must still go through, or this test would pass simply
	// because nothing ever sends.
	if err := store.ApplyPostProjection(context.Background(), PostProjection{
		PostID: "p1", Rev: 1,
		Doc: PostDoc{PostID: "p1", Visibility: "public", ReviewStatus: "approved"},
	}); err != nil {
		t.Fatalf("a valid projection was refused: %v", err)
	}
	if atomic.LoadInt32(&requests) == 0 {
		t.Fatal("a valid projection sent no request; the test proves nothing")
	}
}
