package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// feed-03: share and repost read the post as the caller may see it.
//
// The graph is an httptest fake answering the two calls viewerMayViewPost
// makes; the post row is handed to the real viewerMayViewPost in the place
// loadPostForEngagement fetches it (there is no Postgres in unit tests), so
// what is under test is the real rule and that CreateRepost and SharePost
// ask it. The bare repost read is wired to the same post, so a route that
// went back to it would let the caller through and fail these tests cleanly.

// repostGraph answers /v1/internal/graph/can with canView for every author
// (false is what graph-service says for a blocked viewer and for a stranger
// to a private account) and /v1/graph/relationship with follows.
func repostGraph(t *testing.T, canView, follows bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/internal/graph/can":
			var req canRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := map[string]bool{}
			for _, id := range req.TargetIDs {
				data[id] = canView
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/graph/relationship":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"follows": follows, "followed_by": false}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type repostRig struct {
	svc   *Service
	redis *respServer
	post  *postgres.Post
}

func newRepostRig(t *testing.T, visibility string, canView, follows bool) repostRig {
	t.Helper()
	post := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "post", Visibility: visibility}
	resp := newRESPServer(t)
	rdb := redis.NewClient(&redis.Options{Addr: resp.ln.Addr().String(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })

	svc := NewForHandlerTests(HandlerTestDeps{
		RateLimiter:        deadRedisLimiter(),
		GraphServiceURL:    repostGraph(t, canView, follows),
		InternalServiceKey: "test-key",
		RepostTarget: func(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
			if id != post.ID {
				return nil, nil
			}
			return post, nil
		},
	})
	svc.rdb = rdb
	svc.engagementPostSeam = func(ctx context.Context, id, viewerID uuid.UUID) (*postgres.Post, error) {
		if id != post.ID {
			return nil, ErrPostNotFound
		}
		if !svc.viewerMayViewPost(ctx, post, &viewerID) {
			return nil, ErrPostNotVisible
		}
		return post, nil
	}
	return repostRig{svc: svc, redis: resp, post: post}
}

// sharesWritten is the share markers SharePost left in Redis.
func (r repostRig) sharesWritten() int {
	r.redis.mu.Lock()
	defer r.redis.mu.Unlock()
	n := 0
	for k := range r.redis.data {
		if strings.HasPrefix(k, "shared:") {
			n++
		}
	}
	return n
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// A caller the author has blocked, and a stranger to a followers-only post,
// get the not-found a missing post gets, on every repost and share kind, and
// nothing is recorded.
func TestRepostAndShare_InvisiblePostIsNotFound(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		visibility       string
		canView, follows bool
	}{
		"blocked caller, public post":           {"public", false, true},
		"blocked caller, followers post":        {"followers", false, true},
		"stranger, followers-only post":         {"followers", true, false},
		"one-way follower, friends post":        {"friends", true, true},
		"anyone but the author, private post":   {"private", true, true},
		"stranger to a private account's post":  {"public", false, false},
		"retired circle audience, not a member": {"circle", true, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newRepostRig(t, tc.visibility, tc.canView, tc.follows)
			caller := uuid.New()

			for _, typ := range []string{"plain", "quote"} {
				_, err := rig.svc.CreateRepost(ctx, CreateRepostInput{UserID: caller, PostID: rig.post.ID, Type: typ, QuoteText: "look"})
				if errText(err) != "POST_NOT_FOUND" {
					t.Errorf("CreateRepost %s = %v, want POST_NOT_FOUND", typ, err)
				}
			}
			for _, typ := range []string{"external", "repost", "quote"} {
				res, err := rig.svc.SharePost(ctx, rig.post.ID, caller, typ, "look")
				if errText(err) != "POST_NOT_FOUND" || res != nil {
					t.Errorf("SharePost %s = %+v, %v, want POST_NOT_FOUND", typ, res, err)
				}
			}
			if n := rig.sharesWritten(); n != 0 {
				t.Errorf("%d share markers written for a post the caller may not see", n)
			}
		})
	}
}

