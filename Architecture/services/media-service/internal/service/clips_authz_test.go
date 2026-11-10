package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeLinks struct {
	byMedia map[uuid.UUID][]uuid.UUID
	err     error
}

func (f fakeLinks) PostIDsByMedia(_ context.Context, m uuid.UUID) ([]uuid.UUID, error) {
	return f.byMedia[m], f.err
}

func TestAssertClipsOnPost(t *testing.T) {
	post, otherPost, media := uuid.New(), uuid.New(), uuid.New()
	clips := []postgres.MediaClip{{PostID: post, MediaAssetID: media}, {PostID: post, MediaAssetID: media}}
	ctx := context.Background()

	if err := assertClipsOnPost(ctx, fakeLinks{byMedia: map[uuid.UUID][]uuid.UUID{media: {post}}}, post, clips); err != nil {
		t.Fatalf("author of the post must be allowed: %v", err)
	}
	if err := assertClipsOnPost(ctx, fakeLinks{byMedia: map[uuid.UUID][]uuid.UUID{media: {otherPost}}}, post, clips); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("a post that does not attach the media must be 403-class, got %v", err)
	}
	if err := assertClipsOnPost(ctx, fakeLinks{}, post, clips); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("a post attaching nothing must be refused, got %v", err)
	}
	if err := assertClipsOnPost(ctx, fakeLinks{err: errors.New("down")}, post, clips); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("a failing lookup must fail closed, got %v", err)
	}
	if err := assertClipsOnPost(ctx, nil, post, clips); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("no lookup wired must fail closed, got %v", err)
	}
}

func TestFilterReadableClips(t *testing.T) {
	ok, denied, down := uuid.New(), uuid.New(), uuid.New()
	clips := []postgres.MediaClip{{MediaAssetID: ok}, {MediaAssetID: denied}, {MediaAssetID: ok}}
	authorize := func(_ context.Context, _, m uuid.UUID) error {
		switch m {
		case denied:
			return delivery.ErrDeliveryDenied
		case down:
			return delivery.ErrDeliveryUnresolved
		}
		return nil
	}
	got, err := filterReadableClips(context.Background(), authorize, uuid.Nil, clips)
	if err != nil || len(got) != 2 {
		t.Fatalf("want the 2 readable clips, got %d err %v", len(got), err)
	}
	if _, err := filterReadableClips(context.Background(), authorize, uuid.Nil, []postgres.MediaClip{{MediaAssetID: down}}); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("unresolved must propagate, got %v", err)
	}
}

func TestRedactRenditionStatus(t *testing.T) {
	k, m := "uploads/u/raw", "boom"
	resp := &RenditionStatusResponse{Renditions: []postgres.MediaRendition{{ObjectKey: &k, ErrorMessage: &m}}}
	if got := RedactRenditionStatus(resp, true); got.Renditions[0].ObjectKey == nil {
		t.Fatal("owner must keep object_key")
	}
	got := RedactRenditionStatus(resp, false)
	if got.Renditions[0].ObjectKey != nil || got.Renditions[0].ErrorMessage != nil {
		t.Fatal("non-owner must not see object_key or error_message")
	}
	if resp.Renditions[0].ObjectKey == nil {
		t.Fatal("redaction must not mutate the original")
	}
}

func TestAssertSlotOwner(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	if err := assertSlotOwner(me, "profile", me); err != nil {
		t.Fatalf("own profile must be allowed: %v", err)
	}
	for _, c := range []struct {
		user      uuid.UUID
		ownerType string
		owner     uuid.UUID
	}{
		{me, "profile", other},
		{uuid.Nil, "profile", uuid.Nil},
		{me, "channel", me},
		{me, "group", other},
		{me, "module_profile", other},
	} {
		if err := assertSlotOwner(c.user, c.ownerType, c.owner); !errors.Is(err, ErrNotSlotOwner) {
			t.Fatalf("%+v must be refused with ErrNotSlotOwner, got %v", c, err)
		}
	}
}
