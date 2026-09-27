package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Channel branding (2026-09-27): links / email validation, the banner's
// media authority, the featured post's ownership, and the 60 s tally cache.

func TestNormalizeChannelLinks(t *testing.T) {
	ok, err := NormalizeChannelLinks([]postgres.ChannelLink{{Title: "  Site ", URL: " https://example.com/x "}})
	if err != nil || len(ok) != 1 || ok[0].Title != "Site" || ok[0].URL != "https://example.com/x" {
		t.Fatalf("valid: %+v %v", ok, err)
	}
	empty, err := NormalizeChannelLinks(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("nil in: %+v %v", empty, err)
	}
	bad := [][]postgres.ChannelLink{
		{{Title: "", URL: "https://a.b"}},
		{{Title: "0123456789012345678901234567890123456789X", URL: "https://a.b"}},
		{{Title: "http", URL: "http://a.b"}},
		{{Title: "rel", URL: "/relative"}},
		{{Title: "js", URL: "javascript:alert(1)"}},
		{{Title: "none", URL: ""}},
	}
	for i, links := range bad {
		if _, err := NormalizeChannelLinks(links); !errors.Is(err, ErrInvalidChannelLinks) {
			t.Errorf("bad[%d] %+v: err=%v", i, links, err)
		}
	}
	eleven := make([]postgres.ChannelLink, 11)
	for i := range eleven {
		eleven[i] = postgres.ChannelLink{Title: "t", URL: "https://a.b"}
	}
	if _, err := NormalizeChannelLinks(eleven); !errors.Is(err, ErrInvalidChannelLinks) {
		t.Fatalf("eleven links: %v", err)
	}
}

func TestNormalizeContactEmail(t *testing.T) {
	if got, err := NormalizeContactEmail(" hi@example.com "); err != nil || got != "hi@example.com" {
		t.Fatalf("valid: %q %v", got, err)
	}
	if got, err := NormalizeContactEmail("  "); err != nil || got != "" {
		t.Fatalf("empty clears: %q %v", got, err)
	}
	for _, bad := range []string{"nope", "a@", "@b", "Name <a@b.c>", "a b@c.d"} {
		if _, err := NormalizeContactEmail(bad); !errors.Is(err, ErrInvalidContactEmail) {
			t.Errorf("%q: err=%v", bad, err)
		}
	}
}

func TestChannelBrandingPatchGuards(t *testing.T) {
	owner := uuid.New()
	media := newFakePostEditStore()
	foreign, mine, video := uuid.New(), uuid.New(), uuid.New()
	media.media[foreign] = postgres.MediaOwnership{UploaderID: uuid.New(), Kind: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}
	media.media[mine] = postgres.MediaOwnership{UploaderID: owner, Kind: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}
	media.media[video] = postgres.MediaOwnership{UploaderID: owner, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed"}
	owners := &fakeAuthoringStore{authorID: owner}
	svc := &Service{postEdits: media}
	svc.authoringOwners = owners
	ctx := context.Background()

	var patch postgres.ChannelPatch
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{BannerMediaID: &foreign}, &patch); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("foreign banner: %v", err)
	}
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{BannerMediaID: &video}, &patch); !errors.Is(err, ErrMediaTypeMismatch) {
		t.Fatalf("video banner: %v", err)
	}
	missing := uuid.New()
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{BannerMediaID: &missing}, &patch); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("missing banner: %v", err)
	}
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{BannerMediaID: &mine}, &patch); err != nil || patch.BannerMediaID == nil || *patch.BannerMediaID != mine {
		t.Fatalf("own banner: %v %+v", err, patch)
	}
	// Featured post must be the caller's.
	post := uuid.New()
	owners.authorID = uuid.New()
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{FeaturedPostID: &post}, &patch); !errors.Is(err, ErrFeaturedPostNotOwned) {
		t.Fatalf("foreign featured: %v", err)
	}
	owners.authorID = owner
	patch = postgres.ChannelPatch{}
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{FeaturedPostID: &post}, &patch); err != nil || patch.FeaturedPostID == nil {
		t.Fatalf("own featured: %v %+v", err, patch)
	}
	// Clears pass through without a lookup.
	patch = postgres.ChannelPatch{}
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{ClearBanner: true, ClearFeatured: true, ContactEmail: strptr("")}, &patch); err != nil || !patch.ClearBanner || !patch.ClearFeatured || patch.ContactEmail == nil {
		t.Fatalf("clears: %v %+v", err, patch)
	}
	// Bad links / email refuse.
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{Links: &[]postgres.ChannelLink{{Title: "x", URL: "http://a.b"}}}, &patch); !errors.Is(err, ErrInvalidChannelLinks) {
		t.Fatalf("links: %v", err)
	}
	if err := svc.applyBrandingPatch(ctx, owner, UpdateChannelInput{ContactEmail: strptr("nope")}, &patch); !errors.Is(err, ErrInvalidContactEmail) {
		t.Fatalf("email: %v", err)
	}
}

type countingChannelStore struct {
	*fakeChannelStore
	calls int
}

func (c *countingChannelStore) CountChannelContent(ctx context.Context, id uuid.UUID) (postgres.ChannelContentCounts, error) {
	c.calls++
	return c.fakeChannelStore.CountChannelContent(ctx, id)
}

func TestChannelContentCountsAreCachedAndDroppedOnEdit(t *testing.T) {
	store := &countingChannelStore{fakeChannelStore: newFakeChannelStore()}
	owner := uuid.New()
	store.videos[owner] = 7
	svc := &Service{channels: store}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		c, err := svc.channelContentCounts(ctx, owner)
		if err != nil || c.Videos != 7 {
			t.Fatal(err)
		}
	}
	if store.calls != 1 {
		t.Fatalf("store called %d times within the TTL, want 1", store.calls)
	}
	svc.forgetChannelCounts(owner)
	if _, err := svc.channelContentCounts(ctx, owner); err != nil || store.calls != 2 {
		t.Fatalf("after forget: calls=%d err=%v", store.calls, err)
	}
	// The view carries the tally and an always-array links.
	view := svc.channelView(ctx, uuid.Nil, &postgres.Channel{UserID: owner, Handle: "h", Name: "n"})
	if view.VideoCount != 7 || view.Links == nil {
		t.Fatalf("view: %+v", view)
	}
}
