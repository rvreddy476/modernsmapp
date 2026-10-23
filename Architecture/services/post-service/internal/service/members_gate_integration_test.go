//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A reel / long-video draft saved with a members-only tier publishes gated
// (migration 071), by the person and by the schedule worker alike; a tier
// that is not the author's publishes nothing.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run MembersGateIT -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).
func TestMembersGateITReelDraftPublishesGated(t *testing.T) {
	f := newSoundsITFixture(t)
	svc := reelDraftKindService(t, f)
	ctx := context.Background()
	tier := uuid.New()
	svc.creatorTiersSeam = func(_ context.Context, creator uuid.UUID) ([]CreatorTierRef, error) {
		if creator != f.stranger {
			return nil, nil
		}
		return []CreatorTierRef{{ID: tier, CreatorID: f.stranger, PricePaise: 4900, IsActive: true}}, nil
	}

	for name, publish := range reelDraftPublishers(svc, f.stranger) {
		media := f.video(t, f.stranger)
		d, err := svc.CreateDraft(ctx, &CreateDraftInput{AuthorID: f.stranger, MediaID: &media, Visibility: "public"})
		if err != nil {
			t.Fatal(err)
		}
		f.posts = append(f.posts, d.ID)
		var patch UpdateDraftInput
		if err := json.Unmarshal([]byte(studioDraftPatch), &patch); err != nil {
			t.Fatal(err)
		}
		patch.TierRequiredID = json.RawMessage(`"` + tier.String() + `"`)
		if _, err := svc.UpdateDraft(ctx, d.ID, f.stranger, &patch); err != nil {
			t.Fatalf("%s: patch: %v", name, err)
		}
		post, err := publish(ctx, d.ID)
		if err != nil || post == nil {
			t.Fatalf("%s: post=%v err=%v", name, post, err)
		}
		stored, err := f.store.GetPost(ctx, post.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.TierRequiredID == nil || *stored.TierRequiredID != tier {
			t.Errorf("%s: published without its gate: %v", name, stored.TierRequiredID)
		}
	}

	// Another creator's tier: no post at all.
	media := f.video(t, f.stranger)
	d, err := svc.CreateDraft(ctx, &CreateDraftInput{AuthorID: f.stranger, MediaID: &media, Visibility: "public"})
	if err != nil {
		t.Fatal(err)
	}
	f.posts = append(f.posts, d.ID)
	if _, err := svc.UpdateDraft(ctx, d.ID, f.stranger, &UpdateDraftInput{TierRequiredID: json.RawMessage(`"` + uuid.NewString() + `"`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishDraft(ctx, d.ID, f.stranger, nil); !errors.Is(err, ErrInvalidTier) {
		t.Fatalf("foreign tier: %v, want ErrInvalidTier", err)
	}
	if p, _ := f.store.GetPost(ctx, d.ID); p != nil {
		t.Fatalf("a post was created open: %+v", p)
	}
}
