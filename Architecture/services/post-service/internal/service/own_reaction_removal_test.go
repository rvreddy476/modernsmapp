package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// NEW-3 (reaudit 18 Nov): after a takedown (or the post going private, or a
// block), a viewer can still take their OWN like off the post, while a
// viewer with no like there gets the same 404 as before and cannot add one.

// rawPostStore answers the ungated post read the removal path makes.
type rawPostStore struct {
	listReadStore // unimplemented methods panic: only GetPost is used
	post          *postgres.Post
}

func (s rawPostStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	if s.post != nil && s.post.ID == id {
		return s.post, nil
	}
	return nil, nil
}

// hide makes the gate refuse the rig's post, as a takedown does.
func (r *likeRig) hide() {
	r.svc.engagementPostSeam = func(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
		if postID != r.post.ID {
			return nil, ErrPostNotFound
		}
		return nil, ErrPostNotVisible
	}
	r.svc.listReadSeam = rawPostStore{post: r.post}
}

func TestOwnLikeComesOffAPostTakenDown(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	r.tap(t, user) // liked while visible
	r.hide()

	res, err := r.svc.ToggleLike(context.Background(), r.post.ID, user)
	if err != nil {
		t.Fatalf("unlike after takedown: %v, want it to succeed", err)
	}
	if res.Liked {
		t.Fatal("the tap after a takedown must only unlike")
	}
	if r.hearts.stored(r.post.ID, user) != "" {
		t.Fatal("the like is still stored")
	}
	if got := r.counters(); got.Stored != 0 || got.OutboxNet != 0 {
		t.Fatalf("after the unlike: %+v", got)
	}

	// The like is gone: a second tap would LIKE a post the viewer cannot see.
	if _, err := r.svc.ToggleLike(context.Background(), r.post.ID, user); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("liking a taken-down post: %v, want ErrPostNotVisible", err)
	}
	if reacts, _ := r.hearts.calls(); reacts != 1 {
		t.Fatalf("React calls = %d, want only the first, visible like", reacts)
	}
}

func TestStrangerStillGetsTheGateOnAHiddenPost(t *testing.T) {
	r := newLikeRig(t)
	r.hide()
	if _, err := r.svc.ToggleLike(context.Background(), r.post.ID, uuid.New()); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("a viewer with no like: %v, want ErrPostNotVisible", err)
	}
	if reacts, unreacts := r.hearts.calls(); reacts+unreacts != 0 {
		t.Fatal("a refused tap reached the store")
	}
	// A missing post stays missing.
	if _, err := r.svc.ToggleLike(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("a missing post: %v, want ErrPostNotFound", err)
	}
}

func TestRemoveLike_WorksAfterTakedown(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	r.tap(t, user)
	r.hide()
	if err := r.svc.removeLike(context.Background(), r.post.ID, user); err != nil {
		t.Fatalf("removeLike after takedown: %v", err)
	}
	if r.hearts.stored(r.post.ID, user) != "" {
		t.Fatal("the like is still stored")
	}
}

// flakyHearts reports a like on the first read and none after, as when a
// second tap's unlike lands between the gate's check and the toggle.
type flakyHearts struct {
	*fakeHearts
	reads int
}

func (f *flakyHearts) GetReaction(ctx context.Context, post, user uuid.UUID) (string, error) {
	f.reads++
	if f.reads == 1 {
		return "like", nil
	}
	return f.fakeHearts.GetReaction(ctx, post, user)
}

func TestHiddenPostIsNeverLikedThroughTheRemovalPath(t *testing.T) {
	r := newLikeRig(t)
	r.hide()
	flaky := &flakyHearts{fakeHearts: r.hearts}
	r.svc.heartSeam = flaky
	if _, err := r.svc.ToggleLike(context.Background(), r.post.ID, uuid.New()); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("toggle after the like vanished: %v, want ErrPostNotVisible", err)
	}
	if reacts, _ := r.hearts.calls(); reacts != 0 {
		t.Fatal("a hidden post was liked")
	}
}
