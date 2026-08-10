package search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Re-review v2 P0-2 — a 404 from the fence lookup has two meanings with
// opposite safety consequences, and the old code collapsed them.
//
//	{"found": false}                        the index exists, this author is
//	                                        not fenced  → safe to index
//	{"error":{"type":"index_not_found_…"}}  the fence index is GONE      → we
//	                                        know nothing → must refuse
//
// The second is reachable in production: index creation is best-effort and
// log-only, so a service can start with no fence index at all. Every erased
// author then looked un-erased.

// fenceServer builds a store whose fence lookups return a canned response.
func fenceServer(t *testing.T, status int, body string) *Store {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Index bootstrap probes: report everything present.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, IndexAuthorFences) && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	store, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

// The one case that may report "not fenced".
func TestIsAuthorFenced_ConfirmedMissingDocumentIsSafe(t *testing.T) {
	store := fenceServer(t, http.StatusNotFound,
		`{"_index":"author_fences_v1","_id":"a1","found":false}`)

	fenced, err := store.IsAuthorFenced(context.Background(), "a1")
	if err != nil {
		t.Fatalf("a confirmed missing document must not error: %v", err)
	}
	if fenced {
		t.Fatal("a missing fence document means the author is not erased")
	}
}

// Re-review v3 P0-1 — `found` must be PRESENT and false, not merely absent.
//
// With `Found bool`, Go decodes both `{"found": false}` and a body with no
// `found` field at all to the same zero value. A 404 carrying `{}` — from
// a proxy, a routing error, or an unexpected response shape — was
// therefore indistinguishable from a confirmed document miss, and erasure
// enforcement was skipped for that lookup.
func TestIsAuthorFenced_404WithoutAnExplicitFoundFieldFailsClosed(t *testing.T) {
	cases := map[string]string{
		"empty_object":      `{}`,
		"status_only":       `{"status":404}`,
		"found_null":        `{"found":null}`,
		"index_only":        `{"_index":"author_fences_v1"}`,
		"found_true_on_404": `{"_index":"author_fences_v1","found":true}`,
		"wrong_index":       `{"_index":"something_else","found":false}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := fenceServer(t, http.StatusNotFound, body)

			fenced, err := store.IsAuthorFenced(context.Background(), "a1")
			if err == nil {
				t.Fatalf("RE-REVIEW v3 P0-1 REGRESSION: a 404 body of %s was accepted "+
					"as a confirmed document miss (fenced=%v). Absence of `found` is "+
					"not the same as found:false, and treating it as such disables "+
					"erasure enforcement for this lookup", body, fenced)
			}
			if !errors.Is(err, ErrFenceStateUnknown) {
				t.Fatalf("error should wrap ErrFenceStateUnknown, got %v", err)
			}
			if fenced {
				t.Fatal("the boolean must not claim the author IS fenced; state is unknown")
			}
		})
	}
}

// A 200 that does not explicitly say found:true is also unknown, not "not
// fenced" — the same presence rule applies on the success path.
func TestIsAuthorFenced_200WithoutExplicitFoundFailsClosed(t *testing.T) {
	for name, body := range map[string]string{
		"empty_object": `{}`,
		"found_null":   `{"found":null}`,
		"found_false":  `{"_index":"author_fences_v1","found":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			store := fenceServer(t, http.StatusOK, body)
			if _, err := store.IsAuthorFenced(context.Background(), "a1"); err == nil {
				t.Fatalf("a 200 body of %s must fail closed", body)
			}
		})
	}
}

// The case that used to fail open.
func TestIsAuthorFenced_MissingIndexFailsClosed(t *testing.T) {
	store := fenceServer(t, http.StatusNotFound,
		`{"error":{"type":"index_not_found_exception","reason":"no such index [author_fences_v1]"},"status":404}`)

	fenced, err := store.IsAuthorFenced(context.Background(), "a1")
	if err == nil {
		t.Fatal("RE-REVIEW v2 P0-2 REGRESSION: a missing author_fences_v1 index was " +
			"reported as 'this author is not erased'. Every erased account's content " +
			"becomes indexable again")
	}
	if !errors.Is(err, ErrFenceStateUnknown) {
		t.Fatalf("error should be ErrFenceStateUnknown, got %v", err)
	}
	if fenced {
		t.Fatal("the boolean must not claim the author IS fenced either; the state is unknown")
	}
}

