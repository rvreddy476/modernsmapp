//go:build integration

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The hashtag page's privacy (found during 2-B2): the list and its total
// must both leave out a post that is not effectively approved and a post
// whose author is a private account the viewer does not follow, and must
// agree with each other for every kind of viewer. Real Postgres, real
// graph client against a fake graph-service.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run HashtagPrivacy -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).
func TestHashtagPrivacyListAndTotalAgree(t *testing.T) {
	pool := openHubMediaDB(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS views_count BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS reports_count INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	publicAuthor, privateAuthor, follower, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tag := "privacy" + uuid.NewString()[:8]
	t.Cleanup(func() {
		bg := context.Background()
		for _, a := range []uuid.UUID{publicAuthor, privateAuthor} {
			_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id IN (SELECT id FROM posts WHERE author_id = $1)`, a)
			_, _ = pool.Exec(bg, `DELETE FROM posts WHERE author_id = $1`, a)
		}
	})
	insert := func(author uuid.UUID, review string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, hashtags, review_status, created_at, updated_at)
			VALUES ($1, $2, 'tagged', 'public', 'post', ARRAY[$3]::text[], $4, now(), now())`, id, author, tag, review); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pub1 := insert(publicAuthor, "approved")
	pub2 := insert(publicAuthor, "approved")
	flagged := insert(publicAuthor, "flagged") // nobody's: not approved
	pending := insert(publicAuthor, "pending")
	priv := insert(privateAuthor, "approved") // the private account's follower only

	// graph-service: the private author's posts are viewable by the
	// follower (and themself); everyone's by the public author's audience.
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req canRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || r.URL.Path != "/v1/internal/graph/can" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := map[string]bool{}
		for _, id := range req.TargetIDs {
			switch id {
			case req.ViewerID, publicAuthor.String():
				data[id] = true
			case privateAuthor.String():
				data[id] = req.ViewerID == follower.String()
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(graph.Close)

	svc := New(postgres.New(pool), nil, nil)
	svc.graphServiceURL = graph.URL
	svc.internalServiceKey = "test-key"
	svc.httpClient = graph.Client()

	ids := func(posts []postgres.Post) map[uuid.UUID]bool {
		out := map[uuid.UUID]bool{}
		for _, p := range posts {
			out[p.ID] = true
		}
		return out
	}
	cases := []struct {
		name   string
		viewer *uuid.UUID
		want   []uuid.UUID
	}{
		{"signed out", nil, []uuid.UUID{pub1, pub2}},
		{"stranger", &stranger, []uuid.UUID{pub1, pub2}},
		{"follower of the private account", &follower, []uuid.UUID{pub1, pub2, priv}},
		{"the private author", &privateAuthor, []uuid.UUID{pub1, pub2, priv}},
		// Even its author does not see a flagged/pending post on the tag page.
		{"the public author", &publicAuthor, []uuid.UUID{pub1, pub2}},
	}
	for _, tc := range cases {
		for _, sort := range []postgres.HashtagSortMode{postgres.HashtagSortRecent, postgres.HashtagSortTop} {
			// The list GetPostsByHashtag serves: the store page, then the
			// author gate (hydration needs Scylla, which this rig lacks).
			page, _, err := svc.pgStore.GetPostsByHashtag(ctx, tag, 50, "", sort, nil)
			if err != nil {
				t.Fatalf("%s/%s: list: %v", tc.name, sort, err)
			}
			listed := ids(svc.hashtagPostsVisibleTo(ctx, tc.viewer, page))
			if len(listed) != len(tc.want) {
				t.Fatalf("%s/%s: listed %d posts, want %d (%v)", tc.name, sort, len(listed), len(tc.want), listed)
			}
			for _, id := range tc.want {
				if !listed[id] {
					t.Fatalf("%s/%s: %s missing from the list", tc.name, sort, id)
				}
			}
			for _, id := range []uuid.UUID{flagged, pending} {
				if listed[id] {
					t.Fatalf("%s/%s: an unapproved post was listed", tc.name, sort)
				}
			}
		}
		total, capped, err := svc.HashtagTotal(ctx, tc.viewer, tag, nil)
		if err != nil || capped || total != len(tc.want) {
			t.Fatalf("%s: total = %d capped=%v (%v), want %d — the count must match the list", tc.name, total, capped, err, len(tc.want))
		}
	}

	// graph-service down: the list fails closed and the count is withheld,
	// never a number describing posts the list would not show.
	// (A fresh viewer: the gate caches answers for three seconds.)
	graph.Close()
	later := uuid.New()
	if _, _, err := svc.HashtagTotal(ctx, &later, tag, nil); err == nil {
		t.Fatal("a count was produced while the author gate could not be asked")
	}
}
