package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/graph-service/internal/permission"
	"github.com/atpost/graph-service/internal/store"
	"github.com/google/uuid"
)

// settingsServer answers GET /v1/users/{id}/settings. behave picks, per id,
// how long to wait and which status to send; a 200 carries a public profile.
type settingsBehaviour struct {
	delay  time.Duration
	status int
}

func settingsServer(t *testing.T, behave func(id string) settingsBehaviour) (*Service, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/users/"), "/settings")
		b := behave(id)
		select {
		case <-time.After(b.delay):
		case <-r.Context().Done():
			return
		}
		if b.status != http.StatusOK {
			w.WriteHeader(b.status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"account_visibility":  "public",
			"allow_comments_from": "everyone",
		}})
	}))
	t.Cleanup(srv.Close)
	return &Service{userServiceURL: srv.URL, httpClient: &http.Client{Timeout: 5 * time.Second}}, &inFlight, &maxInFlight
}

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

// Sanity for the assertions below: a public author with no relationship is
// viewable, and the strict-default snapshot is not.
func TestCanBatchFixtureAssumptions(t *testing.T) {
	public := permission.Privacy{AccountVisibility: "public", AllowCommentsFrom: "everyone", WhoCanMention: "everyone", WhoCanTag: "everyone"}
	if !permission.Resolve(permission.ActionViewPosts, permission.Facts{}, public).Allowed {
		t.Fatal("public author should be viewable by a stranger")
	}
	if permission.Resolve(permission.ActionViewPosts, permission.Facts{}, strictPrivacyDefaults()).Allowed {
		t.Fatal("strict defaults must not grant view_posts to a stranger")
	}
}

// 20 uncached authors behind a 400 ms identity: serially that is 8 s, past
// post-service's 5 s budget. Concurrently it is one round trip.
func TestCanBatchFetchesUncachedPrivacyConcurrently(t *testing.T) {
	s, _, maxInFlight := settingsServer(t, func(string) settingsBehaviour {
		return settingsBehaviour{delay: 400 * time.Millisecond, status: http.StatusOK}
	})
	viewer := uuid.New()
	targets := ids(20)

	start := time.Now()
	got := s.resolveCanBatch(context.Background(), viewer, permission.ActionViewPosts, targets, map[uuid.UUID]store.Relationship{})
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("20 uncached authors took %v; privacy must be fetched concurrently", elapsed)
	}
	for _, id := range targets {
		if !got[id.String()] {
			t.Fatalf("public author %s denied: privacy not read", id)
		}
	}
	if m := maxInFlight.Load(); m > PrivacyFetchConcurrency {
		t.Fatalf("max in-flight settings GETs = %d, want <= %d", m, PrivacyFetchConcurrency)
	}
	if m := maxInFlight.Load(); m < 2 {
		t.Fatalf("max in-flight settings GETs = %d: fetches ran serially", m)
	}
}

// One author's identity read fails and another hangs past the per-call
// timeout: those two fail closed, every other author still resolves, and the
// batch answers within its budget.
func TestCanBatchFailsClosedPerAuthor(t *testing.T) {
	targets := ids(6)
	failing, hanging := targets[1].String(), targets[4].String()
	s, _, _ := settingsServer(t, func(id string) settingsBehaviour {
		switch id {
		case failing:
			return settingsBehaviour{status: http.StatusInternalServerError}
		case hanging:
			return settingsBehaviour{delay: 10 * time.Second, status: http.StatusOK}
		}
		return settingsBehaviour{status: http.StatusOK}
	})
	viewer := uuid.New()
	// The hanging author is followed by the viewer: strict defaults still
	// let a follower see posts, so fail-closed is not "deny everything".
	rels := map[uuid.UUID]store.Relationship{targets[4]: {Follows: true}}

	start := time.Now()
	got := s.resolveCanBatch(context.Background(), viewer, permission.ActionViewPosts, targets, rels)
	if elapsed := time.Since(start); elapsed > PrivacyFetchTimeout+500*time.Millisecond {
		t.Fatalf("batch took %v; a hanging author must be cut at the per-call timeout", elapsed)
	}
	for i, id := range targets {
		want := true
		if id.String() == failing {
			want = false // stranger + strict defaults
		}
		if got[id.String()] != want {
			t.Fatalf("target %d (%s): allowed=%v, want %v", i, id, got[id.String()], want)
		}
	}

	// Comment on the strict-default snapshot is denied even for a follower.
	got = s.resolveCanBatch(context.Background(), viewer, permission.ActionComment, targets[4:5], rels)
	if got[targets[4].String()] {
		t.Fatal("comment granted on an unreadable privacy snapshot")
	}
}

// The privacy phase never outlives the caller: with 1 s left it gives up at
// half of that and answers strict defaults rather than nothing.
func TestCanBatchRespectsCallerDeadline(t *testing.T) {
	s, _, _ := settingsServer(t, func(string) settingsBehaviour {
		return settingsBehaviour{delay: 10 * time.Second, status: http.StatusOK}
	})
	viewer := uuid.New()
	targets := ids(30)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	got := s.resolveCanBatch(ctx, viewer, permission.ActionViewPosts, append(targets, viewer), nil)
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Fatalf("privacy phase took %v with 1s caller budget; want about half", elapsed)
	}
	if len(got) != len(targets)+1 {
		t.Fatalf("got %d answers, want %d", len(got), len(targets)+1)
	}
	for _, id := range targets {
		if got[id.String()] {
			t.Fatalf("stranger %s granted on unreadable privacy", id)
		}
	}
	if !got[viewer.String()] {
		t.Fatal("viewer must always pass against themselves")
	}
}

// Duplicates are fetched once; the viewer is never fetched.
func TestFetchPrivacyBatchDedupes(t *testing.T) {
	var calls atomic.Int32
	s, _, _ := settingsServer(t, func(string) settingsBehaviour {
		calls.Add(1)
		return settingsBehaviour{status: http.StatusOK}
	})
	viewer := uuid.New()
	a := uuid.New()
	got := s.resolveCanBatch(context.Background(), viewer, permission.ActionViewPosts, []uuid.UUID{a, a, viewer, a}, nil)
	if !got[a.String()] || !got[viewer.String()] {
		t.Fatalf("answers = %v", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("settings GETs = %d, want 1", n)
	}
}

// No permission source configured: strict defaults for everyone, no panic.
func TestFetchPrivacyBatchWithoutSource(t *testing.T) {
	s := &Service{}
	targets := ids(3)
	got := s.fetchPrivacyBatch(context.Background(), targets)
	for _, id := range targets {
		if got[id] != strictPrivacyDefaults() {
			t.Fatalf("%s: want strict defaults, got %+v", id, got[id])
		}
	}
}
