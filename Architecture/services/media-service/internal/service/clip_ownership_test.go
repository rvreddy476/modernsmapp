package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// POST /v1/clips/:postId authenticated the caller and then threw the
// identity away, so any signed-in user could replace any post's clip
// sequence. These tests pin the authorization that closes it.

func clipsOf(ids ...uuid.UUID) []postgres.MediaClip {
	clips := make([]postgres.MediaClip, len(ids))
	for i, id := range ids {
		clips[i] = postgres.MediaClip{MediaAssetID: id, ClipOrder: i, DurationMs: 1000}
	}
	return clips
}

// ownedBy builds an AssertMediaOwner stand-in: the listed assets belong to
// owner, everything else does not.
func ownedBy(owner uuid.UUID, assets ...uuid.UUID) (func(context.Context, uuid.UUID, uuid.UUID) error, *int) {
	calls := 0
	owned := map[uuid.UUID]bool{}
	for _, a := range assets {
		owned[a] = true
	}
	return func(_ context.Context, mediaID, actorID uuid.UUID) error {
		calls++
		if actorID == owner && owned[mediaID] {
			return nil
		}
		return ErrNotMediaOwner
	}, &calls
}

func TestAssertClipsOwned(t *testing.T) {
	ctx := context.Background()
	owner, stranger := uuid.New(), uuid.New()
	mineA, mineB, theirs := uuid.New(), uuid.New(), uuid.New()

	t.Run("owner of every asset may write the sequence", func(t *testing.T) {
		assertOwner, calls := ownedBy(owner, mineA, mineB)
		if err := assertClipsOwned(ctx, assertOwner, owner, clipsOf(mineA), clipsOf(mineA, mineB)); err != nil {
			t.Fatalf("owner refused: %v", err)
		}
		if *calls != 2 {
			t.Fatalf("each distinct asset must be checked exactly once, got %d checks", *calls)
		}
	})

	t.Run("a stranger is refused", func(t *testing.T) {
		assertOwner, _ := ownedBy(owner, mineA)
		err := assertClipsOwned(ctx, assertOwner, stranger, nil, clipsOf(mineA))
		if !errors.Is(err, ErrNotMediaOwner) {
			t.Fatalf("a stranger must not write another creator's clips: %v", err)
		}
	})

	t.Run("owning the new clips is not enough to replace someone else's sequence", func(t *testing.T) {
		assertOwner, _ := ownedBy(owner, mineA)
		// `theirs` is already in the post; the actor owns only what they
		// are adding. Wiping the existing sequence is still a write to
		// media they do not own.
		err := assertClipsOwned(ctx, assertOwner, owner, clipsOf(theirs), clipsOf(mineA))
		if !errors.Is(err, ErrNotMediaOwner) {
			t.Fatalf("replacing a sequence built from another creator's media must be refused: %v", err)
		}
	})

	t.Run("an anonymous actor is refused before any lookup", func(t *testing.T) {
		assertOwner, calls := ownedBy(owner, mineA)
		if err := assertClipsOwned(ctx, assertOwner, uuid.Nil, nil, clipsOf(mineA)); !errors.Is(err, ErrNotMediaOwner) {
			t.Fatalf("nil actor accepted: %v", err)
		}
		if *calls != 0 {
			t.Fatalf("a nil actor is settled without a store lookup, got %d", *calls)
		}
	})

	t.Run("an unreadable asset fails closed", func(t *testing.T) {
		boom := func(context.Context, uuid.UUID, uuid.UUID) error { return errors.New("connection refused") }
		err := assertClipsOwned(ctx, boom, owner, nil, clipsOf(mineA))
		if !errors.Is(err, ErrNotMediaOwner) {
			t.Fatalf("an unresolved ownership lookup must not permit the write: %v", err)
		}
	})
}