// The refusal does not say whether the post exists: a post the caller may
// not see and an id that names nothing answer with the same error.
func TestRepostAndShare_InvisibleAnswersAsMissing(t *testing.T) {
	ctx := context.Background()
	rig := newRepostRig(t, "followers", true, false)
	caller, missing := uuid.New(), uuid.New()

	_, hidden := rig.svc.CreateRepost(ctx, CreateRepostInput{UserID: caller, PostID: rig.post.ID, Type: "plain"})
	_, absent := rig.svc.CreateRepost(ctx, CreateRepostInput{UserID: caller, PostID: missing, Type: "plain"})
	if hidden == nil || absent == nil || hidden.Error() != absent.Error() {
		t.Fatalf("CreateRepost: hidden = %v, missing = %v; they must be the same answer", hidden, absent)
	}
	_, hidden = rig.svc.SharePost(ctx, rig.post.ID, caller, "external", "")
	_, absent = rig.svc.SharePost(ctx, missing, caller, "external", "")
	if hidden == nil || absent == nil || hidden.Error() != absent.Error() {
		t.Fatalf("SharePost: hidden = %v, missing = %v; they must be the same answer", hidden, absent)
	}
}

// A public post still shares, and still passes the repost gates. (A whole
// CreateRepost writes the repost row in Postgres, which unit tests do not
// have; a quote with no text is refused by the step AFTER the visibility and
// audience checks, which is how the tests in heart_only_test.go show a
// caller got through.)
func TestRepostAndShare_PublicPostStillWorks(t *testing.T) {
	ctx := context.Background()
	rig := newRepostRig(t, "public", true, false) // a stranger who is not blocked
	caller := uuid.New()

	for _, typ := range []string{"external", "repost", "quote"} {
		res, err := rig.svc.SharePost(ctx, rig.post.ID, uuid.New(), typ, "look")
		if err != nil || res == nil || !res.Shared {
			t.Fatalf("SharePost %s on a public post = %+v, %v, want shared", typ, res, err)
		}
	}
	if n := rig.sharesWritten(); n != 3 {
		t.Fatalf("share markers = %d, want 3", n)
	}

	_, err := rig.svc.CreateRepost(ctx, CreateRepostInput{UserID: caller, PostID: rig.post.ID, Type: "quote", QuoteText: "  "})
	if errText(err) != "QUOTE_TEXT_REQUIRED" {
		t.Fatalf("CreateRepost quote on a public post = %v, want the quote-text check (the gates let it through)", err)
	}
}

// A follower may see a followers-only post but may not repost it: the repost
// would show it to the follower's own audience. They may still send its link
// (external), which opens only for people the author let in. The author is
// held to the same rule on their own post.
func TestRepost_FollowersOnlyPostIsNotRepostedByAFollower(t *testing.T) {
	ctx := context.Background()
	rig := newRepostRig(t, "followers", true, true)
	follower := uuid.New()

	for _, caller := range []uuid.UUID{follower, rig.post.AuthorID} {
		for _, typ := range []string{"plain", "quote"} {
			_, err := rig.svc.CreateRepost(ctx, CreateRepostInput{UserID: caller, PostID: rig.post.ID, Type: typ, QuoteText: "look"})
			if errText(err) != "NOT_ELIGIBLE" {
				t.Errorf("CreateRepost %s of a followers-only post = %v, want NOT_ELIGIBLE", typ, err)
			}
		}
		for _, typ := range []string{"repost", "quote"} {
			_, err := rig.svc.SharePost(ctx, rig.post.ID, caller, typ, "look")
			if errText(err) != "CIRCLE_SHARE_RESTRICTED" {
				t.Errorf("SharePost %s of a followers-only post = %v, want CIRCLE_SHARE_RESTRICTED", typ, err)
			}
		}
	}
	if n := rig.sharesWritten(); n != 0 {
		t.Fatalf("%d share markers written by refused reposts", n)
	}
	if res, err := rig.svc.SharePost(ctx, rig.post.ID, follower, "external", ""); err != nil || res == nil || !res.Shared {
		t.Fatalf("a follower's external share = %+v, %v, want shared", res, err)
	}
}
