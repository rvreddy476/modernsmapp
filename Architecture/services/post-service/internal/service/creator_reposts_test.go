package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A creator's Reposts tab (creator_reposts.go): the reposter's profile gate,
// each repost row's own audience, and the original through the batch read
// (its visibility drops and the members-only redaction).

type repostsRig struct {
	reposter, author, viewer, member, tier uuid.UUID
	openPost, gatedPost, hiddenPost        uuid.UUID
	rows                                   []postgres.Repost
	nextCursor                             string
	profileOpen                            bool
	edges                                  followEdges
	storeCalls, gateCalls, edgeCalls       int
}

func newRepostsRig() *repostsRig {
	r := &repostsRig{
		reposter: uuid.New(), author: uuid.New(), viewer: uuid.New(), member: uuid.New(), tier: uuid.New(),
		openPost: uuid.New(), gatedPost: uuid.New(), hiddenPost: uuid.New(),
		profileOpen: true, nextCursor: "2026-10-20T10:00:00Z",
	}
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	repost := func(post uuid.UUID, visibility string) postgres.Repost {
		at = at.Add(-time.Minute)
		return postgres.Repost{ID: uuid.New(), UserID: r.reposter, OriginalPostID: post, RepostType: "plain",
			Visibility: visibility, Status: "active", CreatedAt: at}
	}
	r.rows = []postgres.Repost{
		repost(r.openPost, "public"),
		repost(r.gatedPost, "public"),
		repost(r.hiddenPost, "public"), // the batch read does not return it
		repost(r.openPost, "followers"),
	}
	return r
}

func (r *repostsRig) svc() *Service {
	f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{r.member: {r.tier: true}}}
	s := f.svc()
	s.userRepostsSeam = func(_ context.Context, userID uuid.UUID, _ int, _ string) ([]postgres.Repost, string, error) {
		r.storeCalls++
		if userID != r.reposter {
			return nil, "", nil
		}
		return append([]postgres.Repost(nil), r.rows...), r.nextCursor, nil
	}
	s.repostProfileGate = func(context.Context, *uuid.UUID, uuid.UUID) bool {
		r.gateCalls++
		return r.profileOpen
	}
	s.repostEdgesSeam = func(_ context.Context, _ *uuid.UUID, reposter uuid.UUID) map[uuid.UUID]followEdges {
		r.edgeCalls++
		return map[uuid.UUID]followEdges{reposter: r.edges}
	}
	s.batchPostsSeam = func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
		out := map[uuid.UUID]*PostDetail{}
		for _, id := range ids {
			switch id {
			case r.openPost:
				out[id] = &PostDetail{Post: &postgres.Post{ID: id, AuthorID: r.author, Visibility: "public", Text: "open body"}}
			case r.gatedPost:
				d := gatedDetail(r.author, r.tier)
				d.Post.ID = id
				out[id] = d
			}
		}
		return out, nil
	}
	return s
}

func repostPostIDs(res *ListUserRepostsResult) []uuid.UUID {
	var out []uuid.UUID
	for _, it := range res.Items {
		out = append(out, it.OriginalPostID)
	}
	return out
}

