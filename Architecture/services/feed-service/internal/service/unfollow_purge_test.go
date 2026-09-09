package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The unfollow purge deletes real rows now (it deleted nothing until
// 2026-09-09 — the DELETE was a filtering query Scylla refused). These pin
// WHO it is allowed to delete from.
//
// The trap: home_timeline_by_user is not a materialised follower list.
// FanoutPost writes to the author's followers UNION their connections, and
// for a "trusted" post to the author's close friends instead. So an
// unfollow says nothing about whether these rows should go, and there is no
// mechanism that puts them back if they should not have.

func TestPurge_ProceedsForAPlainExFollower(t *testing.T) {
	retained, why := fanoutClaimSurvivesUnfollow(viewerRelationship{})
	if retained {
		t.Fatalf("someone who only ever followed keeps nothing after unfollowing, got %q", why)
	}
}

func TestPurge_SkippedForAConnection(t *testing.T) {
	retained, why := fanoutClaimSurvivesUnfollow(viewerRelationship{IsConnection: true})
	if !retained {
		t.Fatal("FanoutPost targets the author's connections as well as their followers, " +
			"so a connection's rows survive an unfollow — purging them deletes content " +
			"the next fanout will keep delivering, with nothing to restore it")
	}
	if why == "" {
		t.Fatal("a retained claim must name itself for the consumer log")
	}
}

func TestPurge_SkippedForTheAuthorsCloseFriend(t *testing.T) {
	// The AUTHOR's list, not the viewer's — trusted posts are fanned out to
	// exactly this set, so these rows are not the follow's to retract.
	retained, _ := fanoutClaimSurvivesUnfollow(viewerRelationship{ViewerIsCloseFriendOfTarget: true})
	if !retained {
		t.Fatal("a close-friends post is fanned out to the author's close friends; " +
			"unfollowing does not remove the viewer from that audience")
	}
	// The other direction must NOT protect anything: anyone could otherwise
	// keep an ex-followee's rows by adding them to their own list.
	retained, _ = fanoutClaimSurvivesUnfollow(viewerRelationship{})
	if retained {
		t.Fatal("the viewer's own close-friends list is not an audience claim")
	}
}

// A stale or redelivered unfollow for a pair who follow each other again
// must not delete a live follow's timeline.
func TestPurge_SkippedWhenTheGraphSaysTheyFollowAgain(t *testing.T) {
	retained, why := fanoutClaimSurvivesUnfollow(viewerRelationship{Follows: true})
	if !retained {
		t.Fatalf("an unfollow event for a pair who follow NOW is stale; purging on it "+
			"empties a live follow's feed (why=%q)", why)
	}
}

// relationshipStub answers POST /v1/graph/relationships/batch with one
// fixed relationship for every target, or a status when status != 200.
func relationshipStub(t *testing.T, rel viewerRelationship, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/graph/relationships/batch" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.Unmarshal(raw, &req)
		out := map[string]viewerRelationship{}
		if body != "omit" {
			for _, id := range req.TargetIDs {
				out[id] = rel
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func TestFanoutClaimAfterUnfollow_ReadsTheGraph(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()

	cases := []struct {
		name string
		rel  viewerRelationship
		want bool
	}{
		{"a plain ex-follower is purged", viewerRelationship{}, false},
		{"a connection is not", viewerRelationship{IsConnection: true}, true},
		{"the author's close friend is not", viewerRelationship{ViewerIsCloseFriendOfTarget: true}, true},
		{"a re-follow is not", viewerRelationship{Follows: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			graph := relationshipStub(t, tc.rel, http.StatusOK, "")
			defer graph.Close()
			s := newFeedServiceWithGraph(graph.URL)

			got, _, err := s.FanoutClaimAfterUnfollow(context.Background(), follower, followee)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("retained = %v, want %v", got, tc.want)
			}
		})
	}
}

// Failing closed. Every one of these must return an error, because the
// consumer purges only on a clean (false, nil) — an unverifiable
// relationship must never be read as permission to delete. This is the same
// inversion graph-service's own M4-P0-1 note describes: uncertainty
// rendered as permission.
func TestFanoutClaimAfterUnfollow_UncertaintyIsNeverPermission(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()

	t.Run("graph-service errors", func(t *testing.T) {
		graph := relationshipStub(t, viewerRelationship{}, http.StatusInternalServerError, "boom")
		defer graph.Close()
		s := newFeedServiceWithGraph(graph.URL)
		if _, _, err := s.FanoutClaimAfterUnfollow(context.Background(), follower, followee); err == nil {
			t.Fatal("a 500 from graph-service must not be reported as 'nothing retains these rows'")
		}
	})

	t.Run("the answer omits the target", func(t *testing.T) {
		graph := relationshipStub(t, viewerRelationship{}, http.StatusOK, "omit")
		defer graph.Close()
		s := newFeedServiceWithGraph(graph.URL)
		if _, _, err := s.FanoutClaimAfterUnfollow(context.Background(), follower, followee); err == nil {
			t.Fatal("an absent entry means unknown, never 'no relationship'")
		}
	})

	t.Run("graph-service is unreachable", func(t *testing.T) {
		graph := relationshipStub(t, viewerRelationship{}, http.StatusOK, "")
		graph.Close() // nothing listening
		s := newFeedServiceWithGraph(graph.URL)
		if _, _, err := s.FanoutClaimAfterUnfollow(context.Background(), follower, followee); err == nil {
			t.Fatal("a transport failure must not authorise a delete")
		}
	})

	t.Run("no graph configured", func(t *testing.T) {
		s := &Service{}
		if _, _, err := s.FanoutClaimAfterUnfollow(context.Background(), follower, followee); err == nil {
			t.Fatal("without graph-service the relationship cannot be established at all")
		}
	})
}