func TestIsAuthorFenced_ServerErrorsFailClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"internal_error":    {http.StatusInternalServerError, `{"error":{"type":"internal"}}`},
		"service_down":      {http.StatusServiceUnavailable, `{}`},
		"malformed_body":    {http.StatusOK, `not json at all`},
		"200_without_found": {http.StatusOK, `{"_index":"author_fences_v1"}`},
		"error_in_200_body": {http.StatusOK, `{"error":{"type":"search_phase_execution_exception"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			store := fenceServer(t, tc.status, tc.body)
			fenced, err := store.IsAuthorFenced(context.Background(), "a1")
			if err == nil {
				t.Fatalf("%s must fail closed, got fenced=%v err=nil", name, fenced)
			}
			if !errors.Is(err, ErrFenceStateUnknown) {
				t.Fatalf("error should wrap ErrFenceStateUnknown, got %v", err)
			}
		})
	}
}

func TestIsAuthorFenced_FoundDocumentIsFenced(t *testing.T) {
	store := fenceServer(t, http.StatusOK,
		`{"_index":"author_fences_v1","_id":"a1","found":true,"_source":{"author_id":"a1"}}`)

	fenced, err := store.IsAuthorFenced(context.Background(), "a1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fenced {
		t.Fatal("a present fence document means the author IS erased")
	}
}

// The whole point of failing closed: no eligible document may be written
// when the fence state cannot be established.
func TestIndexPostUnlessAuthorErased_WritesNothingWhenFenceStateIsUnknown(t *testing.T) {
	var postWrites int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, IndexAuthorFences) {
			// The fence index is gone.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"index_not_found_exception"},"status":404}`))
			return
		}
		if strings.Contains(r.URL.Path, IndexPosts) && r.Method != http.MethodGet {
			postWrites++
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	store, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	postWrites = 0

	err = store.IndexPostUnlessAuthorErased(context.Background(), PostProjection{
		PostID: "p1", Rev: 1,
		Doc: PostDoc{
			PostID: "p1", AuthorID: "a1", Text: "should not be indexed",
			Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		},
	})
	if err == nil {
		t.Fatal("indexing must fail when erasure state is unknown")
	}
	if postWrites != 0 {
		t.Fatalf("%d write(s) reached posts_v1 despite unknown erasure state; the "+
			"check must run before any content is published", postWrites)
	}
}

// Re-review v2 P0-3 — the sweep-retry proof, made deterministic.
//
// The previous proof called EraseAuthorContent under churn, then ran a
// SECOND clean SweepAuthorPosts, and only then asserted. That second sweep
// could repair whatever the first one missed, so the test passed whether
// or not the response was ever inspected. It proved nothing about retry.
//
// This drives SweepAuthorPosts against a scripted response sequence and
// asserts on the sweep itself, with no cleanup in between.
func TestSweepAuthorPosts_RetriesUntilTheResponseIsClean(t *testing.T) {
	responses := []string{
		`{"updated":3,"version_conflicts":2,"timed_out":false,"failures":[]}`,
		`{"updated":1,"version_conflicts":0,"timed_out":true,"failures":[]}`,
		`{"updated":0,"version_conflicts":0,"timed_out":false,"failures":[{"cause":{"reason":"mapper_parsing"}}]}`,
		`{"updated":2,"version_conflicts":0,"timed_out":false,"failures":[]}`, // clean
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "_update_by_query") {
			i := calls
			calls++
			if i >= len(responses) {
				i = len(responses) - 1
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(responses[i]))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	store, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	calls = 0

	if err := store.SweepAuthorPosts(context.Background(), "a1"); err != nil {
		t.Fatalf("the sweep should have succeeded on the 4th (clean) response: %v", err)
	}
	if calls != 4 {
		t.Fatalf("sweep ran %d time(s), want 4 — conflicts, a timeout and a failure "+
			"must each force a retry; accepting any of them leaves an erased "+
			"account's documents unswept", calls)
	}
}

// A sweep that never comes back clean must ERROR, so the consumer refuses
// to commit and Kafka redelivers the deletion.
func TestSweepAuthorPosts_ErrorsWhenItNeverComesBackClean(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "_update_by_query") {
			calls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"updated":0,"version_conflicts":7,"timed_out":false,"failures":[]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	store, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	calls = 0

	if err := store.SweepAuthorPosts(context.Background(), "a1"); err == nil {
		t.Fatal("a permanently conflicted sweep must return an error; silently " +
			"succeeding leaves surviving public documents for a deleted account")
	}
	if calls != sweepAttempts {
		t.Fatalf("sweep ran %d time(s), want %d attempts before giving up", calls, sweepAttempts)
	}
}

// Individual response fields must each be treated as "not clean".
func TestSweepAuthorPosts_EachUncleanSignalForcesRetry(t *testing.T) {
	cases := map[string]string{
		"version_conflicts": `{"updated":1,"version_conflicts":1,"timed_out":false,"failures":[]}`,
		"timed_out":         `{"updated":1,"version_conflicts":0,"timed_out":true,"failures":[]}`,
		"failures":          `{"updated":1,"version_conflicts":0,"timed_out":false,"failures":[{"cause":{"reason":"x"}}]}`,
	}
	for name, unclean := range cases {
		t.Run(name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				if strings.Contains(r.URL.Path, "_update_by_query") {
					calls++
					w.WriteHeader(http.StatusOK)
					if calls == 1 {
						_, _ = w.Write([]byte(unclean))
					} else {
						_, _ = w.Write([]byte(`{"updated":0,"version_conflicts":0,"timed_out":false,"failures":[]}`))
					}
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			store, err := New(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			calls = 0
			if err := store.SweepAuthorPosts(context.Background(), "a1"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if calls != 2 {
				t.Fatalf("%s did not force a retry (ran %d times, want 2)", name, calls)
			}
		})
	}
}