func TestCreatorRepostsClosedProfileAnswersAnEmptyPage(t *testing.T) {
	r := newRepostsRig()
	r.profileOpen = false
	res, err := r.svc().ListUserReposts(context.Background(), r.reposter, &r.viewer, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Items == nil || len(res.Items) != 0 || res.NextCursor != "" {
		t.Fatalf("closed profile answered %+v", res)
	}
	if r.storeCalls != 0 {
		t.Fatal("the reposts were read for a viewer the profile gate refused")
	}
	// Signed out is asked the same gate.
	r.gateCalls = 0
	if res, _ := r.svc().ListUserReposts(context.Background(), r.reposter, nil, 20, ""); len(res.Items) != 0 || r.gateCalls != 1 {
		t.Fatalf("signed-out viewer: items=%d gate calls=%d", len(res.Items), r.gateCalls)
	}
}

func TestCreatorRepostsStrangerSeesOnlyWhatTheyMayRead(t *testing.T) {
	r := newRepostsRig()
	res, err := r.svc().ListUserReposts(context.Background(), r.reposter, &r.viewer, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	got := repostPostIDs(res)
	// public open + public gated; the hidden original and the followers-only
	// row (viewer does not follow the reposter) are left off.
	if len(got) != 2 || got[0] != r.openPost || got[1] != r.gatedPost {
		t.Fatalf("items = %v", got)
	}
	if res.NextCursor != r.nextCursor {
		t.Fatalf("next_cursor = %q, want the store's", res.NextCursor)
	}
	for _, it := range res.Items {
		if it.Post == nil || it.Post.Post == nil || it.Post.Post.ID != it.OriginalPostID {
			t.Fatalf("item without its original: %+v", it)
		}
	}
	if !isRedacted(res.Items[1].Post.Post) {
		t.Fatalf("a non-member read the members-only original: %+v", res.Items[1].Post.Post)
	}
	if r.gateCalls != 1 {
		t.Fatalf("profile gate asked %d times", r.gateCalls)
	}
}

func TestCreatorRepostsFollowerSeesFollowersRowsAndMemberTheBody(t *testing.T) {
	r := newRepostsRig()
	r.edges = followEdges{follows: true}
	res, err := r.svc().ListUserReposts(context.Background(), r.reposter, &r.member, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := repostPostIDs(res); len(got) != 3 || got[2] != r.openPost {
		t.Fatalf("items = %v, want the followers row too", got)
	}
	if !isFull(res.Items[1].Post.Post) {
		t.Fatalf("a member got the redacted original: %+v", res.Items[1].Post.Post)
	}
}

func TestCreatorRepostsOwnTabSkipsTheReposterGatesNotTheOriginals(t *testing.T) {
	r := newRepostsRig()
	r.profileOpen = false
	res, err := r.svc().ListUserReposts(context.Background(), r.reposter, &r.reposter, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.gateCalls != 0 || r.edgeCalls != 0 {
		t.Fatalf("own tab asked the reposter gates: profile=%d edges=%d", r.gateCalls, r.edgeCalls)
	}
	// Own rows, followers one included; the original the batch drops is
	// still left off, and the gated original still redacted (not a member).
	if got := repostPostIDs(res); len(got) != 3 {
		t.Fatalf("items = %v", got)
	}
	if !isRedacted(res.Items[1].Post.Post) {
		t.Fatal("the reposter read a members-only original they are not a member of")
	}
}

func TestCreatorRepostsEmptyAfterFilteringKeepsPaging(t *testing.T) {
	for name, keep := range map[string][2]int{
		"original not readable":         {2, 3}, // only the hidden original
		"row audience refused (follow)": {3, 4}, // only the followers row
	} {
		r := newRepostsRig()
		r.rows = r.rows[keep[0]:keep[1]]
		res, err := r.svc().ListUserReposts(context.Background(), r.reposter, &r.viewer, 20, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 0 || res.Items == nil || res.NextCursor != r.nextCursor {
			t.Fatalf("%s: filtered-empty page = %+v, want [] with the store's next_cursor", name, res)
		}
	}
}

func TestCreatorRepostsInvalidCursor(t *testing.T) {
	r := newRepostsRig()
	_, err := r.svc().ListUserReposts(context.Background(), r.reposter, &r.viewer, 20, "not-a-time")
	if !errors.Is(err, ErrInvalidRepostCursor) {
		t.Fatalf("err = %v, want ErrInvalidRepostCursor", err)
	}
	if r.storeCalls != 0 {
		t.Fatal("the store was read with a bad cursor")
	}
}

// The default edge source asks graph-service about the reposter only when a
// row is restricted, and a failed lookup drops the restricted rows.
func TestCreatorRepostsDefaultEdgesFailClosed(t *testing.T) {
	r := newRepostsRig()
	s := r.svc()
	s.repostEdgesSeam = nil
	s.graphServiceURL = "http://127.0.0.1:1" // unreachable: every lookup fails
	res, err := s.ListUserReposts(context.Background(), r.reposter, &r.viewer, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		for _, row := range r.rows {
			if row.ID == it.RepostID && row.Visibility == "followers" {
				t.Fatal("a followers-only repost was listed after the edge lookup failed")
			}
		}
	}
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want the two public rows", len(res.Items))
	}
}
